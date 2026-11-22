package model

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func walletTransferFixture(t *testing.T, paid, bonus, reward int) (AuthSessionIdentity, AuthSessionIdentity) {
	t.Helper()
	root, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Updates(map[string]any{"quota": paid + bonus + reward, "quota_paid": paid, "quota_bonus": bonus, "quota_reward": reward}).Error)
	return root, agent
}

func TestAgentSimpleWalletTransferConservesPaidQuotaAndReplaysOriginalReceipt(t *testing.T) {
	_, agent := walletTransferFixture(t, 1000, 200, 300)
	request := AgentSimpleTransferRequest{RequestID: "wallet-transfer-original", Quota: 321}
	first, err := TransferWalletToAgentSimple(agent, request)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.EqualValues(t, 321, first.WalletQuota)
	assert.EqualValues(t, 321, first.AvailableQuota)
	_, err = TransferWalletToAgentSimple(agent, AgentSimpleTransferRequest{RequestID: "wallet-transfer-next", Quota: 79})
	require.NoError(t, err)
	replay, err := TransferWalletToAgentSimple(agent, request)
	require.NoError(t, err)
	assert.Equal(t, first, replay)
	request.Quota++
	_, err = TransferWalletToAgentSimple(agent, request)
	require.ErrorIs(t, err, ErrAgentQuotaConflict)
	user := getUserBalances(t, agent.UserID)
	assert.Equal(t, 600, user.QuotaPaid)
	assert.Equal(t, 200, user.QuotaBonus)
	assert.Equal(t, 300, user.QuotaReward)
	assertQuotaInvariant(t, user)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.EqualValues(t, 400, account.WalletQuota)
	assert.EqualValues(t, 1000, int64(user.QuotaPaid)+account.WalletQuota)
	assert.Zero(t, account.CashQuota)
	assert.Zero(t, account.RewardQuota)
	assert.Zero(t, account.PaymentTotalQuota)
	assert.Zero(t, account.CreditOutstandingQuota)
	var debits []BalanceLedger
	require.NoError(t, DB.Where("user_id = ?", agent.UserID).Order("id").Find(&debits).Error)
	require.Len(t, debits, 2)
	assert.Equal(t, "paid", debits[0].Account)
	assert.Equal(t, -321, debits[0].Delta)
	assert.Equal(t, 1000, debits[0].BalanceBefore)
	assert.Equal(t, 679, debits[0].BalanceAfter)
	assert.Equal(t, -79, debits[1].Delta)
	var ledger []AgentSimpleLedger
	require.NoError(t, DB.Where("agent_user_id = ?", agent.UserID).Order("id").Find(&ledger).Error)
	require.Len(t, ledger, 2)
	assert.EqualValues(t, 321, ledger[0].DeltaWalletQuota)
	assert.EqualValues(t, 321, ledger[0].WalletAfter)
	assert.EqualValues(t, 79, ledger[1].DeltaWalletQuota)
	assert.EqualValues(t, 400, ledger[1].WalletAfter)
	for _, schema := range []any{&AgentCommissionOutbox{}, &AgentCommissionAward{}, &ReferralCommission{}, &TopUp{}} {
		var count int64
		require.NoError(t, DB.Model(schema).Count(&count).Error)
		assert.Zero(t, count, "wallet transfers create neither payments nor commissions")
	}
}

func TestAgentSimpleWalletTransferRejectsInsufficientPaidWithoutConsumingBonus(t *testing.T) {
	_, agent := walletTransferFixture(t, 99, 500, 700)
	request := AgentSimpleTransferRequest{RequestID: "wallet-transfer-insufficient", Quota: 100}
	_, err := TransferWalletToAgentSimple(agent, request)
	require.ErrorIs(t, err, ErrAgentSimpleInsufficient)
	user := getUserBalances(t, agent.UserID)
	assert.Equal(t, 99, user.QuotaPaid)
	assert.Equal(t, 500, user.QuotaBonus)
	assert.Equal(t, 700, user.QuotaReward)
	assertQuotaInvariant(t, user)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.WalletQuota)
	var count int64
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ?", agent.UserID).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Updates(map[string]any{"quota": 1300, "quota_paid": 100}).Error)
	_, err = TransferWalletToAgentSimple(agent, request)
	require.NoError(t, err, "failed transfers must not consume the intent")
	assert.Zero(t, getUserBalances(t, agent.UserID).QuotaPaid)
}

func TestAgentSimpleWalletTransferRollsBackWalletAndIntentWhenAgentLedgerFails(t *testing.T) {
	_, agent := walletTransferFixture(t, 1000, 100, 0)
	injected := errors.New("synthetic agent wallet ledger failure")
	const callback = "wallet-transfer-ledger-failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "agent_simple_ledgers" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Create().Remove(callback) })
	request := AgentSimpleTransferRequest{RequestID: "wallet-transfer-rollback", Quota: 500}
	_, err := TransferWalletToAgentSimple(agent, request)
	require.ErrorIs(t, err, injected)
	user := getUserBalances(t, agent.UserID)
	assert.Equal(t, 1000, user.QuotaPaid)
	assert.Equal(t, 1100, user.Quota)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.WalletQuota)
	for _, schema := range []any{&BalanceLedger{}, &BalanceLedgerOperation{}} {
		var count int64
		require.NoError(t, DB.Model(schema).Count(&count).Error)
		assert.Zero(t, count)
	}
	var count int64
	require.NoError(t, DB.Model(&AgentRedemptionOperation{}).Where("actor_user_id = ?", agent.UserID).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Callback().Create().Remove(callback))
	view, err := TransferWalletToAgentSimple(agent, request)
	require.NoError(t, err)
	assert.EqualValues(t, 500, view.WalletQuota)
	assert.Equal(t, 500, getUserBalances(t, agent.UserID).QuotaPaid)
}

func TestAgentSimpleWalletTransferValidatesBoundsAndCombinedCapacity(t *testing.T) {
	for _, quota := range []int64{-1, 0, int64(common.MaxWalletQuota) + 1} {
		t.Run(fmt.Sprint(quota), func(t *testing.T) {
			_, agent := walletTransferFixture(t, 100, 0, 0)
			_, err := TransferWalletToAgentSimple(agent, AgentSimpleTransferRequest{RequestID: "wallet-transfer-invalid", Quota: quota})
			require.ErrorIs(t, err, ErrAgentSimpleInvalid)
			assert.Equal(t, 100, getUserBalances(t, agent.UserID).QuotaPaid)
		})
	}
	t.Run("combined_source_capacity", func(t *testing.T) {
		_, agent := walletTransferFixture(t, 100, 0, 0)
		require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("cash_quota", common.MaxWalletQuota-50).Error)
		_, err := TransferWalletToAgentSimple(agent, AgentSimpleTransferRequest{RequestID: "wallet-transfer-over-capacity", Quota: 51})
		require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
		assert.Equal(t, 100, getUserBalances(t, agent.UserID).QuotaPaid)
		view, err := TransferWalletToAgentSimple(agent, AgentSimpleTransferRequest{RequestID: "wallet-transfer-exact-capacity", Quota: 50})
		require.NoError(t, err)
		assert.EqualValues(t, common.MaxWalletQuota, view.AvailableQuota)
		assert.EqualValues(t, 50, view.WalletQuota)
	})
}

func TestAgentSimpleWalletTransferRequiresCurrentOwnActiveAgentSession(t *testing.T) {
	for _, scenario := range []string{"admin", "root", "ordinary", "disabled_user", "paused", "cancelled", "missing_account", "missing_session", "revoked_session", "expired_session", "stale_auth", "stale_session", "another_user_session"} {
		t.Run(scenario, func(t *testing.T) {
			root, agent := walletTransferFixture(t, 100, 0, 0)
			identity := agent
			switch scenario {
			case "admin", "root", "ordinary":
				role := common.RoleCommonUser
				if scenario == "admin" {
					role = common.RoleAdminUser
				}
				if scenario == "root" {
					role = common.RoleRootUser
				}
				require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("role", role).Error)
				if scenario == "ordinary" {
					require.NoError(t, DB.Where("user_id = ?", agent.UserID).Delete(&AgentProfile{}).Error)
				}
			case "disabled_user":
				require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("status", common.UserStatusDisabled).Error)
			case "paused", "cancelled":
				require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("status", scenario).Error)
			case "missing_account":
				require.NoError(t, DB.Delete(&AgentSimpleAccount{}, agent.UserID).Error)
			case "missing_session":
				identity.SessionID = "nonexistent-wallet-session"
			case "revoked_session":
				require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", identity.SessionID).Update("status", UserSessionStatusRevoked).Error)
			case "expired_session":
				require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", identity.SessionID).Update("expires_at", common.GetTimestamp()-1).Error)
			case "stale_auth":
				identity.UserAuthVersion++
			case "stale_session":
				identity.SessionVersion++
			case "another_user_session":
				identity.SessionID = root.SessionID
			}
			_, err := TransferWalletToAgentSimple(identity, AgentSimpleTransferRequest{RequestID: "wallet-transfer-not-authorized", Quota: 50})
			require.Error(t, err)
			assert.Equal(t, 100, getUserBalances(t, agent.UserID).QuotaPaid)
			var count int64
			require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ?", agent.UserID).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestAgentSimpleWalletSourceSurvivesMixedCodeCancellationAndExpiry(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("expire=%t", expire), func(t *testing.T) {
			_, agent := walletTransferFixture(t, 100, 0, 0)
			_, err := TransferWalletToAgentSimple(agent, AgentSimpleTransferRequest{RequestID: "wallet-transfer-for-code", Quota: 100})
			require.NoError(t, err)
			require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"cash_quota": 40, "reward_quota": 30, "root_quota": 20}).Error)
			code, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "wallet-mixed-code", Name: "Mixed source code", Quota: 190, Mode: AgentSimpleCodeDirect})
			require.NoError(t, err)
			var allocation AgentSimpleCodeAllocation
			require.NoError(t, DB.Where("redemption_id = ? AND source = ?", code.Id, AgentSimpleSourceWallet).First(&allocation).Error)
			assert.EqualValues(t, 100, allocation.Quota)
			assert.Equal(t, AgentSimpleAllocationReserved, allocation.State)
			if expire {
				require.NoError(t, DB.Model(code).Update("expired_time", common.GetTimestamp()-1).Error)
			}
			for range 2 {
				if expire {
					_, err = ExpireAgentSimpleCodes(t.Context(), 100)
				} else {
					_, err = CancelAgentSimpleCode(agent, AgentSimpleCodeCancelRequest{RequestID: "wallet-mixed-cancel", CodeID: code.Id, Version: int64(code.BudgetVersion), Reason: "Synthetic cancellation"})
				}
				require.NoError(t, err)
			}
			var account AgentSimpleAccount
			require.NoError(t, DB.First(&account, agent.UserID).Error)
			assert.EqualValues(t, 100, account.WalletQuota)
			assert.EqualValues(t, 40, account.CashQuota)
			assert.EqualValues(t, 30, account.RewardQuota)
			assert.EqualValues(t, 20, account.RootQuota)
			assert.Zero(t, getUserBalances(t, agent.UserID).QuotaPaid)
			require.NoError(t, DB.First(&allocation, allocation.ID).Error)
			assert.Equal(t, AgentSimpleAllocationReleased, allocation.State)
		})
	}
}

func TestAgentSimpleWalletCodeRedemptionConsumesSourceExactlyOnce(t *testing.T) {
	_, agent := walletTransferFixture(t, 100, 0, 0)
	_, err := TransferWalletToAgentSimple(agent, AgentSimpleTransferRequest{RequestID: "wallet-transfer-to-redeem", Quota: 100})
	require.NoError(t, err)
	code, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "wallet-redeem-code", Name: "Wallet funded", Quota: 100, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	credited, err := Redeem(code.Key, agentTestRecipientID)
	require.NoError(t, err)
	assert.Equal(t, 100, credited)
	_, err = Redeem(code.Key, agentTestRecipientID)
	require.Error(t, err)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.WalletQuota)
	assert.Zero(t, account.CashQuota)
	assert.Zero(t, account.CreditOutstandingQuota)
	assert.Equal(t, 100, getUserBalances(t, agentTestRecipientID).QuotaPaid)
	assert.Zero(t, getUserBalances(t, agent.UserID).QuotaPaid)
	var allocation AgentSimpleCodeAllocation
	require.NoError(t, DB.Where("redemption_id = ? AND source = ?", code.Id, AgentSimpleSourceWallet).First(&allocation).Error)
	assert.Equal(t, AgentSimpleAllocationSpent, allocation.State)
	assert.EqualValues(t, 100, allocation.Quota)
}

func TestAgentSimpleWalletExitReturnsOnlyOriginalWalletSource(t *testing.T) {
	root, agent := walletTransferFixture(t, 1000, 200, 0)
	_, err := TransferWalletToAgentSimple(agent, AgentSimpleTransferRequest{RequestID: "wallet-transfer-for-exit", Quota: 500})
	require.NoError(t, err)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"cash_quota": 150, "reward_quota": 70, "status": AgentSimpleStatusCancelled}).Error)
	request := AgentSimpleExitRequest{RequestID: "wallet-exit-refund-rejected", AgentUserID: agent.UserID, Quota: 500, Source: AgentSimpleSourceWallet, Target: "refund", Reason: "Synthetic exit", Evidence: "Synthetic evidence"}
	_, err = SettleAgentSimpleExit(root, request)
	require.ErrorIs(t, err, ErrAgentSimpleInvalid)
	request.RequestID, request.Target, request.Quota, request.SettleAll = "wallet-exit-return", "wallet", 0, true
	first, err := SettleAgentSimpleExit(root, request)
	require.NoError(t, err)
	assert.EqualValues(t, 500, first.Quota)
	assert.Zero(t, first.NetFen)
	replay, err := SettleAgentSimpleExit(root, request)
	require.NoError(t, err)
	assert.Equal(t, first, replay)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.WalletQuota)
	assert.EqualValues(t, 150, account.CashQuota)
	assert.EqualValues(t, 70, account.RewardQuota)
	user := getUserBalances(t, agent.UserID)
	assert.Equal(t, 1000, user.QuotaPaid)
	assert.Equal(t, 200, user.QuotaBonus)
	assertQuotaInvariant(t, user)
}

// Run against a permitted disposable native database to verify row locking.
// SQLite's single connection cannot establish native concurrency correctness.
func TestAgentSimpleWalletTransferConcurrentRequestsConserveBalance(t *testing.T) {
	for _, sameIntent := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_intent=%t", sameIntent), func(t *testing.T) {
			_, agent := walletTransferFixture(t, 100, 0, 0)
			start, results := make(chan struct{}), make(chan error, 2)
			for i := range 2 {
				requestID := "wallet-concurrent-first"
				if !sameIntent && i == 1 {
					requestID = "wallet-concurrent-second"
				}
				go func() {
					<-start
					_, err := TransferWalletToAgentSimple(agent, AgentSimpleTransferRequest{RequestID: requestID, Quota: 75})
					results <- err
				}()
			}
			close(start)
			failures := 0
			for range 2 {
				err := <-results
				if err != nil {
					failures++
					if sameIntent {
						assert.NoError(t, err)
					} else {
						assert.ErrorIs(t, err, ErrAgentSimpleInsufficient)
					}
				}
			}
			if sameIntent {
				assert.Zero(t, failures)
			} else {
				assert.Equal(t, 1, failures)
			}
			assert.Equal(t, 25, getUserBalances(t, agent.UserID).QuotaPaid)
			var account AgentSimpleAccount
			require.NoError(t, DB.First(&account, agent.UserID).Error)
			assert.EqualValues(t, 75, account.WalletQuota)
			var count int64
			require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ?", agent.UserID).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestAgentSimpleWalletMixedCreditReturnDoesNotApplyDiscountedDebtOffset(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("expire=%t", expire), func(t *testing.T) {
			_, agent := walletTransferFixture(t, 50, 0, 0)
			_, err := TransferWalletToAgentSimple(agent, AgentSimpleTransferRequest{RequestID: "wallet-transfer-mixed-credit", Quota: 50})
			require.NoError(t, err)
			require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("credit_limit_quota", 300).Error)
			returned, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "wallet-mixed-credit-return", Name: "Returned wallet and credit", Quota: 100, Mode: AgentSimpleCodeDirect})
			require.NoError(t, err)
			spent, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "wallet-mixed-credit-spent", Name: "Other credit debt", Quota: 100, Mode: AgentSimpleCodeDirect})
			require.NoError(t, err)
			_, err = Redeem(spent.Key, agentTestRecipientID)
			require.NoError(t, err)
			if expire {
				require.NoError(t, DB.Model(returned).Update("expired_time", common.GetTimestamp()-1).Error)
				_, err = ExpireAgentSimpleCodes(t.Context(), 100)
			} else {
				_, err = CancelAgentSimpleCode(agent, AgentSimpleCodeCancelRequest{RequestID: "wallet-mixed-credit-cancel", CodeID: returned.Id, Version: int64(returned.BudgetVersion), Reason: "Synthetic return"})
			}
			require.NoError(t, err)
			var account AgentSimpleAccount
			require.NoError(t, DB.First(&account, agent.UserID).Error)
			assert.EqualValues(t, 50, account.WalletQuota, "wallet principal returns at face value despite other debt")
			assert.Zero(t, account.CashQuota)
			assert.Zero(t, account.RewardQuota)
			assert.Zero(t, account.CreditReservedQuota)
			assert.EqualValues(t, 90, account.CreditOutstandingQuota)
			var debt AgentSimpleCreditDebt
			require.NoError(t, DB.Where("code_id = ?", spent.Id).First(&debt).Error)
			assert.Zero(t, debt.PaidQuota)
			assert.EqualValues(t, 90, debt.Quota)
		})
	}
}

func TestAgentSimpleWalletTransferPreservesMaximumLengthRequestID(t *testing.T) {
	_, agent := walletTransferFixture(t, 100, 0, 0)
	request := AgentSimpleTransferRequest{RequestID: strings.Repeat("w", 96), Quota: 73}
	first, err := TransferWalletToAgentSimple(agent, request)
	require.NoError(t, err)
	replay, err := TransferWalletToAgentSimple(agent, request)
	require.NoError(t, err)
	assert.Equal(t, first, replay)
	assert.EqualValues(t, 73, first.WalletQuota)
	assert.Equal(t, 27, getUserBalances(t, agent.UserID).QuotaPaid)
	var walletRows []BalanceLedger
	require.NoError(t, DB.Where("user_id = ?", agent.UserID).Find(&walletRows).Error)
	require.Len(t, walletRows, 1)
	assert.Equal(t, request.RequestID, walletRows[0].ReferenceId)
	assert.Contains(t, walletRows[0].IdempotencyKey, request.RequestID)
	assert.Equal(t, -73, walletRows[0].Delta)
	var agentRows []AgentSimpleLedger
	require.NoError(t, DB.Where("agent_user_id = ?", agent.UserID).Find(&agentRows).Error)
	require.Len(t, agentRows, 1)
	assert.Equal(t, request.RequestID, agentRows[0].RequestID)
	assert.Equal(t, request.RequestID, agentRows[0].ReferenceID)
	assert.EqualValues(t, 73, agentRows[0].DeltaWalletQuota)
	assert.EqualValues(t, 73, agentRows[0].WalletAfter)
}

func TestAgentSimpleWalletTransferRequestIDCannotReplayAnotherOwnersReceipt(t *testing.T) {
	_, agent := walletTransferFixture(t, 100, 0, 0)
	other := agentSimpleIdentity(t, agentTestOtherID, "wallet-other-owner-session")
	require.NoError(t, DB.Create(&AgentSimpleAccount{AgentUserID: other.UserID, Status: AgentSimpleStatusActive, Version: 1}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", other.UserID).Updates(map[string]any{"quota": 200, "quota_paid": 200}).Error)
	request := AgentSimpleTransferRequest{RequestID: "wallet-shared-request-id", Quota: 60}
	original, err := TransferWalletToAgentSimple(agent, request)
	require.NoError(t, err)
	otherView, err := TransferWalletToAgentSimple(other, request)
	require.ErrorIs(t, err, ErrAgentQuotaConflict)
	assert.Nil(t, otherView)
	assert.Equal(t, 40, getUserBalances(t, agent.UserID).QuotaPaid)
	assert.Equal(t, 200, getUserBalances(t, other.UserID).QuotaPaid)
	var otherAccount AgentSimpleAccount
	require.NoError(t, DB.First(&otherAccount, other.UserID).Error)
	assert.Zero(t, otherAccount.WalletQuota)
	for _, schema := range []any{&BalanceLedger{}, &AgentSimpleLedger{}} {
		var count int64
		column := "user_id"
		if _, ok := schema.(*AgentSimpleLedger); ok {
			column = "agent_user_id"
		}
		require.NoError(t, DB.Model(schema).Where(column+" = ?", other.UserID).Count(&count).Error)
		assert.Zero(t, count)
	}
	replay, err := TransferWalletToAgentSimple(agent, request)
	require.NoError(t, err)
	assert.Equal(t, original, replay)
	request.RequestID = "wallet-other-owner-new-id"
	view, err := TransferWalletToAgentSimple(other, request)
	require.NoError(t, err)
	assert.EqualValues(t, 60, view.WalletQuota)
	assert.Equal(t, 140, getUserBalances(t, other.UserID).QuotaPaid)
}

// walletLegacyAgentSimpleAccount preserves the pre-wallet A07 schema.
type walletLegacyAgentSimpleAccount struct {
	AgentUserID             int    `json:"agent_user_id" gorm:"primaryKey;autoIncrement:false"`
	Status                  string `json:"status" gorm:"type:varchar(16);not null"`
	CashQuota               int64  `json:"cash_quota" gorm:"type:bigint;not null;default:0"`
	RewardQuota             int64  `json:"reward_quota" gorm:"type:bigint;not null;default:0"`
	RootQuota               int64  `json:"root_quota" gorm:"type:bigint;not null;default:0"`
	CreditLimitQuota        int64  `json:"credit_limit_quota" gorm:"type:bigint;not null;default:0"`
	CreditReservedQuota     int64  `json:"credit_reserved_quota" gorm:"type:bigint;not null;default:0"`
	CreditOutstandingQuota  int64  `json:"credit_outstanding_quota" gorm:"type:bigint;not null;default:0"`
	PaymentPendingQuota     int64  `json:"payment_pending_quota" gorm:"type:bigint;not null;default:0"`
	PaymentAvailableQuota   int64  `json:"payment_available_quota" gorm:"type:bigint;not null;default:0"`
	PaymentTotalQuota       int64  `json:"payment_total_quota" gorm:"type:bigint;not null;default:0"`
	PaymentDebtQuota        int64  `json:"payment_debt_quota" gorm:"type:bigint;not null;default:0"`
	PaymentQuota            int64  `json:"payment_quota" gorm:"type:bigint;not null;default:0"`
	PaymentReversedQuota    int64  `json:"payment_reversed_quota" gorm:"type:bigint;not null;default:0"`
	PaymentWithdrawnQuota   int64  `json:"payment_withdrawn_quota" gorm:"type:bigint;not null;default:0"`
	PaymentTransferredQuota int64  `json:"payment_transferred_quota" gorm:"type:bigint;not null;default:0"`
	PaymentOffsetQuota      int64  `json:"payment_offset_quota" gorm:"type:bigint;not null;default:0"`
	Version                 int64  `json:"version" gorm:"not null;default:1"`
	LastActivityAt          int64  `json:"last_activity_at" gorm:"not null;default:0"`
	CreatedAt               int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt               int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (walletLegacyAgentSimpleAccount) TableName() string { return "agent_simple_accounts" }

// walletLegacyAgentSimpleLedger preserves the pre-wallet A07 schema.
type walletLegacyAgentSimpleLedger struct {
	DeltaCreditLimit        int64  `json:"delta_credit_limit" gorm:"type:bigint;not null;default:0"`
	CreditLimitAfter        int64  `json:"credit_limit_after" gorm:"type:bigint;not null;default:0"`
	ID                      int64  `json:"id" gorm:"primaryKey"`
	EventKey                string `json:"-" gorm:"type:varchar(160);not null;uniqueIndex"`
	AgentUserID             int    `json:"agent_user_id" gorm:"not null;index"`
	ActorUserID             int    `json:"actor_user_id" gorm:"not null"`
	Kind                    string `json:"kind" gorm:"type:varchar(32);not null"`
	ReferenceType           string `json:"reference_type" gorm:"type:varchar(32);not null"`
	ReferenceID             string `json:"reference_id" gorm:"type:varchar(128);not null"`
	RequestID               string `json:"request_id" gorm:"type:varchar(96)"`
	DeltaCashQuota          int64  `json:"delta_cash_quota" gorm:"type:bigint;not null"`
	DeltaRewardQuota        int64  `json:"delta_reward_quota" gorm:"type:bigint;not null"`
	DeltaRootQuota          int64  `json:"delta_root_quota" gorm:"type:bigint;not null"`
	DeltaCreditReserved     int64  `json:"delta_credit_reserved" gorm:"type:bigint;not null"`
	DeltaCreditOutstanding  int64  `json:"delta_credit_outstanding" gorm:"type:bigint;not null"`
	DeltaPaymentPending     int64  `json:"delta_payment_pending" gorm:"type:bigint;not null"`
	DeltaPaymentAvailable   int64  `json:"delta_payment_available" gorm:"type:bigint;not null"`
	DeltaPaymentTotal       int64  `json:"delta_payment_total" gorm:"type:bigint;not null"`
	DeltaPaymentDebt        int64  `json:"delta_payment_debt" gorm:"type:bigint;not null"`
	DeltaPaymentQuota       int64  `json:"delta_payment_quota" gorm:"type:bigint;not null"`
	DeltaPaymentReversed    int64  `json:"delta_payment_reversed" gorm:"type:bigint;not null"`
	DeltaPaymentWithdrawn   int64  `json:"delta_payment_withdrawn" gorm:"type:bigint;not null"`
	DeltaPaymentTransferred int64  `json:"delta_payment_transferred" gorm:"type:bigint;not null"`
	DeltaPaymentOffset      int64  `json:"delta_payment_offset" gorm:"type:bigint;not null"`
	CashAfter               int64  `json:"cash_after" gorm:"type:bigint;not null"`
	RewardAfter             int64  `json:"reward_after" gorm:"type:bigint;not null"`
	RootAfter               int64  `json:"root_after" gorm:"type:bigint;not null"`
	CreditReservedAfter     int64  `json:"credit_reserved_after" gorm:"type:bigint;not null"`
	CreditOutstandingAfter  int64  `json:"credit_outstanding_after" gorm:"type:bigint;not null"`
	PaymentPendingAfter     int64  `json:"payment_pending_after" gorm:"type:bigint;not null"`
	PaymentAvailableAfter   int64  `json:"payment_available_after" gorm:"type:bigint;not null"`
	PaymentTotalAfter       int64  `json:"payment_total_after" gorm:"type:bigint;not null"`
	PaymentDebtAfter        int64  `json:"payment_debt_after" gorm:"type:bigint;not null"`
	PaymentAfter            int64  `json:"payment_after" gorm:"type:bigint;not null"`
	PaymentReversedAfter    int64  `json:"payment_reversed_after" gorm:"type:bigint;not null"`
	PaymentWithdrawnAfter   int64  `json:"payment_withdrawn_after" gorm:"type:bigint;not null"`
	PaymentTransferredAfter int64  `json:"payment_transferred_after" gorm:"type:bigint;not null"`
	PaymentOffsetAfter      int64  `json:"payment_offset_after" gorm:"type:bigint;not null"`
	Reason                  string `json:"reason" gorm:"type:varchar(255);not null"`
	CreatedAt               int64  `json:"created_at" gorm:"autoCreateTime;index"`
}

func (walletLegacyAgentSimpleLedger) TableName() string { return "agent_simple_ledgers" }

func TestAgentSimpleWalletSchemaUpgradePreservesBalancesAndLedgerUniqueness(t *testing.T) {
	if os.Getenv("TEST_A07_SCHEMA_COMPAT") != "1" {
		t.Skip("set TEST_A07_SCHEMA_COMPAT=1 with a disposable database")
	}
	setupLedgerTestState(t)
	require.NoError(t, DB.Migrator().DropTable(&AgentSimpleLedger{}, &AgentSimpleAccount{}))
	t.Cleanup(func() { require.NoError(t, DB.AutoMigrate(&AgentSimpleAccount{}, &AgentSimpleLedger{})) })
	require.NoError(t, DB.AutoMigrate(&walletLegacyAgentSimpleAccount{}, &walletLegacyAgentSimpleLedger{}))
	oldAccount := walletLegacyAgentSimpleAccount{AgentUserID: agentTestOwnerID, Status: AgentSimpleStatusPaused, CashQuota: 150, RewardQuota: 70, RootQuota: 30, Version: 4}
	oldLedger := walletLegacyAgentSimpleLedger{EventKey: "wallet-upgrade-existing-event", AgentUserID: agentTestOwnerID, ActorUserID: agentTestAdminID, Kind: "adjustment", ReferenceType: "test", ReferenceID: "wallet-upgrade-reference", DeltaCashQuota: 150, DeltaRewardQuota: 70, DeltaRootQuota: 30, CashAfter: 150, RewardAfter: 70, RootAfter: 30, Reason: "Synthetic existing ledger"}
	require.NoError(t, DB.Create(&oldAccount).Error)
	require.NoError(t, DB.Create(&oldLedger).Error)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&AgentSimpleAccount{}, &AgentSimpleLedger{}))
		var account AgentSimpleAccount
		require.NoError(t, DB.First(&account, agentTestOwnerID).Error)
		assert.Zero(t, account.WalletQuota)
		assert.EqualValues(t, 150, account.CashQuota)
		assert.EqualValues(t, 70, account.RewardQuota)
		assert.EqualValues(t, 30, account.RootQuota)
		assert.EqualValues(t, 4, account.Version)
		assert.Equal(t, AgentSimpleStatusPaused, account.Status)
		var ledger AgentSimpleLedger
		require.NoError(t, DB.First(&ledger, oldLedger.ID).Error)
		assert.Zero(t, ledger.DeltaWalletQuota)
		assert.Zero(t, ledger.WalletAfter)
		assert.EqualValues(t, 150, ledger.DeltaCashQuota)
		assert.EqualValues(t, 70, ledger.DeltaRewardQuota)
		assert.EqualValues(t, 30, ledger.DeltaRootQuota)
		assert.EqualValues(t, 150, ledger.CashAfter)
		assert.EqualValues(t, 70, ledger.RewardAfter)
		assert.EqualValues(t, 30, ledger.RootAfter)
		assert.Equal(t, oldLedger.EventKey, ledger.EventKey)
	}
	duplicate := AgentSimpleLedger{EventKey: oldLedger.EventKey, AgentUserID: agentTestOwnerID, ActorUserID: agentTestAdminID, Kind: "adjustment", ReferenceType: "test", ReferenceID: "duplicate", Reason: "Duplicate event must fail"}
	require.Error(t, DB.Create(&duplicate).Error)
	var count int64
	require.NoError(t, DB.Model(&AgentSimpleLedger{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
