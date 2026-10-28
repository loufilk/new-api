package model

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type financialLockOrderRoleKey struct{}

// This is specifically a cross-source user/Token lock-order regression, not a
// same-request idempotency race. SQLite cannot demonstrate this native row-lock
// cycle, so its serialized writer is deliberately not reported as proof here.
func runFinancialSubscriptionLockOrderCases(t *testing.T) {
	t.Helper()
	dialect := common.MainDatabaseType()
	if dialect == common.DatabaseTypeSQLite {
		t.Skip("SQLite writer serialization is not native row-lock-order evidence")
	}
	db := DB
	require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}))
	pool, err := db.DB()
	require.NoError(t, err)
	previousMax := pool.Stats().MaxOpenConnections
	pool.SetMaxOpenConns(2)
	t.Cleanup(func() { pool.SetMaxOpenConns(previousMax) })
	t.Run("supplement_vs_wallet_same_user_token", func(t *testing.T) {
		_, sub := subscriptionFinancialSeed(t, db)
		sub.QuotaPeriod = 1
		require.NoError(t, db.Model(sub).Update("quota_period", 1).Error)
		user := User{Id: sub.UserId, Username: "financial-lock-order", AffCode: "financial-lock-order", Quota: 1000, QuotaPaid: 1000}
		require.NoError(t, db.Create(&user).Error)
		token := Token{UserId: user.Id, Key: "synthetic-financial-lock-order-token", RemainQuota: 1000}
		require.NoError(t, db.Create(&token).Error)
		receipt := BillingFinalization{RequestID: "financial-lock-order-sub", UserID: user.Id, Source: "subscription", TokenID: token.Id, TokenFingerprint: BillingTokenFingerprint(token.Key), SubscriptionID: sub.Id, SubscriptionPeriod: 1}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var workers sync.WaitGroup
		// Every early assertion also releases callback waits and joins workers
		// before fixture cleanup can restore the package-global database.
		defer func() { cancel(); workers.Wait() }()
		aHeld := make(chan string, 1)
		releaseA := make(chan struct{})
		bAttemptsUser := make(chan struct{})
		bHoldsUser := make(chan struct{})
		aAttemptsUser := make(chan struct{})
		var firstA, firstB, attemptA, attemptB sync.Once
		var oldOrder, cycleReached atomic.Bool
		var connections [2]atomic.Int64
		isUpdateLock := func(tx *gorm.DB) bool {
			locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
			return ok && locking.Strength == "UPDATE"
		}
		wait := func(tx *gorm.DB, ch <-chan struct{}) {
			select {
			case <-ch:
			case <-ctx.Done():
				tx.AddError(fmt.Errorf("lock-order fixture barrier: %w", ctx.Err()))
			}
		}
		beforeName, afterName := "financial-lock-order-before", "financial-lock-order-after"
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register(beforeName, func(tx *gorm.DB) {
			role, ok := tx.Statement.Context.Value(financialLockOrderRoleKey{}).(int)
			if !ok || tx.Statement.Schema == nil || tx.Error != nil {
				return
			}
			if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
				tx.AddError(errors.New("lock-order fixture requires real SQL transactions"))
				return
			}
			if connections[role].Load() == 0 {
				query := "SELECT CONNECTION_ID()"
				if dialect == common.DatabaseTypePostgreSQL {
					query = "SELECT pg_backend_pid()"
				}
				var connectionID int64
				if err := tx.Statement.ConnPool.QueryRowContext(ctx, query).Scan(&connectionID); err != nil {
					tx.AddError(err)
					return
				}
				connections[role].Store(connectionID)
			}
			typ := tx.Statement.Schema.ModelType
			if !isUpdateLock(tx) {
				return // An ordinary metadata SELECT is not a lock attempt.
			}
			if role == 1 && typ == reflect.TypeFor[User]() {
				attemptB.Do(func() { close(bAttemptsUser) })
			}
			if role == 0 && oldOrder.Load() && typ == reflect.TypeFor[User]() {
				attemptA.Do(func() { close(aAttemptsUser) })
			}
			if role == 1 && oldOrder.Load() && typ == reflect.TypeFor[Token]() {
				cycleReached.Store(true)
			}
		}))
		require.NoError(t, db.Callback().Query().After("gorm:query").Register(afterName, func(tx *gorm.DB) {
			role, ok := tx.Statement.Context.Value(financialLockOrderRoleKey{}).(int)
			if !ok || tx.Statement.Schema == nil || tx.Error != nil {
				return
			}
			if !isUpdateLock(tx) {
				return // SQL completion alone does not prove row-lock ownership.
			}
			typ := tx.Statement.Schema.ModelType
			if role == 0 && (typ == reflect.TypeFor[User]() || typ == reflect.TypeFor[Token]()) {
				firstA.Do(func() {
					kind := "user"
					if typ == reflect.TypeFor[Token]() {
						kind = "token"
					}
					aHeld <- kind // SQL has returned: this lock is genuinely held.
					wait(tx, releaseA)
				})
			}
			if role == 1 && typ == reflect.TypeFor[User]() {
				firstB.Do(func() {
					close(bHoldsUser)
					if oldOrder.Load() {
						// Old source->Token->user: B holds user until A
						// attempts stats; then B genuinely requests A's Token.
						wait(tx, aAttemptsUser)
					}
				})
			}
		}))
		t.Cleanup(func() {
			require.NoError(t, db.Callback().Query().Remove(beforeName))
			require.NoError(t, db.Callback().Query().Remove(afterName))
		})

		var errs [2]error
		var applied bool
		workers.Go(func() {
			applied, errs[0] = ApplySubscriptionSupplement(context.WithValue(ctx, financialLockOrderRoleKey{}, 0), receipt, 80, func(tx *gorm.DB) error {
				return ApplyTaskUsageDeltaTx(tx, user.Id, 0, 80, 1)
			})
		})
		var firstLock string
		select {
		case firstLock = <-aHeld:
		case <-ctx.Done():
			t.Fatal("subscription did not acquire its first financial row lock: ", ctx.Err())
		}
		oldOrder.Store(firstLock == "token")
		workers.Go(func() {
			errs[1] = db.WithContext(context.WithValue(ctx, financialLockOrderRoleKey{}, 1)).Transaction(func(tx *gorm.DB) error {
				_, err := DebitQuotaTx(tx, LedgerEntry{UserId: user.Id, Delta: 20, Reason: LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: "financial-lock-order-wallet", RequestId: "financial-lock-order-wallet", IdempotencyKey: "billing:settle:financial-lock-order-wallet:20"}, AccountPaid)
				if err != nil {
					return err
				}
				if err := ApplyBillingTokenDeltaTx(tx, &receipt, -20); err != nil {
					return err
				}
				return ApplyTaskUsageDeltaTx(tx, user.Id, 0, 20, 1)
			})
		})
		// New user-first: never wait for B to acquire the user already held
		// by A. Old Token-first: hold both opposing locks before releasing A.
		ready := bAttemptsUser
		if oldOrder.Load() {
			ready = bHoldsUser
		}
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("wallet did not reach the intended native contention boundary: ", ctx.Err())
		}
		close(releaseA)
		workers.Wait()
		t.Logf("native %s physical connections=%d,%d first subscription lock=%s old-order cycle reached=%t errors=%v", dialect, connections[0].Load(), connections[1].Load(), firstLock, cycleReached.Load(), errs)
		require.Positive(t, connections[0].Load())
		require.Positive(t, connections[1].Load())
		require.NotEqual(t, connections[0].Load(), connections[1].Load(), "two physical SQL connections, not a shared in-process mutex/pool queue")
		if oldOrder.Load() {
			require.True(t, cycleReached.Load(), "RED must reach the real user/Token cycle, not time out at a synthetic barrier")
		}
		require.NoError(t, errs[0], "subscription accounting must not be a deadlock victim")
		require.NoError(t, errs[1], "wallet accounting must not be a deadlock victim")
		require.True(t, applied)
		require.Equal(t, "user", firstLock)
		require.NoError(t, db.First(&user, user.Id).Error)
		require.NoError(t, db.First(&token, token.Id).Error)
		require.NoError(t, db.First(sub, sub.Id).Error)
		assert.Equal(t, 980, user.QuotaPaid)
		assert.Equal(t, 980, user.Quota)
		assert.Equal(t, 100, user.UsedQuota)
		assert.Equal(t, 2, user.RequestCount)
		assert.EqualValues(t, 80, sub.AmountUsed)
		assert.Equal(t, 900, token.RemainQuota)
		assert.Equal(t, 100, token.UsedQuota)
		var terminal BillingFinalization
		require.NoError(t, db.First(&terminal, "request_id = ?", receipt.RequestID).Error)
		assert.Equal(t, BillingSettled, terminal.State)
		assert.Equal(t, 80, terminal.ActualQuota)
		var operations int64
		require.NoError(t, db.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", "billing:settle:financial-lock-order-wallet:20").Count(&operations).Error)
		assert.EqualValues(t, 1, operations)
	})
}
