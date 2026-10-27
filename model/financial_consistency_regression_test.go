package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// SQLite's production immediate transaction mode and the independently useful
// deferred/WAL control are separate cases, never interchangeable evidence.
func financialSubscriptionDatabase(t *testing.T, mode string, kind common.DatabaseType, dsn string) *gorm.DB {
	t.Helper()
	if kind != common.DatabaseTypeSQLite {
		return subscriptionPeriodDatabase(t, kind, dsn)
	}
	_, options, ok := strings.Cut(common.SQLitePath, "?")
	require.True(t, ok)
	require.Contains(t, options, "_txlock=immediate")
	if mode == "sqlite_deferred" {
		options = strings.Replace(options, "_txlock=immediate", "_txlock=deferred", 1)
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "financial.db")+"?"+options), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	tables, err := db.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables)
	oldDB, oldKind, oldRedis := DB, common.MainDatabaseType(), common.RedisEnabled
	DB = db
	common.SetMainDatabaseType(kind)
	common.RedisEnabled = false
	t.Cleanup(func() { DB = oldDB; common.SetMainDatabaseType(oldKind); common.RedisEnabled = oldRedis })
	require.NoError(t, db.AutoMigrate(&Option{}))
	var journal, version string
	var busy int
	require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&journal).Error)
	require.NoError(t, db.Raw("PRAGMA busy_timeout").Scan(&busy).Error)
	require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	require.Equal(t, "wal", journal)
	require.Equal(t, 30000, busy)
	t.Logf("financial subscription fixture mode=%s sqlite=%s journal=%s busy_timeout=%d DSN-options=%s", mode, version, journal, busy, options)
	return db
}

func TestFinancialConsistencySubscriptionDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name string
		kind common.DatabaseType
		env  string
	}{
		{"sqlite_immediate", common.DatabaseTypeSQLite, ""},
		{"sqlite_deferred", common.DatabaseTypeSQLite, ""},
		{"mysql", common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"},
		{"postgres", common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			for _, scenario := range []string{"request_identity", "refund_receipt_write_failure", "old_period_refund"} {
				t.Run(scenario, func(t *testing.T) {
					db := financialSubscriptionDatabase(t, dialect.name, dialect.kind, dsn)
					require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
					plan, sub := subscriptionPeriodSeed(t, db)
					if scenario != "old_period_refund" {
						plan.QuotaResetPeriod = SubscriptionResetNever
						require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Save(plan).Error)
						InvalidateSubscriptionPlanCache(plan.Id)
					}
					const request = "financial-regression-base"
					_, err := PreConsumeUserSubscription(request, sub.UserId, "model", 0, 100)
					require.NoError(t, err)
					var receipt SubscriptionPreConsumeRecord
					require.NoError(t, db.Where("request_id = ?", request).First(&receipt).Error)
					if scenario == "request_identity" {
						_, err = PreConsumeUserSubscription(request, sub.UserId+1, "model", 0, 100)
						assert.Error(t, err, "a request ID never authorizes another user")
						_, err = PreConsumeUserSubscription(request, sub.UserId, "model", 0, 101)
						assert.Error(t, err, "the same request ID must preserve the original amount")
						_, err = PreConsumeUserSubscription(request, sub.UserId, "model", 0, 100)
						assert.NoError(t, err)
						require.NoError(t, db.First(sub, sub.Id).Error)
						assert.EqualValues(t, 100, sub.AmountUsed)
						return
					}
					if scenario == "old_period_refund" {
						now := GetDBTimestamp(db)
						require.NoError(t, db.Model(sub).Updates(map[string]any{"last_reset_time": now - 120, "next_reset_time": now - 60}).Error)
						reset, resetErr := ResetDueSubscriptions(10)
						require.NoError(t, resetErr)
						require.Equal(t, 1, reset)
						_, err = PreConsumeUserSubscription("financial-regression-new-period", sub.UserId, "model", 0, 200)
						require.NoError(t, err)
					} else {
						_, err = PreConsumeUserSubscription("financial-regression-other-usage", sub.UserId, "model", 0, 200)
						require.NoError(t, err)
						var hits atomic.Int32
						var enabled atomic.Bool
						enabled.Store(true)
						injected := errors.New("financial regression: receipt write rejected before SQL")
						name := "financial-regression:receipt-write"
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
							if enabled.Load() && tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeFor[SubscriptionPreConsumeRecord]() && tx.Error == nil {
								hits.Add(1)
								tx.AddError(injected)
							}
						}))
						t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(name)) })
						err = financialRefundWithDeadline(t, db, request)
						assert.ErrorIs(t, err, injected)
						assert.EqualValues(t, 1, hits.Load(), "failure must precede the receipt SQL, not a post-COMMIT callback")
						require.NoError(t, db.First(sub, sub.Id).Error)
						assert.EqualValues(t, 300, sub.AmountUsed, "failed receipt write must roll back this refund and preserve other usage")
						var after SubscriptionPreConsumeRecord
						require.NoError(t, db.First(&after, receipt.Id).Error)
						assert.Equal(t, "consumed", after.Status)
						enabled.Store(false)
					}
					assert.NoError(t, financialRefundWithDeadline(t, db, request))
					require.NoError(t, db.First(sub, sub.Id).Error)
					assert.EqualValues(t, 200, sub.AmountUsed, "only the original request's current-period hold may be returned")
					var after SubscriptionPreConsumeRecord
					require.NoError(t, db.First(&after, receipt.Id).Error)
					assert.Equal(t, "refunded", after.Status)
					assert.NoError(t, financialRefundWithDeadline(t, db, request))
					require.NoError(t, db.First(sub, sub.Id).Error)
					assert.EqualValues(t, 200, sub.AmountUsed, "a replay must never consume another request's usage")
				})
			}
		})
	}
}

func financialRefundWithDeadline(t *testing.T, db *gorm.DB, request string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	previous := DB
	DB = db.WithContext(ctx)
	defer func() { DB = previous }()
	return RefundSubscriptionPreConsume(request)
}

// Called from the existing dedicated native finalization matrix. No secondary
// connection or schema is created, and all wallet reads use the current Tx.
func runFinancialWalletReceiptCases(t *testing.T) {
	for _, scenario := range []string{"partial_remaining_replay", "rollback_then_retry", "legacy_overcredit_rejected"} {
		t.Run(scenario, func(t *testing.T) {
			request := "wallet-receipt-" + common.GetRandomString(12)
			user := User{Username: request, AffCode: request, Quota: 3, QuotaBonus: 1, QuotaReward: 1, QuotaPaid: 1}
			require.NoError(t, DB.Create(&user).Error)
			t.Cleanup(func() {
				for _, entity := range []any{&BalanceLedger{}, &BalanceLedgerOperation{}} {
					require.NoError(t, DB.Where("user_id = ?", user.Id).Delete(entity).Error)
				}
				require.NoError(t, DB.Unscoped().Delete(&user).Error)
			})
			_, err := DebitQuota(LedgerEntry{UserId: user.Id, Delta: 3, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:preconsume:" + request}, AccountBonus, AccountReward, AccountPaid)
			require.NoError(t, err)
			partial := LedgerEntry{UserId: user.Id, Reason: LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:settle_refund:" + request + ":2"}
			full := LedgerEntry{UserId: user.Id, Reason: LedgerReasonBillingRefund, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:refund:" + request}
			if scenario == "legacy_overcredit_rejected" {
				_, err = CreditQuotaShares(partial, []LedgerShare{{Account: AccountBonus, Amount: 2}})
				require.NoError(t, err)
				var before User
				require.NoError(t, DB.First(&before, user.Id).Error)
				err = DB.Transaction(func(tx *gorm.DB) error { _, _, err := CreditWalletRefundTx(tx, full, 0); return err })
				require.ErrorIs(t, err, ErrMissingWalletDebitReceipt)
				var after User
				require.NoError(t, DB.First(&after, user.Id).Error)
				assert.Equal(t, before, after)
				var rows int64
				require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", full.IdempotencyKey).Count(&rows).Error)
				assert.Zero(t, rows)
				return
			}
			if scenario == "rollback_then_retry" {
				injected := errors.New("financial owner fails after SQL wallet credit")
				err = DB.Transaction(func(tx *gorm.DB) error {
					_, remaining, err := CreditWalletRefundTx(tx, partial, 2)
					if err != nil {
						return err
					}
					assert.Equal(t, []LedgerShare{{Account: AccountPaid, Amount: 1}}, remaining)
					return injected
				})
				require.ErrorIs(t, err, injected)
				var rolledBack User
				require.NoError(t, DB.First(&rolledBack, user.Id).Error)
				assert.Zero(t, rolledBack.Quota)
				var rows int64
				require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", partial.IdempotencyKey).Count(&rows).Error)
				assert.Zero(t, rows)
			}
			for _, replay := range []bool{false, true} {
				require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
					result, remaining, err := CreditWalletRefundTx(tx, partial, 2)
					if err != nil {
						return err
					}
					assert.Equal(t, replay, result.Replayed)
					assert.Equal(t, []LedgerShare{{Account: AccountPaid, Amount: 1}}, remaining)
					return nil
				}))
			}
			for _, replay := range []bool{false, true} {
				require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
					result, remaining, err := CreditWalletRefundTx(tx, full, 0)
					if err != nil {
						return err
					}
					assert.Equal(t, replay, result.Replayed)
					assert.Empty(t, remaining)
					_, err = WalletRequestReceiptTx(tx, user.Id, request)
					return err
				}))
			}
			var after User
			require.NoError(t, DB.First(&after, user.Id).Error)
			assert.Equal(t, 1, after.QuotaBonus)
			assert.Equal(t, 1, after.QuotaReward)
			assert.Equal(t, 1, after.QuotaPaid)
			assert.Equal(t, 3, after.Quota)
			var credits []BalanceLedger
			require.NoError(t, DB.Where("user_id = ? AND delta > 0", user.Id).Find(&credits).Error)
			totals := map[string]int{}
			for _, row := range credits {
				totals[row.Account] += row.Delta
			}
			assert.Equal(t, map[string]int{"bonus": 1, "reward": 1, "paid": 1}, totals, "actual committed credits must equal each original source, not just the total")
		})
	}
}
