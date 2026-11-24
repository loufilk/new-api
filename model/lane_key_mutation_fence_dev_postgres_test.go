//go:build lane_billing_integration_dev

package model

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLaneKeyMutationFenceDevPostgres(t *testing.T) {
	db, a, b := finiteKeyDevFixture(t)
	ctx := t.Context()
	require.ErrorIs(t, installLaneKeyMutationFenceDev(ctx, DB), ErrLaneBillingOwnerUnavailable)
	for range 2 {
		require.NoError(t, installLaneKeyMutationFenceDev(ctx, db))
	}
	require.NoError(t, db.AutoMigrate(&Option{}))
	oldDB := DB
	DB = db
	t.Cleanup(func() { DB = oldDB })
	id := finiteKeyDevIdentity(a)
	require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
	op := LaneBillingFiniteKeyTransfer{Identity: id, OperationID: "mutation-grant-old", Slot: a.KeySlot, Direction: "grant", Amount: 70}
	_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, op)
	require.NoError(t, err)
	waiting, unknown := a, a
	waiting.Owner.RequestID = "held-through-disable"
	unknown.Owner.RequestID = "unknown-through-disable"
	reserve := func(binding laneBillingChildBinding) error {
		store := &laneBillingEnrolledPGChildStore{laneBillingPGChildStore: laneBillingPGChildStore{binding: binding}}
		_, err := ReserveLaneBillingOwner(ctx, db, binding.Owner, 15, store)
		return err
	}
	for _, binding := range []laneBillingChildBinding{a, waiting, unknown} {
		require.NoError(t, reserve(binding))
	}
	for _, binding := range []laneBillingChildBinding{a, unknown} {
		require.NoError(t, claimEnrolledLaneBillingChildDispatch(ctx, db, binding, binding.FinalWireSHA256))
	}
	require.NoError(t, markLaneBillingChildUnknown(ctx, db, unknown))

	// A presentation-only edit stays possible but an absolute management limit
	// or finite→unlimited change cannot reconstruct spendable share+raw money.
	before := finiteKeyDevReadOriginal(t, db, id)
	edited := before
	edited.Name = "still the same enrolled credential"
	require.NoError(t, edited.UpdateFromSnapshotAtGroupConfigVersion(before, 1, 0, nil))
	before = finiteKeyDevReadOriginal(t, db, id)
	reject := func(err error) {
		t.Helper()
		var e *pgconn.PgError
		require.True(t, errors.As(err, &e), "expected a database lifecycle refusal: %v", err)
		require.Equal(t, "55000", e.Code)
	}
	for _, change := range []func(*Token){func(k *Token) { k.RemainQuota++ }, func(k *Token) { k.UnlimitedQuota = true }} {
		edited = before
		change(&edited)
		reject(edited.UpdateFromSnapshotAtGroupConfigVersion(before, 1, 0, nil))
	}
	reject(db.Model(&Token{}).Where("id = ?", id.TokenID).UpdateColumn("key", "synthetic-replacement").Error)
	reject(db.Unscoped().Delete(&Token{}, id.TokenID).Error)
	require.Equal(t, before, finiteKeyDevReadOriginal(t, db, id), "refusal rolled back even the earlier permission revision")
	var state string
	readState := func() string {
		t.Helper()
		require.NoError(t, db.Raw("SELECT state FROM lane_billing_key_enrollments_v1 WHERE user_id=? AND token_id=? AND epoch=?", id.UserID, id.TokenID, id.Epoch).Scan(&state).Error)
		return state
	}
	require.Equal(t, LaneBillingFiniteKeyActive, readState())
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		save := errors.New("abort edit")
		err := tx.Transaction(func(inner *gorm.DB) error {
			require.NoError(t, inner.Model(&Token{}).Where("id = ?", id.TokenID).UpdateColumn("status", common.TokenStatusDisabled).Error)
			return save
		})
		require.ErrorIs(t, err, save)
		return nil
	}))
	require.Equal(t, LaneBillingFiniteKeyActive, readState(), "revocation rolls back with the permission edit")

	key := finiteKeyDevReadOriginal(t, db, id)
	_, err = key.DisableForOwner(1)
	require.NoError(t, err)
	require.Equal(t, LaneBillingFiniteKeyRevoked, readState())
	// Even if an administrator later restores the exact same status without
	// creating a new generation, an earlier pre-dispatch hold cannot run.
	require.NoError(t, db.Model(&Token{}).Where("id = ?", id.TokenID).UpdateColumn("status", common.TokenStatusEnabled).Error)
	require.Error(t, claimEnrolledLaneBillingChildDispatch(ctx, db, waiting, waiting.FinalWireSHA256))
	fresh := a
	fresh.Owner.RequestID = "new-after-aba"
	require.Error(t, reserve(fresh))
	require.NoError(t, key.DeleteForOwner(1))
	require.NoError(t, freezeLaneBillingChildUsage(ctx, db, a, 10, strings.Repeat("e", 64)))
	require.NoError(t, finishLaneBillingChild(ctx, db, a, false))
	require.NoError(t, finishLaneBillingChild(ctx, db, waiting, true))
	require.Error(t, finishLaneBillingChild(ctx, db, unknown, true))
	childStoreEvidence(t, db, unknown, 15, 0, laneBillingChildUnknown)
	_, share := childStoreBalances(t, db, a)
	require.Equal(t, 10, share.Spent)
	require.Equal(t, 15, share.Held)
	require.Equal(t, 45, share.Available)
	op.OperationID, op.Direction, op.Amount = "reclaim-old-only", "reclaim", 45
	_, _, err = ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, op)
	require.NoError(t, err)
	original := finiteKeyDevReadOriginal(t, db, id)
	require.True(t, original.DeletedAt.Valid)
	require.Equal(t, 275, original.RemainQuota)
	require.Equal(t, 170, finiteKeyDevReadOriginal(t, db, finiteKeyDevIdentity(b)).RemainQuota)
	// A different key can occupy a new token ID, but cannot receive an old receipt.
	wrong := op
	wrong.Identity = finiteKeyDevIdentity(b)
	_, _, err = ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, wrong)
	require.ErrorIs(t, err, ErrLaneBillingFiniteKeyConflict)
}

// Enrollment locks the original Key without updating the tokens row. A waiter
// must see the newly committed enrollment rather than its outer UPDATE's older
// snapshot. Locate the actual lock wait; do not infer an ordering from a sleep.
func TestLaneKeyMutationFenceDevFirstEnrollmentRacePostgres(t *testing.T) {
	db, a, _ := finiteKeyDevFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, installLaneKeyMutationFenceDev(ctx, db))
	id := finiteKeyDevIdentity(a)
	holder := db.WithContext(ctx).Begin()
	require.NoError(t, holder.Error)
	t.Cleanup(func() { _ = holder.Rollback().Error })
	_, err := lockLaneBillingFiniteKeyUserTx(holder, id, "UPDATE")
	require.NoError(t, err)
	_, err = lockLaneBillingFiniteKeyTokenTx(holder, id, "UPDATE")
	require.NoError(t, err)
	require.NoError(t, holder.Create(&laneBillingFiniteKeyEnrollmentV1{UserID: id.UserID, TokenID: id.TokenID, Epoch: id.Epoch, TokenFingerprint: id.TokenFingerprint, State: LaneBillingFiniteKeyActive}).Error)
	var holderPID int
	require.NoError(t, holder.Raw("SELECT pg_backend_pid()").Scan(&holderPID).Error)
	pidCh := make(chan int, 1)
	done := make(chan error, 1)
	go func() {
		done <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var pid int
			if err := tx.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
				return err
			}
			pidCh <- pid
			return tx.Model(&Token{}).Where("id = ?", id.TokenID).UpdateColumn("status", common.TokenStatusDisabled).Error
		})
	}()
	var waiterPID int
	select {
	case waiterPID = <-pidCh:
	case err := <-done:
		t.Fatalf("waiter did not start: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Eventually(t, func() bool {
		var blocked bool
		return db.WithContext(ctx).Raw("SELECT ? = ANY(pg_blocking_pids(?))", holderPID, waiterPID).Scan(&blocked).Error == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, holder.Commit().Error)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var enrollment laneBillingFiniteKeyEnrollmentV1
	require.NoError(t, db.Where("user_id = ? AND token_id = ? AND epoch = ?", id.UserID, id.TokenID, id.Epoch).Take(&enrollment).Error)
	require.Equal(t, LaneBillingFiniteKeyRevoked, enrollment.State)
}
