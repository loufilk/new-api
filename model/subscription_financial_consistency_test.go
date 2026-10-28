package model

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func subscriptionFinancialSeed(t *testing.T, db *gorm.DB) (*SubscriptionPlan, *UserSubscription) {
	t.Helper()
	plan, sub := subscriptionPeriodSeed(t, db)
	plan.QuotaResetPeriod = SubscriptionResetNever
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Save(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	sub.UserId = 10000 + sub.Id
	require.NoError(t, db.Model(sub).Select("user_id").Updates(sub).Error)
	return plan, sub
}

func subscriptionFinancialIdentity(t *testing.T, sub *UserSubscription, requestID string, amount int64) SubscriptionBillingIdentity {
	t.Helper()
	result, err := PreConsumeUserSubscription(requestID, sub.UserId, "fixture", 0, amount)
	require.NoError(t, err)
	return SubscriptionBillingIdentity{
		RequestID: requestID, UserID: sub.UserId, SubscriptionID: result.UserSubscriptionId,
		QuotaPeriod: result.QuotaPeriod, PreConsumed: result.PreConsumed,
	}
}

// This wrapper commits a real SQL transaction before deliberately returning an
// error. It exercises an unknown ACK without pretending a rollback took place.
type subscriptionLostAckPool struct {
	gorm.ConnPool
	committed atomic.Int32
}

type subscriptionLostAckTx struct {
	*sql.Tx
	pool *subscriptionLostAckPool
}

var errSubscriptionLostAck = errors.New("injected subscription commit acknowledgement loss")

func (p *subscriptionLostAckPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &subscriptionLostAckTx{Tx: tx, pool: p}, nil
}

func (tx *subscriptionLostAckTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	tx.pool.committed.Add(1)
	return errSubscriptionLostAck
}

func subscriptionLoseCommitAck(t *testing.T, db *gorm.DB, apply func() error) {
	t.Helper()
	original := DB
	wrapped := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	pool := &subscriptionLostAckPool{ConnPool: db.ConnPool}
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	DB = wrapped
	defer func() { DB = original }()
	require.ErrorIs(t, apply(), errSubscriptionLostAck)
	require.EqualValues(t, 1, pool.committed.Load(), "a real COMMIT, not a pre-commit fault, must precede the error")
}

func TestSubscriptionFinancialConsistencyDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"}} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			db := subscriptionPeriodDatabase(t, dialect.kind, dsn)
			require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))

			t.Run("absolute_reserve_and_terminal_identity", func(t *testing.T) {
				_, sub := subscriptionFinancialSeed(t, db)
				identity := subscriptionFinancialIdentity(t, sub, "absolute-reserve", 100)
				require.NoError(t, ReserveSubscriptionPreConsume(identity, 300))
				replayed, err := PreConsumeUserSubscription(identity.RequestID, identity.UserID, "fixture", 0, 100)
				require.NoError(t, err)
				assert.True(t, replayed.Replayed)
				assert.EqualValues(t, 300, replayed.ReservedAmount)
				require.NoError(t, ReserveSubscriptionPreConsume(identity, 300))
				require.NoError(t, ReserveSubscriptionPreConsume(identity, 150))
				require.NoError(t, ReserveSubscriptionPreConsume(identity, 500))
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, 500, sub.AmountUsed)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					return ValidateSubscriptionPreConsumeTx(tx, identity, 500)
				}))
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					return ValidateSubscriptionPreConsumeTx(tx, identity, 300)
				}))
				for _, state := range []string{"consumed", "settled"} {
					if state == "settled" {
						require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
							applied, err := SettleSubscriptionPreConsumeTx(tx, identity, 240)
							assert.True(t, applied)
							return err
						}))
					}
					for _, mutate := range []func(*SubscriptionBillingIdentity){
						func(v *SubscriptionBillingIdentity) { v.UserID++ },
						func(v *SubscriptionBillingIdentity) { v.SubscriptionID++ },
						func(v *SubscriptionBillingIdentity) { v.QuotaPeriod++ },
						func(v *SubscriptionBillingIdentity) { v.PreConsumed++ },
					} {
						wrong := identity
						mutate(&wrong)
						require.Error(t, ReserveSubscriptionPreConsume(wrong, 500), state)
						require.Error(t, db.Transaction(func(tx *gorm.DB) error {
							_, err := SettleSubscriptionPreConsumeTx(tx, wrong, 240)
							return err
						}), state)
						require.Error(t, db.Transaction(func(tx *gorm.DB) error {
							_, err := RefundSubscriptionPreConsumeTx(tx, wrong)
							return err
						}), state)
					}
				}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					applied, err := SettleSubscriptionPreConsumeTx(tx, identity, 240)
					assert.False(t, applied)
					return err
				}))
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SettleSubscriptionPreConsumeTx(tx, identity, 241)
					return err
				}))
				require.ErrorIs(t, RefundSubscriptionPreConsume(identity.RequestID), ErrBillingFinalized)
				_, err = PreConsumeUserSubscription(identity.RequestID, identity.UserID, "fixture", 0, 100)
				require.ErrorIs(t, err, ErrBillingFinalized)
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, 240, sub.AmountUsed)
				var record SubscriptionPreConsumeRecord
				require.NoError(t, db.Where("request_id = ?", identity.RequestID).First(&record).Error)
				assert.EqualValues(t, 100, record.PreConsumed)
				assert.EqualValues(t, 500, record.ReservedAmount)
				assert.EqualValues(t, 240, record.SettledAmount)
				assert.Equal(t, "settled", record.Status)
			})

			t.Run("real_commit_lost_ack_replay", func(t *testing.T) {
				_, sub := subscriptionFinancialSeed(t, db)
				identity := subscriptionFinancialIdentity(t, sub, "lost-ack-reserve", 100)
				subscriptionLoseCommitAck(t, db, func() error { return ReserveSubscriptionPreConsume(identity, 300) })
				require.NoError(t, ReserveSubscriptionPreConsume(identity, 300))
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, 300, sub.AmountUsed)
				subscriptionLoseCommitAck(t, db, func() error { return RefundSubscriptionPreConsume(identity.RequestID) })
				require.NoError(t, RefundSubscriptionPreConsume(identity.RequestID))
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.Zero(t, sub.AmountUsed)
				var record SubscriptionPreConsumeRecord
				require.NoError(t, db.Where("request_id = ?", identity.RequestID).First(&record).Error)
				assert.Equal(t, "refunded", record.Status)
				assert.EqualValues(t, 300, record.ReservedAmount)

				settleID := subscriptionFinancialIdentity(t, sub, "lost-ack-settle", 100)
				subscriptionLoseCommitAck(t, db, func() error {
					return DB.Transaction(func(tx *gorm.DB) error {
						_, err := SettleSubscriptionPreConsumeTx(tx, settleID, 200)
						return err
					})
				})
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					applied, err := SettleSubscriptionPreConsumeTx(tx, settleID, 200)
					assert.False(t, applied)
					return err
				}))
				require.ErrorIs(t, RefundSubscriptionPreConsume(settleID.RequestID), ErrBillingFinalized)
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, 200, sub.AmountUsed)
			})

			t.Run("receipt_write_failure_rolls_back_reserve_and_settle", func(t *testing.T) {
				_, sub := subscriptionFinancialSeed(t, db)
				identity := subscriptionFinancialIdentity(t, sub, "write-failure", 100)
				injected := errors.New("injected receipt update failure")
				callback := "subscription-finance-receipt-update"
				receiptTable := db.NamingStrategy.TableName("SubscriptionPreConsumeRecord")
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == receiptTable {
						tx.AddError(injected)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
				require.ErrorIs(t, ReserveSubscriptionPreConsume(identity, 300), injected)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SettleSubscriptionPreConsumeTx(tx, identity, 200)
					return err
				}), injected)
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, 100, sub.AmountUsed)
				var record SubscriptionPreConsumeRecord
				require.NoError(t, db.Where("request_id = ?", identity.RequestID).First(&record).Error)
				assert.Equal(t, "consumed", record.Status)
				assert.EqualValues(t, 100, record.ReservedAmount)
				assert.Zero(t, record.SettledAmount)
			})

			t.Run("caller_transaction_rollback_preserves_reservation", func(t *testing.T) {
				_, sub := subscriptionFinancialSeed(t, db)
				injected := errors.New("injected caller rollback after subscription mutation")
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := PreConsumeUserSubscriptionTx(tx, "caller-preconsume", sub.UserId, "fixture", 0, 100)
					if err != nil {
						return err
					}
					return injected
				}), injected)
				var count int64
				require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", "caller-preconsume").Count(&count).Error)
				assert.Zero(t, count)
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.Zero(t, sub.AmountUsed)
				assert.Zero(t, sub.QuotaPeriod)
				identity := subscriptionFinancialIdentity(t, sub, "caller-preconsume", 100)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					if err := ReserveSubscriptionPreConsumeTx(tx, identity, 300); err != nil {
						return err
					}
					return injected
				}), injected)
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, 100, sub.AmountUsed)
				var record SubscriptionPreConsumeRecord
				require.NoError(t, db.Where("request_id = ?", identity.RequestID).First(&record).Error)
				assert.EqualValues(t, 100, record.ReservedAmount)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SettleSubscriptionPreConsumeTx(tx, identity, 200)
					return err
				}))
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					if err := AdjustSubscriptionTaskQuotaTx(tx, identity, 200, 150); err != nil {
						return err
					}
					return injected
				}), injected)
				require.NoError(t, db.First(sub, sub.Id).Error)
				require.NoError(t, db.First(&record, record.Id).Error)
				assert.EqualValues(t, 200, sub.AmountUsed)
				assert.EqualValues(t, 200, record.SettledAmount)
			})

			t.Run("retired_period_rejects_growth_and_closes_old_settlement", func(t *testing.T) {
				plan, sub := subscriptionFinancialSeed(t, db)
				identity := subscriptionFinancialIdentity(t, sub, "retired-settlement", 100)
				require.NoError(t, ReserveSubscriptionPreConsume(identity, 300))
				_, err := AdminResetUserSubscriptionsByPlan(sub.UserId, plan.Id, false)
				require.NoError(t, err)
				subscriptionFinancialIdentity(t, sub, "new-period-unrelated", 200)
				require.Error(t, ReserveSubscriptionPreConsume(identity, 400))
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SettleSubscriptionPreConsumeTx(tx, identity, 400)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SettleSubscriptionPreConsumeTx(tx, identity, 250)
					return err
				}))
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, 200, sub.AmountUsed)
				assert.Greater(t, sub.QuotaPeriod, identity.QuotaPeriod)
				var record SubscriptionPreConsumeRecord
				require.NoError(t, db.Where("request_id = ?", identity.RequestID).First(&record).Error)
				assert.Equal(t, "settled", record.Status)
				assert.EqualValues(t, 250, record.SettledAmount)
			})

			t.Run("task_adjustment_requires_exact_prior_evidence", func(t *testing.T) {
				plan, sub := subscriptionFinancialSeed(t, db)
				identity := subscriptionFinancialIdentity(t, sub, "task-evidence", 100)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := SettleSubscriptionPreConsumeTx(tx, identity, 200)
					return err
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					stored, err := GetSubscriptionBillingIdentityTx(tx, identity.RequestID, identity.UserID, identity.SubscriptionID, identity.QuotaPeriod, 100)
					if err != nil {
						return err
					}
					assert.Equal(t, identity, stored)
					return AdjustSubscriptionTaskQuotaTx(tx, stored, 200, 150)
				}))
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					return AdjustSubscriptionTaskQuotaTx(tx, identity, 200, 150)
				}))
				_, err := AdminResetUserSubscriptionsByPlan(sub.UserId, plan.Id, false)
				require.NoError(t, err)
				subscriptionFinancialIdentity(t, sub, "task-new-period", 250)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					return AdjustSubscriptionTaskQuotaTx(tx, identity, 150, 200)
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					return AdjustSubscriptionTaskQuotaTx(tx, identity, 150, 0)
				}))
				require.ErrorIs(t, RefundSubscriptionPreConsume(identity.RequestID), ErrBillingFinalized)
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, 250, sub.AmountUsed)
				var record SubscriptionPreConsumeRecord
				require.NoError(t, db.Where("request_id = ?", identity.RequestID).First(&record).Error)
				assert.Equal(t, "settled", record.Status)
				assert.Zero(t, record.SettledAmount)
			})

			t.Run("unknown_legacy_period_requires_review", func(t *testing.T) {
				_, sub := subscriptionFinancialSeed(t, db)
				sub.AmountUsed = 350
				require.NoError(t, db.Select("amount_used").Updates(sub).Error)
				record := SubscriptionPreConsumeRecord{RequestId: "unknown-legacy", UserId: sub.UserId, UserSubscriptionId: sub.Id, PreConsumed: 200, Status: "consumed"}
				require.NoError(t, db.Create(&record).Error)
				err := RefundSubscriptionPreConsume(record.RequestId)
				require.Error(t, err)
				code, class := BillingRefundErrorCode(err)
				assert.Equal(t, "subscription_period_unknown", code)
				assert.Equal(t, "review_required", class)
				_, err = PreConsumeUserSubscription(record.RequestId, record.UserId, "fixture", 0, record.PreConsumed)
				require.Error(t, err)
				fresh := subscriptionFinancialIdentity(t, sub, "known-after-legacy", 100)
				assert.Positive(t, fresh.QuotaPeriod)
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, 450, sub.AmountUsed)
				var unchanged SubscriptionPreConsumeRecord
				require.NoError(t, db.First(&unchanged, record.Id).Error)
				assert.Equal(t, record, unchanged)
			})

			t.Run("overflow_cannot_credit_or_publish_a_receipt", func(t *testing.T) {
				_, sub := subscriptionFinancialSeed(t, db)
				sub.AmountUsed, sub.AmountTotal = math.MaxInt64-50, 0
				require.NoError(t, db.Select("amount_used", "amount_total").Updates(sub).Error)
				for _, amount := range []int64{100, common.MaxQuota + 1, math.MaxInt64} {
					_, err := PreConsumeUserSubscription("overflow-reservation", sub.UserId, "fixture", 0, amount)
					require.Error(t, err)
				}
				require.NoError(t, db.First(sub, sub.Id).Error)
				assert.EqualValues(t, math.MaxInt64-50, sub.AmountUsed)
				assert.Zero(t, sub.QuotaPeriod)
				var count int64
				require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", "overflow-reservation").Count(&count).Error)
				assert.Zero(t, count)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					return AdjustSubscriptionQuotaPeriodTx(tx, sub.UserId, sub.Id, 1, math.MinInt64)
				}))
			})

			t.Run("concurrent_request_claims_validate_the_winner", func(t *testing.T) {
				for _, mode := range []string{"same_identity", "different_user", "different_amount"} {
					t.Run(mode, func(t *testing.T) {
						_, first := subscriptionFinancialSeed(t, db)
						_, second := subscriptionFinancialSeed(t, db)
						users, amounts := [2]int{first.UserId, first.UserId}, [2]int64{100, 100}
						if mode == "different_user" {
							users[1] = second.UserId
						}
						if mode == "different_amount" {
							amounts[1] = 101
						}
						type result struct {
							value *SubscriptionPreConsumeResult
							err   error
						}
						var results [2]result
						start := make(chan struct{})
						var wg sync.WaitGroup
						for i := range 2 {
							wg.Go(func() {
								<-start
								results[i].value, results[i].err = PreConsumeUserSubscription("claim-"+mode, users[i], "fixture", 0, amounts[i])
							})
						}
						close(start)
						wg.Wait()
						successes, replays := 0, 0
						for _, result := range results {
							if result.err == nil {
								successes++
								if result.value.Replayed {
									replays++
								}
							} else {
								code, class := BillingRefundErrorCode(result.err)
								assert.Equal(t, "identity_mismatch", code)
								assert.Equal(t, "review_required", class)
							}
						}
						if mode == "same_identity" {
							assert.Equal(t, 2, successes)
							assert.Equal(t, 1, replays)
						} else {
							assert.Equal(t, 1, successes)
							assert.Zero(t, replays)
						}
						var record SubscriptionPreConsumeRecord
						require.NoError(t, db.Where("request_id = ?", "claim-"+mode).First(&record).Error)
						require.NoError(t, db.First(first, first.Id).Error)
						require.NoError(t, db.First(second, second.Id).Error)
						assert.Equal(t, record.PreConsumed, first.AmountUsed+second.AmountUsed)
					})
				}
			})

			t.Run("concurrent_settle_and_refund_have_one_terminal_owner", func(t *testing.T) {
				_, sub := subscriptionFinancialSeed(t, db)
				identity := subscriptionFinancialIdentity(t, sub, "terminal-race", 100)
				require.NoError(t, ReserveSubscriptionPreConsume(identity, 200))
				subscriptionFinancialIdentity(t, sub, "terminal-race-unrelated", 100)
				start := make(chan struct{})
				var settleErr, refundErr error
				var wg sync.WaitGroup
				wg.Go(func() {
					<-start
					settleErr = db.Transaction(func(tx *gorm.DB) error {
						_, err := SettleSubscriptionPreConsumeTx(tx, identity, 150)
						return err
					})
				})
				wg.Go(func() { <-start; refundErr = RefundSubscriptionPreConsume(identity.RequestID) })
				close(start)
				wg.Wait()
				var record SubscriptionPreConsumeRecord
				require.NoError(t, db.Where("request_id = ?", identity.RequestID).First(&record).Error)
				require.NoError(t, db.First(sub, sub.Id).Error)
				if record.Status == "settled" {
					require.NoError(t, settleErr)
					require.ErrorIs(t, refundErr, ErrBillingFinalized)
					assert.EqualValues(t, 250, sub.AmountUsed)
					assert.EqualValues(t, 150, record.SettledAmount)
				} else {
					require.NoError(t, refundErr)
					require.ErrorIs(t, settleErr, ErrBillingFinalized)
					assert.Equal(t, "refunded", record.Status)
					assert.EqualValues(t, 100, sub.AmountUsed)
					assert.Zero(t, record.SettledAmount)
				}
			})
		})
	}
}
