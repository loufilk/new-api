package service

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DSNs must name disposable databases. These cases use real wallet operations,
// token updates, task reloads, rollbacks and a separately configured log store.
func TestTaskWalletReceiptsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name   common.DatabaseType
		env    string
		logEnv string
	}{
		{common.DatabaseTypeSQLite, "", ""},
		{common.DatabaseTypeMySQL, "TEST_MYSQL_DSN", "TEST_MYSQL_LOG_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN", "TEST_POSTGRES_LOG_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			open := func(dsn string) *gorm.DB {
				var driver gorm.Dialector = sqlite.Open(":memory:")
				if dialect.name == common.DatabaseTypeMySQL {
					driver = mysql.Open(dsn)
				} else if dialect.name == common.DatabaseTypePostgreSQL {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
				db, err := gorm.Open(driver, &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
				t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
				return db
			}
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " is not configured")
			}
			db := open(dsn)
			logDB := db
			if logDSN := os.Getenv(dialect.logEnv); logDSN != "" || dialect.name == common.DatabaseTypeSQLite {
				logDB = open(logDSN)
			}
			oldDB, oldLogDB := model.DB, model.LOG_DB
			oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			model.DB, model.LOG_DB = db, logDB
			common.SetDatabaseTypes(dialect.name, dialect.name)
			t.Cleanup(func() {
				model.DB, model.LOG_DB = oldDB, oldLogDB
				common.SetDatabaseTypes(oldMainType, oldLogType)
				model.InvalidateModelBonusEligibleCache()
			})
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.Option{}, &model.Task{}, &model.Midjourney{}, &model.UserSubscription{}, &model.SubscriptionPlan{}, &model.SubscriptionPreConsumeRecord{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			query := "SELECT version()"
			if dialect.name == common.DatabaseTypeSQLite {
				query = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("validated %s: %s", dialect.name, version)
			seedBonusEligibleModel(t, "mj_imagine", boolPtr(true))
			t.Cleanup(func() {
				require.NoError(t, db.Unscoped().Where("model_name = ?", "mj_imagine").Delete(&model.Model{}).Error)
			})
			runTaskWalletReceiptCases(t)
			runMidjourneyWalletReceiptCases(t)
		})
	}
}

func seedReceiptActors(t *testing.T, bonus, reward int) (*model.User, *model.Token, *model.Channel) {
	t.Helper()
	identity := "receipt-" + common.GetRandomString(8)
	user := &model.User{Username: identity, AffCode: identity, Status: common.UserStatusEnabled, QuotaPaid: 1000, QuotaBonus: bonus, QuotaReward: reward, Quota: 1000 + bonus + reward}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{UserId: user.Id, Key: fmt.Sprintf("receipt-key-%d", user.Id), Status: common.TokenStatusEnabled, RemainQuota: 2000, Name: "receipt-token"}
	require.NoError(t, model.DB.Create(token).Error)
	channel := &model.Channel{Name: "receipt-channel", Key: "unused", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(channel).Error)
	t.Cleanup(func() {
		for _, entity := range []any{&model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.SubscriptionPreConsumeRecord{}, &model.Task{}, &model.Midjourney{}, &model.UserSubscription{}} {
			require.NoError(t, model.DB.Unscoped().Where("user_id = ?", user.Id).Delete(entity).Error)
		}
		require.NoError(t, model.LOG_DB.Where("user_id = ?", user.Id).Delete(&model.Log{}).Error)
		require.NoError(t, model.DB.Unscoped().Delete(token).Error)
		require.NoError(t, model.DB.Unscoped().Delete(channel).Error)
		require.NoError(t, model.DB.Unscoped().Delete(user).Error)
	})
	return user, token, channel
}

func countReceiptLogs(t *testing.T, userID int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ?", userID).Count(&count).Error)
	return count
}

func seedReceiptTask(t *testing.T, user *model.User, token *model.Token, channel *model.Channel, quota int, order ...model.LedgerAccount) *model.Task {
	t.Helper()
	if len(order) == 0 {
		order = []model.LedgerAccount{model.AccountBonus, model.AccountReward, model.AccountPaid}
	}
	var receipt *model.LedgerResult
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = model.DebitQuotaTx(tx, model.LedgerEntry{UserId: user.Id, Delta: quota, Reason: model.LedgerReasonBillingPreConsume, IdempotencyKey: fmt.Sprintf("receipt-preconsume-%d", user.Id)}, order...)
		if err != nil {
			return err
		}
		return model.ApplyTokenQuotaDeltaTx(tx, token.Id, token.Key, -quota)
	}))
	task := makeTask(user.Id, channel.Id, quota, token.Id, BillingSourceWallet, 0)
	task.TaskID = fmt.Sprintf("receipt-task-%d", user.Id)
	task.Status = model.TaskStatusSuccess
	task.PrivateData.WalletDebit = appendTaskShares(nil, receipt)
	task.PrivateData.TokenFingerprint = model.BillingTokenFingerprint(token.Key)
	task.PrivateData.FinancialVersion = 2
	task.PrivateData.SubmissionAccounted = true
	require.NoError(t, model.DB.Create(task).Error)
	seedChargedAccounting(t, user.Id, channel.Id, token.Id, quota, 1)
	return task
}

// Only a known provider failure can authorize a failed-task refund. This
// fixture transition occurs before fault injection and cannot overwrite a
// concurrent SUCCESS through the production polling API.
func markReceiptTaskFailed(t *testing.T, task *model.Task) {
	t.Helper()
	task.Status = model.TaskStatusFailure
	require.NoError(t, model.DB.Model(task).Update("status", task.Status).Error)
}

func failReceiptUpdates(t *testing.T, table string) func() {
	t.Helper()
	var enabled atomic.Bool
	enabled.Store(true)
	name := "test:receipt-rollback:" + t.Name()
	require.NoError(t, model.DB.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
		if enabled.Load() && tx.Statement.Table == table {
			tx.AddError(errors.New("forced billing persistence failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(name)) })
	return func() { enabled.Store(false) }
}

const (
	officialFailureBeforeSQL   = "before_sql"
	officialFailureAfterCommit = "after_commit"
)

type officialUpdateFault struct {
	table        string
	boundary     string
	enabled      atomic.Bool
	hits         atomic.Int32
	rowsAffected atomic.Int64
	insideTx     atomic.Bool
}

// An After("gorm:update") callback registered after GORM's defaults is sorted
// after their commit callback, not immediately after SQL. Keep failReceiptUpdates
// for the existing explicit outer-transaction rollback proofs; independent
// official writes need a separately defined before-SQL or after-commit boundary.
func injectOfficialUpdateFault(t *testing.T, table, boundary string) *officialUpdateFault {
	t.Helper()
	require.Contains(t, []string{officialFailureBeforeSQL, officialFailureAfterCommit}, boundary)
	fault := &officialUpdateFault{table: table, boundary: boundary}
	fault.enabled.Store(true)
	name := "test:official-update-fault:" + t.Name()
	callback := func(tx *gorm.DB) {
		if !fault.enabled.Load() || tx.Statement.Table != table || tx.Error != nil {
			return
		}
		fault.hits.Add(1)
		fault.rowsAffected.Store(tx.RowsAffected)
		_, insideTx := tx.Statement.ConnPool.(gorm.TxCommitter)
		fault.insideTx.Store(insideTx)
		tx.AddError(errors.New("synthetic official " + table + " failure at " + boundary))
	}
	var err error
	if boundary == officialFailureBeforeSQL {
		err = model.DB.Callback().Update().Before("gorm:update").Register(name, callback)
	} else {
		err = model.DB.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(name, callback)
	}
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(name)) })
	return fault
}

func (f *officialUpdateFault) disable() { f.enabled.Store(false) }

func (f *officialUpdateFault) requireOneHit(t *testing.T) {
	t.Helper()
	require.EqualValues(t, 1, f.hits.Load(), "exactly one %s fault at %s must explain the result", f.table, f.boundary)
	if f.boundary == officialFailureAfterCommit {
		require.EqualValues(t, 1, f.rowsAffected.Load(), "the target SQL must have affected its row before the synthetic error")
		require.False(t, f.insideTx.Load(), "default commit must have returned the connection before the synthetic error")
	}
	t.Logf("official fault table=%s boundary=%s hits=%d rows_affected=%d inside_tx=%t", f.table, f.boundary, f.hits.Load(), f.rowsAffected.Load(), f.insideTx.Load())
}

func runTaskWalletReceiptCases(t *testing.T) {
	t.Run("settlement_falls_back_and_persists_cumulative_receipt", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 40)
		task := seedReceiptTask(t, user, token, channel, 100)
		stale := *task
		RecalculateTaskQuota(t.Context(), task, 300, common.NewMessage("receipt settlement"))
		var persisted model.Task
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.Equal(t, 300, persisted.Quota)
		assert.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 100}, {Account: model.AccountReward, Amount: 40}, {Account: model.AccountPaid, Amount: 160}}, persisted.PrivateData.WalletDebit)
		assertUserBalances(t, user.Id, 0, 840, 0)
		assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
		assert.Equal(t, 300, getTokenUsedQuota(t, token.Id))

		RecalculateTaskQuota(t.Context(), &stale, 300, common.NewMessage("stale settlement replay"))
		assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		assert.Equal(t, persisted.PrivateData.WalletDebit, stale.PrivateData.WalletDebit)

		stale = persisted
		require.False(t, RefundTaskQuota(t.Context(), &persisted, "successful terminal settlement cannot become a refund"))
		require.False(t, RefundTaskQuota(t.Context(), &stale, "stale conflicting terminal decision"))
		assertUserBalances(t, user.Id, 0, 840, 0)
		assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
		assert.Equal(t, 300, getTokenUsedQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.Equal(t, 300, persisted.Quota)
		assert.Equal(t, stale.PrivateData.WalletDebit, persisted.PrivateData.WalletDebit)
		used, requests := getUserUsageAccounting(t, user.Id)
		assert.Equal(t, 300, used)
		assert.Equal(t, 1, requests)
	})

	t.Run("partial_refund_persists_remaining_accounts", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 700, 0)
		task := seedReceiptTask(t, user, token, channel, 1000)
		RecalculateTaskQuota(t.Context(), task, 400, common.NewMessage("partial refund"))
		var persisted model.Task
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.Equal(t, 400, persisted.Quota)
		assert.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 280}, {Account: model.AccountPaid, Amount: 120}}, persisted.PrivateData.WalletDebit)
		assertUserBalances(t, user.Id, 420, 880, 0)
		assert.Equal(t, 1600, getTokenRemainQuota(t, token.Id))
	})

	t.Run("settlement_keeps_existing_bonus_exclusion", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 40)
		task := seedReceiptTask(t, user, token, channel, 100, model.AccountPaid)
		// Current model policy permits bonus, but this request's recorded funding
		// identity only permits generally spendable accounts.
		task.PrivateData.BillingContext.BillingModelName = "mj_imagine"
		RecalculateTaskQuota(t.Context(), task, 1040, common.NewMessage("preserve request bonus exclusion"))
		assertUserBalances(t, user.Id, 100, 0, 0)
		assert.Equal(t, 960, getTokenRemainQuota(t, token.Id))
		var persisted model.Task
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 1000}, {Account: model.AccountReward, Amount: 40}}, persisted.PrivateData.WalletDebit)
	})

	t.Run("legacy_missing_receipt_fails_closed", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 0, 0)
		task := seedReceiptTask(t, user, token, channel, 100, model.AccountPaid)
		task.PrivateData.WalletDebit = nil
		require.NoError(t, model.DB.Model(task).Select("private_data").Updates(task).Error)
		RecalculateTaskQuota(t.Context(), task, 300, common.NewMessage("cannot invent legacy paid source"))
		var persisted model.Task
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.Nil(t, persisted.PrivateData.WalletDebit)
		assert.Equal(t, 100, persisted.Quota)
		markReceiptTaskFailed(t, &persisted)
		require.False(t, RefundTaskQuota(t.Context(), &persisted, "missing source requires review"))
		assertUserBalances(t, user.Id, 0, 900, 0)
		assert.Equal(t, 1900, getTokenRemainQuota(t, token.Id))
		assert.Zero(t, countReceiptLogs(t, user.Id))
	})

	for _, table := range []string{"tokens", "tasks"} {
		t.Run("settlement_rolls_back_on_"+table+"_failure", func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 40)
			task := seedReceiptTask(t, user, token, channel, 100)
			restore := failReceiptUpdates(t, table)
			RecalculateTaskQuota(t.Context(), task, 300, common.NewMessage("failed settlement"))
			assertUserBalances(t, user.Id, 0, 1000, 40)
			assert.Equal(t, 1900, getTokenRemainQuota(t, token.Id))
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, 100, persisted.Quota)
			assert.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 100}}, persisted.PrivateData.WalletDebit)
			var count int64
			require.NoError(t, model.DB.Model(&model.BalanceLedgerOperation{}).Where("idempotency_key = ?", "task:settle:"+task.TaskID).Count(&count).Error)
			assert.Zero(t, count)
			restore()
			RecalculateTaskQuota(t.Context(), task, 300, common.NewMessage("settlement retry"))
			assert.Equal(t, 300, task.Quota)
			assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
		})
		t.Run("refund_atomic_"+table+"_failure", func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 40)
			task := seedReceiptTask(t, user, token, channel, 300)
			markReceiptTaskFailed(t, task)
			fault := injectOfficialUpdateFault(t, table, officialFailureBeforeSQL)
			require.False(t, RefundTaskQuota(t.Context(), task, "atomic refund failure"))
			fault.requireOneHit(t)
			assertUserBalances(t, user.Id, 0, 840, 0)
			assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, 300, persisted.Quota)
			assert.Equal(t, task.PrivateData.WalletDebit, persisted.PrivateData.WalletDebit)
			var count int64
			require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Where("user_id = ? AND reason = ?", user.Id, model.LedgerReasonTaskRefund).Count(&count).Error)
			assert.Zero(t, count)
			assert.Zero(t, countReceiptLogs(t, user.Id))
			fault.disable()
			require.True(t, RefundTaskQuota(t.Context(), &persisted, "retry after rollback"))
			require.True(t, RefundTaskQuota(t.Context(), task, "stale repeat after commit"))
			fault.requireOneHit(t)
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assertUserBalances(t, user.Id, 100, 1000, 40)
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		})
	}

	t.Run("subscription_settlement_rolls_back_with_token_failure", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 0, 0)
		plan := &model.SubscriptionPlan{Title: "task receipt subscription", TotalAmount: 5000, QuotaResetPeriod: model.SubscriptionResetNever}
		require.NoError(t, model.DB.Session(&gorm.Session{SkipHooks: true}).Create(plan).Error)
		model.InvalidateSubscriptionPlanCache(plan.Id)
		t.Cleanup(func() {
			model.InvalidateSubscriptionPlanCache(plan.Id)
			require.NoError(t, model.DB.Session(&gorm.Session{SkipHooks: true}).Delete(plan).Error)
		})
		now := model.GetDBTimestamp()
		sub := model.UserSubscription{UserId: user.Id, PlanId: plan.Id, AmountTotal: 5000, QuotaPeriod: 1, Status: "active", StartTime: now - 10, EndTime: now + 3600}
		require.NoError(t, model.DB.Create(&sub).Error)
		info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: fmt.Sprintf("subscription-task-%d", user.Id), ForcePreConsume: true, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}}
		info.UserSetting.BillingPreference = "subscription_only"
		c := testGinContext(t)
		c.Set("token_quota", token.RemainQuota)
		session, apiErr := NewBillingSession(c, info, 100)
		require.Nil(t, apiErr)
		info.Billing = session
		task := financialTaskForSubmission(info, 100)
		require.NoError(t, PersistTaskBilling(info, task))
		fault := injectOfficialUpdateFault(t, "tokens", officialFailureBeforeSQL)
		RecalculateTaskQuota(t.Context(), task, 300, common.NewMessage("subscription terminal settlement"))
		fault.requireOneHit(t)
		require.NoError(t, model.DB.First(&sub, sub.Id).Error)
		assert.EqualValues(t, 100, sub.AmountUsed)
		assert.Equal(t, 1900, getTokenRemainQuota(t, token.Id))
		assert.Equal(t, 100, task.Quota)
		fault.disable()
		RecalculateTaskQuota(t.Context(), task, 300, common.NewMessage("retry terminal settlement"))
		require.NoError(t, model.DB.First(&sub, sub.Id).Error)
		assert.EqualValues(t, 300, sub.AmountUsed)
		assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
		assert.Equal(t, 300, task.Quota)
	})
}

func receiptMidjourneyInfo(user *model.User, token *model.Token, channel *model.Channel) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: fmt.Sprintf("receipt-mj-%d", user.Id), UserQuota: user.Quota, BillingSource: BillingSourceWallet, BillingModelName: "mj_imagine", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}}
	info.UserSetting.QuotaWarningEnabled = boolPtr(false)
	info.UserSetting.QuotaWarningThreshold = -1 // Keep synthetic finance fixtures notification-free on 093.
	return info
}

func prepareReceiptMidjourney(t *testing.T, info *relaycommon.RelayInfo) *model.Midjourney {
	t.Helper()
	task := &model.Midjourney{UserId: info.UserId, ChannelId: info.ChannelId, MjId: info.RequestId, Action: "IMAGINE"}
	prepared, err := PrepareMidjourneyTaskBilling(info, task, 300, true)
	require.NoError(t, err)
	require.True(t, prepared)
	require.NoError(t, task.Insert())
	return task
}

func runMidjourneyWalletReceiptCases(t *testing.T) {
	t.Run("midjourney_restores_original_accounts_from_stale_observer", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 50)
		info := receiptMidjourneyInfo(user, token, channel)
		task := prepareReceiptMidjourney(t, info)
		observer := *task
		billed, err := SettleMidjourneyTaskBilling(info, task, true)
		require.NoError(t, err)
		require.True(t, billed)
		assertUserBalances(t, user.Id, 0, 850, 0)
		assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
		persisted := getMidjourneyTask(t, task.Id)
		assert.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 100}, {Account: model.AccountReward, Amount: 50}, {Account: model.AccountPaid, Amount: 150}}, persisted.WalletDebit)
		encoded, err := common.Marshal(persisted)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "wallet_debit")

		billed, err = SettleMidjourneyTaskBilling(info, task, true)
		require.NoError(t, err)
		require.False(t, billed, "replay must not emit another submission log")
		assertUserBalances(t, user.Id, 0, 850, 0)
		assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))

		// The observer predates the debit. Its status update must preserve the
		// receipt, and its subsequent refund must load that committed receipt.
		observer.Status = "FAILURE"
		won, err := observer.UpdateWithStatus("")
		require.NoError(t, err)
		require.True(t, won)
		assert.Equal(t, persisted.WalletDebit, getMidjourneyTask(t, task.Id).WalletDebit)
		require.True(t, RefundMidjourneyQuota(t.Context(), &observer, "provider failure"))
		require.True(t, RefundMidjourneyQuota(t.Context(), task, "duplicate observer"))
		assertUserBalances(t, user.Id, 100, 1000, 50)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.Zero(t, getTokenUsedQuota(t, token.Id))
		assert.Zero(t, getMidjourneyTask(t, task.Id).Quota)
		assert.Empty(t, getMidjourneyTask(t, task.Id).WalletDebit)
		var logs int64
		require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ?", user.Id).Count(&logs).Error)
		assert.EqualValues(t, 1, logs)
	})

	for _, table := range []string{"tokens", "midjourneys"} {
		t.Run("midjourney_charge_rolls_back_on_"+table+"_failure", func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 50)
			info := receiptMidjourneyInfo(user, token, channel)
			task := prepareReceiptMidjourney(t, info)
			restore := failReceiptUpdates(t, table)
			billed, err := SettleMidjourneyTaskBilling(info, task, true)
			require.Error(t, err)
			assert.False(t, billed)
			assertUserBalances(t, user.Id, 100, 1000, 50)
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			var operations int64
			require.NoError(t, model.DB.Model(&model.BalanceLedgerOperation{}).Where("user_id = ?", user.Id).Count(&operations).Error)
			assert.Zero(t, operations)
			persisted := getMidjourneyTask(t, task.Id)
			require.NotNil(t, persisted.WalletDebit, "empty new receipts must not become legacy NULL")
			assert.Empty(t, persisted.WalletDebit)
			restore()
			// A crash/failure before the debit must never mint a paid refund.
			require.True(t, RefundMidjourneyQuota(t.Context(), &persisted, "uncharged task"))
			assertUserBalances(t, user.Id, 100, 1000, 50)
			assert.Zero(t, getMidjourneyTask(t, task.Id).Quota)
		})
	}

	for _, table := range []string{"tokens", "midjourneys"} {
		t.Run("midjourney_refund_atomic_"+table+"_failure", func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 50)
			info := receiptMidjourneyInfo(user, token, channel)
			task := prepareReceiptMidjourney(t, info)
			billed, err := SettleMidjourneyTaskBilling(info, task, true)
			require.NoError(t, err)
			require.True(t, billed)
			receipt := append([]model.LedgerShare(nil), task.WalletDebit...)
			require.NoError(t, model.DB.Model(task).Update("status", "FAILURE").Error)
			fault := injectOfficialUpdateFault(t, table, officialFailureBeforeSQL)
			require.False(t, RefundMidjourneyQuota(t.Context(), task, "atomic refund failure"))
			fault.requireOneHit(t)
			assertUserBalances(t, user.Id, 0, 850, 0)
			assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
			persisted := getMidjourneyTask(t, task.Id)
			assert.Equal(t, 300, persisted.Quota)
			assert.Equal(t, receipt, persisted.WalletDebit)
			var refunds int64
			require.NoError(t, model.DB.Model(&model.BalanceLedgerOperation{}).Where("idempotency_key = ?", fmt.Sprintf("midjourney:financial-refund:%d", task.Id)).Count(&refunds).Error)
			assert.Zero(t, refunds)
			assert.Zero(t, countReceiptLogs(t, user.Id))
			fault.disable()
			require.True(t, RefundMidjourneyQuota(t.Context(), &persisted, "retry after rollback"))
			require.True(t, RefundMidjourneyQuota(t.Context(), task, "stale repeat after commit"))
			fault.requireOneHit(t)
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assertUserBalances(t, user.Id, 100, 1000, 50)
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		})
	}

	t.Run("midjourney_failure_before_charge_never_debits", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 50)
		info := receiptMidjourneyInfo(user, token, channel)
		task := prepareReceiptMidjourney(t, info)
		observer := *task
		observer.Status = "FAILURE"
		won, err := observer.UpdateWithStatus("")
		require.NoError(t, err)
		require.True(t, won)
		billed, err := SettleMidjourneyTaskBilling(info, task, true)
		require.Error(t, err)
		assert.False(t, billed)
		require.True(t, RefundMidjourneyQuota(t.Context(), &observer, "early provider failure"))
		assertUserBalances(t, user.Id, 100, 1000, 50)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
	})
}
