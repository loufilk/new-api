package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All financial assertions run on an isolated real PostgreSQL schema.
func TestFinancialReviewFixesPostgres(t *testing.T) {
	openTaskPollingSQLFixture(t, "postgres")
	require.NoError(t, model.DB.AutoMigrate(&model.BillingFinalization{}, &model.TopUp{}, &model.Model{}, &model.Vendor{}, &model.ModelBonusEligibleAudit{}, &model.ReferralCommission{}, &model.SubscriptionPlan{}, &model.Midjourney{}, &model.Redemption{}, &model.SubscriptionOrder{}, &model.SystemTask{}, &model.SystemTaskLock{}))
	t.Run("creem_manual_preserves_quota_units", testCreemManualQuotaUnits)
	t.Run("poll_failure_preserves_hold", testPollFailurePreservesHold)
	t.Run("timeout_preserves_hold", testTimeoutPreservesHold)
	t.Run("timeout_old_and_new", TestSweepTimedOutTasksPreservesAllHeldEvidence)
	t.Run("single_poll_classification", TestUpdateVideoSingleTaskPollClassification)
	t.Run("batch_poll_classification", TestUpdateBatchTasksPollClassification)
	t.Run("trusted_failure_refunds_once", TestUpdateSunoTasksStalePollsRefundExactlyOnce)
}

func testCreemManualQuotaUnits(t *testing.T) {
	const quota = 1_000_000
	for _, manualFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "manual_first", false: "callback_first"}[manualFirst], func(t *testing.T) {
			user := &model.User{Username: "creem-" + common.GetRandomString(10), AffCode: common.GetRandomString(10), Status: common.UserStatusEnabled}
			require.NoError(t, model.DB.Create(user).Error)
			order := &model.TopUp{UserId: user.Id, Amount: quota, Money: 2, TradeNo: "creem-" + common.GetRandomString(16), PaymentMethod: model.PaymentMethodCreem, PaymentProvider: model.PaymentProviderCreem, Status: common.TopUpStatusPending}
			require.NoError(t, order.Insert())
			if manualFirst {
				require.NoError(t, model.ManualCompleteTopUp(order.TradeNo, ""))
			} else {
				require.NoError(t, model.RechargeCreem(order.TradeNo, "", "", ""))
			}
			var initialTopupLogs int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeTopup).Count(&initialTopupLogs).Error)
			require.EqualValues(t, 1, initialTopupLogs, "the first completed credit must record one success log")
			require.NoError(t, model.ManualCompleteTopUp(order.TradeNo, ""))
			require.NoError(t, model.RechargeCreem(order.TradeNo, "", "", ""), "a completed order acknowledges callback replay without a second credit")
			var replayedTopupLogs int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeTopup).Count(&replayedTopupLogs).Error)
			assert.Equal(t, initialTopupLogs, replayedTopupLogs, "completion replay must not append a zero-credit success log")
			var stored model.User
			require.NoError(t, model.DB.First(&stored, user.Id).Error)
			assert.Equal(t, quota, stored.QuotaPaid)
			assert.Equal(t, quota, stored.Quota)
			var entries []model.BalanceLedger
			require.NoError(t, model.DB.Where("user_id = ?", user.Id).Find(&entries).Error)
			require.Len(t, entries, 1)
			assert.Equal(t, quota, entries[0].Delta)
			var completed model.TopUp
			require.NoError(t, model.DB.First(&completed, order.Id).Error)
			assert.Equal(t, common.TopUpStatusSuccess, completed.Status)
			assert.EqualValues(t, quota, completed.Amount)
			var rewardEvents int64
			require.NoError(t, model.DB.Model(&model.AgentCommissionOutbox{}).Where("reference_id = ? AND source_user_id = ?", order.TradeNo, user.Id).Count(&rewardEvents).Error)
			if manualFirst {
				assert.Zero(t, rewardEvents, "a later callback must not turn an administrator credit into a paid reward event")
			} else {
				assert.EqualValues(t, 1, rewardEvents, "callback replay must retain the single original payment event")
			}
		})
	}
}

func testPollFailurePreservesHold(t *testing.T) {
	truncate(t)
	const userID, tokenID, channelID = 4601, 4601, 4601
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "financial-review-token", 7_000)
	seedTaskPollingChannel(t, channelID, true)
	seedChargedAccounting(t, userID, channelID, tokenID, 2_000, 1)
	task := makeTask(userID, channelID, 2_000, tokenID, BillingSourceWallet, 0)
	task.TaskID = "query-outage-review"
	task.PrivateData.UpstreamTaskID = "accepted-upstream-task"
	require.NoError(t, model.DB.Create(task).Error)
	previous := constant.TaskPollMaxFailures
	constant.TaskPollMaxFailures = 1
	t.Cleanup(func() { constant.TaskPollMaxFailures = previous })
	require.NoError(t, recordPollFailure(context.Background(), &scriptedPollingAdaptor{}, task, task.Status, pollClassTransport, 0, "connection reset"))
	var stored model.Task
	require.NoError(t, model.DB.First(&stored, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusUnknown, stored.Status)
	assert.Equal(t, 2_000, stored.Quota)
	assert.NotEmpty(t, stored.PrivateData.FinancialReviewReason)
	assert.Nil(t, stored.PrivateData.FinancialIntent)
	assertPollingFailureAccountingUnchanged(t, userID, tokenID, channelID, 10_000, 7_000, 2_000, true)
}

func testTimeoutPreservesHold(t *testing.T) {
	truncate(t)
	const userID, tokenID, channelID = 4602, 4602, 4602
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "financial-timeout-token", 7_000)
	seedTaskPollingChannel(t, channelID, true)
	seedChargedAccounting(t, userID, channelID, tokenID, 2_000, 1)
	task := makeTask(userID, channelID, 2_000, tokenID, BillingSourceWallet, 0)
	task.TaskID = "timeout-review"
	task.PrivateData.UpstreamTaskID = "accepted-long-running-task"
	task.SubmitTime = time.Now().Add(-time.Hour).Unix()
	require.NoError(t, model.DB.Create(task).Error)
	previous := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previous })
	sweepTimedOutTasks(context.Background())
	var stored model.Task
	require.NoError(t, model.DB.First(&stored, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusUnknown, stored.Status)
	assert.Equal(t, 2_000, stored.Quota)
	assert.NotEmpty(t, stored.PrivateData.FinancialReviewReason)
	assert.Nil(t, stored.PrivateData.FinancialIntent)
	assertPollingFailureAccountingUnchanged(t, userID, tokenID, channelID, 10_000, 7_000, 2_000, true)
}
