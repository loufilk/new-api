package model

import (
	"context"
	"errors"
	"strings"
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

// A disposable alternative store tests the in-transaction contract without
// importing the tagged laboratory or publishing a production backing table.
// It uses the real source/token operations, not simulated success callbacks.
type laneOwnerKernelTestRow struct {
	RequestID        string `gorm:"type:varchar(64);primaryKey"`
	LaneID           string
	Epoch            int64
	UserID           int
	TokenID          int
	TokenFingerprint string
	Held             int
	State            string
}

type laneOwnerKernelTestStore struct {
	reserveCalls   atomic.Int32
	failure        error
	omitReceipt    bool
	reportedReplay bool
	beforeReserve  func() error
}

func (s *laneOwnerKernelTestStore) LockLaneBillingReceiptTx(tx *gorm.DB, requestID string) (LaneBillingOwnerReceipt, error) {
	var row laneOwnerKernelTestRow
	if err := lockForUpdate(tx).Where("request_id = ?", requestID).Take(&row).Error; err != nil {
		return LaneBillingOwnerReceipt{}, err
	}
	return LaneBillingOwnerReceipt{
		Identity: LaneBillingOwnerIdentity{RequestID: row.RequestID, LaneID: row.LaneID, Epoch: row.Epoch, UserID: row.UserID, TokenID: row.TokenID, TokenFingerprint: row.TokenFingerprint},
		Held:     row.Held, State: row.State,
	}, nil
}

func (s *laneOwnerKernelTestStore) ReserveLaneBillingReceiptTx(tx *gorm.DB, id LaneBillingOwnerIdentity, target int) (bool, error) {
	s.reserveCalls.Add(1)
	if s.beforeReserve != nil {
		if err := s.beforeReserve(); err != nil {
			return false, err
		}
	}
	entry := LedgerEntry{UserId: id.UserID, Delta: target, Reason: LedgerReasonBillingPreConsume,
		ReferenceType: "lane_owner_kernel_test", ReferenceId: id.RequestID, RequestId: id.RequestID, IdempotencyKey: "owner-kernel:" + id.RequestID}
	debit, err := DebitQuotaTx(tx, entry, AccountPaid)
	if err != nil {
		return false, err
	}
	if debit.Replayed {
		return false, errors.New("unexpected fixture debit replay")
	}
	if err := ReserveBillingTokenTx(tx, &BillingFinalization{UserID: id.UserID, TokenID: id.TokenID, TokenFingerprint: id.TokenFingerprint}, target, false); err != nil {
		return false, err
	}
	if s.failure != nil {
		return false, s.failure
	}
	if s.omitReceipt {
		return true, nil
	} // injected dishonest success; read-back must still fail
	row := laneOwnerKernelTestRow{RequestID: id.RequestID, LaneID: id.LaneID, Epoch: id.Epoch, UserID: id.UserID, TokenID: id.TokenID,
		TokenFingerprint: id.TokenFingerprint, Held: target, State: LaneBillingReceiptHeld}
	insert := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&row)
	return insert.RowsAffected == 1 && !s.reportedReplay, insert.Error
}

func laneOwnerKernelFixture(t *testing.T) LaneBillingOwnerIdentity {
	t.Helper()
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires disposable PostgreSQL TEST_MODEL_POSTGRES_DSN")
	}
	require.NoError(t, DB.AutoMigrate(&BillingFinalization{}, &laneOwnerKernelTestRow{}))
	name := "owner-kernel-" + common.GetUUID()[:8]
	user := User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Quota: 1000, QuotaPaid: 1000}
	require.NoError(t, DB.Create(&user).Error)
	key := Token{UserId: user.Id, Name: name, Key: "synthetic-only-" + name, Status: common.TokenStatusEnabled, RemainQuota: 1000}
	require.NoError(t, DB.Create(&key).Error)
	t.Cleanup(func() {
		for _, entity := range []any{&laneOwnerKernelTestRow{}, &BillingFinalization{}, &BalanceLedger{}, &BalanceLedgerOperation{}, &BonusSourceState{}, &BonusSourceLot{}, &BonusSourceAccount{}} {
			require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Where("user_id = ?", user.Id).Delete(entity).Error)
		}
		require.NoError(t, DB.Unscoped().Delete(&Token{}, key.Id).Error)
		require.NoError(t, DB.Unscoped().Delete(&User{}, user.Id).Error)
	})
	return LaneBillingOwnerIdentity{RequestID: name + "-request", LaneID: name, Epoch: 1, UserID: user.Id, TokenID: key.Id, TokenFingerprint: BillingTokenFingerprint(key.Key)}
}

func laneOwnerKernelOrdinary(id LaneBillingOwnerIdentity) BillingFinalization {
	return BillingFinalization{RequestID: id.RequestID, UserID: id.UserID, Source: "wallet", TokenID: id.TokenID, TokenFingerprint: id.TokenFingerprint}
}

func assertLaneOwnerKernelAccounts(t *testing.T, id LaneBillingOwnerIdentity, wantAmount int, wantOwners, wantReceipts int64) {
	t.Helper()
	var user User
	var token Token
	require.NoError(t, DB.Select("quota", "quota_paid").First(&user, id.UserID).Error)
	require.NoError(t, DB.Select("remain_quota", "used_quota").First(&token, id.TokenID).Error)
	assert.Equal(t, 1000-wantAmount, user.Quota)
	assert.Equal(t, 1000-wantAmount, user.QuotaPaid)
	assert.Equal(t, 1000-wantAmount, token.RemainQuota)
	assert.Equal(t, wantAmount, token.UsedQuota)
	for _, spec := range []struct {
		entity any
		want   int64
	}{
		{&BillingFinalization{}, wantOwners}, {&laneOwnerKernelTestRow{}, wantReceipts},
		{&BalanceLedger{}, int64(min(1, wantAmount))}, {&BalanceLedgerOperation{}, int64(min(1, wantAmount))},
	} {
		var got int64
		require.NoError(t, DB.Model(spec.entity).Where("user_id = ?", id.UserID).Count(&got).Error)
		assert.Equal(t, spec.want, got)
	}
}

func TestLaneBillingOwnerKernelPostgres(t *testing.T) {
	t.Run("ordinary zero hold wins without new owner SQL", func(t *testing.T) {
		id := laneOwnerKernelFixture(t)
		ctx := t.Context()
		ordinary := laneOwnerKernelOrdinary(id)
		prepare := func(*gorm.DB, *BillingFinalization) error { return nil }
		var sqlCount atomic.Int32
		const watch = "test:owner-kernel-ordinary-sql"
		callback := func(*gorm.DB) { sqlCount.Add(1) }
		require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(watch, callback))
		require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(watch, callback))
		require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(watch, callback))
		owner, attempted, err := ReserveBillingFinalization(ctx, ordinary, nil, 0, prepare)
		require.NoError(t, DB.Callback().Create().Remove(watch))
		require.NoError(t, DB.Callback().Query().Remove(watch))
		require.NoError(t, DB.Callback().Update().Remove(watch))
		require.NoError(t, err)
		assert.EqualValues(t, 4, sqlCount.Load(), "original create/owner read/user lock/save only")
		assert.True(t, attempted.FreshInitialPrepared)
		assert.Equal(t, BillingHeld, owner.State)
		store := &laneOwnerKernelTestStore{}
		result, err := ReserveLaneBillingOwner(ctx, DB, id, 50, store)
		assert.ErrorIs(t, err, ErrBillingRequestOwnerConflict)
		assert.Empty(t, result)
		assert.Zero(t, store.reserveCalls.Load())
		assertLaneOwnerKernelAccounts(t, id, 0, 1, 0)
	})
	t.Run("lane wins and ordinary users cannot debit or refund", func(t *testing.T) {
		id := laneOwnerKernelFixture(t)
		ctx := t.Context()
		store := &laneOwnerKernelTestStore{}
		result, err := ReserveLaneBillingOwner(ctx, DB, id, 40, store)
		require.NoError(t, err)
		assert.True(t, result.Fresh)
		assert.Equal(t, LaneBillingOwnerReceipt{Identity: id, Held: 40, State: LaneBillingReceiptHeld}, result.Receipt)
		before := BillingFinalization{}
		require.NoError(t, DB.Where("request_id = ?", id.RequestID).Take(&before).Error)
		assert.True(t, isLaneBillingOwnerMarker(before))
		assert.Zero(t, before.SourceHeld)
		assert.Zero(t, before.TokenHeld)
		ordinary := laneOwnerKernelOrdinary(id)
		var applied atomic.Int32
		_, attempt, err := ReserveBillingFinalization(ctx, ordinary, nil, 40, func(*gorm.DB, *BillingFinalization) error { applied.Add(1); return nil })
		assert.ErrorIs(t, err, ErrBillingRequestOwnerConflict)
		assert.Zero(t, applied.Load())
		assert.Empty(t, attempt)
		code, class := BillingRefundErrorCode(err)
		assert.Equal(t, "terminal_owner_conflict", code)
		assert.Equal(t, billingRefundReview, class)
		_, err = ReadBillingReservation(ctx, ordinary)
		assert.ErrorIs(t, err, ErrBillingRequestOwnerConflict)
		_, err = ProbeBillingFinalization(ctx, ordinary)
		assert.ErrorIs(t, err, ErrBillingRequestOwnerConflict)
		_, err = RequestBillingRefund(ctx, ordinary)
		assert.ErrorIs(t, err, ErrBillingRequestOwnerConflict)
		_, err = SettleBillingFinalization(ctx, ordinary, 1, func(*gorm.DB) error { applied.Add(1); return nil })
		assert.ErrorIs(t, err, ErrBillingRequestOwnerConflict)
		assert.Zero(t, applied.Load())
		_, err = ApplyBillingRefund(ctx, id.RequestID)
		assert.Error(t, err)
		require.NoError(t, RecordBillingRefundFailure(ctx, id.RequestID, errors.New("synthetic wrong worker")))
		claim, err := ClaimBillingSettlementRecovery(ctx, id.RequestID)
		require.NoError(t, err)
		assert.Nil(t, claim)
		after := BillingFinalization{}
		require.NoError(t, DB.Where("request_id = ?", id.RequestID).Take(&after).Error)
		assert.Equal(t, before, after)
		assertLaneOwnerKernelAccounts(t, id, 40, 1, 1)
	})
}

func TestLaneBillingOwnerKernelLostACKAndReplayPostgres(t *testing.T) {
	id := laneOwnerKernelFixture(t)
	store := &laneOwnerKernelTestStore{}
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	lost := DB.Session(&gorm.Session{NewDB: true, Context: t.Context()})
	lost.Statement.ConnPool = loseQuotaCommitAck{sqlDB}
	result, err := ReserveLaneBillingOwner(t.Context(), lost, id, 50, store)
	assert.ErrorIs(t, err, ErrLaneBillingOwnerCommitUncertain)
	assert.Empty(t, result, "uncertain COMMIT grants no hold acknowledgment")
	assertLaneOwnerKernelAccounts(t, id, 50, 1, 1)
	store = &laneOwnerKernelTestStore{}
	result, err = ReserveLaneBillingOwner(t.Context(), DB, id, 50, store)
	require.NoError(t, err)
	assert.False(t, result.Fresh)
	assert.Equal(t, 50, result.Receipt.Held)
	assert.Zero(t, store.reserveCalls.Load())
	changed := id
	changed.Epoch++
	_, err = ReserveLaneBillingOwner(t.Context(), DB, changed, 50, store)
	assert.ErrorIs(t, err, ErrLaneBillingOwnerEvidence)
	_, err = ReserveLaneBillingOwner(t.Context(), DB, id, 51, store)
	assert.ErrorIs(t, err, ErrLaneBillingOwnerEvidence)
	changed = id
	changed.TokenFingerprint = strings.Repeat("0", 64)
	_, err = ReserveLaneBillingOwner(t.Context(), DB, changed, 50, store)
	assert.ErrorIs(t, err, ErrBillingRequestOwnerConflict)
	assert.Zero(t, store.reserveCalls.Load())
	assertLaneOwnerKernelAccounts(t, id, 50, 1, 1)
}

func TestLaneBillingOwnerKernelRollbackAndHistoryPostgres(t *testing.T) {
	id := laneOwnerKernelFixture(t)
	ctx := t.Context()
	for _, check := range []struct {
		name  string
		store *laneOwnerKernelTestStore
		want  error
	}{
		{"write failure", &laneOwnerKernelTestStore{failure: errors.New("synthetic-private-driver-credentials")}, ErrLaneBillingOwnerStore},
		{"writes without child receipt", &laneOwnerKernelTestStore{omitReceipt: true}, ErrLaneBillingOwnerEvidence},
		{"store reports non-fresh child", &laneOwnerKernelTestStore{reportedReplay: true}, ErrLaneBillingOwnerEvidence},
	} {
		t.Run(check.name, func(t *testing.T) {
			result, err := ReserveLaneBillingOwner(ctx, DB, id, 50, check.store)
			require.ErrorIs(t, err, check.want)
			assert.Empty(t, result)
			assert.EqualValues(t, 1, check.store.reserveCalls.Load())
			assert.NotContains(t, err.Error(), "synthetic-private-driver-credentials")
			assertLaneOwnerKernelAccounts(t, id, 0, 0, 0)
		})
	}
	store := &laneOwnerKernelTestStore{}
	legacy := laneOwnerKernelTestRow{RequestID: id.RequestID, LaneID: id.LaneID, Epoch: id.Epoch, UserID: id.UserID, TokenID: id.TokenID,
		TokenFingerprint: id.TokenFingerprint, Held: 50, State: LaneBillingReceiptHeld}
	require.NoError(t, DB.Create(&legacy).Error)
	_, err := ReserveLaneBillingOwner(ctx, DB, id, 50, store)
	assert.ErrorIs(t, err, ErrLaneBillingOwnerEvidence)
	assert.Zero(t, store.reserveCalls.Load())
	assertLaneOwnerKernelAccounts(t, id, 0, 0, 1)
	require.NoError(t, DB.Delete(&legacy).Error)
	marker := laneOwnerKernelOrdinary(id)
	marker.Source = laneBillingOwnerSource
	marker.State = laneBillingOwnerState
	require.NoError(t, DB.Create(&marker).Error)
	_, err = ReserveLaneBillingOwner(ctx, DB, id, 50, store)
	assert.ErrorIs(t, err, ErrLaneBillingOwnerEvidence)
	assert.Zero(t, store.reserveCalls.Load())
	assertLaneOwnerKernelAccounts(t, id, 0, 1, 0)

	tx := DB.Begin()
	require.NoError(t, tx.Error)
	_, err = ReserveLaneBillingOwner(ctx, tx, id, 50, store)
	assert.ErrorIs(t, err, ErrLaneBillingOwnerUnavailable)
	require.NoError(t, tx.Rollback().Error)
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeMySQL, oldLog)
	_, err = ReserveLaneBillingOwner(ctx, DB, id, 50, store)
	common.SetDatabaseTypes(oldMain, oldLog)
	assert.ErrorIs(t, err, ErrLaneBillingOwnerUnavailable)
	assertLaneOwnerKernelAccounts(t, id, 0, 1, 0)
}

func TestLaneBillingOwnerKernelConcurrentClaimsPostgres(t *testing.T) {
	for _, laneFirst := range []bool{false, true} {
		name := "ordinary_wins"
		if laneFirst {
			name = "lane_wins"
		}
		t.Run(name, func(t *testing.T) {
			id := laneOwnerKernelFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			var workers sync.WaitGroup
			defer func() { cancel(); workers.Wait() }()
			blocked, release := make(chan struct{}), make(chan struct{})
			wait := func() error {
				close(blocked)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			type pidContext struct{}
			winnerPIDs, loserPIDs := make(chan int, 1), make(chan int, 1)
			const watch = "test:owner-kernel-competing-backends"
			require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(watch, func(tx *gorm.DB) {
				pids, ok := tx.Statement.Context.Value(pidContext{}).(chan int)
				if !ok || tx.Statement.Table != "billing_finalizations" {
					return
				}
				var pid int
				if err := tx.Session(&gorm.Session{NewDB: true}).Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
					tx.AddError(err)
					return
				}
				select {
				case pids <- pid:
				case <-tx.Statement.Context.Done():
					tx.AddError(tx.Statement.Context.Err())
				}
			}))
			t.Cleanup(func() { require.NoError(t, DB.Callback().Create().Remove(watch)) })
			winner, loser := make(chan error, 1), make(chan error, 1)
			winningCtx, losingCtx := context.WithValue(ctx, pidContext{}, winnerPIDs), context.WithValue(ctx, pidContext{}, loserPIDs)
			var callbacks atomic.Int32
			ordinary := func(c context.Context, pause func() error) error {
				_, _, err := ReserveBillingFinalization(c, laneOwnerKernelOrdinary(id), nil, 0, func(*gorm.DB, *BillingFinalization) error {
					callbacks.Add(1)
					if pause != nil {
						return pause()
					}
					return nil
				})
				return err
			}
			store := &laneOwnerKernelTestStore{}
			if laneFirst {
				store.beforeReserve = wait
				workers.Go(func() { _, err := ReserveLaneBillingOwner(winningCtx, DB, id, 60, store); winner <- err })
			} else {
				workers.Go(func() { winner <- ordinary(winningCtx, wait) })
			}
			select {
			case <-blocked:
			case err := <-winner:
				t.Fatalf("winning entry never reached owner barrier: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var winnerPID, loserPID int
			select {
			case winnerPID = <-winnerPIDs:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if laneFirst {
				workers.Go(func() { loser <- ordinary(losingCtx, nil) })
			} else {
				workers.Go(func() { _, err := ReserveLaneBillingOwner(losingCtx, DB, id, 60, store); loser <- err })
			}
			select {
			case loserPID = <-loserPIDs:
			case err := <-loser:
				t.Fatalf("losing entry never reached owner barrier: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.Eventually(t, func() bool {
				var waiting bool
				err := DB.WithContext(ctx).Raw("SELECT ? = ANY (pg_blocking_pids(?))", winnerPID, loserPID).Scan(&waiting).Error
				return err == nil && waiting
			}, 2*time.Second, 10*time.Millisecond, "loser must be blocked by the exact winner backend")
			close(release)
			require.NoError(t, <-winner)
			assert.ErrorIs(t, <-loser, ErrBillingRequestOwnerConflict)
			if laneFirst {
				assert.Zero(t, callbacks.Load())
				assert.EqualValues(t, 1, store.reserveCalls.Load())
				assertLaneOwnerKernelAccounts(t, id, 60, 1, 1)
			} else {
				assert.EqualValues(t, 1, callbacks.Load())
				assert.Zero(t, store.reserveCalls.Load())
				assertLaneOwnerKernelAccounts(t, id, 0, 1, 0)
			}
		})
	}
}

var _ LaneBillingOwnerStore = (*laneOwnerKernelTestStore)(nil)
