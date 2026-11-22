package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentSimpleWithdrawalCombinesSourcesAndSharesMonthlyLimit(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("aff_quota", 8_000_000).Error)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"payment_total_quota": 5_000_000, "payment_available_quota": 5_000_000}).Error)
	request := AgentSimpleWithdrawalRequest{RequestID: "a07-combined-withdraw", UserID: agent.UserID, ReferralQuota: 6_000_000, PaymentQuota: 4_000_000, Reason: "Customer support payout", Evidence: "Synthetic completed payout"}
	first, err := RecordAgentSimpleWithdrawal(root, request)
	require.NoError(t, err)
	assert.EqualValues(t, 10_000_000, first.Quota)
	assert.EqualValues(t, 200_000, first.FeeQuota)
	assert.EqualValues(t, 9_800_000, first.NetQuota)
	replay, err := RecordAgentSimpleWithdrawal(root, request)
	require.NoError(t, err)
	assert.Equal(t, first.ID, replay.ID)
	assert.Equal(t, 2_000_000, getUserBalances(t, agent.UserID).AffQuota)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.EqualValues(t, 1_000_000, account.PaymentAvailableQuota)
	request.RequestID = "a07-withdraw-insufficient"
	_, err = RecordAgentSimpleWithdrawal(root, request)
	require.ErrorIs(t, err, ErrAgentSimpleInsufficient)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("aff_quota", 12_000_000).Error)
	request.ReferralQuota, request.PaymentQuota = 10_000_000, 0
	_, err = RecordAgentSimpleWithdrawal(root, request)
	require.ErrorIs(t, err, ErrAgentSimpleConflict, "source changes cannot bypass the calendar-month limit")
}

func TestAgentSimpleWithdrawalKeepsReferralRecoverySeparateFromPaymentIncome(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("aff_quota", -500_000).Error)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"payment_total_quota": 10_000_000, "payment_available_quota": 10_000_000}).Error)
	request := AgentSimpleWithdrawalRequest{RequestID: "a07-negative-referral", UserID: agent.UserID, PaymentQuota: 10_000_000, Reason: "Synthetic payment income payout", Evidence: "Synthetic completed payout"}
	withReferral := request
	withReferral.ReferralQuota = 1
	_, err := RecordAgentSimpleWithdrawal(root, withReferral)
	require.ErrorIs(t, err, ErrAgentSimpleInsufficient)
	result, err := RecordAgentSimpleWithdrawal(root, request)
	require.NoError(t, err, "a referral recovery must not block independent payment income")
	assert.EqualValues(t, 10_000_000, result.PaymentQuota)
	assert.Zero(t, result.ReferralQuota)
	assert.Equal(t, -500_000, getUserBalances(t, agent.UserID).AffQuota)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.PaymentAvailableQuota)
	assert.EqualValues(t, 10_000_000, account.PaymentWithdrawnQuota)
}

func TestAgentSimpleWithdrawalKeepsPaymentRecoverySeparateFromReferralIncome(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("aff_quota", 15_000_000).Error)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"payment_reversed_quota": 2_500_000, "payment_debt_quota": 2_500_000}).Error)
	request := AgentSimpleWithdrawalRequest{RequestID: "a07-independent-referral", UserID: agent.UserID, ReferralQuota: 15_000_000, Reason: "Synthetic referral payout", Evidence: "Synthetic completed payout"}
	withPayment := request
	withPayment.PaymentQuota = 1
	_, err := RecordAgentSimpleWithdrawal(root, withPayment)
	require.ErrorIs(t, err, ErrAgentSimpleInsufficient)
	for range 2 {
		result, err := RecordAgentSimpleWithdrawal(root, request)
		require.NoError(t, err)
		assert.Zero(t, result.PaymentQuota)
		assert.EqualValues(t, 15_000_000, result.ReferralQuota)
	}
	assert.Zero(t, getUserBalances(t, agent.UserID).AffQuota)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.EqualValues(t, 2_500_000, account.PaymentDebtQuota)
	assert.Zero(t, account.PaymentAvailableQuota)
}

func TestAgentSimplePaymentTransferOffsetsDebtBeforeMovingOnlyRemainingIncome(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("aff_quota", 500_000).Error)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"credit_limit_quota": 5_000_000, "credit_outstanding_quota": 5_000_000, "payment_total_quota": 15_000_000, "payment_available_quota": 15_000_000}).Error)
	require.NoError(t, DB.Create(&AgentSimpleCreditDebt{AgentUserID: agent.UserID, CodeID: 87101, Quota: 5_000_000}).Error)
	request := AgentSimplePaymentTransferCommand{RequestID: "a07-transfer-net", Quota: 10_000_001, Reason: "Synthetic transfer"}
	_, err := TransferAgentSimplePaymentProfit(agent, request)
	require.ErrorIs(t, err, ErrAgentSimpleInsufficient, "gross income cannot be transferred before repaying the debt")
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.EqualValues(t, 5_000_000, account.CreditOutstandingQuota, "an unsuccessful transfer must roll back its offset")
	request.Quota--
	for range 2 {
		_, err := TransferAgentSimplePaymentProfit(agent, request)
		require.NoError(t, err)
	}
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.CreditOutstandingQuota)
	assert.Zero(t, account.PaymentAvailableQuota)
	assert.EqualValues(t, 10_000_000, account.PaymentQuota)
	assert.EqualValues(t, 10_000_000, account.PaymentTransferredQuota)
	assert.EqualValues(t, 5_000_000, account.PaymentOffsetQuota)
	assert.Zero(t, account.CashQuota)
	assert.EqualValues(t, 500_000, getUserBalances(t, agent.UserID).AffQuota)
	require.NoError(t, validateAgentSimpleAccount(&account))
}

func TestAgentSimpleInactivityDependsOnSalesRatherThanRechargeOrOldCodeUse(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	old := common.GetTimestamp() - 91*24*60*60
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"created_at": old, "last_activity_at": common.GetTimestamp(), "cash_quota": 1_000_000}).Error)
	rows, _, err := ListAgentSimpleAgents(root, 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].Inactive, "non-sales activity must not clear the 90-day flag")
	code, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-recent-sale", Name: "Recent code", Quota: 500_000, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	rows, _, err = ListAgentSimpleAgents(root, 0, 20)
	require.NoError(t, err)
	assert.False(t, rows[0].Inactive)
	require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Model(&AgentSimpleLedger{}).Where("agent_user_id = ? AND kind = ?", agent.UserID, "code_issue").UpdateColumn("created_at", old).Error)
	_, err = Redeem(code.Key, agentTestRecipientID)
	require.NoError(t, err)
	rows, _, err = ListAgentSimpleAgents(root, 0, 20)
	require.NoError(t, err)
	assert.True(t, rows[0].Inactive, "redeeming an old code does not count as a new sale")
	simpleAccountingPayment(t, agent, "a07-new-customer-payment", 120)
	rows, _, err = ListAgentSimpleAgents(root, 0, 20)
	require.NoError(t, err)
	assert.False(t, rows[0].Inactive, "customer payment counts as sales activity")
}

func TestAgentSimpleWithdrawalOrdinaryUserAndFrozenRewardGuards(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	user := User{Username: "a07-withdraw-ordinary", AffCode: "a07-withdraw-ordinary", Status: common.UserStatusEnabled, AffQuota: 10_000_000}
	require.NoError(t, DB.Create(&user).Error)
	request := AgentSimpleWithdrawalRequest{RequestID: "a07-ordinary-withdraw", UserID: user.Id, ReferralQuota: 10_000_000, Reason: "Support payout", Evidence: "Synthetic paid receipt"}
	_, err := RecordAgentSimpleWithdrawal(agent, request)
	require.ErrorIs(t, err, ErrAgentSimplePermission)
	reward := ReferralCommission{UserId: user.Id, SourceUserId: agent.UserID, ReferenceType: ReferralReferenceTopUp, ReferenceId: "a07-withdraw-hold", RewardQuota: 10_000_000, PaidQuota: 200_000_000, Status: ReferralCommissionStatusFrozen, MaturesAt: common.GetTimestamp() + 3600}
	require.NoError(t, DB.Create(&reward).Error)
	_, err = RecordAgentSimpleWithdrawal(root, request)
	require.ErrorIs(t, err, ErrAgentSimpleInsufficient)
	require.NoError(t, DB.Model(&reward).Update("matures_at", common.GetTimestamp()-1).Error)
	_, err = RecordAgentSimpleWithdrawal(root, request)
	require.NoError(t, err, "ordinary users do not need an agent account to withdraw")
	assert.Zero(t, getUserBalances(t, user.Id).AffQuota)
}

func TestAgentSimpleWeeklySettlementOffsetsPaymentThenReferralAndRemindsWithoutPausing(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("aff_quota", 200).Error)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"credit_limit_quota": 1000, "credit_outstanding_quota": 800, "payment_total_quota": 500, "payment_available_quota": 500}).Error)
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, agentSimpleLocation)
	require.NoError(t, DB.Create(&AgentSimpleCreditDebt{AgentUserID: agent.UserID, CodeID: 999, Quota: 800, CreatedAt: monday.Unix() - 3600}).Error)
	for range 2 {
		_, err := runAgentSimpleWeeklySettlementAt(context.Background(), 100, monday)
		require.NoError(t, err)
	}
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.EqualValues(t, 100, account.CreditOutstandingQuota)
	assert.EqualValues(t, 500, account.PaymentOffsetQuota)
	assert.Zero(t, account.PaymentAvailableQuota)
	assert.Zero(t, getUserBalances(t, agent.UserID).AffQuota)
	var bills []AgentSimpleBill
	require.NoError(t, DB.Where("agent_user_id = ?", agent.UserID).Find(&bills).Error)
	require.Len(t, bills, 1)
	assert.EqualValues(t, 100, bills[0].Quota)
	for range 2 {
		_, err := runAgentSimpleWeeklySettlementAt(context.Background(), 100, monday.AddDate(0, 0, 3))
		require.NoError(t, err)
	}
	var reminders []AgentSimpleReminder
	require.NoError(t, DB.Where("agent_user_id = ?", agent.UserID).Find(&reminders).Error)
	assert.Len(t, reminders, 2, "one notice per day for each of agent and root")
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Equal(t, AgentSimpleStatusActive, account.Status)
	_, err := runAgentSimpleWeeklySettlementAt(context.Background(), 100, monday.AddDate(0, 0, 7))
	require.NoError(t, err)
	var billed int64
	require.NoError(t, DB.Model(&AgentSimpleBill{}).Where("agent_user_id = ?", agent.UserID).Select("SUM(quota - paid_quota)").Scan(&billed).Error)
	assert.EqualValues(t, 100, billed, "carried debt cannot be billed twice")
}

func TestAgentSimpleReferralTransferRetainsNonRefundableSourceAndReplays(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("aff_quota", 500_000).Error)
	request := AgentSimpleTransferRequest{RequestID: "a07-referral-to-agent", Quota: 500_000}
	for range 2 {
		_, err := TransferReferralToAgentBalance(agent, request)
		require.NoError(t, err)
	}
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.EqualValues(t, 500_000, account.RewardQuota)
	assert.Zero(t, account.CashQuota)
	assert.Zero(t, getUserBalances(t, agent.UserID).AffQuota)
}

func TestAgentSimpleRootHistoryAndCodeRevealAreScopedAndAudited(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	_, err := AdjustAgentSimpleBalance(root, AgentSimpleAdjustmentRequest{RequestID: "a07-query-balance", AgentUserID: agent.UserID, Delta: 500, Reason: "Synthetic root allocation"})
	require.NoError(t, err)
	code, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-query-code", Name: "Root inspection", Quota: 100, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	_, _, err = ListAgentSimpleAgents(agent, 0, 20)
	require.ErrorIs(t, err, ErrAgentSimplePermission)
	rows, total, err := ListAgentSimpleAgents(root, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 400, rows[0].AvailableQuota)
	for _, kind := range []string{"ledger", "bills", "reminders", "withdrawals", "recharges", "referrals"} {
		_, _, err = QueryAgentSimpleHistory(root, kind, agent.UserID, 0, 20)
		require.NoError(t, err, kind)
		_, _, err = QueryAgentSimpleHistory(agent, kind, root.UserID, 0, 20)
		require.ErrorIs(t, err, ErrAgentSimplePermission, kind)
	}
	_, err = RevealAgentSimpleCode(agent, code.Id, "Cannot inspect other credentials")
	require.ErrorIs(t, err, ErrAgentSimplePermission)
	key, err := RevealAgentSimpleCode(root, code.Id, "Customer support inspection")
	require.NoError(t, err)
	assert.Equal(t, code.Key, key)
	var audit AgentSimpleCodeViewAudit
	require.NoError(t, DB.Where("code_id = ?", code.Id).First(&audit).Error)
	assert.Equal(t, root.UserID, audit.ActorUserID)
	assert.Equal(t, "Customer support inspection", audit.Reason)
}

func TestAgentSimpleNewWeeklyDebtIsBilledNextMondayAndRepaymentUpdatesOnlyItsBill(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, agentSimpleLocation)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"credit_limit_quota": 200, "credit_outstanding_quota": 150}).Error)
	debts := []AgentSimpleCreditDebt{
		{AgentUserID: agent.UserID, CodeID: 1001, Quota: 100, CreatedAt: monday.Unix() - 1},
		{AgentUserID: agent.UserID, CodeID: 1002, Quota: 50, CreatedAt: monday.Unix() + 1},
	}
	require.NoError(t, DB.Create(&debts).Error)
	_, err := runAgentSimpleWeeklySettlementAt(context.Background(), 100, monday.AddDate(0, 0, 1))
	require.NoError(t, err)
	var first AgentSimpleBill
	require.NoError(t, DB.Where("agent_user_id = ? AND week_start = ?", agent.UserID, monday.Unix()).First(&first).Error)
	assert.EqualValues(t, 100, first.Quota)
	_, err = RepayAgentSimpleCredit(root, AgentSimpleRepaymentRequest{RequestID: "a07-bill-payment", AgentUserID: agent.UserID, Quota: 120, Reason: "Synthetic paid repayment"})
	require.NoError(t, err)
	require.NoError(t, DB.First(&first, first.ID).Error)
	assert.EqualValues(t, 100, first.PaidQuota)
	_, err = runAgentSimpleWeeklySettlementAt(context.Background(), 100, monday.AddDate(0, 0, 7))
	require.NoError(t, err)
	var second AgentSimpleBill
	require.NoError(t, DB.Where("agent_user_id = ? AND week_start = ?", agent.UserID, monday.AddDate(0, 0, 7).Unix()).First(&second).Error)
	assert.EqualValues(t, 30, second.Quota)
}

func TestAgentSimpleRootStatusCreatesProfileAndPauseClosesUnpaidLinks(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	newAgent := User{Username: "a07-new-agent", AffCode: "a07-new-agent", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&newAgent).Error)
	_, err := SetAgentSimpleStatus(root, AgentSimpleStatusRequest{RequestID: "a07-open-agent", AgentUserID: newAgent.Id, Status: AgentSimpleStatusActive, Reason: "Root opened this agent"})
	require.NoError(t, err)
	profile, err := GetAgentProfile(newAgent.Id)
	require.NoError(t, err)
	assert.True(t, profile.Allows(AgentCapabilitySales))
	_, err = CreateAgentSimplePaymentOrder(agent, AgentSimplePaymentRequest{RequestID: "a07-pause-link", Name: "Pending at pause", Quota: 500_000, SalePriceFen: 120, PaymentMethod: "alipay"}, "a07-pause-link", "synthetic-pause-link-token-00000000000000000", "a07-pause-trade", "https://pay.example.test", `{}`, "https://example.test")
	require.NoError(t, err)
	request := AgentSimpleStatusRequest{RequestID: "a07-pause-agent", AgentUserID: agent.UserID, Status: AgentSimpleStatusPaused, Reason: "Root paused new sales"}
	_, err = SetAgentSimpleStatus(root, request)
	require.NoError(t, err)
	_, err = SetAgentSimpleStatus(root, request)
	require.NoError(t, err)
	public, err := GetAgentSimplePublicPayment("a07-pause-link", "synthetic-pause-link-token-00000000000000000")
	require.NoError(t, err)
	assert.Equal(t, AgentSimplePaymentClosed, public.Status)
	assert.Empty(t, public.PaymentURL)
	_, err = CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-paused-new-code", Name: "Blocked", Quota: 100, Mode: AgentSimpleCodeDirect})
	require.ErrorIs(t, err, ErrAgentSimpleUnavailable)
	_, err = GetAgentSimpleAccount(agent, agent.UserID)
	require.NoError(t, err, "pausing sales must preserve read access to balances")
}

func TestAgentSimpleExitMovesRewardToWalletExactlyOnce(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"status": AgentSimpleStatusCancelled, "reward_quota": 1_000_000, "root_quota": 0}).Error)
	record, err := SettleAgentSimpleExit(root, AgentSimpleExitRequest{RequestID: "a07-exit-to-wallet", AgentUserID: agent.UserID, Quota: 1_000_000, Source: "income", Target: "wallet", Reason: "Move remaining reward", Evidence: "Synthetic exit evidence"})
	require.NoError(t, err)
	assert.Zero(t, record.NetFen)
	assert.EqualValues(t, 1_000_000, getUserBalances(t, agent.UserID).QuotaPaid)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.RewardQuota)
	require.NoError(t, DB.First(&record, record.ID).Error)
	assert.Equal(t, "wallet", record.Target)
	replay, err := SettleAgentSimpleExit(root, AgentSimpleExitRequest{RequestID: "a07-exit-to-wallet", AgentUserID: agent.UserID, Quota: 1_000_000, Source: "income", Target: "wallet", Reason: "Move remaining reward", Evidence: "Synthetic exit evidence"})
	require.NoError(t, err)
	assert.Equal(t, record.ID, replay.ID)
}

func TestAgentSimpleCashExitRecordsDiscountFeeAndFenRoundingExactlyOnce(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		quota, netFen int64
	}{{"thousand_dollars", 500_000_000, 87_300}, {"fractional_fen", 505_000, 88}} {
		t.Run(scenario.name, func(t *testing.T) {
			root, agent := simpleAccountingFixture(t)
			require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"status": AgentSimpleStatusPaused, "cash_quota": scenario.quota}).Error)
			request := AgentSimpleExitRequest{RequestID: "a07-exit-cash-refund", AgentUserID: agent.UserID, Quota: scenario.quota, Source: "cash", Target: "refund", Reason: "Synthetic cash refund", Evidence: "Synthetic completed refund"}
			first, err := SettleAgentSimpleExit(root, request)
			require.NoError(t, err)
			assert.Equal(t, scenario.netFen, first.NetFen)
			replay, err := SettleAgentSimpleExit(root, request)
			require.NoError(t, err)
			assert.Equal(t, first.ID, replay.ID)
			var account AgentSimpleAccount
			require.NoError(t, DB.First(&account, agent.UserID).Error)
			assert.Zero(t, account.CashQuota)
			assert.Zero(t, getUserBalances(t, agent.UserID).QuotaPaid)
			request.RequestID = "a07-exit-income-not-cash"
			request.Source = "income"
			_, err = SettleAgentSimpleExit(root, request)
			require.ErrorIs(t, err, ErrAgentSimpleInvalid)
		})
	}
}

func TestAgentSimpleExitSettleAllPreservesSubCentQuota(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	_, err := SetAgentSimpleStatus(root, AgentSimpleStatusRequest{RequestID: "a07-exit-all-pause", AgentUserID: agent.UserID, Status: AgentSimpleStatusPaused, Reason: "Synthetic exit preparation"})
	require.NoError(t, err)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("cash_quota", 555).Error)
	record, err := SettleAgentSimpleExit(root, AgentSimpleExitRequest{RequestID: "a07-exit-all-wallet", AgentUserID: agent.UserID, Source: "cash", Target: "wallet", SettleAll: true, Reason: "Settle exact remaining balance", Evidence: "Synthetic exact settlement"})
	require.NoError(t, err)
	assert.EqualValues(t, 555, record.Quota)
	assert.Zero(t, record.NetFen)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.CashQuota)
	assert.EqualValues(t, 555, getUserBalances(t, agent.UserID).QuotaPaid)
	replay, err := SettleAgentSimpleExit(root, AgentSimpleExitRequest{RequestID: "a07-exit-all-wallet", AgentUserID: agent.UserID, Source: "cash", Target: "wallet", SettleAll: true, Reason: "Settle exact remaining balance", Evidence: "Synthetic exact settlement"})
	require.NoError(t, err)
	assert.Equal(t, record.ID, replay.ID)
}
