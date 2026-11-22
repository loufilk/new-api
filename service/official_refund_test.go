package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Historical test names are retained for the reviewed CI selector contract.
// Their assertions now require atomic financial outcomes, not old parity bugs.
func TestOfficialRequestRefundMarksBeforeSourceAndDoesNotRetry(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	s := currentRefundSession(t, "request-source-failure", 100)
	fault := injectOfficialUpdateFault(t, "users", officialFailureBeforeSQL)
	c := testGinContext(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("POST", "/refund", nil).WithContext(ctx)
	s.Refund(c)
	waitRefundCallback(t, s)
	fault.requireOneHit(t)
	assertUserBalances(t, 31201, 0, 900, 0)
	assert.Equal(t, 900, getTokenRemainQuota(t, 31202))
	var pending model.BillingFinalization
	require.NoError(t, model.DB.Where("request_id = ?", s.relayInfo.RequestId).First(&pending).Error)
	assert.Equal(t, model.BillingRefundReady, pending.State)
	assert.Positive(t, pending.Attempts, "a durable failed attempt is inspectable and recoverable")
	fault.disable()
	executeBillingRefund(t.Context(), pending.RequestID)
	executeBillingRefund(t.Context(), pending.RequestID)
	assertUserBalances(t, 31201, 0, 1000, 0)
	assert.Equal(t, 1000, getTokenRemainQuota(t, 31202))
	require.NoError(t, model.DB.Where("request_id = ?", pending.RequestID).First(&pending).Error)
	assert.Equal(t, model.BillingRefunded, pending.State)
	s.Refund(c)
	waitRefundCallback(t, s)
	fault.requireOneHit(t)
}

func TestOfficialRequestRefundTokenFailureKeepsSourceCredit(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	s := currentRefundSession(t, "request-token-failure", 100)
	fault := injectOfficialUpdateFault(t, "tokens", officialFailureBeforeSQL)
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	fault.requireOneHit(t)
	assertUserBalances(t, 31201, 0, 900, 0)
	assert.Equal(t, 900, getTokenRemainQuota(t, 31202))
	var pending model.BillingFinalization
	require.NoError(t, model.DB.Where("request_id = ?", s.relayInfo.RequestId).First(&pending).Error)
	assert.Equal(t, model.BillingRefundReady, pending.State)
	fault.disable()
	executeBillingRefund(t.Context(), pending.RequestID)
	executeBillingRefund(t.Context(), pending.RequestID)
	assertUserBalances(t, 31201, 0, 1000, 0)
	assert.Equal(t, 1000, getTokenRemainQuota(t, 31202))
	fault.requireOneHit(t)
}

func TestOfficialSubscriptionBaseFailureStillReturnsExtraAndToken(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	s, cancel := settlementDeadlineSession(t, "subscription-refund-atomic", true, BillingSourceSubscription)
	cancel()
	require.NoError(t, s.Reserve(150))
	require.EqualValues(t, 650, getSubscriptionUsed(t, 93203))
	fault := injectOfficialUpdateFault(t, "subscription_pre_consume_records", officialFailureBeforeSQL)
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	fault.requireOneHit(t)
	assert.EqualValues(t, 650, getSubscriptionUsed(t, 93203), "base and extra holds must both roll back")
	assert.Equal(t, 850, getTokenRemainQuota(t, 93202))
	fault.disable()
	executeBillingRefund(t.Context(), s.relayInfo.RequestId)
	executeBillingRefund(t.Context(), s.relayInfo.RequestId)
	assert.EqualValues(t, 500, getSubscriptionUsed(t, 93203), "unrelated usage remains charged")
	assert.Equal(t, 1000, getTokenRemainQuota(t, 93202))
	fault.requireOneHit(t)
}

func TestOfficialTaskRefundIndependentStages(t *testing.T) {
	for _, stage := range []string{"source", "token", "quota_clear"} {
		t.Run(stage, func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 40)
			task := seedReceiptTask(t, user, token, channel, 300)
			markReceiptTaskFailed(t, task)
			table := map[string]string{"source": "users", "token": "tokens", "quota_clear": "tasks"}[stage]
			fault := injectOfficialUpdateFault(t, table, officialFailureBeforeSQL)
			require.False(t, RefundTaskQuota(t.Context(), task, "atomic stage failure"))
			fault.requireOneHit(t)
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, 300, persisted.Quota)
			assertUserBalances(t, user.Id, 0, 840, 0)
			assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, countReceiptLogs(t, user.Id))
			for _, entity := range []any{&model.BalanceLedgerOperation{}, &model.BalanceLedger{}} {
				var rows int64
				require.NoError(t, model.DB.Model(entity).Where("user_id = ? AND idempotency_key = ?", user.Id, fmt.Sprintf("task:financial:%d:0", task.ID)).Count(&rows).Error)
				assert.Zero(t, rows)
			}
			fault.disable()
			require.True(t, RefundTaskQuota(t.Context(), &persisted, "retry after rollback"))
			require.True(t, RefundTaskQuota(t.Context(), task, "stale retry after commit"))
			assertUserBalances(t, user.Id, 100, 1000, 40)
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, getTokenUsedQuota(t, token.Id))
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		})
	}
}

func TestOfficialRefundPostCommitErrorsDoNotRollBack(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	t.Run("request_token", func(t *testing.T) {
		s := currentRefundSession(t, "refund-real-commit-lost-ack", 100)
		receipt, err := s.terminalReceiptLocked()
		require.NoError(t, err)
		_, err = model.RequestBillingRefund(t.Context(), receipt)
		require.NoError(t, err)
		flag := terminalCommitFault(t, 2)
		applied, err := model.ApplyBillingRefund(t.Context(), receipt.RequestID)
		require.ErrorContains(t, err, "synthetic committed ACK lost")
		require.False(t, applied)
		require.Zero(t, flag.Load(), "the real COMMIT fault must be consumed")
		assertUserBalances(t, 31201, 0, 1000, 0)
		assert.Equal(t, 1000, getTokenRemainQuota(t, 31202))
		applied, err = model.ApplyBillingRefund(t.Context(), receipt.RequestID)
		require.NoError(t, err)
		assert.False(t, applied, "the durable refund owner prevents double credit after lost ACK")
		s.Refund(testGinContext(t))
		waitRefundCallback(t, s)
		assert.Equal(t, 1000, getTokenRemainQuota(t, 31202))
	})
	t.Run("task_quota_clear", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 40)
		task := seedReceiptTask(t, user, token, channel, 300)
		markReceiptTaskFailed(t, task)
		taskFinancialCommitFault(t, 2)
		require.False(t, RefundTaskQuota(t.Context(), task, "real committed refund ACK lost"))
		var persisted model.Task
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.Zero(t, persisted.Quota)
		assert.Empty(t, persisted.PrivateData.WalletDebit)
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		require.True(t, RefundTaskQuota(t.Context(), &persisted, "reconcile committed zero quota"))
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	})
}

func TestOfficialTaskHistoricalPendingIsReadOnly(t *testing.T) {
	for _, kind := range []string{"flag", "private_only", "both"} {
		t.Run(kind, func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 40)
			task := seedReceiptTask(t, user, token, channel, 100)
			task.Status = model.TaskStatusInProgress
			task.Progress = "50%"
			task.SubmitTime = time.Now().Add(-48 * time.Hour).Unix()
			task.PrivateData.FinancialVersion = 0
			task.PrivateData.SubmissionAccounted = false
			task.PrivateData.TokenFingerprint = ""
			task.SettlementPending = kind != "private_only"
			if kind != "flag" {
				task.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "submit", Quota: 200}
			}
			require.NoError(t, model.DB.Save(task).Error)
			before := *task
			require.False(t, RefundTaskQuota(t.Context(), task, "not a new official intent"))
			RecalculateTaskQuota(t.Context(), task, 0, common.NewMessage("must not settle"))
			require.NoError(t, ApplyTaskPollResult(t.Context(), nil, task, relaycommon.FailTaskInfo("failure"), nil))
			finalizeTerminalTask(t.Context(), nil, task, relaycommon.FailTaskInfo("failure"))
			oldTimeout := constant.TaskTimeoutMinutes
			constant.TaskTimeoutMinutes = 1
			t.Cleanup(func() { constant.TaskTimeoutMinutes = oldTimeout })
			sweepTimedOutTasks(t.Context())
			require.NoError(t, model.DB.First(task, task.ID).Error)
			assert.Equal(t, before, *task, "compatibility boundary must not rewrite status, quota, metadata or old intent")
			assertUserBalances(t, user.Id, 0, 1000, 40)
			assert.Equal(t, 1900, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, countReceiptLogs(t, user.Id))
			for _, active := range model.GetAllUnFinishSyncTasks(100) {
				assert.NotEqual(t, task.ID, active.ID)
			}
		})
	}
}

func TestOfficial093WalletSnapshotLimitationsRemainExplicit(t *testing.T) {
	t.Run("small_three_account_remainder", func(t *testing.T) {
		original := []model.LedgerShare{{Account: model.AccountBonus, Amount: 1}, {Account: model.AccountReward, Amount: 1}, {Account: model.AccountPaid, Amount: 1}}
		require.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 1}, {Account: model.AccountReward, Amount: 1}}, SplitRefund(original, 2))
	})
	for _, order := range []string{"submit_before_task_refund", "task_refund_before_submit"} {
		t.Run(order, func(t *testing.T) {
			truncate(t)
			seedBonusUser(t, 31201, 1, 1)
			seedToken(t, 31202, 31201, "synthetic-original-refund-token", 1000)
			seedChannel(t, 31204)
			request := "net-submit-" + order
			require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
			seedBonusEligibleModel(t, "owned-submit-allocation", boolPtr(true))
			info := &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", RequestId: request, OriginModelName: "owned-submit-allocation", ForcePreConsume: true, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 31204}, UserQuota: 2, UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningThreshold: -1}}
			c := testGinContext(t)
			c.Set("token_quota", 1000)
			s, apiErr := NewBillingSession(c, info, 2)
			require.Nil(t, apiErr)
			info.Billing = s
			var held model.BillingFinalization
			require.NoError(t, model.DB.Where("request_id = ?", request).First(&held).Error)
			require.Equal(t, model.BillingHeld, held.State)
			require.Equal(t, 2, held.SourceHeld)
			require.Equal(t, 2, held.TokenHeld)
			task := makeTask(31201, 31204, 1, 31202, BillingSourceWallet, 0)
			task.PrivateData.RequestId = request
			task.PrivateData.WalletDebit = WalletSessionDebitShares(info)
			if order == "task_refund_before_submit" {
				require.False(t, RefundTaskQuota(t.Context(), task, "unpersisted task cannot authorize money"))
				assertUserBalances(t, 31201, 0, 0, 0)
			}
			require.NoError(t, PersistTaskBilling(info, task))
			var saved model.Task
			require.NoError(t, model.DB.First(&saved, task.ID).Error)
			assert.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 1}}, saved.PrivateData.WalletDebit, "persist only final outstanding shares after the submit adjustment")
			assertUserBalances(t, 31201, 1, 0, 0)
			markReceiptTaskFailed(t, &saved)
			require.True(t, RefundTaskQuota(t.Context(), &saved, "provider confirmed failure"))
			require.True(t, RefundTaskQuota(t.Context(), &saved, "duplicate failure"))
			assertUserBalances(t, 31201, 1, 1, 0)
			assert.Equal(t, 1000, getTokenRemainQuota(t, 31202))
			require.ErrorIs(t, s.Settle(1), model.ErrBillingFinalized)
		})
	}
}
