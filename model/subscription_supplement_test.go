package model

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Each supplement belongs to an already captured subscription period and token,
// but owns a separate operation from the main request's refundable hold.
func subscriptionSupplementSeed(t *testing.T, db *gorm.DB, request string) (*UserSubscription, *Token, BillingFinalization) {
	t.Helper()
	_, sub := subscriptionFinancialSeed(t, db)
	require.NoError(t, db.Create(&User{Id: sub.UserId, Username: fmt.Sprintf("supplement-user-%d", sub.UserId), AffCode: fmt.Sprintf("supp-%d", sub.UserId)}).Error)
	identity := subscriptionFinancialIdentity(t, sub, request, 100)
	sub.QuotaPeriod = identity.QuotaPeriod
	key := "synthetic-supplement-token-" + request
	token := &Token{UserId: sub.UserId, Key: key, RemainQuota: 900, UsedQuota: 100}
	require.NoError(t, db.Create(token).Error)
	main := BillingFinalization{
		RequestID: request, UserID: sub.UserId, Source: "subscription", TokenID: token.Id,
		TokenFingerprint: BillingTokenFingerprint(key), SubscriptionID: sub.Id, SubscriptionPeriod: sub.QuotaPeriod,
		SourceHeld: 100, PreConsumed: 100, TokenHeld: 100, State: BillingHeld,
	}
	require.NoError(t, db.Create(&main).Error)
	supplement := main
	supplement.RequestID = BillingTokenFingerprint("subscription:supplement:" + request + ":wss:1:")
	supplement.SourceHeld, supplement.PreConsumed, supplement.TokenHeld = 0, 0, 0
	supplement.State = ""
	return sub, token, supplement
}

func subscriptionSupplementBalances(t *testing.T, db *gorm.DB, subID, tokenID int, used int64, tokenRemaining, tokenUsed int) {
	t.Helper()
	var sub UserSubscription
	var token Token
	require.NoError(t, db.First(&sub, subID).Error)
	require.NoError(t, db.Unscoped().First(&token, tokenID).Error)
	assert.Equal(t, used, sub.AmountUsed)
	assert.Equal(t, tokenRemaining, token.RemainQuota)
	assert.Equal(t, tokenUsed, token.UsedQuota)
}

// The injected failure is a real SQL error inside the transaction, not a fake
// successful COMMIT or an error before the transaction begins. It is placed at
// the token/receipt write boundary after the source write has already happened.
func subscriptionSupplementFailSQLWrite(t *testing.T, db *gorm.DB, target any) (*bool, func()) {
	t.Helper()
	statement := &gorm.Statement{DB: db}
	require.NoError(t, statement.Parse(target))
	table := statement.Table
	missingTable := table + "_absent_fault"
	require.False(t, db.Migrator().HasTable(missingTable))
	name := "subscription_supplement_sql_fault"
	fired := false
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table != table {
			return
		}
		fired = true
		// Fixture names are generated ASCII identifiers, never user input.
		_, err := tx.Statement.ConnPool.ExecContext(tx.Statement.Context, "UPDATE "+missingTable+" SET nonexistent = 1")
		tx.AddError(err)
	}))
	removed := false
	remove := func() {
		if !removed {
			require.NoError(t, db.Callback().Update().Remove(name))
			removed = true
		}
	}
	t.Cleanup(remove)
	return &fired, remove
}

func TestSubscriptionSupplementDatabaseMatrix(t *testing.T) {
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
			require.NoError(t, db.AutoMigrate(&User{}, &SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &Token{}, &BillingFinalization{}))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&Token{}, &BillingFinalization{}, &User{})) })

			t.Run("same_operation_and_independent_segments", func(t *testing.T) {
				sub, token, receipt := subscriptionSupplementSeed(t, db, "supplement-replay")
				for _, expectedApplied := range []bool{true, false, false} {
					applied, err := ApplySubscriptionSupplement(context.Background(), receipt, 80)
					require.NoError(t, err)
					assert.Equal(t, expectedApplied, applied)
					subscriptionSupplementBalances(t, db, sub.Id, token.Id, 180, 820, 180)
				}
				second := receipt
				second.RequestID = BillingTokenFingerprint("subscription:supplement:supplement-replay:wss:2:")
				applied, err := ApplySubscriptionSupplement(context.Background(), second, 80)
				require.NoError(t, err)
				assert.True(t, applied, "equal-valued segments are distinct operations")
				subscriptionSupplementBalances(t, db, sub.Id, token.Id, 260, 740, 260)
				var main BillingFinalization
				require.NoError(t, db.Where("request_id = ?", "supplement-replay").First(&main).Error)
				assert.Equal(t, BillingHeld, main.State)
				assert.Equal(t, 100, main.SourceHeld)
				assert.Equal(t, 100, main.TokenHeld)
				var original SubscriptionPreConsumeRecord
				require.NoError(t, db.Where("request_id = ?", "supplement-replay").First(&original).Error)
				assert.Equal(t, "consumed", original.Status)
				assert.EqualValues(t, 100, original.ReservedAmount, "independent charges must never enlarge the main refundable hold")
			})

			t.Run("concurrent_same_operation_has_one_owner", func(t *testing.T) {
				sub, token, receipt := subscriptionSupplementSeed(t, db, "supplement-race")
				type outcome struct {
					applied bool
					err     error
				}
				start := make(chan struct{})
				results := make(chan outcome, 2)
				var workers sync.WaitGroup
				for range 2 {
					workers.Go(func() {
						<-start
						applied, err := ApplySubscriptionSupplement(context.Background(), receipt, 80)
						results <- outcome{applied, err}
					})
				}
				close(start)
				workers.Wait()
				close(results)
				owners := 0
				for result := range results {
					require.NoError(t, result.err)
					if result.applied {
						owners++
					}
				}
				assert.Equal(t, 1, owners)
				subscriptionSupplementBalances(t, db, sub.Id, token.Id, 180, 820, 180)
			})

			t.Run("amount_and_full_identity_conflicts", func(t *testing.T) {
				sub, token, receipt := subscriptionSupplementSeed(t, db, "supplement-conflict")
				applied, err := ApplySubscriptionSupplement(context.Background(), receipt, 80)
				require.NoError(t, err)
				require.True(t, applied)
				applied, err = ApplySubscriptionSupplement(context.Background(), receipt, 81)
				require.Error(t, err)
				assert.False(t, applied)
				for _, mutate := range []func(*BillingFinalization){
					func(r *BillingFinalization) { r.UserID++ },
					func(r *BillingFinalization) { r.SubscriptionID++ },
					func(r *BillingFinalization) { r.SubscriptionPeriod++ },
					func(r *BillingFinalization) { r.TokenID++ },
					func(r *BillingFinalization) {
						r.TokenFingerprint = BillingTokenFingerprint("synthetic-replacement-token")
					},
				} {
					changed := receipt
					mutate(&changed)
					applied, err := ApplySubscriptionSupplement(context.Background(), changed, 80)
					require.Error(t, err)
					assert.False(t, applied)
					subscriptionSupplementBalances(t, db, sub.Id, token.Id, 180, 820, 180)
				}
				var stored BillingFinalization
				require.NoError(t, db.Where("request_id = ?", receipt.RequestID).First(&stored).Error)
				assert.Equal(t, BillingSettled, stored.State)
				assert.Equal(t, 80, stored.ActualQuota)
			})

			for _, fault := range []struct {
				name   string
				target any
			}{{"token_sql_error", &Token{}}, {"terminal_receipt_sql_error", &BillingFinalization{}}} {
				t.Run(fault.name, func(t *testing.T) {
					sub, token, receipt := subscriptionSupplementSeed(t, db, "supplement-"+fault.name)
					fired, remove := subscriptionSupplementFailSQLWrite(t, db, fault.target)
					applied, err := ApplySubscriptionSupplement(context.Background(), receipt, 80)
					require.Error(t, err)
					assert.False(t, applied)
					require.True(t, *fired, "the intended late SQL boundary must actually fail")
					remove()
					subscriptionSupplementBalances(t, db, sub.Id, token.Id, 100, 900, 100)
					var count int64
					require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", receipt.RequestID).Count(&count).Error)
					assert.Zero(t, count, "rollback must remove the claimed operation as well as source/token changes")
					applied, err = ApplySubscriptionSupplement(context.Background(), receipt, 80)
					require.NoError(t, err)
					assert.True(t, applied)
					subscriptionSupplementBalances(t, db, sub.Id, token.Id, 180, 820, 180)
				})
			}

			t.Run("real_commit_lost_ack_replay", func(t *testing.T) {
				sub, token, receipt := subscriptionSupplementSeed(t, db, "supplement-lost-ack")
				subscriptionLoseCommitAck(t, db, func() error {
					applied, err := ApplySubscriptionSupplement(context.Background(), receipt, 80)
					assert.False(t, applied, "an uncertain ACK cannot be claimed as acknowledged success")
					return err
				})
				subscriptionSupplementBalances(t, db, sub.Id, token.Id, 180, 820, 180)
				applied, err := ApplySubscriptionSupplement(context.Background(), receipt, 80)
				require.NoError(t, err)
				assert.False(t, applied, "the durable operation must absorb the retry")
				subscriptionSupplementBalances(t, db, sub.Id, token.Id, 180, 820, 180)
			})

			t.Run("retired_period_rejects_new_charge_but_keeps_prior_replay", func(t *testing.T) {
				sub, token, receipt := subscriptionSupplementSeed(t, db, "supplement-retired")
				applied, err := ApplySubscriptionSupplement(context.Background(), receipt, 80)
				require.NoError(t, err)
				require.True(t, applied)
				_, err = AdminResetUserSubscriptionsByPlan(sub.UserId, sub.PlanId, false)
				require.NoError(t, err)
				require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", sub.Id).Update("amount_used", 200).Error)
				var current UserSubscription
				require.NoError(t, db.First(&current, sub.Id).Error)
				require.Greater(t, current.QuotaPeriod, receipt.SubscriptionPeriod)
				applied, err = ApplySubscriptionSupplement(context.Background(), receipt, 80)
				require.NoError(t, err)
				assert.False(t, applied)
				newCharge := receipt
				newCharge.RequestID = BillingTokenFingerprint("subscription:supplement:supplement-retired:wss:2:")
				applied, err = ApplySubscriptionSupplement(context.Background(), newCharge, 80)
				require.Error(t, err)
				assert.False(t, applied)
				subscriptionSupplementBalances(t, db, sub.Id, token.Id, 200, 820, 180)
				var count int64
				require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", newCharge.RequestID).Count(&count).Error)
				assert.Zero(t, count, "a rejected old-period charge must not persist a successful new owner")
			})

			t.Run("invalid_input_and_replacement_token_fail_closed", func(t *testing.T) {
				sub, token, receipt := subscriptionSupplementSeed(t, db, "supplement-invalid")
				for _, amount := range []int{0, -1, common.MaxQuota + 1} {
					applied, err := ApplySubscriptionSupplement(context.Background(), receipt, amount)
					require.Error(t, err, fmt.Sprint(amount))
					assert.False(t, applied)
				}
				for _, mutate := range []func(*BillingFinalization){
					func(r *BillingFinalization) { r.SubscriptionPeriod = 0 },
					func(r *BillingFinalization) { r.SourceHeld = 1 },
					func(r *BillingFinalization) { r.TokenHeld = 1 },
					func(r *BillingFinalization) { r.PreConsumed = 1 },
					func(r *BillingFinalization) { r.Source = "wallet" },
				} {
					changed := receipt
					mutate(&changed)
					applied, err := ApplySubscriptionSupplement(context.Background(), changed, 80)
					require.Error(t, err)
					assert.False(t, applied)
				}
				require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Update("key", "synthetic-rotated-supplement-key").Error)
				applied, err := ApplySubscriptionSupplement(context.Background(), receipt, 80)
				require.Error(t, err, "the surviving token must still match original authorization")
				assert.False(t, applied)
				subscriptionSupplementBalances(t, db, sub.Id, token.Id, 100, 900, 100)
				var count int64
				require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", receipt.RequestID).Count(&count).Error)
				assert.Zero(t, count)
			})
		})
	}
}
