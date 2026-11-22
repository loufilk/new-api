package service

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These tests exercise complete leased recovery jobs. Faults target only the
// named persistence boundary; neither the financial executor nor Finish is stubbed.
func recoveryFaultPass(t *testing.T, status model.SystemTaskStatus) *model.SystemTask {
	t.Helper()
	handler := billingRefundRecoveryHandler{}
	job, err := model.CreateSystemTask(handler.Type(), nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("task_id = ?", job.TaskID).Delete(&model.SystemTaskLock{}).Error)
		require.NoError(t, model.DB.Where("task_id = ?", job.TaskID).Delete(&model.SystemTask{}).Error)
	})
	runner := "recovery-fault-" + job.TaskID
	claimed, acquired, err := model.ClaimSystemTask(job.ID, handler.Type(), runner, time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, acquired)
	handler.Run(t.Context(), claimed, runner)
	finished, err := model.GetSystemTaskByTaskID(job.TaskID)
	require.NoError(t, err)
	require.NotNil(t, finished)
	require.Equal(t, status, finished.Status)
	return finished
}

func recoveryStoredCursor(t *testing.T, job *model.SystemTask) billingRecoveryCursor {
	t.Helper()
	var stored model.SystemTask
	require.NoError(t, model.DB.First(&stored, job.ID).Error)
	var cursor billingRecoveryCursor
	require.NotEmpty(t, stored.State, "only a database read proves durable progress")
	require.NoError(t, common.UnmarshalJsonStr(stored.State, &cursor))
	return cursor
}

// All count rows match the production SQL predicate, not just an in-memory
// legacy classification. Private-only samples are deliberately counted separately.
func seedRecoveryLegacyPage(t *testing.T, count int) ([]model.Task, *model.User, *model.Token, *model.Channel) {
	t.Helper()
	user, token, channel := seedReceiptActors(t, 100, 40)
	rows := make([]model.Task, count)
	for i := range rows {
		rows[i] = model.Task{
			TaskID: fmt.Sprintf("recovery-legacy-%d-%d", user.Id, i), UserId: user.Id, ChannelId: channel.Id,
			Quota: 17, Status: model.TaskStatusFailure, SettlementPending: true,
			Data:        []byte(`{"historical":"must-not-change"}`),
			PrivateData: model.TaskPrivateData{TokenId: token.Id, RequestId: fmt.Sprintf("historical-%d-%d", user.Id, i)},
		}
		if i%2 == 1 {
			rows[i].PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "submit", Quota: 17}
		}
		if i == 0 {
			// Retain a real original source receipt and token debit among the
			// legacy rows; unchanged accounting must not be a vacuous empty check.
			charged := seedReceiptTask(t, user, token, channel, 17)
			rows[i].ID = charged.ID
			rows[i].PrivateData.WalletDebit = charged.PrivateData.WalletDebit
			require.NoError(t, model.DB.Save(&rows[i]).Error)
		} else {
			require.NoError(t, model.DB.Create(&rows[i]).Error)
		}
		// Compare the persisted representation, including native JSON
		// normalization, rather than the pre-write Go value.
		var stored model.Task
		require.NoError(t, model.DB.First(&stored, rows[i].ID).Error)
		rows[i] = stored
	}
	var candidates int64
	require.NoError(t, model.DB.Model(&model.Task{}).Where("user_id = ? AND settlement_pending = ?", user.Id, true).Count(&candidates).Error)
	require.EqualValues(t, count, candidates)
	return rows, user, token, channel
}

func requireRecoveryRefund(t *testing.T, task *model.Task, user *model.User, token *model.Token, channel *model.Channel) reviewAccountingSnapshot {
	t.Helper()
	var stored model.Task
	require.NoError(t, model.DB.First(&stored, task.ID).Error)
	require.Zero(t, stored.Quota)
	require.False(t, stored.SettlementPending)
	require.Nil(t, stored.PrivateData.FinancialIntent)
	require.NotEmpty(t, stored.PrivateData.FinancialOperationID)
	after := reviewAccounting(t, user, token, channel)
	assert.Equal(t, 1140, after.Quota)
	assert.Equal(t, 1000, after.Paid)
	assert.Equal(t, 100, after.Bonus)
	assert.Equal(t, 40, after.Reward)
	assert.Equal(t, 2000, after.TokenRemain)
	assert.Zero(t, after.TokenUsed)
	assert.Zero(t, after.Used)
	assert.Zero(t, after.ChannelUsed)
	assert.Equal(t, 1, after.Requests)
	assert.EqualValues(t, 1, after.Logs)
	assert.Len(t, after.Operations, 2, "one reservation and one terminal operation")
	assert.Len(t, after.Ledger, 6, "three original debits and exactly three matching credits")
	return after
}

func TestBillingRecoveryMixedLegacyPageProgress(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	legacy, oldUser, oldToken, oldChannel := seedRecoveryLegacyPage(t, 101)
	privateOnly := model.Task{TaskID: fmt.Sprintf("recovery-private-only-%d", oldUser.Id), UserId: oldUser.Id, ChannelId: oldChannel.Id, Quota: 19, Status: model.TaskStatusSuccess, PrivateData: model.TaskPrivateData{PendingSettlement: &model.TaskSettlement{Phase: "submit", Quota: 19}}}
	require.NoError(t, model.DB.Create(&privateOnly).Error)
	legacy = append(legacy, privateOnly)
	oldMoney := reviewAccounting(t, oldUser, oldToken, oldChannel)

	type blockedEvidence struct {
		task    model.Task
		user    *model.User
		token   *model.Token
		channel *model.Channel
		money   reviewAccountingSnapshot
	}
	blocked := make([]blockedEvidence, 32)
	for i := range blocked {
		task, user, token, channel := seedAtomicFinancialTask(t, false)
		task.PrivateData.FinancialIntent.ActualQuota = 2000
		require.NoError(t, model.DB.Save(task).Error)
		blocked[i] = blockedEvidence{*task, user, token, channel, reviewAccounting(t, user, token, channel)}
	}
	payable, user, token, channel := seedAtomicFinancialTask(t, true)
	before := reviewAccounting(t, user, token, channel)

	// A completed v2 receipt with an inconsistent residual flag is evidence,
	// not permission to redo the refund or to silently clean historical state.
	completed, doneUser, doneToken, doneChannel := seedAtomicFinancialTask(t, true)
	applied, err := ApplyTaskQuota(t.Context(), completed, 0, true)
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, model.DB.Model(completed).Update("settlement_pending", true).Error)
	require.NoError(t, model.DB.First(completed, completed.ID).Error)
	doneBefore := *completed
	doneMoney := reviewAccounting(t, doneUser, doneToken, doneChannel)

	handler := billingRefundRecoveryHandler{}
	require.True(t, handler.Enabled(), "an all-legacy first page must not disable later new work")
	first := recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
	require.Equal(t, legacy[99].ID, recoveryStoredCursor(t, first).TaskID)
	require.Equal(t, before, reviewAccounting(t, user, token, channel))
	second := recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
	require.Greater(t, second.ID, first.ID)
	require.Equal(t, blocked[31].task.ID, recoveryStoredCursor(t, second).TaskID)
	require.Equal(t, before, reviewAccounting(t, user, token, channel), "32 actual failed intents precede the healthy refund")
	third := recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
	require.GreaterOrEqual(t, recoveryStoredCursor(t, third).TaskID, payable.ID)
	after := requireRecoveryRefund(t, payable, user, token, channel)
	fourth := recoveryFaultPass(t, model.SystemTaskStatusSucceeded) // wrap; no duplicate financial side
	for i, job := range []*model.SystemTask{first, second, third, fourth} {
		var result struct {
			Processed int `json:"processed"`
		}
		require.NoError(t, common.UnmarshalJsonStr(job.Result, &result))
		require.Equal(t, []int{0, 32, 1, 0}[i], result.Processed, "durable worker results must prove the complete failed head page was attempted")
	}
	require.Equal(t, after, reviewAccounting(t, user, token, channel))
	for _, original := range legacy {
		var stored model.Task
		require.NoError(t, model.DB.First(&stored, original.ID).Error)
		assert.Equal(t, original, stored, "the entire historical row must remain unchanged")
	}
	require.Equal(t, oldMoney, reviewAccounting(t, oldUser, oldToken, oldChannel))
	for _, original := range blocked {
		var stored model.Task
		require.NoError(t, model.DB.First(&stored, original.task.ID).Error)
		assert.Equal(t, original.task, stored, "failure cannot erase or rewrite the retryable intent")
		assert.Equal(t, original.money, reviewAccounting(t, original.user, original.token, original.channel))
	}
	var doneAfter model.Task
	require.NoError(t, model.DB.First(&doneAfter, completed.ID).Error)
	assert.Equal(t, doneBefore, doneAfter)
	assert.Equal(t, doneMoney, reviewAccounting(t, doneUser, doneToken, doneChannel))
}

func TestBillingRecoveryCursorWriteFailureThenResume(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, history := range []bool{true, false} {
		t.Run(fmt.Sprintf("history_page=%t", history), func(t *testing.T) {
			var legacy []model.Task
			if history {
				legacy, _, _, _ = seedRecoveryLegacyPage(t, 101)
			}
			payable, user, token, channel := seedAtomicFinancialTask(t, true)
			before := reviewAccounting(t, user, token, channel)
			var hits atomic.Int32
			var enabled atomic.Bool
			enabled.Store(true)
			db := model.DB
			name := "recovery-state-write-" + t.Name()
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
				updates, ok := tx.Statement.Dest.(map[string]any)
				_, state := updates["state"]
				if enabled.Load() && ok && state && tx.Statement.Table == "system_tasks" {
					hits.Add(1)
					tx.AddError(errors.New("synthetic recovery cursor update unavailable"))
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(name)) })
			failed := recoveryFaultPass(t, model.SystemTaskStatusFailed)
			require.EqualValues(t, 1, hits.Load(), "the real state UPDATE, not Finish, must encounter the fault")
			require.Equal(t, "refund_scan_failed", failed.Error)
			require.Empty(t, failed.State)
			require.Equal(t, before, reviewAccounting(t, user, token, channel))
			var pending model.Task
			require.NoError(t, model.DB.First(&pending, payable.ID).Error)
			require.Equal(t, *payable, pending, "a rejected cursor must precede all financial attempts")

			enabled.Store(false)
			resumed := recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
			if history {
				require.Equal(t, legacy[99].ID, recoveryStoredCursor(t, resumed).TaskID)
				require.Equal(t, before, reviewAccounting(t, user, token, channel))
				resumed = recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
			}
			require.GreaterOrEqual(t, recoveryStoredCursor(t, resumed).TaskID, payable.ID)
			after := requireRecoveryRefund(t, payable, user, token, channel)
			recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
			require.Equal(t, after, reviewAccounting(t, user, token, channel))
			for _, original := range legacy {
				var stored model.Task
				require.NoError(t, model.DB.First(&stored, original.ID).Error)
				require.Equal(t, original, stored)
			}
		})
	}
}

func TestBillingRecoverySchedulerCreationFailureThenResume(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	payable, user, token, channel := seedAtomicFinancialTask(t, true)
	before := reviewAccounting(t, user, token, channel)
	handler := billingRefundRecoveryHandler{}
	withSystemTaskRegistry(t, handler)
	require.Zero(t, countSystemTasks(t, handler.Type()), "fixture owns an empty recovery schedule")
	_, hits, remove := failLifecycleWrite(t, &model.SystemTask{}, true)
	runSystemTaskScheduler()
	require.EqualValues(t, 1, hits.Load(), "the real scheduler must attempt the failing INSERT")
	require.Zero(t, countSystemTasks(t, handler.Type()), "failed creation must not leave a phantom active job")
	require.Equal(t, before, reviewAccounting(t, user, token, channel))
	var pending model.Task
	require.NoError(t, model.DB.First(&pending, payable.ID).Error)
	require.Equal(t, *payable, pending)
	remove()
	runSystemTaskScheduler() // do not bypass recovery by directly creating the retry
	require.EqualValues(t, 1, countSystemTasks(t, handler.Type()))
	job, err := model.GetActiveSystemTask(handler.Type())
	require.NoError(t, err)
	require.NotNil(t, job)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("task_id = ?", job.TaskID).Delete(&model.SystemTaskLock{}).Error)
		require.NoError(t, model.DB.Where("task_id = ?", job.TaskID).Delete(&model.SystemTask{}).Error)
	})
	claimed, acquired, err := model.ClaimSystemTask(job.ID, handler.Type(), "recovery-scheduler-retry", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, acquired)
	handler.Run(t.Context(), claimed, "recovery-scheduler-retry")
	finished, err := model.GetSystemTaskByTaskID(job.TaskID)
	require.NoError(t, err)
	require.NotNil(t, finished)
	require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	after := requireRecoveryRefund(t, payable, user, token, channel)
	runSystemTaskScheduler()
	require.EqualValues(t, 1, countSystemTasks(t, handler.Type()))
	require.Equal(t, after, reviewAccounting(t, user, token, channel))
}
