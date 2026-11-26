package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// This opt-in must name an empty, disposable database, never a service database.
func TestBillingLockedTokenMutationPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_LOCKED_TOKEN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated TEST_LOCKED_TOKEN_POSTGRES_DSN")
	}
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	tables, err := db.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables, "refusing a nonempty token regression database")
	oldDB, oldRedis := DB, common.RedisEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	DB, common.RedisEnabled = db, false
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() { DB, common.RedisEnabled = oldDB, oldRedis; common.SetDatabaseTypes(oldMain, oldLog) })
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &BillingFinalization{}, &BalanceLedgerOperation{}, &BalanceLedger{}))
	require.NoError(t, db.AutoMigrate(BonusSourceModels()...))
	serial := 0
	seed := func(t *testing.T) (Token, BillingFinalization) {
		t.Helper()
		serial++
		name := fmt.Sprintf("locked-token-%d", serial)
		user := User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Quota: 1000, QuotaPaid: 1000}
		require.NoError(t, db.Create(&user).Error)
		token := Token{UserId: user.Id, Key: "synthetic-" + name, Status: common.TokenStatusEnabled, RemainQuota: 100, UsedQuota: 20}
		require.NoError(t, db.Create(&token).Error)
		return token, BillingFinalization{RequestID: name, UserID: user.Id, Source: "wallet", TokenID: token.Id, TokenFingerprint: BillingTokenFingerprint(token.Key)}
	}

	for _, path := range []struct {
		name  string
		apply func(*gorm.DB, Token, BillingFinalization) error
	}{
		{"reservation", func(tx *gorm.DB, _ Token, receipt BillingFinalization) error {
			return ReserveBillingTokenTx(tx, &receipt, 20, false)
		}},
		{"settlement", func(tx *gorm.DB, token Token, receipt BillingFinalization) error {
			return SettleTokenQuotaDeltaTx(tx, token.Id, receipt.UserID, token.Key, -20)
		}},
		{"terminal_owner", func(tx *gorm.DB, _ Token, receipt BillingFinalization) error {
			return ApplyBillingTokenDeltaTx(tx, &receipt, -20)
		}},
	} {
		t.Run(path.name, func(t *testing.T) {
			t.Run("successive_mutations_read_current_counters", func(t *testing.T) {
				token, receipt := seed(t)
				reads := 0
				callback := "locked_token_read_measurement"
				require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" {
						reads++
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					for range 2 {
						if err := path.apply(tx, token, receipt); err != nil {
							return err
						}
					}
					return nil
				}))
				t.Logf("two committed token mutations used %d token SELECTs", reads)
				var after Token
				require.NoError(t, db.First(&after, token.Id).Error)
				assert.Equal(t, 60, after.RemainQuota)
				assert.Equal(t, 60, after.UsedQuota)
				assert.Equal(t, token.Status, after.Status)
				assert.Equal(t, token.CacheRevision, after.CacheRevision)
			})
			t.Run("later_overflow_rolls_back_both_mutations", func(t *testing.T) {
				token, receipt := seed(t)
				require.NoError(t, db.Model(&token).Update("used_quota", math.MaxInt-30).Error)
				err := db.Transaction(func(tx *gorm.DB) error {
					if err := path.apply(tx, token, receipt); err != nil {
						return err
					}
					return path.apply(tx, token, receipt)
				})
				require.Error(t, err)
				code, _ := BillingRefundErrorCode(err)
				assert.Equal(t, "token_counter_bounds", code)
				var after Token
				require.NoError(t, db.First(&after, token.Id).Error)
				assert.Equal(t, 100, after.RemainQuota)
				assert.Equal(t, math.MaxInt-30, after.UsedQuota)
			})
			t.Run("identity_mismatch_never_mutates", func(t *testing.T) {
				token, receipt := seed(t)
				for _, field := range []string{"owner", "credential"} {
					t.Run(field, func(t *testing.T) {
						wrongToken, wrongReceipt := token, receipt
						if field == "owner" {
							wrongReceipt.UserID++
						} else {
							wrongToken.Key = "synthetic-replacement"
							wrongReceipt.TokenFingerprint = BillingTokenFingerprint(wrongToken.Key)
						}
						require.Error(t, db.Transaction(func(tx *gorm.DB) error { return path.apply(tx, wrongToken, wrongReceipt) }))
					})
				}
				var after Token
				require.NoError(t, db.First(&after, token.Id).Error)
				assert.Equal(t, token.RemainQuota, after.RemainQuota)
				assert.Equal(t, token.UsedQuota, after.UsedQuota)
			})
			t.Run("revocation_not_revived", func(t *testing.T) {
				token, receipt := seed(t)
				require.NoError(t, db.Model(&token).Update("status", common.TokenStatusDisabled).Error)
				require.NoError(t, db.Delete(&token).Error)
				err := db.Transaction(func(tx *gorm.DB) error { return path.apply(tx, token, receipt) })
				wantRemain, wantUsed := 80, 40
				if path.name == "reservation" {
					require.ErrorIs(t, err, gorm.ErrRecordNotFound)
					wantRemain, wantUsed = 100, 20
				} else {
					require.NoError(t, err)
				}
				var after Token
				require.NoError(t, db.Unscoped().First(&after, token.Id).Error)
				assert.Equal(t, wantRemain, after.RemainQuota)
				assert.Equal(t, wantUsed, after.UsedQuota)
				assert.Equal(t, common.TokenStatusDisabled, after.Status)
				assert.True(t, after.DeletedAt.Valid)
			})
			t.Run("rollback_preserves_cache_fence", func(t *testing.T) {
				token, receipt := seed(t)
				server := useUserCacheMiniRedis(t)
				_, err := GetTokenByKey(token.Key, true)
				require.NoError(t, err)
				require.True(t, server.Exists(getTokenCacheKey(token.Key)))
				rollback := errors.New("synthetic rollback after token write")
				err = db.Transaction(func(tx *gorm.DB) error {
					if err := path.apply(tx, token, receipt); err != nil {
						return err
					}
					return rollback
				})
				require.ErrorIs(t, err, rollback)
				assert.True(t, server.Exists(getTokenCacheFenceKey(token.Key)))
				assert.False(t, server.Exists(getTokenCacheKey(token.Key)))
				published, err := cacheInitToken(token)
				require.NoError(t, err)
				assert.Zero(t, published, "pre-mutation snapshot must remain fenced after rollback")
				var after Token
				require.NoError(t, db.First(&after, token.Id).Error)
				assert.Equal(t, token.RemainQuota, after.RemainQuota)
				assert.Equal(t, token.UsedQuota, after.UsedQuota)
			})
			t.Run("elapsed_deadline_never_mutates", func(t *testing.T) {
				token, receipt := seed(t)
				ctx := tokenDelayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
				err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return path.apply(tx, token, receipt) })
				require.ErrorIs(t, err, context.DeadlineExceeded)
				var after Token
				require.NoError(t, db.First(&after, token.Id).Error)
				assert.Equal(t, token.RemainQuota, after.RemainQuota)
				assert.Equal(t, token.UsedQuota, after.UsedQuota)
			})
		})
	}

	t.Run("reservation_replay_and_settlement_replay_are_at_most_once", func(t *testing.T) {
		token, authorization := seed(t)
		calls := 0
		reserve := func(tx *gorm.DB, held *BillingFinalization) error {
			calls++
			_, err := DebitQuotaTx(tx, LedgerEntry{UserId: token.UserId, Delta: 20, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: held.RequestID, RequestId: held.RequestID, IdempotencyKey: "billing:preconsume:" + held.RequestID}, AccountPaid)
			if err != nil {
				return err
			}
			if err := ReserveBillingTokenTx(tx, held, 20, false); err != nil {
				return err
			}
			held.SourceHeld, held.TokenHeld, held.Paid = 20, 20, 20
			return nil
		}
		held, attempt, err := ReserveBillingFinalization(t.Context(), authorization, nil, 20, reserve)
		require.NoError(t, err)
		require.True(t, attempt.Applied)
		replayed, attempt, err := ReserveBillingFinalization(t.Context(), authorization, nil, 20, reserve)
		require.NoError(t, err)
		assert.False(t, attempt.Applied)
		assert.Equal(t, held, replayed)
		assert.Equal(t, 1, calls)
		for range 2 {
			_, err := SettleBillingFinalization(t.Context(), held, 20, func(*gorm.DB) error { calls++; return nil })
			require.NoError(t, err)
		}
		assert.Equal(t, 2, calls)
		var after Token
		var user User
		require.NoError(t, db.First(&after, token.Id).Error)
		require.NoError(t, db.First(&user, token.UserId).Error)
		assert.Equal(t, 80, after.RemainQuota)
		assert.Equal(t, 40, after.UsedQuota)
		assert.Equal(t, 980, user.Quota)
		assert.Equal(t, 980, user.QuotaPaid)
	})

	t.Run("source_validation_still_requires_live_wallet_owner", func(t *testing.T) {
		for _, state := range []string{"live", "deleted", "missing"} {
			t.Run(state, func(t *testing.T) {
				token, receipt := seed(t)
				if state != "live" {
					query := db
					if state == "missing" {
						query = query.Unscoped()
					}
					require.NoError(t, query.Delete(&User{}, token.UserId).Error)
				}
				err := db.Transaction(func(tx *gorm.DB) error { return validateBillingSourceTx(tx, &receipt) })
				if state == "live" {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				}
			})
		}
	})
}
