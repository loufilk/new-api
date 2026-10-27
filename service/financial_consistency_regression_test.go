package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Reuse the reviewed same-binary owned consumer and exact topology protocol.
// These registrations add new assertion scopes, not another execution harness.
func init() {
	refundFixtureTopology["TestFinancialConsistencyTerminalContention"] = []string{"", "wallet_settle_refund", "task_refund", "midjourney_refund"}
	refundFixtureTopology["TestFinancialConsistencyLostACKRefund"] = []string{"", "readable", "read_failure"}
	refundFixtureTopology["TestFinancialConsistencyTaskRefundAtomic"] = []string{"", "batch_false", "batch_false/tokens", "batch_false/tasks", "batch_true", "batch_true/tokens", "batch_true/tasks"}
	refundFixtureTopology["TestFinancialConsistencyMidjourneyRefundAtomic"] = []string{""}
	refundFixtureTopology["TestFinancialConsistencyTaskSubmitNetReceipt"] = []string{"", "trusted_zero", "preconsume_more", "preconsume_less", "reserved", "submit_rollback", "submit_lost_ack"}
	refundFixtureTopology["TestRealtimeBillingLifecycle"] = []string{"", "wallet", "wallet/aggregate", "wallet/segment_rollback", "wallet/segment_lost_ack", "wallet/final_lost_ack", "wallet/partial_refund", "wallet/zero_usage", "wallet/zero_price", "wallet/fixed_price", "wallet/missing_total", "subscription", "subscription/aggregate", "subscription/segment_rollback", "subscription/segment_lost_ack", "subscription/final_lost_ack", "subscription/partial_refund", "subscription/zero_usage", "subscription/zero_price", "subscription/fixed_price", "subscription/missing_total"}
	refundFixtureTopology["TestRealtimeBillingTrustedObservedOnly"] = []string{""}
	refundFixtureTopology["TestSubscriptionReservationOldPeriodSnapshotUnknown"] = []string{"", "fresh_replay", "reserve_lost_ack"}
	refundFixtureTopology["TestBillingUnownedHoldRequiresReview"] = []string{"", "refund", "settle_zero", "settle_more", "handoff_zero", "handoff_more", "free_task_zero"}
	// Authored fixed topology: never inferred from runtime child output.
	refundFixtureTopology["TestBillingFinalizationSessionDatabaseMatrix"] = append(refundFixtureTopology["TestBillingFinalizationSessionDatabaseMatrix"],
		"financial_lost_ack",
		"financial_lost_ack/readable",
		"financial_lost_ack/read_failure",
		"financial_task_refund",
		"financial_task_refund/batch_false",
		"financial_task_refund/batch_false/tokens",
		"financial_task_refund/batch_false/tasks",
		"financial_task_refund/batch_true",
		"financial_task_refund/batch_true/tokens",
		"financial_task_refund/batch_true/tasks",
		"financial_mj_refund",
		"financial_stale_success",
		"financial_metadata",
		"financial_metadata/both_ids",
		"financial_metadata/channel",
		"financial_submit",
		"financial_submit/trusted_zero",
		"financial_submit/preconsume_more",
		"financial_submit/preconsume_less",
		"financial_submit/reserved",
		"financial_submit/submit_rollback",
		"financial_submit/submit_lost_ack",
		"financial_task_identity",
		"financial_task_identity/hard_delete",
		"financial_task_identity/same_id_new_key",
		"task_atomic",
		"task_atomic/refund_rollback_source",
		"task_atomic/refund_rollback_token",
		"task_atomic/refund_rollback_user_usage",
		"task_atomic/refund_rollback_channel_usage",
		"task_atomic/refund_rollback_task_receipt",
		"task_atomic/refund_rollback_main_log",
		"task_atomic/commit_boundary_refund_false_mode_1",
		"task_atomic/commit_boundary_refund_false_mode_2",
		"task_atomic/commit_boundary_refund_true_mode_1",
		"task_atomic/commit_boundary_refund_true_mode_2",
		"task_atomic/settlement_more_retains_exact_net_receipt",
		"task_atomic/concurrent_fresh_snapshots",
		"task_atomic/fail_closed_wallet_receipt",
		"task_atomic/fail_closed_token_fingerprint",
		"task_atomic/fail_closed_source_only_refund",
		"task_atomic/fail_closed_legacy_no_token",
		"task_atomic/soft_deleted_original_token",
		"mj_atomic",
		"mj_atomic/charge_quota_0_commit_mode_1",
		"mj_atomic/charge_quota_0_commit_mode_2",
		"mj_atomic/charge_quota_300_commit_mode_1",
		"mj_atomic/charge_quota_300_commit_mode_2",
		"mj_atomic/refund_tokens",
		"mj_atomic/refund_channels",
		"mj_atomic/refund_midjourneys",
		"mj_atomic/refund_logs",
		"mj_atomic/refund_lost_ack",
		"mj_atomic/concurrent_refund_fresh_snapshots",
		"mj_atomic/fail_closed_missing_identity",
		"mj_atomic/fail_closed_missing_receipt",
		"mj_atomic/fail_closed_source_only_refund",
		"mj_atomic/fail_closed_source_only_charge",
		"mj_atomic/uncharged_failure_never_refunds",
		"mj_atomic/no_token_uses_explicit_identity",
		"separate_log",
		"separate_log/midjourney_false",
		"separate_log/midjourney_true",
		"lifecycle_initial",
		"lifecycle_initial/wallet",
		"lifecycle_initial/wallet/source_receipt",
		"lifecycle_initial/wallet/token",
		"lifecycle_initial/wallet/held",
		"lifecycle_initial/subscription",
		"lifecycle_initial/subscription/source_receipt",
		"lifecycle_initial/subscription/token",
		"lifecycle_initial/subscription/held",
		"lifecycle_reserve",
		"lifecycle_reserve/wallet",
		"lifecycle_reserve/wallet/source_receipt",
		"lifecycle_reserve/wallet/token",
		"lifecycle_reserve/wallet/held",
		"lifecycle_reserve/subscription",
		"lifecycle_reserve/subscription/source_receipt",
		"lifecycle_reserve/subscription/token",
		"lifecycle_reserve/subscription/held",
		"lifecycle_admission_failure",
		"lifecycle_admission_failure/wallet",
		"lifecycle_admission_failure/subscription",
		"lifecycle_ready_recovery",
		"lifecycle_ready_recovery/wallet",
		"lifecycle_ready_recovery/subscription",
		"lifecycle_unknown_delivery",
		"lifecycle_unknown_delivery/wallet",
		"lifecycle_unknown_delivery/subscription",
		"lifecycle_trusted_zero",
		"lifecycle_reservation_ack",
		"lifecycle_reservation_ack/wallet",
		"lifecycle_reservation_ack/wallet/initial",
		"lifecycle_reservation_ack/wallet/reserve",
		"lifecycle_reservation_ack/subscription",
		"lifecycle_reservation_ack/subscription/initial",
		"lifecycle_reservation_ack/subscription/reserve",
		"mj_request_owner",
		"mj_request_owner/same_request_same_amount",
		"mj_request_owner/same_request_different_amount",
		"mj_request_owner/same_request_other_user",
		"mj_request_owner/zero_request_duplicate",
		"mj_request_owner/canceled_request_cannot_recharge",
		"mj_request_owner/source_only_different_amount_fails_closed",
		"mj_zero_refund_identity",
		"financial_contention",
		"financial_contention/wallet_settle_refund",
		"financial_contention/task_refund",
		"financial_contention/midjourney_refund",
		"review_failure_usage",
		"review_failure_usage/token_write_fault=false",
		"review_failure_usage/token_write_fault=true",
		"review_legacy_terminal",
		"review_legacy_terminal/SUCCESS",
		"review_legacy_terminal/FAILURE",
		"review_zero_price_usage",
		"review_task_recovery_fairness",
		"review_mj_recovery_fairness",
		"review_quota_export",
		"review_quota_export/separate_log=false",
		"review_quota_export/separate_log=true",
		"realtime_lifecycle",
		"realtime_lifecycle/wallet",
		"realtime_lifecycle/wallet/aggregate",
		"realtime_lifecycle/wallet/segment_rollback",
		"realtime_lifecycle/wallet/segment_lost_ack",
		"realtime_lifecycle/wallet/final_lost_ack",
		"realtime_lifecycle/wallet/partial_refund",
		"realtime_lifecycle/wallet/zero_usage",
		"realtime_lifecycle/wallet/zero_price",
		"realtime_lifecycle/wallet/fixed_price",
		"realtime_lifecycle/wallet/missing_total",
		"realtime_lifecycle/subscription",
		"realtime_lifecycle/subscription/aggregate",
		"realtime_lifecycle/subscription/segment_rollback",
		"realtime_lifecycle/subscription/segment_lost_ack",
		"realtime_lifecycle/subscription/final_lost_ack",
		"realtime_lifecycle/subscription/partial_refund",
		"realtime_lifecycle/subscription/zero_usage",
		"realtime_lifecycle/subscription/zero_price",
		"realtime_lifecycle/subscription/fixed_price",
		"realtime_lifecycle/subscription/missing_total",
		"realtime_trusted",
		"subscription_snapshot_unknown",
		"subscription_snapshot_unknown/fresh_replay",
		"subscription_snapshot_unknown/reserve_lost_ack",
		"unowned_hold",
		"unowned_hold/refund",
		"unowned_hold/settle_zero",
		"unowned_hold/settle_more",
		"unowned_hold/handoff_zero",
		"unowned_hold/handoff_more",
		"unowned_hold/free_task_zero",
		"audio_fixed_price",
		"audio_fixed_price/fixed_price",
		"audio_fixed_price/token_ratios",
	)
}

func TestFinancialConsistencyLostACKRefund(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, scenario := range []string{"readable", "read_failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, cancelClient := settlementDeadlineSession(t, "financial-lost-ack-"+scenario, true, BillingSourceWallet)
			cancelClient()
			pool := installSettlementDeadlinePool(t, "committed_lost_ack", 1) // Intent commits first; fault money.
			result := make(chan error, 1)
			joined := false
			go func() { result <- s.Settle(200) }()
			defer func() {
				close(pool.released)
				if !joined {
					<-result
				}
				waitRefundCallback(t, s)
			}()
			err := awaitSettlementResult(t, result)
			joined = true
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.True(t, s.settlementUncertain)
			assertSettlementBalances(t, 800, 800)
			var before model.BillingFinalization
			require.NoError(t, model.DB.Where("request_id = ?", s.relayInfo.RequestId).First(&before).Error)
			require.Equal(t, model.BillingSettled, before.State)
			require.Equal(t, 200, before.ActualQuota)
			var blocked atomic.Bool
			var readFailures atomic.Int32
			if scenario == "read_failure" {
				blocked.Store(true)
				name := "financial-regression:terminal-read"
				require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
					if blocked.Load() && tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeFor[model.BillingFinalization]() {
						readFailures.Add(1)
						tx.AddError(errors.New("financial regression: authoritative terminal read unavailable"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(name)) })
			}
			s.Refund(testGinContext(t))
			waitRefundCallback(t, s)
			assertSettlementBalances(t, 800, 800)
			if scenario == "read_failure" {
				assert.Zero(t, readFailures.Load(), "a frozen actual blocks refund without needing a potentially unavailable read")
				blocked.Store(false)
				s.Refund(testGinContext(t))
				waitRefundCallback(t, s)
				assertSettlementBalances(t, 800, 800)
			}
			var after model.BillingFinalization
			require.NoError(t, model.DB.Where("request_id = ?", before.RequestID).First(&after).Error)
			assert.Equal(t, before, after)
		})
	}
}

func TestFinancialConsistencyTaskRefundAtomic(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprintf("batch_%t", batch), func(t *testing.T) {
			for _, table := range []string{"tokens", "tasks"} {
				t.Run(table, func(t *testing.T) {
					user, token, channel := seedReceiptActors(t, 100, 40)
					task := seedReceiptTask(t, user, token, channel, 300)
					markReceiptTaskFailed(t, task)
					oldBatch := common.BatchUpdateEnabled
					common.BatchUpdateEnabled = batch
					t.Cleanup(func() { common.BatchUpdateEnabled = oldBatch })
					fault := injectOfficialUpdateFault(t, table, officialFailureBeforeSQL)
					assert.False(t, RefundTaskQuota(t.Context(), task, "financial atomic refund"))
					fault.requireOneHit(t)
					var persisted model.Task
					require.NoError(t, model.DB.First(&persisted, task.ID).Error)
					assert.Equal(t, 300, persisted.Quota)
					assertUserBalances(t, user.Id, 0, 840, 0)
					assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
					assert.Equal(t, 300, getTokenUsedQuota(t, token.Id))
					used, requests := getUserUsageAccounting(t, user.Id)
					assert.Equal(t, 300, used)
					assert.Equal(t, 1, requests)
					assert.EqualValues(t, 300, getChannelUsedQuota(t, channel.Id))
					assert.Zero(t, countReceiptLogs(t, user.Id), "no success log may precede the financial commit")
					fault.disable()
					assert.True(t, RefundTaskQuota(t.Context(), &persisted, "explicit retry after rollback"))
					require.NoError(t, model.DB.First(&persisted, task.ID).Error)
					assert.Zero(t, persisted.Quota)
					assertUserBalances(t, user.Id, 100, 1000, 40)
					assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
					assert.Zero(t, getTokenUsedQuota(t, token.Id))
					used, requests = getUserUsageAccounting(t, user.Id)
					assert.Zero(t, used)
					assert.Equal(t, 1, requests)
					assert.Zero(t, getChannelUsedQuota(t, channel.Id))
					assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
					assert.True(t, RefundTaskQuota(t.Context(), &persisted, "duplicate retry"))
					assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
					assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
				})
			}
		})
	}
}

func TestFinancialConsistencyMidjourneyRefundAtomic(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	user, token, channel := seedReceiptActors(t, 0, 0)
	info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: "financial-mj-charge", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}}
	info.UserQuota = user.Quota
	info.UserSetting.QuotaWarningThreshold = -1
	info.UserSetting.QuotaWarningEnabled = boolPtr(false)
	task := &model.Midjourney{UserId: user.Id, ChannelId: channel.Id, Action: "IMAGINE", MjId: "financial-mj-refund", Status: "IN_PROGRESS", Progress: "50%"}
	prepared, err := PrepareMidjourneyTaskBilling(info, task, 300, true)
	require.NoError(t, err)
	require.NoError(t, task.Insert())
	billed, err := SettleMidjourneyTaskBilling(info, task, prepared)
	require.NoError(t, err)
	require.True(t, billed)
	fault := injectOfficialUpdateFault(t, "midjourneys", officialFailureBeforeSQL)
	assert.False(t, RefundMidjourneyQuota(t.Context(), task, "financial atomic MJ refund"))
	fault.requireOneHit(t)
	persisted := getMidjourneyTask(t, task.Id)
	assert.Equal(t, 300, persisted.Quota)
	assertUserBalances(t, user.Id, 0, 700, 0)
	assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
	assert.Equal(t, 300, getTokenUsedQuota(t, token.Id))
	used, requests := getUserUsageAccounting(t, user.Id)
	assert.Equal(t, 300, used)
	assert.Equal(t, 1, requests)
	assert.EqualValues(t, 300, getChannelUsedQuota(t, channel.Id))
	assert.Zero(t, countReceiptLogs(t, user.Id))
	fault.disable()
	assert.True(t, RefundMidjourneyQuota(t.Context(), &persisted, "explicit retry after rollback"))
	persisted = getMidjourneyTask(t, task.Id)
	assert.Zero(t, persisted.Quota)
	assertUserBalances(t, user.Id, 0, 1000, 0)
	assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
	assert.Zero(t, getTokenUsedQuota(t, token.Id))
	used, requests = getUserUsageAccounting(t, user.Id)
	assert.Zero(t, used)
	assert.Equal(t, 1, requests)
	assert.Zero(t, getChannelUsedQuota(t, channel.Id))
	assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	assert.True(t, RefundMidjourneyQuota(t.Context(), &persisted, "duplicate retry"))
	assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
	assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
}

func TestFinancialConsistencyStaleChannelFailurePreservesSuccess(t *testing.T) {
	user, token, channel := seedReceiptActors(t, 0, 0)
	task := seedReceiptTask(t, user, token, channel, 100)
	task.Platform = "batch-plugin"
	task.Status = model.TaskStatusInProgress
	task.Progress = "50%"
	task.PrivateData.UpstreamTaskID = "financial-stale-channel"
	require.NoError(t, model.DB.Save(task).Error)
	stale := *task
	// Deterministic real entry-point ordering: the poll owns an old snapshot,
	// then an HTTP-style ApplyTaskPollResult commits SUCCESS before its error.
	require.NoError(t, ApplyTaskPollResult(t.Context(), nil, task, &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, Progress: "100%"}, nil))
	var succeeded model.Task
	require.NoError(t, model.DB.First(&succeeded, task.ID).Error)
	require.Equal(t, model.TaskStatus(model.TaskStatusSuccess), succeeded.Status)
	require.NoError(t, model.DB.Delete(channel).Error)
	upstreamID := stale.GetUpstreamTaskID()
	_ = updateBatchTasks(t.Context(), &batchPollingAdaptor{}, channel.Id, []string{upstreamID}, map[string]*model.Task{upstreamID: &stale})
	var after model.Task
	require.NoError(t, model.DB.First(&after, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), after.Status, "stale metadata failure must not overwrite a winning success")
	assert.Equal(t, succeeded.Quota, after.Quota)
	assert.Equal(t, succeeded.PrivateData, after.PrivateData)
	assert.Equal(t, 900, getUserQuota(t, user.Id))
	assert.Equal(t, 1900, getTokenRemainQuota(t, token.Id))
}

func TestFinancialConsistencyUnknownMetadataNeverBlindRefunds(t *testing.T) {
	for _, missing := range []string{"both_ids", "channel"} {
		t.Run(missing, func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 0, 0)
			task := seedReceiptTask(t, user, token, channel, 100)
			task.Status = model.TaskStatusInProgress
			task.Progress = "50%"
			task.Platform = constant.TaskPlatformSuno
			task.SubmitTime = time.Now().Unix()
			if missing == "both_ids" {
				task.TaskID = ""
				task.PrivateData.UpstreamTaskID = ""
			} else {
				require.NoError(t, model.DB.Delete(channel).Error)
			}
			require.NoError(t, model.DB.Save(task).Error)
			previousFactory := GetTaskAdaptorFunc
			GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return &taskPollingFetchAdaptor{} }
			t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })
			previousQueryLimit := constant.TaskQueryLimit
			constant.TaskQueryLimit = 100
			t.Cleanup(func() { constant.TaskQueryLimit = previousQueryLimit })
			summary := RunTaskPollingOnce(t.Context(), nil)
			require.Equal(t, 1, summary.UnfinishedTasks, "the real poll must select this charged task before testing metadata handling")
			require.Equal(t, 1, summary.PlatformsScanned)
			var after model.Task
			require.NoError(t, model.DB.First(&after, task.ID).Error)
			assert.NotEqual(t, model.TaskStatus(model.TaskStatusFailure), after.Status, "unknown delivery must not become an unaccounted terminal failure")
			assert.NotEqual(t, "100%", after.Progress)
			assert.NotEmpty(t, after.FailReason, "the unresolved charged task needs an inspectable diagnostic")
			oldTimeout := constant.TaskTimeoutMinutes
			constant.TaskTimeoutMinutes = 1
			t.Cleanup(func() { constant.TaskTimeoutMinutes = oldTimeout })
			require.NoError(t, model.DB.Model(&after).Update("submit_time", time.Now().Add(-2*time.Hour).Unix()).Error)
			sweepTimedOutTasks(t.Context())
			require.NoError(t, model.DB.First(&after, task.ID).Error)
			assert.Equal(t, 100, after.Quota, "metadata quarantine must not become an automatic timeout refund")
			assert.Equal(t, 900, getUserQuota(t, user.Id))
			assert.Equal(t, 1900, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, countReceiptLogs(t, user.Id))
		})
	}
}

// Use the same initialization and handoff as the submit route; the input may
// still hold a gross pre-settlement snapshot. Only SQL's net receipt is trusted.
func financialTaskForSubmission(info *relaycommon.RelayInfo, quota int) *model.Task {
	task := model.InitTask(constant.TaskPlatformSuno, info)
	task.Quota = quota
	task.Status = model.TaskStatusInProgress
	task.PrivateData.RequestId = info.RequestId
	task.PrivateData.BillingSource = info.BillingSource
	task.PrivateData.SubscriptionId = info.SubscriptionId
	task.PrivateData.SubscriptionQuotaPeriod = info.SubscriptionQuotaPeriod
	task.PrivateData.TokenId = info.TokenId
	task.PrivateData.WalletDebit = WalletSessionDebitShares(info)
	return task
}

func TestFinancialConsistencyTaskSubmitNetReceipt(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, scenario := range []string{"trusted_zero", "preconsume_more", "preconsume_less", "reserved", "submit_rollback", "submit_lost_ack"} {
		t.Run(scenario, func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 40)
			name := fmt.Sprintf("submit-net-model-%d", user.Id)
			seedBonusEligibleModel(t, name, boolPtr(true))
			t.Cleanup(func() {
				require.NoError(t, model.DB.Unscoped().Where("model_name = ?", name).Delete(&model.Model{}).Error)
				model.InvalidateModelBonusEligibleCache()
			})
			setting := operation_setting.GetQuotaSetting()
			oldTrust := setting.TrustQuotaUSD
			setting.TrustQuotaUSD = 0
			t.Cleanup(func() { setting.TrustQuotaUSD = oldTrust })
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: fmt.Sprintf("submit-net-%d", user.Id), OriginModelName: name, ForcePreConsume: true, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}, UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
			pre, actual := 100, 300
			if scenario == "preconsume_more" {
				pre, actual = 300, 100
			}
			if scenario == "trusted_zero" {
				info.ForcePreConsume = false
				setting.TrustQuotaUSD = 0.0001
			}
			c := testGinContext(t)
			c.Set("token_quota", token.RemainQuota)
			s, apiErr := NewBillingSession(c, info, pre)
			require.Nil(t, apiErr)
			info.Billing = s
			if scenario == "trusted_zero" {
				require.Zero(t, s.GetPreConsumedQuota())
			}
			if scenario == "reserved" {
				require.NoError(t, s.Reserve(250))
				actual = 200
			}
			task := financialTaskForSubmission(info, actual)
			if scenario == "submit_rollback" || scenario == "submit_lost_ack" {
				mode := int32(1)
				if scenario == "submit_lost_ack" {
					mode = 2
				}
				taskFinancialCommitFault(t, mode)
				err := PersistTaskBilling(info, task)
				require.Error(t, err)
				var rows int64
				require.NoError(t, model.DB.Model(&model.Task{}).Where("user_id = ?", user.Id).Count(&rows).Error)
				if mode == 1 {
					assert.Zero(t, rows)
					assert.Equal(t, 1040, getUserQuota(t, user.Id))
					assert.Equal(t, 1900, getTokenRemainQuota(t, token.Id))
					used, requests := getUserUsageAccounting(t, user.Id)
					assert.Zero(t, used)
					assert.Zero(t, requests)
				} else {
					assert.EqualValues(t, 1, rows)
					s.Refund(c)
					waitRefundCallback(t, s)
					assert.Equal(t, 840, getUserQuota(t, user.Id), "unknown submit ACK is not rollback evidence")
					assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
				}
			}
			require.NoError(t, PersistTaskBilling(info, task))
			require.NoError(t, PersistTaskBilling(info, task), "same task handoff replays without another charge or request statistic")
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, model.BillingTokenFingerprint(token.Key), persisted.PrivateData.TokenFingerprint)
			assert.Equal(t, 2, persisted.PrivateData.FinancialVersion)
			assert.True(t, persisted.PrivateData.SubmissionAccounted)
			var authoritative []model.LedgerShare
			require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
				var err error
				authoritative, err = model.WalletRequestReceiptTx(tx, user.Id, info.RequestId)
				return err
			}))
			assert.Equal(t, authoritative, persisted.PrivateData.WalletDebit)
			require.NoError(t, model.ValidateTaskWalletReceipt(authoritative, actual))
			assert.Equal(t, 1140-actual, getUserQuota(t, user.Id))
			assert.Equal(t, 2000-actual, getTokenRemainQuota(t, token.Id))
			used, requests := getUserUsageAccounting(t, user.Id)
			assert.Equal(t, actual, used)
			assert.Equal(t, 1, requests)
			assert.EqualValues(t, actual, getChannelUsedQuota(t, channel.Id))
			s.Refund(c)
			waitRefundCallback(t, s)
			assert.Equal(t, 1140-actual, getUserQuota(t, user.Id))
			markReceiptTaskFailed(t, &persisted)
			require.True(t, RefundTaskQuota(t.Context(), &persisted, "provider confirmed failure after submit"))
			require.True(t, RefundTaskQuota(t.Context(), task, "stale observer duplicate"))
			assertUserBalances(t, user.Id, 100, 1000, 40)
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		})
	}
}

func TestFinancialConsistencyTaskOriginalTokenIdentity(t *testing.T) {
	for _, change := range []string{"hard_delete", "same_id_new_key"} {
		t.Run(change, func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 40)
			task := seedReceiptTask(t, user, token, channel, 300)
			markReceiptTaskFailed(t, task)
			originalFingerprint := task.PrivateData.TokenFingerprint
			if change == "hard_delete" {
				require.NoError(t, model.DB.Unscoped().Delete(token).Error)
			} else {
				require.NoError(t, model.DB.Model(token).Update("key", token.Key+"-replacement").Error)
			}
			ok := RefundTaskQuota(t.Context(), task, "original authorized token only")
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, originalFingerprint, persisted.PrivateData.TokenFingerprint)
			if change == "hard_delete" {
				require.True(t, ok)
				assertUserBalances(t, user.Id, 100, 1000, 40)
				assert.Zero(t, persisted.Quota)
				var tokens int64
				require.NoError(t, model.DB.Unscoped().Model(&model.Token{}).Where("id = ?", token.Id).Count(&tokens).Error)
				assert.Zero(t, tokens, "hard deletion must not resurrect a token")
			} else {
				require.False(t, ok)
				assertUserBalances(t, user.Id, 0, 840, 0)
				assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
				assert.Equal(t, 300, persisted.Quota)
				assert.Zero(t, countReceiptLogs(t, user.Id))
			}
		})
	}
}

// A native race must have two real transactions, not two goroutines queued on
// the fixture's single connection. Suspend each before its FIRST owner SQL:
// request claim uses INSERT; Task/MJ use SELECT FOR UPDATE. Suspending after
// either lock would create an artificial test deadlock instead of contention.
func financialNativeContentionBarrier(t *testing.T, entity any, create bool) func() {
	t.Helper()
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		t.Log("SQLite control uses the existing one-connection fixture; native transaction overlap is proved separately")
		return func() {}
	}
	db := model.DB
	pool, err := db.DB()
	require.NoError(t, err)
	previous := pool.Stats().MaxOpenConnections
	pool.SetMaxOpenConns(2)
	statement := &gorm.Statement{DB: db}
	require.NoError(t, statement.Parse(entity))
	target := statement.Schema.ModelType
	name := "financial-contention:" + t.Name()
	var arrivals atomic.Int32
	both := make(chan struct{})
	callback := func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.ModelType != target {
			return
		}
		if _, transactional := tx.Statement.ConnPool.(gorm.TxCommitter); !transactional {
			return
		}
		n := arrivals.Add(1)
		if n == 2 {
			close(both)
		}
		if n <= 2 {
			select {
			case <-both:
			case <-time.After(5 * time.Second):
				tx.AddError(errors.New("financial test fixture did not reach two simultaneous native transactions"))
			}
		}
	}
	if create {
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, callback))
	} else {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register(name, callback))
	}
	t.Cleanup(func() {
		if create {
			require.NoError(t, db.Callback().Create().Remove(name))
		} else {
			require.NoError(t, db.Callback().Query().Remove(name))
		}
		pool.SetMaxOpenConns(previous)
	})
	return func() {
		require.GreaterOrEqual(t, arrivals.Load(), int32(2), "both real SQL transactions must cross the pre-owner barrier")
		t.Logf("native %s: two simultaneous SQL transactions reached the pre-owner SQL barrier", common.MainDatabaseType())
	}
}

func TestFinancialConsistencyTerminalContention(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	t.Run("wallet_settle_refund", func(t *testing.T) {
		settler := currentRefundSession(t, "wallet-competing-terminal", 100)
		other := *settler.relayInfo
		other.Billing = nil
		c := testGinContext(t)
		c.Set("token_quota", 900)
		refunder, apiErr := NewBillingSession(c, &other, 100)
		require.Nil(t, apiErr)
		require.NotSame(t, settler, refunder, "different callers must not serialize through a shared in-memory mutex")
		assertUserBalances(t, 31201, 0, 900, 0)
		assert.Equal(t, 900, getTokenRemainQuota(t, 31202))
		proved := financialNativeContentionBarrier(t, &model.BillingFinalization{}, true)
		var settleErr error
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Go(func() { <-start; settleErr = settler.Settle(200) })
		workers.Go(func() { <-start; refunder.Refund(c) })
		close(start)
		workers.Wait()
		waitRefundCallback(t, refunder)
		proved()
		var owner model.BillingFinalization
		require.NoError(t, model.DB.First(&owner, "request_id = ?", other.RequestId).Error)
		switch owner.State {
		case model.BillingSettled:
			require.NoError(t, settleErr)
			assert.Equal(t, 200, owner.ActualQuota)
			assertUserBalances(t, 31201, 0, 800, 0)
			assert.Equal(t, 800, getTokenRemainQuota(t, 31202))
			require.NoError(t, settler.Settle(200))
		case model.BillingRefunded:
			require.Error(t, settleErr)
			assertUserBalances(t, 31201, 0, 1000, 0)
			assert.Equal(t, 1000, getTokenRemainQuota(t, 31202))
			require.Error(t, settler.Settle(200))
		default:
			t.Fatalf("a completed race must have exactly one durable terminal owner, got %q", owner.State)
		}
		before := owner
		refunder.Refund(c)
		waitRefundCallback(t, refunder)
		require.NoError(t, model.DB.First(&owner, "request_id = ?", other.RequestId).Error)
		assert.Equal(t, before, owner, "replays cannot replace the chosen terminal decision")
	})
	t.Run("task_refund", func(t *testing.T) {
		task, user, token, _ := seedAtomicFinancialTask(t, true)
		var snapshots [2]model.Task
		for i := range snapshots {
			require.NoError(t, model.DB.First(&snapshots[i], task.ID).Error)
		}
		proved := financialNativeContentionBarrier(t, &model.Task{}, false)
		var applied [2]bool
		var errs [2]error
		start := make(chan struct{})
		var workers sync.WaitGroup
		for i := range snapshots {
			workers.Go(func() { <-start; applied[i], errs[i] = ApplyTaskQuota(t.Context(), &snapshots[i], 0, true) })
		}
		close(start)
		workers.Wait()
		proved()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.NotEqual(t, applied[0], applied[1])
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	})
	t.Run("midjourney_refund", func(t *testing.T) {
		task, info, user, token, _ := seedAtomicFinancialMidjourney(t, 300, true)
		applied, err := SettleMidjourneyTaskBilling(info, task, true)
		require.NoError(t, err)
		require.True(t, applied)
		var snapshots [2]model.Midjourney
		for i := range snapshots {
			require.NoError(t, model.DB.First(&snapshots[i], task.Id).Error)
		}
		proved := financialNativeContentionBarrier(t, &model.Midjourney{}, false)
		var results [2]bool
		start := make(chan struct{})
		var workers sync.WaitGroup
		for i := range snapshots {
			workers.Go(func() {
				<-start
				results[i] = RefundMidjourneyQuota(t.Context(), &snapshots[i], "two independent provider-failure observers")
			})
		}
		close(start)
		workers.Wait()
		proved()
		require.True(t, results[0])
		require.True(t, results[1])
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	})
}
