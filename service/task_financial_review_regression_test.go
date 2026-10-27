package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Independent review regressions exercise the production poll/worker bridge,
// not only ApplyTaskQuota with a hand-selected terminal status.
type reviewUsagePollingAdaptor struct {
	scriptedPollingAdaptor
	actualQuota int
}

func (a *reviewUsagePollingAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return a.actualQuota
}

type reviewAccountingSnapshot struct {
	Quota, Paid, Bonus, Reward, Used, Requests int
	TokenRemain, TokenUsed, ChannelUsed        int
	Ledger                                     []model.BalanceLedger
	Operations                                 []model.BalanceLedgerOperation
	Logs                                       int64
}

func reviewAccounting(t *testing.T, user *model.User, token *model.Token, channel *model.Channel) reviewAccountingSnapshot {
	t.Helper()
	var u model.User
	var tok model.Token
	var ch model.Channel
	require.NoError(t, model.DB.First(&u, user.Id).Error)
	require.NoError(t, model.DB.First(&tok, token.Id).Error)
	require.NoError(t, model.DB.First(&ch, channel.Id).Error)
	s := reviewAccountingSnapshot{Quota: u.Quota, Paid: u.QuotaPaid, Bonus: u.QuotaBonus, Reward: u.QuotaReward, Used: u.UsedQuota, Requests: u.RequestCount, TokenRemain: tok.RemainQuota, TokenUsed: tok.UsedQuota, ChannelUsed: int(ch.UsedQuota), Logs: countReceiptLogs(t, u.Id)}
	require.NoError(t, model.DB.Where("user_id = ?", u.Id).Order("id").Find(&s.Ledger).Error)
	require.NoError(t, model.DB.Where("user_id = ?", u.Id).Order("idempotency_key").Find(&s.Operations).Error)
	return s
}

func reviewRecoveryPass(t *testing.T) {
	t.Helper()
	handler := billingRefundRecoveryHandler{}
	job, err := model.CreateSystemTask(handler.Type(), handler.NewPayload(), nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("task_id = ?", job.TaskID).Delete(&model.SystemTaskLock{}).Error)
		require.NoError(t, model.DB.Where("task_id = ?", job.TaskID).Delete(&model.SystemTask{}).Error)
	})
	runner := "independent-financial-review"
	claimed, acquired, err := model.ClaimSystemTask(job.ID, handler.Type(), runner, time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, acquired)
	handler.Run(t.Context(), claimed, runner)
	finished, err := model.GetSystemTaskByTaskID(job.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status, "the real worker must finish under its actual lease")
}

func TestTaskFinancialReviewFailureUsageCommitAndRecovery(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprintf("token_write_fault=%t", fault), func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 0, 0)
			task := seedReceiptTask(t, user, token, channel, 300)
			task.Status = model.TaskStatusInProgress
			require.NoError(t, model.DB.Save(task).Error)
			before := reviewAccounting(t, user, token, channel)
			remove := func() {}
			var assertFault func()
			if fault {
				_, hits, release := failLifecycleWrite(t, &model.Token{}, false)
				remove = release
				assertFault = func() {
					require.EqualValues(t, 1, hits.Load(), "fault must reach the real token UPDATE, after terminal intent commit")
				}
			}
			adaptor := &reviewUsagePollingAdaptor{actualQuota: 100}
			result := &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "provider failed after billable work"}
			require.NoError(t, ApplyTaskPollResult(t.Context(), adaptor, task, result, []byte(`{"provider_result":"partial"}`)))
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			require.EqualValues(t, model.TaskStatusFailure, persisted.Status)
			if fault {
				assertFault()
				require.True(t, persisted.SettlementPending)
				require.NotNil(t, persisted.PrivateData.FinancialIntent)
				require.Equal(t, "settle", persisted.PrivateData.FinancialIntent.Kind)
				require.Equal(t, 100, persisted.PrivateData.FinancialIntent.ActualQuota)
				require.Equal(t, before, reviewAccounting(t, user, token, channel), "failed token SQL must roll back source, usage and log together")
				remove()
				reviewRecoveryPass(t)
				require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			}
			require.Equal(t, 100, persisted.Quota)
			require.False(t, persisted.SettlementPending)
			require.Nil(t, persisted.PrivateData.FinancialIntent)
			after := reviewAccounting(t, user, token, channel)
			require.Equal(t, 900, after.Quota)
			require.Equal(t, 1900, after.TokenRemain)
			require.Equal(t, 100, after.TokenUsed)
			require.Equal(t, 100, after.Used)
			require.Equal(t, 100, after.ChannelUsed)
			require.Equal(t, 1, after.Requests)
			require.EqualValues(t, 1, after.Logs)
			// Reloaded replay and a separate scheduled pass cannot repeat any side.
			require.NoError(t, ApplyTaskPollResult(t.Context(), adaptor, &persisted, result, []byte(`{"provider_result":"partial"}`)))
			reviewRecoveryPass(t)
			require.Equal(t, after, reviewAccounting(t, user, token, channel))
		})
	}
}

func TestTaskFinancialReviewLegacyTerminalObservation(t *testing.T) {
	for _, terminal := range []model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailure} {
		t.Run(string(terminal), func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 0, 0)
			task := seedReceiptTask(t, user, token, channel, 300)
			task.Status = model.TaskStatusInProgress
			task.PrivateData.FinancialVersion = 0
			task.PrivateData.SubmissionAccounted = false
			task.PrivateData.TokenFingerprint = ""
			require.NoError(t, model.DB.Save(task).Error)
			before := reviewAccounting(t, user, token, channel)
			body := []byte(`{"provider_result":"legacy-complete"}`)
			result := &relaycommon.TaskInfo{Status: string(terminal), Url: "https://example.invalid/legacy-result", Reason: "provider legacy failure"}
			require.NoError(t, ApplyTaskPollResult(t.Context(), &reviewUsagePollingAdaptor{actualQuota: 100}, task, result, body))
			var saved model.Task
			require.NoError(t, model.DB.First(&saved, task.ID).Error)
			require.Equal(t, terminal, saved.Status, "legacy identity uncertainty must not hide an observed provider result forever")
			require.JSONEq(t, string(body), string(saved.Data))
			require.Equal(t, "100%", saved.Progress)
			require.Positive(t, saved.FinishTime)
			if terminal == model.TaskStatusSuccess {
				require.Equal(t, result.Url, saved.PrivateData.ResultURL)
			} else {
				require.Equal(t, result.Reason, saved.FailReason)
			}
			require.NotEmpty(t, saved.PrivateData.FinancialReviewReason)
			require.Zero(t, saved.PrivateData.FinancialVersion)
			require.Empty(t, saved.PrivateData.TokenFingerprint)
			require.False(t, saved.PrivateData.SubmissionAccounted)
			require.Nil(t, saved.PrivateData.FinancialIntent)
			require.False(t, saved.SettlementPending)
			require.Equal(t, 300, saved.Quota)
			reviewRecoveryPass(t)
			require.Equal(t, before, reviewAccounting(t, user, token, channel), "observation and review must not backfill historical token identity or touch money")
		})
	}
}

func TestTaskFinancialReviewFailureZeroPriceUsage(t *testing.T) {
	oldModels, oldGroups := ratio_setting.ModelRatio2JSONString(), ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"review-free":0}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldModels))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
	})
	user, token, channel := seedReceiptActors(t, 0, 0)
	task := seedReceiptTask(t, user, token, channel, 300)
	task.Status, task.Group = model.TaskStatusInProgress, "review-free"
	require.NoError(t, model.DB.Save(task).Error)
	result := &relaycommon.TaskInfo{Status: model.TaskStatusFailure, TotalTokens: 10, Reason: "failed with measured usage in a free group"}
	require.NoError(t, ApplyTaskPollResult(t.Context(), &reviewUsagePollingAdaptor{}, task, result, []byte(`{"usage":10}`)))
	var saved model.Task
	require.NoError(t, model.DB.First(&saved, task.ID).Error)
	require.Zero(t, saved.Quota)
	require.False(t, saved.SettlementPending)
	require.Contains(t, saved.PrivateData.FinancialOperationID, ":settle:0", "an explicitly zero-priced usage decision is valid, not an unproven failure charge")
	after := reviewAccounting(t, user, token, channel)
	require.Equal(t, 1000, after.Quota)
	require.Equal(t, 2000, after.TokenRemain)
	require.Zero(t, after.Used)
	require.Zero(t, after.TokenUsed)
	require.Zero(t, after.ChannelUsed)
	require.Equal(t, 1, after.Requests)
	reviewRecoveryPass(t)
	require.Equal(t, after, reviewAccounting(t, user, token, channel))
}

func TestTaskFinancialReviewRecoveryFairness(t *testing.T) {
	// The first full page contains legitimate upcharges that cannot currently be
	// funded. Their retryable failure must not starve a later authorized refund.
	var blocked []*model.Task
	for i := 0; i < 32; i++ {
		task, _, _, _ := seedAtomicFinancialTask(t, false)
		task.PrivateData.FinancialIntent.ActualQuota = 2000
		require.NoError(t, model.DB.Save(task).Error)
		blocked = append(blocked, task)
	}
	payable, user, token, channel := seedAtomicFinancialTask(t, true)
	reviewRecoveryPass(t)
	reviewRecoveryPass(t) // a NEW scheduler task, not reuse of an in-memory cursor
	var saved model.Task
	require.NoError(t, model.DB.First(&saved, payable.ID).Error)
	require.Zero(t, saved.Quota, "later-page refund must make progress despite an entire failing head page")
	require.False(t, saved.SettlementPending)
	after := reviewAccounting(t, user, token, channel)
	require.Equal(t, 1140, after.Quota)
	require.Equal(t, 2000, after.TokenRemain)
	require.Zero(t, after.Used)
	require.Zero(t, after.ChannelUsed)
	for _, task := range blocked {
		var pending model.Task
		require.NoError(t, model.DB.First(&pending, task.ID).Error)
		require.Equal(t, 300, pending.Quota)
		require.True(t, pending.SettlementPending, "fairness must retain retryable intent, not silently discard it")
		require.Equal(t, 840, getUserQuota(t, pending.UserId))
		require.Equal(t, 1700, getTokenRemainQuota(t, pending.PrivateData.TokenId))
	}
	reviewRecoveryPass(t)
	require.Equal(t, after, reviewAccounting(t, user, token, channel))
}

func TestMidjourneyFinancialReviewRecoveryFairness(t *testing.T) {
	var blocked []*model.Midjourney
	for i := 0; i < 33; i++ {
		task, info, _, token, _ := seedAtomicFinancialMidjourney(t, 300, true)
		applied, err := SettleMidjourneyTaskBilling(info, task, true)
		require.NoError(t, err)
		require.True(t, applied)
		task.Status = "FAILURE"
		require.NoError(t, model.DB.Model(task).Update("status", task.Status).Error)
		if i < 32 {
			// Correctly captured original authorization must NOT refund a token
			// row whose key was replaced, but these failures cannot block others.
			require.NoError(t, model.DB.Model(token).Update("key", fmt.Sprintf("review-replacement-%d", token.Id)).Error)
			blocked = append(blocked, task)
			continue
		}
		reviewRecoveryPass(t)
		reviewRecoveryPass(t)
		var saved model.Midjourney
		require.NoError(t, model.DB.First(&saved, task.Id).Error)
		require.Zero(t, saved.Quota, "an identity-invalid head page must not starve a valid later refund")
		require.Equal(t, 1140, getUserQuota(t, task.UserId))
		require.Equal(t, 2000, getTokenRemainQuota(t, task.TokenId))
		require.EqualValues(t, 1, countReceiptLogs(t, task.UserId))
	}
	for _, task := range blocked {
		var saved model.Midjourney
		require.NoError(t, model.DB.First(&saved, task.Id).Error)
		require.Equal(t, 300, saved.Quota)
		require.Equal(t, 840, getUserQuota(t, task.UserId))
		require.Equal(t, 1700, getTokenRemainQuota(t, task.TokenId))
		require.Zero(t, countReceiptLogs(t, task.UserId), "identity mismatch cannot issue even a display refund")
	}
}

func TestTaskFinancialReviewQuotaExportParity(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate_log=%t", separate), func(t *testing.T) {
			oldExport := common.DataExportEnabled
			common.DataExportEnabled = true
			model.CacheQuotaDataLock.Lock()
			oldCache := model.CacheQuotaData
			model.CacheQuotaData = make(map[string]*model.QuotaData)
			model.CacheQuotaDataLock.Unlock()
			t.Cleanup(func() {
				common.DataExportEnabled = oldExport
				model.CacheQuotaDataLock.Lock()
				model.CacheQuotaData = oldCache
				model.CacheQuotaDataLock.Unlock()
			})
			oldLog, oldMainType, oldLogType := model.LOG_DB, common.MainDatabaseType(), common.LogDatabaseType()
			model.LOG_DB = model.DB
			t.Cleanup(func() { model.LOG_DB = oldLog; common.SetDatabaseTypes(oldMainType, oldLogType) })
			if separate {
				logDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
				require.NoError(t, err)
				pool, err := logDB.DB()
				require.NoError(t, err)
				pool.SetMaxOpenConns(1)
				t.Cleanup(func() { require.NoError(t, pool.Close()) })
				require.NoError(t, logDB.AutoMigrate(&model.Log{}))
				model.LOG_DB = logDB
				common.SetDatabaseTypes(oldMainType, common.DatabaseTypeSQLite)
			}
			user, token, channel := seedReceiptActors(t, 0, 0)
			task := seedReceiptTask(t, user, token, channel, 300)
			applied, err := ApplyTaskQuota(t.Context(), task, 400, false)
			require.NoError(t, err)
			require.True(t, applied)
			applied, err = ApplyTaskQuota(t.Context(), task, 400, false)
			require.NoError(t, err)
			require.False(t, applied)
			model.CacheQuotaDataLock.Lock()
			encoded, err := json.Marshal(model.CacheQuotaData)
			model.CacheQuotaDataLock.Unlock()
			require.NoError(t, err)
			var exported map[string]model.QuotaData
			require.NoError(t, json.Unmarshal(encoded, &exported))
			require.Len(t, exported, 1, "same-database transactional logs must not suppress acknowledged dashboard export")
			for _, row := range exported {
				require.Equal(t, user.Id, row.UserID)
				require.Equal(t, token.Id, row.TokenID)
				require.Equal(t, channel.Id, row.ChannelID)
				require.Equal(t, 100, row.Quota)
				require.Equal(t, 1, row.Count, "a successful replay must not repeat the export")
			}
		})
	}
}
