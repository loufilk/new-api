package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Preserve the underlying SQL-pool identity: the production same-database log
// decision must remain testable while the real COMMIT boundary is faulted.
type taskFinancialCommitPool struct {
	*terminalCommitPool
	db *sql.DB
}

func (p *taskFinancialCommitPool) GetDBConn() (*sql.DB, error) { return p.db, nil }

func taskFinancialCommitFault(t *testing.T, mode int32) {
	t.Helper()
	original := model.DB
	db, err := original.DB()
	require.NoError(t, err)
	flag := &atomic.Int32{}
	flag.Store(mode)
	wrapped := original.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	pool := &taskFinancialCommitPool{terminalCommitPool: &terminalCommitPool{ConnPool: original.ConnPool, mode: flag}, db: db}
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	model.DB = wrapped
	t.Cleanup(func() { model.DB = original })
}

func seedAtomicFinancialTask(t *testing.T, refund bool) (*model.Task, *model.User, *model.Token, *model.Channel) {
	t.Helper()
	user, token, channel := seedReceiptActors(t, 100, 40)
	task := seedReceiptTask(t, user, token, channel, 300)
	task.PrivateData.TokenFingerprint = model.BillingTokenFingerprint(token.Key)
	task.PrivateData.FinancialVersion = 2
	task.PrivateData.SubmissionAccounted = true
	actual, kind := 100, "settle"
	if refund {
		task.Status, actual, kind = model.TaskStatusFailure, 0, "refund"
	}
	task.SettlementPending = true
	task.PrivateData.FinancialIntent = &model.TaskFinancialIntent{Kind: kind, PreviousQuota: 300, ActualQuota: actual, OperationID: fmt.Sprintf("test:task-financial:%d", task.ID), Reason: common.NewMessage("test task financial decision")}
	require.NoError(t, model.DB.Save(task).Error)
	return task, user, token, channel
}

func failAtomicFinancialLog(t *testing.T) func() {
	t.Helper()
	var enabled atomic.Bool
	enabled.Store(true)
	name := "test:financial-log-failure:" + t.Name()
	require.NoError(t, model.DB.Callback().Create().After("gorm:create").Register(name, func(tx *gorm.DB) {
		if enabled.Load() && tx.Statement.Table == "logs" {
			tx.AddError(errors.New("synthetic main-database log write failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Create().Remove(name)) })
	return func() { enabled.Store(false) }
}

func failAtomicFinancialUserUsage(t *testing.T) func() {
	t.Helper()
	var enabled atomic.Bool
	enabled.Store(true)
	name := "test:financial-usage-failure:" + t.Name()
	require.NoError(t, model.DB.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if _, usage := updates["used_quota"]; enabled.Load() && ok && usage && tx.Statement.Table == "users" {
			tx.AddError(errors.New("synthetic transactional user usage failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(name)) })
	return func() { enabled.Store(false) }
}

// This runner deliberately uses the current main/log handles, so the native
// matrix can execute exactly the same SQL failure and replay invariants.
func runTaskFinancialAtomicCases(t *testing.T) {
	for _, stage := range []string{"source", "token", "user_usage", "channel_usage", "task_receipt", "main_log"} {
		t.Run("refund_rollback_"+stage, func(t *testing.T) {
			task, user, token, channel := seedAtomicFinancialTask(t, true)
			var release func()
			if stage == "main_log" {
				release = failAtomicFinancialLog(t)
			} else if stage == "user_usage" {
				release = failAtomicFinancialUserUsage(t)
			} else {
				table := map[string]string{"source": "users", "token": "tokens", "channel_usage": "channels", "task_receipt": "tasks"}[stage]
				release = failReceiptUpdates(t, table)
			}
			applied, err := ApplyTaskQuota(t.Context(), task, 0, true)
			require.Error(t, err)
			assert.False(t, applied)
			assertUserBalances(t, user.Id, 0, 840, 0)
			assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, 300, persisted.Quota)
			assert.True(t, persisted.SettlementPending)
			assert.Empty(t, persisted.PrivateData.FinancialOperationID)
			assert.Zero(t, countReceiptLogs(t, user.Id))
			require.NoError(t, model.DB.First(user, user.Id).Error)
			require.NoError(t, model.DB.First(channel, channel.Id).Error)
			assert.Equal(t, 300, user.UsedQuota)
			assert.EqualValues(t, 300, channel.UsedQuota)
			release()
			applied, err = ApplyTaskQuota(t.Context(), &persisted, 0, true)
			require.NoError(t, err)
			assert.True(t, applied)
			assertUserBalances(t, user.Id, 100, 1000, 40)
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, persisted.Quota)
			assert.Empty(t, persisted.PrivateData.WalletDebit)
			assert.False(t, persisted.SettlementPending)
			assert.NotEmpty(t, persisted.PrivateData.FinancialOperationID)
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
			require.NoError(t, model.DB.First(user, user.Id).Error)
			require.NoError(t, model.DB.First(channel, channel.Id).Error)
			assert.Zero(t, user.UsedQuota)
			assert.Equal(t, 1, user.RequestCount)
			assert.Zero(t, channel.UsedQuota)
			applied, err = ApplyTaskQuota(t.Context(), task, 0, true)
			require.NoError(t, err)
			assert.False(t, applied)
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		})
	}
	for _, refund := range []bool{false, true} {
		for _, mode := range []int32{1, 2} {
			t.Run(fmt.Sprintf("commit_boundary_refund_%t_mode_%d", refund, mode), func(t *testing.T) {
				task, user, token, _ := seedAtomicFinancialTask(t, refund)
				actual := 100
				if refund {
					actual = 0
				}
				taskFinancialCommitFault(t, mode)
				applied, err := ApplyTaskQuota(t.Context(), task, actual, refund)
				require.Error(t, err)
				assert.False(t, applied)
				var persisted model.Task
				require.NoError(t, model.DB.First(&persisted, task.ID).Error)
				if mode == 1 {
					assert.Equal(t, 300, persisted.Quota)
					assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
					assert.Zero(t, countReceiptLogs(t, user.Id))
				} else {
					assert.Equal(t, actual, persisted.Quota)
					assert.Equal(t, 2000-actual, getTokenRemainQuota(t, token.Id))
					assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
				}
				applied, err = ApplyTaskQuota(t.Context(), task, actual, refund)
				require.NoError(t, err)
				assert.Equal(t, mode == 1, applied)
				assert.Equal(t, actual, task.Quota)
				assert.Equal(t, 2000-actual, getTokenRemainQuota(t, token.Id))
				assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
				applied, err = ApplyTaskQuota(t.Context(), task, actual+1, false)
				require.ErrorIs(t, err, model.ErrTaskFinancialReview)
				assert.False(t, applied)
				assert.Equal(t, 2000-actual, getTokenRemainQuota(t, token.Id))
			})
		}
	}
	t.Run("settlement_more_retains_exact_net_receipt", func(t *testing.T) {
		task, user, token, channel := seedAtomicFinancialTask(t, false)
		task.PrivateData.FinancialIntent.ActualQuota = 500
		require.NoError(t, model.DB.Save(task).Error)
		applied, err := ApplyTaskQuota(t.Context(), task, 500, false)
		require.NoError(t, err)
		assert.True(t, applied)
		assert.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 100}, {Account: model.AccountReward, Amount: 40}, {Account: model.AccountPaid, Amount: 360}}, task.PrivateData.WalletDebit)
		assertUserBalances(t, user.Id, 0, 640, 0)
		assert.Equal(t, 1500, getTokenRemainQuota(t, token.Id))
		require.NoError(t, model.DB.First(user, user.Id).Error)
		require.NoError(t, model.DB.First(channel, channel.Id).Error)
		assert.Equal(t, 500, user.UsedQuota)
		assert.Equal(t, 1, user.RequestCount)
		assert.EqualValues(t, 500, channel.UsedQuota)
	})
	t.Run("concurrent_fresh_snapshots", func(t *testing.T) {
		task, user, token, _ := seedAtomicFinancialTask(t, true)
		var snapshots [2]model.Task
		for i := range snapshots {
			require.NoError(t, model.DB.First(&snapshots[i], task.ID).Error)
		}
		var results [2]bool
		var errs [2]error
		var workers sync.WaitGroup
		start := make(chan struct{})
		for i := range snapshots {
			workers.Go(func() {
				<-start
				results[i], errs[i] = ApplyTaskQuota(t.Context(), &snapshots[i], 0, true)
			})
		}
		close(start)
		workers.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.NotEqual(t, results[0], results[1])
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	})
	for _, corrupt := range []string{"wallet_receipt", "token_fingerprint", "source_only_refund", "legacy_no_token"} {
		t.Run("fail_closed_"+corrupt, func(t *testing.T) {
			task, user, token, _ := seedAtomicFinancialTask(t, true)
			switch corrupt {
			case "wallet_receipt":
				task.PrivateData.WalletDebit = nil
				require.NoError(t, model.DB.Save(task).Error)
			case "legacy_no_token":
				task.PrivateData.FinancialVersion = 0
				task.PrivateData.SubmissionAccounted = false
				task.PrivateData.TokenId = 0
				task.PrivateData.TokenFingerprint = ""
				task.PrivateData.FinancialIntent = nil
				task.SettlementPending = false
				require.NoError(t, model.DB.Save(task).Error)
			case "token_fingerprint":
				require.NoError(t, model.DB.Model(token).Update("key", "synthetic-replacement-token").Error)
			case "source_only_refund":
				_, err := model.CreditQuotaShares(model.LedgerEntry{UserId: user.Id, Account: model.AccountPaid, Reason: model.LedgerReasonTaskRefund, ReferenceType: "task", ReferenceId: task.TaskID, RequestId: task.TaskID, IdempotencyKey: "task:refund:" + task.TaskID}, task.PrivateData.WalletDebit)
				require.NoError(t, err)
			}
			applied, err := ApplyTaskQuota(t.Context(), task, 0, true)
			require.Error(t, err)
			assert.False(t, applied)
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, 300, persisted.Quota)
			assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, countReceiptLogs(t, user.Id))
			if corrupt == "source_only_refund" {
				assertUserBalances(t, user.Id, 100, 1000, 40)
			} else {
				assertUserBalances(t, user.Id, 0, 840, 0)
			}
		})
	}
	t.Run("soft_deleted_original_token", func(t *testing.T) {
		task, user, token, _ := seedAtomicFinancialTask(t, true)
		require.NoError(t, model.DB.Delete(token).Error)
		applied, err := ApplyTaskQuota(t.Context(), task, 0, true)
		require.NoError(t, err)
		assert.True(t, applied)
		require.NoError(t, model.DB.Unscoped().First(token, token.Id).Error)
		assert.True(t, token.DeletedAt.Valid)
		assert.Equal(t, 2000, token.RemainQuota)
		assertUserBalances(t, user.Id, 100, 1000, 40)
	})
}

func seedAtomicFinancialMidjourney(t *testing.T, quota int, withToken bool) (*model.Midjourney, *relaycommon.RelayInfo, *model.User, *model.Token, *model.Channel) {
	t.Helper()
	user, token, channel := seedReceiptActors(t, 100, 40)
	info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: fmt.Sprintf("atomic-mj-%d", user.Id), UserQuota: user.Quota, IsPlayground: !withToken, BillingSource: BillingSourceWallet, BillingModelName: "mj_imagine", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}}
	// Financial fault/owner tests do not own the optional notification daemon.
	info.UserSetting.QuotaWarningEnabled = boolPtr(false)
	task := &model.Midjourney{UserId: user.Id, ChannelId: channel.Id, MjId: fmt.Sprintf("atomic-mj-task-%d", user.Id), Action: "IMAGINE", Status: "IN_PROGRESS", Progress: "50%"}
	prepared, err := PrepareMidjourneyTaskBilling(info, task, quota, true)
	require.NoError(t, err)
	require.True(t, prepared)
	require.NoError(t, task.Insert())
	return task, info, user, token, channel
}

func runMidjourneyFinancialAtomicCases(t *testing.T) {
	for _, quota := range []int{0, 300} {
		for _, mode := range []int32{1, 2} {
			t.Run(fmt.Sprintf("charge_quota_%d_commit_mode_%d", quota, mode), func(t *testing.T) {
				task, info, user, token, channel := seedAtomicFinancialMidjourney(t, quota, true)
				taskFinancialCommitFault(t, mode)
				applied, err := SettleMidjourneyTaskBilling(info, task, true)
				require.Error(t, err)
				assert.False(t, applied)
				var persisted model.Midjourney
				require.NoError(t, model.DB.First(&persisted, task.Id).Error)
				assert.Equal(t, mode == 2, persisted.BillingAccounted)
				applied, err = SettleMidjourneyTaskBilling(info, task, true)
				require.NoError(t, err)
				assert.Equal(t, mode == 1, applied)
				assert.True(t, task.BillingAccounted)
				assert.Equal(t, quota, task.BillingOriginalQuota)
				assert.Equal(t, 2000-quota, getTokenRemainQuota(t, token.Id))
				require.NoError(t, model.DB.First(user, user.Id).Error)
				require.NoError(t, model.DB.First(channel, channel.Id).Error)
				assert.Equal(t, quota, user.UsedQuota)
				assert.Equal(t, 1, user.RequestCount)
				assert.EqualValues(t, quota, channel.UsedQuota)
				applied, err = SettleMidjourneyTaskBilling(info, task, true)
				require.NoError(t, err)
				assert.False(t, applied)
				otherRequest := *info
				otherRequest.RequestId += "-conflict"
				_, err = SettleMidjourneyTaskBilling(&otherRequest, task, true)
				require.ErrorIs(t, err, model.ErrTaskFinancialReview)
			})
		}
	}
	for _, stage := range []string{"tokens", "channels", "midjourneys", "logs", "lost_ack"} {
		t.Run("refund_"+stage, func(t *testing.T) {
			task, info, user, token, channel := seedAtomicFinancialMidjourney(t, 300, true)
			applied, err := SettleMidjourneyTaskBilling(info, task, true)
			require.NoError(t, err)
			require.True(t, applied)
			var release func()
			switch stage {
			case "logs":
				release = failAtomicFinancialLog(t)
			case "lost_ack":
				taskFinancialCommitFault(t, 2)
				release = func() {}
			default:
				release = failReceiptUpdates(t, stage)
			}
			require.False(t, RefundMidjourneyQuota(t.Context(), task, "test failed task"))
			if stage == "lost_ack" {
				assertUserBalances(t, user.Id, 100, 1000, 40)
				assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			} else {
				assertUserBalances(t, user.Id, 100, 740, 0)
				assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
				assert.Zero(t, countReceiptLogs(t, user.Id))
			}
			release()
			require.True(t, RefundMidjourneyQuota(t.Context(), task, "retry failed task"))
			assertUserBalances(t, user.Id, 100, 1000, 40)
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, task.Quota)
			assert.Equal(t, 300, task.BillingOriginalQuota)
			assert.Equal(t, info.RequestId, task.BillingRequestID)
			assert.True(t, task.BillingAccounted)
			assert.Empty(t, task.WalletDebit)
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
			require.NoError(t, model.DB.First(user, user.Id).Error)
			require.NoError(t, model.DB.First(channel, channel.Id).Error)
			assert.Zero(t, user.UsedQuota)
			assert.Equal(t, 1, user.RequestCount)
			assert.Zero(t, channel.UsedQuota)
			require.True(t, RefundMidjourneyQuota(t.Context(), task, "duplicate failed task"))
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		})
	}
	t.Run("concurrent_refund_fresh_snapshots", func(t *testing.T) {
		task, info, user, token, _ := seedAtomicFinancialMidjourney(t, 300, true)
		applied, err := SettleMidjourneyTaskBilling(info, task, true)
		require.NoError(t, err)
		require.True(t, applied)
		var snapshots [2]model.Midjourney
		for i := range snapshots {
			require.NoError(t, model.DB.First(&snapshots[i], task.Id).Error)
		}
		var results [2]bool
		var workers sync.WaitGroup
		start := make(chan struct{})
		for i := range snapshots {
			workers.Go(func() {
				<-start
				results[i] = RefundMidjourneyQuota(t.Context(), &snapshots[i], "concurrent failure")
			})
		}
		close(start)
		workers.Wait()
		assert.True(t, results[0])
		assert.True(t, results[1])
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	})
	for _, corrupt := range []string{"missing_identity", "missing_receipt", "source_only_refund", "source_only_charge"} {
		t.Run("fail_closed_"+corrupt, func(t *testing.T) {
			task, info, user, token, _ := seedAtomicFinancialMidjourney(t, 300, true)
			if corrupt == "source_only_charge" {
				_, err := model.DebitQuota(model.LedgerEntry{UserId: user.Id, Account: model.AccountPaid, Delta: 300, Reason: model.LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: info.RequestId, RequestId: info.RequestId, IdempotencyKey: fmt.Sprintf("billing:settle:%s:%d", info.RequestId, 300)}, model.AccountReward, model.AccountPaid)
				require.NoError(t, err)
				applied, err := SettleMidjourneyTaskBilling(info, task, true)
				require.ErrorIs(t, err, model.ErrTaskFinancialReview)
				assert.False(t, applied)
				assert.False(t, RefundMidjourneyQuota(t.Context(), task, "must reconcile source-only charge"))
				assert.Equal(t, 300, task.Quota)
				assert.False(t, task.BillingAccounted)
				assertUserBalances(t, user.Id, 100, 740, 0)
				assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
				return
			}
			applied, err := SettleMidjourneyTaskBilling(info, task, true)
			require.NoError(t, err)
			require.True(t, applied)
			switch corrupt {
			case "missing_identity":
				require.NoError(t, model.DB.Model(task).Update("token_fingerprint", "").Error)
			case "missing_receipt":
				require.NoError(t, model.DB.Model(task).Update("wallet_debit", nil).Error)
			case "source_only_refund":
				_, err := model.CreditQuotaShares(model.LedgerEntry{UserId: user.Id, Account: model.AccountPaid, Reason: model.LedgerReasonTaskRefund, ReferenceType: "mj_task", ReferenceId: task.MjId, RequestId: task.MjId, IdempotencyKey: "midjourney:refund:" + task.MjId}, task.WalletDebit)
				require.NoError(t, err)
			}
			require.False(t, RefundMidjourneyQuota(t.Context(), task, "must not guess"))
			assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
			assert.Equal(t, 300, task.Quota)
			assert.Zero(t, countReceiptLogs(t, user.Id))
			if corrupt == "source_only_refund" {
				assertUserBalances(t, user.Id, 100, 1000, 40)
			} else {
				assertUserBalances(t, user.Id, 100, 740, 0)
			}
		})
	}
	t.Run("uncharged_failure_never_refunds", func(t *testing.T) {
		task, info, user, token, _ := seedAtomicFinancialMidjourney(t, 300, true)
		require.True(t, RefundMidjourneyQuota(t.Context(), task, "failed before charge"))
		assert.False(t, task.BillingAccounted)
		assert.Zero(t, task.Quota)
		assert.Equal(t, 300, task.BillingOriginalQuota)
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		applied, err := SettleMidjourneyTaskBilling(info, task, true)
		require.NoError(t, err)
		assert.False(t, applied)
		assertUserBalances(t, user.Id, 100, 1000, 40)
	})
	t.Run("no_token_uses_explicit_identity", func(t *testing.T) {
		task, info, user, token, _ := seedAtomicFinancialMidjourney(t, 300, false)
		applied, err := SettleMidjourneyTaskBilling(info, task, true)
		require.NoError(t, err)
		require.True(t, applied)
		assert.Zero(t, task.TokenId)
		assert.True(t, task.HasCurrentBillingIdentity())
		require.True(t, RefundMidjourneyQuota(t.Context(), task, "no-token task failed"))
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assertUserBalances(t, user.Id, 100, 1000, 40)
	})
}

func TestTaskFinancialAtomic(t *testing.T) { runTaskFinancialAtomicCases(t) }

func TestMidjourneyFinancialAtomic(t *testing.T) { runMidjourneyFinancialAtomicCases(t) }

// A separately configured log is not a distributed financial transaction.
// Display failure never rolls back money and a financial replay never retries it.
func TestTaskFinancialSeparateLogFailureIsAtMostOnce(t *testing.T) {
	for _, midjourney := range []bool{false, true} {
		t.Run(fmt.Sprintf("midjourney_%t", midjourney), func(t *testing.T) {
			logDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			pool, err := logDB.DB()
			require.NoError(t, err)
			pool.SetMaxOpenConns(1)
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			oldLogDB, oldMainType, oldLogType := model.LOG_DB, common.MainDatabaseType(), common.LogDatabaseType()
			model.LOG_DB = logDB
			common.SetDatabaseTypes(oldMainType, common.DatabaseTypeSQLite)
			t.Cleanup(func() {
				model.LOG_DB = oldLogDB
				common.SetDatabaseTypes(oldMainType, oldLogType)
				require.NoError(t, pool.Close())
			})
			var attempts atomic.Int32
			require.NoError(t, logDB.Callback().Create().Before("gorm:create").Register("test:separate-log-unavailable", func(tx *gorm.DB) {
				attempts.Add(1)
				tx.AddError(errors.New("synthetic separate log unavailable"))
			}))
			var user *model.User
			var token *model.Token
			if midjourney {
				task, info, u, tok, _ := seedAtomicFinancialMidjourney(t, 300, true)
				user, token = u, tok
				applied, err := SettleMidjourneyTaskBilling(info, task, true)
				require.NoError(t, err)
				require.True(t, applied)
				require.True(t, RefundMidjourneyQuota(t.Context(), task, "log display unavailable"))
				require.True(t, RefundMidjourneyQuota(t.Context(), task, "no retry of separate display"))
			} else {
				task, u, tok, _ := seedAtomicFinancialTask(t, true)
				user, token = u, tok
				applied, err := ApplyTaskQuota(t.Context(), task, 0, true)
				require.NoError(t, err)
				require.True(t, applied)
				applied, err = ApplyTaskQuota(t.Context(), task, 0, true)
				require.NoError(t, err)
				assert.False(t, applied)
			}
			assert.EqualValues(t, 1, attempts.Load())
			assertUserBalances(t, user.Id, 100, 1000, 40)
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, countReceiptLogs(t, user.Id))
		})
	}
}

func runMidjourneyFinancialRequestOwnerCases(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		firstQuota, nextQuota int
		otherUser             bool
	}{
		{name: "same_request_same_amount", firstQuota: 300, nextQuota: 300},
		{name: "same_request_different_amount", firstQuota: 300, nextQuota: 500},
		{name: "same_request_other_user", firstQuota: 300, nextQuota: 300, otherUser: true},
		{name: "zero_request_duplicate", firstQuota: 0, nextQuota: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, info, user, token, _ := seedAtomicFinancialMidjourney(t, tc.firstQuota, true)
			applied, err := SettleMidjourneyTaskBilling(info, first, true)
			require.NoError(t, err)
			require.True(t, applied)
			nextInfo := *info
			if tc.otherUser {
				other, otherToken, otherChannel := seedReceiptActors(t, 100, 40)
				nextInfo.UserId, nextInfo.TokenId, nextInfo.TokenKey = other.Id, otherToken.Id, otherToken.Key
				nextInfo.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: otherChannel.Id}
			}
			next := &model.Midjourney{UserId: nextInfo.UserId, ChannelId: nextInfo.ChannelId, MjId: first.MjId + "-duplicate", Action: "IMAGINE", Status: "IN_PROGRESS"}
			prepared, err := PrepareMidjourneyTaskBilling(&nextInfo, next, tc.nextQuota, true)
			require.NoError(t, err)
			require.True(t, prepared)
			require.NoError(t, next.Insert())
			applied, err = SettleMidjourneyTaskBilling(&nextInfo, next, true)
			require.Error(t, err)
			assert.False(t, applied)
			assert.False(t, getMidjourneyTask(t, next.Id).BillingAccounted)
			assert.Equal(t, 2000-tc.firstQuota, getTokenRemainQuota(t, token.Id))
			used, count := getUserUsageAccounting(t, user.Id)
			assert.Equal(t, tc.firstQuota, used)
			assert.Equal(t, 1, count)
			if tc.otherUser {
				assertUserBalances(t, nextInfo.UserId, 100, 1000, 40)
				assert.Equal(t, 2000, getTokenRemainQuota(t, nextInfo.TokenId))
			}
			var owner model.BillingFinalization
			require.NoError(t, model.DB.First(&owner, "request_id = ?", info.RequestId).Error)
			assert.Equal(t, model.BillingTaskOwned, owner.State)
			assert.EqualValues(t, first.Id, owner.TaskID)
			assert.Equal(t, tc.firstQuota, owner.ActualQuota)
		})
	}
	t.Run("canceled_request_cannot_recharge", func(t *testing.T) {
		task, info, user, token, _ := seedAtomicFinancialMidjourney(t, 300, true)
		require.NoError(t, model.DB.Model(task).Update("status", "FAILURE").Error)
		require.True(t, RefundMidjourneyQuota(t.Context(), task, "failed before billing"))
		var owner model.BillingFinalization
		require.NoError(t, model.DB.First(&owner, "request_id = ?", info.RequestId).Error)
		assert.Zero(t, owner.ActualQuota)
		assert.False(t, task.BillingAccounted)
		assert.Equal(t, 300, task.BillingOriginalQuota)
		applied, err := SettleMidjourneyTaskBilling(info, task, true)
		require.NoError(t, err)
		assert.False(t, applied)
		next := &model.Midjourney{UserId: user.Id, ChannelId: task.ChannelId, MjId: task.MjId + "-retry", Status: "IN_PROGRESS"}
		prepared, err := PrepareMidjourneyTaskBilling(info, next, 500, true)
		require.NoError(t, err)
		require.True(t, prepared)
		require.NoError(t, next.Insert())
		applied, err = SettleMidjourneyTaskBilling(info, next, true)
		require.Error(t, err)
		assert.False(t, applied)
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		used, count := getUserUsageAccounting(t, user.Id)
		assert.Zero(t, used)
		assert.Zero(t, count)
	})
	t.Run("source_only_different_amount_fails_closed", func(t *testing.T) {
		task, info, user, token, _ := seedAtomicFinancialMidjourney(t, 300, true)
		_, err := model.DebitQuota(model.LedgerEntry{
			UserId: user.Id, Account: model.AccountPaid, Delta: 100, Reason: model.LedgerReasonBillingSettle,
			ReferenceType: "relay", ReferenceId: info.RequestId, RequestId: info.RequestId,
			IdempotencyKey: fmt.Sprintf("billing:settle:%s:100", info.RequestId),
		}, model.AccountReward, model.AccountPaid)
		require.NoError(t, err)
		applied, err := SettleMidjourneyTaskBilling(info, task, true)
		require.Error(t, err)
		assert.False(t, applied)
		assert.False(t, RefundMidjourneyQuota(t.Context(), task, "cannot infer token accounting"))
		assert.Equal(t, 300, getMidjourneyTask(t, task.Id).Quota)
		assertUserBalances(t, user.Id, 100, 940, 0)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		var owners int64
		require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("request_id = ?", info.RequestId).Count(&owners).Error)
		assert.Zero(t, owners)
	})
}

func TestMidjourneyFinancialRequestOwner(t *testing.T) {
	runMidjourneyFinancialRequestOwnerCases(t)
}

// Zero is an amount, not proof that an unknown request ever completed billing.
func runMidjourneyFinancialZeroRefundIdentityCase(t *testing.T) {
	charged, info, user, token, _ := seedAtomicFinancialMidjourney(t, 0, true)
	applied, err := SettleMidjourneyTaskBilling(info, charged, true)
	require.NoError(t, err)
	require.True(t, applied)
	require.True(t, RefundMidjourneyQuota(t.Context(), charged, "zero charge repeat"))
	require.True(t, RefundMidjourneyQuota(t.Context(), charged, "zero charge repeat again"))
	used, count := getUserUsageAccounting(t, user.Id)
	assert.Zero(t, used)
	assert.Equal(t, 1, count)
	assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
	require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).Delete(&model.BillingFinalization{}).Error)
	assert.False(t, RefundMidjourneyQuota(t.Context(), charged, "missing owner is not success"))

	canceled, cancelInfo, cancelUser, _, _ := seedAtomicFinancialMidjourney(t, 0, true)
	require.True(t, RefundMidjourneyQuota(t.Context(), canceled, "cancel zero preparation"))
	require.True(t, RefundMidjourneyQuota(t.Context(), canceled, "repeat zero cancellation"))
	applied, err = SettleMidjourneyTaskBilling(cancelInfo, canceled, true)
	require.Error(t, err)
	assert.False(t, applied)
	used, count = getUserUsageAccounting(t, cancelUser.Id)
	assert.Zero(t, used)
	assert.Zero(t, count)
	assert.False(t, getMidjourneyTask(t, canceled.Id).BillingAccounted)
	var owner model.BillingFinalization
	require.NoError(t, model.DB.First(&owner, "request_id = ?", cancelInfo.RequestId).Error)
	assert.Zero(t, owner.ActualQuota)

	legacy, legacyInfo, _, _, _ := seedAtomicFinancialMidjourney(t, 0, true)
	require.NoError(t, model.DB.Model(legacy).Update("token_fingerprint", "").Error)
	assert.False(t, RefundMidjourneyQuota(t.Context(), legacy, "legacy zero has no completion evidence"))
	var owners int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("request_id = ?", legacyInfo.RequestId).Count(&owners).Error)
	assert.Zero(t, owners)
}

func TestMidjourneyFinancialZeroRefundIdentity(t *testing.T) {
	runMidjourneyFinancialZeroRefundIdentityCase(t)
}
