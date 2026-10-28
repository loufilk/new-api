package model

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// These opt-ins MUST name fresh, dedicated, disposable databases, never the
// service matrix's databases or generic TEST_* DSNs. No test drops a database.
func TestBillingFinalizationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_FINALIZATION_MODEL_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_FINALIZATION_MODEL_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
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
			sqlDB.SetMaxOpenConns(8)
			if dialect.name == common.DatabaseTypeSQLite {
				sqlDB.SetMaxOpenConns(1)
			}
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			tables, err := db.Migrator().GetTables()
			require.NoError(t, err)
			require.Empty(t, tables, "dedicated database must be empty; refusing existing data")
			oldDB, oldType, oldLogType := DB, common.MainDatabaseType(), common.LogDatabaseType()
			DB = db
			common.SetDatabaseTypes(dialect.name, dialect.name)
			t.Cleanup(func() { DB = oldDB; common.SetDatabaseTypes(oldType, oldLogType) })
			// Representative previous-schema database, then additive migration.
			require.NoError(t, db.AutoMigrate(&Option{}, &User{}, &Token{}, &BalanceLedgerOperation{}, &BalanceLedger{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &Task{}))
			require.False(t, db.Migrator().HasTable(&BillingFinalization{}))
			require.NoError(t, db.AutoMigrate(&BillingFinalization{}))
			serial := 0
			seed := func(t *testing.T, sourceHeld, tokenHeld int) BillingFinalization {
				serial++
				id := 41000 + serial
				request := fmt.Sprintf("terminal-%d", id)
				key := fmt.Sprintf("synthetic-terminal-token-%d", id)
				require.NoError(t, db.Create(&User{Id: id, Username: request, AffCode: request, Quota: 1000, QuotaPaid: 500, QuotaBonus: 300, QuotaReward: 200}).Error)
				require.NoError(t, db.Create(&Token{Id: id, UserId: id, Key: key, RemainQuota: 1000 - tokenHeld, UsedQuota: tokenHeld}).Error)
				result := &LedgerResult{}
				if sourceHeld > 0 {
					var err error
					result, err = DebitQuota(LedgerEntry{UserId: id, Delta: sourceHeld, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:preconsume:" + request}, []LedgerAccount{AccountPaid, AccountBonus, AccountReward}...)
					require.NoError(t, err)
				}
				r := BillingFinalization{RequestID: request, UserID: id, Source: "wallet", TokenID: id, TokenFingerprint: BillingTokenFingerprint(key), SourceHeld: sourceHeld, TokenHeld: tokenHeld, PreConsumed: sourceHeld}
				for _, row := range result.Rows {
					switch LedgerAccount(row.Account) {
					case AccountPaid:
						r.Paid -= row.Delta
					case AccountBonus:
						r.Bonus -= row.Delta
					case AccountReward:
						r.Reward -= row.Delta
					}
				}
				return r
			}
			// Source-evidence rejections must start behind a genuine held owner;
			// otherwise a missing-owner rejection could make these tests falsely green.
			seedHeld := func(t *testing.T, amount int) BillingFinalization {
				t.Helper()
				serial++
				id := 41000 + serial
				request := fmt.Sprintf("terminal-held-%d", id)
				key := fmt.Sprintf("synthetic-held-token-%d", id)
				require.NoError(t, db.Create(&User{Id: id, Username: request, AffCode: request, Quota: 1000, QuotaPaid: 500, QuotaBonus: 300, QuotaReward: 200}).Error)
				require.NoError(t, db.Create(&Token{Id: id, UserId: id, Key: key, RemainQuota: 1000}).Error)
				authorization := BillingFinalization{RequestID: request, UserID: id, Source: "wallet", TokenID: id, TokenFingerprint: BillingTokenFingerprint(key)}
				r, attempt, err := ReserveBillingFinalization(t.Context(), authorization, nil, amount, func(tx *gorm.DB, held *BillingFinalization) error {
					result, err := DebitQuotaTx(tx, LedgerEntry{UserId: id, Delta: amount, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:preconsume:" + request}, AccountPaid, AccountBonus, AccountReward)
					if err != nil {
						return err
					}
					for _, row := range result.Rows {
						switch LedgerAccount(row.Account) {
						case AccountPaid:
							held.Paid -= row.Delta
						case AccountBonus:
							held.Bonus -= row.Delta
						case AccountReward:
							held.Reward -= row.Delta
						}
					}
					if err := ReserveBillingTokenTx(tx, held, amount, false); err != nil {
						return err
					}
					held.SourceHeld, held.TokenHeld = amount, amount
					return nil
				})
				require.NoError(t, err)
				require.True(t, attempt.Applied)
				require.True(t, attempt.FreshInitialPrepared)
				require.Equal(t, BillingHeld, r.State)
				return r
			}
			balances := func(t *testing.T, r BillingFinalization, wallet, tokenQuota int) {
				t.Helper()
				var u User
				var token Token
				require.NoError(t, db.First(&u, r.UserID).Error)
				require.NoError(t, db.Unscoped().First(&token, r.TokenID).Error)
				require.Equal(t, wallet, u.Quota)
				require.Equal(t, tokenQuota, token.RemainQuota)
			}

			t.Run("token_counter_bounds", runFinancialTokenCounterBoundsCases)
			if dialect.name != common.DatabaseTypeSQLite {
				t.Run("subscription_wallet_lock_order", runFinancialSubscriptionLockOrderCases)
			}

			t.Run("wallet_refund_sequence", runFinancialWalletReceiptCases)

			t.Run("receipt_mismatch_and_legacy_replay", func(t *testing.T) {
				r := seedHeld(t, 100)
				bad := r
				bad.Paid = 99
				bad.SourceHeld = 99
				_, err := SettleBillingFinalization(context.Background(), bad, bad.PreConsumed, func(*gorm.DB) error { return nil })
				require.Error(t, err)
				// An old source-only refund is NOT proof a token/owner committed.
				_, err = CreditQuotaShares(LedgerEntry{UserId: r.UserID, Delta: 100, Reason: LedgerReasonBillingRefund, ReferenceType: "relay", ReferenceId: r.RequestID, RequestId: r.RequestID, IdempotencyKey: "billing:refund:" + r.RequestID}, r.Shares())
				require.NoError(t, err)
				_, err = SettleBillingFinalization(context.Background(), r, r.PreConsumed, func(*gorm.DB) error { return nil })
				require.Error(t, err)
				code, _ := BillingRefundErrorCode(err)
				require.Equal(t, "source_evidence", code)
				balances(t, r, 1000, 900)
			})
			t.Run("near_miss_realtime_key_rejected", func(t *testing.T) {
				for _, suffix := range []string{"wss:01:20", "wss:+1:20", "wss:0:20", "wss:1:21", "wss:18446744073709551616:20"} {
					t.Run(suffix, func(t *testing.T) {
						r := seedHeld(t, 100)
						_, err := DebitQuota(LedgerEntry{UserId: r.UserID, Delta: 20, Reason: LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: r.RequestID, RequestId: r.RequestID, IdempotencyKey: "billing:settle:" + r.RequestID + ":" + suffix}, AccountPaid)
						require.NoError(t, err)
						_, err = SettleBillingFinalization(context.Background(), r, r.PreConsumed, func(*gorm.DB) error { return nil })
						require.Error(t, err)
						code, _ := BillingRefundErrorCode(err)
						require.Equal(t, "source_evidence", code)
						balances(t, r, 880, 900)
					})
				}
			})
			t.Run("migration_preserves_pending_and_tombstones", func(t *testing.T) {
				r := seed(t, 100, 100)
				r.State = BillingRefundPending
				require.NoError(t, db.Create(&r).Error)
				empty := seed(t, 100, 0)
				empty.TokenID, empty.TokenFingerprint = 0, ""
				empty.State = BillingRefunded
				require.NoError(t, db.Create(&empty).Error)
				before := []BillingFinalization{}
				require.NoError(t, db.Order("request_id").Find(&before).Error)
				// Reproduce the initial fixed-width schema on this owned fixture,
				// including existing empty no-token and full SHA-256 identities.
				legacy := struct {
					TokenFingerprint string `gorm:"type:char(64);not null"`
				}{}
				require.NoError(t, db.Table("billing_finalizations").Migrator().AlterColumn(&legacy, "TokenFingerprint"))
				if dialect.name == common.DatabaseTypePostgreSQL {
					var padded string
					require.NoError(t, db.Model(&BillingFinalization{}).Select("token_fingerprint").Where("request_id = ?", empty.RequestID).Scan(&padded).Error)
					require.Equal(t, 64, len(padded), "legacy PostgreSQL CHAR identity padding reproducer")
				}
				require.NoError(t, db.AutoMigrate(&BillingFinalization{}))
				recorder := &migrationSQLRecorder{}
				require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(&BillingFinalization{}))
				require.Empty(t, recorder.schemaMutations(), "second migration must not repeat schema changes")
				after := []BillingFinalization{}
				require.NoError(t, db.Order("request_id").Find(&after).Error)
				require.Equal(t, before, after)
				balances(t, r, 900, 900)
				empty.TokenID = empty.UserID // inspect the unrelated unchanged fixture token
				balances(t, empty, 900, 1000)
			})
			t.Run("rollback_probe_no_permanent_owner", func(t *testing.T) {
				r := seed(t, 100, 100)
				stored, err := ProbeBillingFinalization(context.Background(), r)
				require.NoError(t, err)
				require.Nil(t, stored)
				var count int64
				require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", r.RequestID).Count(&count).Error)
				require.Zero(t, count)
				malformed := r
				malformed.State = "building"
				require.NoError(t, db.Create(&malformed).Error)
				_, err = ProbeBillingFinalization(context.Background(), r)
				require.ErrorContains(t, err, "invalid persisted")
			})
		})
	}
}
