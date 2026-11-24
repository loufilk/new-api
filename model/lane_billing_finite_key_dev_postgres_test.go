//go:build lane_billing_integration_dev

package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func finiteKeyDevIdentity(b laneBillingChildBinding) LaneBillingFiniteKeyIdentity {
	return LaneBillingFiniteKeyIdentity{UserID: b.Owner.UserID, TokenID: b.Owner.TokenID,
		Epoch: b.KeyEpoch, TokenFingerprint: b.Owner.TokenFingerprint}
}

func finiteKeyDevFixture(t *testing.T, releases ...LaneStoreReleaseV1) (*gorm.DB, laneBillingChildBinding, laneBillingChildBinding) {
	t.Helper()
	db, a, b := childStoreFixture(t, releases...)
	// The old child-store fixture seeds budgets directly. Remove just those
	// two fixture rows so this suite derives all Key funding from enrollment.
	for _, binding := range []laneBillingChildBinding{a, b} {
		k := finiteKeyDevIdentity(binding)
		result := db.Where("user_id = ? AND token_id = ? AND epoch = ?", k.UserID, k.TokenID, k.Epoch).
			Delete(&laneBillingFiniteKeyShareV1{})
		require.NoError(t, result.Error)
		require.EqualValues(t, 1, result.RowsAffected)
	}
	require.NoError(t, db.Model(&Token{}).Where("id = ?", b.Owner.TokenID).Update("remain_quota", 170).Error)
	for range 2 {
		require.NoError(t, InstallLaneBillingFiniteKeyDevSchema(t.Context(), db))
	}
	return db, a, b
}

func finiteKeyDevReadOriginal(t *testing.T, db *gorm.DB, id LaneBillingFiniteKeyIdentity) Token {
	t.Helper()
	var token Token
	require.NoError(t, db.Unscoped().First(&token, id.TokenID).Error)
	return token
}

func TestLaneBillingFiniteKeyDevEnrollmentPostgres(t *testing.T) {
	db, a, b := finiteKeyDevFixture(t)
	ctx := t.Context()
	ka, kb := finiteKeyDevIdentity(a), finiteKeyDevIdentity(b)
	grantA := LaneBillingFiniteKeyTransfer{Identity: ka, OperationID: "grant-existing-a", Slot: a.KeySlot, Direction: "grant", Amount: 80}
	grantB := LaneBillingFiniteKeyTransfer{Identity: kb, OperationID: "grant-existing-b", Slot: b.KeySlot, Direction: "grant", Amount: 50}
	_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, grantA)
	require.Error(t, err, "no enrollment, no escrow")
	require.Equal(t, 300, finiteKeyDevReadOriginal(t, db, ka).RemainQuota)
	for _, id := range []LaneBillingFiniteKeyIdentity{ka, kb} {
		require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
	}
	pool, err := db.DB()
	require.NoError(t, err)
	lost := db.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{pool}
	_, _, err = ApplyLaneBillingFiniteKeyDevTransfer(ctx, lost, grantA)
	require.ErrorIs(t, err, ErrLaneBillingOwnerCommitUncertain, "real commit with lost acknowledgement")
	receipt, replay, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, grantA)
	require.NoError(t, err)
	require.True(t, replay)
	require.EqualValues(t, 300, receipt.RawBefore)
	require.EqualValues(t, 220, receipt.RawAfter)
	require.EqualValues(t, 80, receipt.GrantedAfter)
	require.Zero(t, receipt.ReturnedAfter)
	require.Positive(t, receipt.ShareVersion)
	second, replay, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, grantB)
	require.NoError(t, err)
	require.False(t, replay)
	require.EqualValues(t, 170, second.RawBefore)
	require.EqualValues(t, 120, second.RawAfter)
	for _, mutate := range []func(*LaneBillingFiniteKeyTransfer){
		func(v *LaneBillingFiniteKeyTransfer) { v.Direction = "reclaim" },
		func(v *LaneBillingFiniteKeyTransfer) { v.Amount++ },
		func(v *LaneBillingFiniteKeyTransfer) { v.Slot++ },
		func(v *LaneBillingFiniteKeyTransfer) { v.Identity.Epoch++ },
		func(v *LaneBillingFiniteKeyTransfer) { v.Identity = kb },
		func(v *LaneBillingFiniteKeyTransfer) { v.Identity.TokenFingerprint = strings.Repeat("e", 64) },
	} {
		changed := grantA
		mutate(&changed)
		_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, changed)
		require.ErrorIs(t, err, ErrLaneBillingFiniteKeyConflict)
	}
	require.Equal(t, 220, finiteKeyDevReadOriginal(t, db, ka).RemainQuota)
	require.Equal(t, 120, finiteKeyDevReadOriginal(t, db, kb).RemainQuota)
	require.Equal(t, 20, finiteKeyDevReadOriginal(t, db, ka).UsedQuota)
	require.Equal(t, 21, finiteKeyDevReadOriginal(t, db, kb).UsedQuota)
	var history, paidTransfers int64
	require.NoError(t, db.Model(&BillingFinalization{}).Where("state = ?", BillingSettled).Count(&history).Error)
	require.EqualValues(t, 2, history)
	require.NoError(t, db.Model(&SharedWalletTransfer{}).Count(&paidTransfers).Error)
	require.EqualValues(t, 1, paidTransfers)
}

func TestLaneBillingFiniteKeyDevLifecyclePostgres(t *testing.T) {
	db, a, b := finiteKeyDevFixture(t)
	ctx := t.Context()
	ka, kb := finiteKeyDevIdentity(a), finiteKeyDevIdentity(b)
	for _, binding := range []laneBillingChildBinding{a, b} {
		id := finiteKeyDevIdentity(binding)
		require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
		_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, LaneBillingFiniteKeyTransfer{Identity: id,
			OperationID: "initial-" + binding.Owner.RequestID, Slot: binding.KeySlot, Direction: "grant", Amount: 70})
		require.NoError(t, err)
	}
	reserve := func(binding laneBillingChildBinding) error {
		store := &laneBillingEnrolledPGChildStore{laneBillingPGChildStore: laneBillingPGChildStore{binding: binding}}
		_, err := ReserveLaneBillingOwner(ctx, db, binding.Owner, 15, store)
		return err
	}
	unknown, waiting := a, a
	unknown.Owner.RequestID, waiting.Owner.RequestID = "old-unknown", "old-undispatched"
	for _, binding := range []laneBillingChildBinding{a, unknown, waiting} {
		require.NoError(t, reserve(binding))
	}
	for _, binding := range []laneBillingChildBinding{a, unknown} {
		require.NoError(t, claimEnrolledLaneBillingChildDispatch(ctx, db, binding, binding.FinalWireSHA256))
	}
	require.NoError(t, markLaneBillingChildUnknown(ctx, db, unknown))
	require.NoError(t, ChangeLaneBillingFiniteKeyDevState(ctx, db, ka, LaneBillingFiniteKeyPaused))
	blocked := a
	blocked.Owner.RequestID = "pause-disallows-new-reserve"
	require.Error(t, reserve(blocked))
	require.Error(t, claimEnrolledLaneBillingChildDispatch(ctx, db, waiting, waiting.FinalWireSHA256))
	childStoreEvidence(t, db, waiting, 15, 0, LaneBillingReceiptHeld)
	require.Error(t, EnrollLaneBillingFiniteKeyDev(ctx, db, ka), "enrollment cannot reopen a paused generation")
	require.NoError(t, freezeLaneBillingChildUsage(ctx, db, a, 10, strings.Repeat("e", 64)))
	require.NoError(t, finishLaneBillingChild(ctx, db, a, false), "pause permits measured old settlement")
	require.NoError(t, finishLaneBillingChild(ctx, db, waiting, true), "pause permits proven nondispatch refund")
	require.Error(t, finishLaneBillingChild(ctx, db, unknown, true), "unknown delivery is not refundable")
	require.NoError(t, ChangeLaneBillingFiniteKeyDevState(ctx, db, ka, LaneBillingFiniteKeyRevoked))
	require.Error(t, EnrollLaneBillingFiniteKeyDev(ctx, db, ka))
	require.Error(t, ChangeLaneBillingFiniteKeyDevState(ctx, db, ka, LaneBillingFiniteKeyPaused), "revoked is terminal")
	require.Error(t, reserve(blocked))
	rollover := ka
	rollover.Epoch++
	require.Error(t, EnrollLaneBillingFiniteKeyDev(ctx, db, rollover), "unknown held and spent block a new generation")

	// Revoking this generation must not deny its sibling's shared wallet.
	require.NoError(t, reserve(b))
	require.NoError(t, ChangeLaneBillingFiniteKeyDevState(ctx, db, kb, LaneBillingFiniteKeyRevoked))
	require.Error(t, claimEnrolledLaneBillingChildDispatch(ctx, db, b, b.FinalWireSHA256))
	require.NoError(t, finishLaneBillingChild(ctx, db, b, true))
	reclaim := LaneBillingFiniteKeyTransfer{Identity: kb, OperationID: "return-all-b", Slot: b.KeySlot, Direction: "reclaim", Amount: 70}
	receipt, replay, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, reclaim)
	require.NoError(t, err)
	require.False(t, replay)
	require.EqualValues(t, 170, receipt.RawAfter)
	next := kb
	next.Epoch++
	require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, next), "fully returned, hold-free, spend-free revoked original may roll forward")
}

func TestLaneBillingFiniteKeyDevOriginalReclaimPostgres(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replace bool
	}{{"replacement_fingerprint_never_credited", true}, {"soft_deleted_original_stays_deleted", false}} {
		t.Run(tc.name, func(t *testing.T) {
			db, a, _ := finiteKeyDevFixture(t)
			ctx := t.Context()
			id := finiteKeyDevIdentity(a)
			require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
			op := LaneBillingFiniteKeyTransfer{Identity: id, OperationID: "grant-before-change", Slot: a.KeySlot, Direction: "grant", Amount: 70}
			_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, op)
			require.NoError(t, err)
			require.NoError(t, ChangeLaneBillingFiniteKeyDevState(ctx, db, id, LaneBillingFiniteKeyRevoked))
			op.OperationID, op.Direction = "return-after-change", "reclaim"
			if tc.replace {
				require.NoError(t, db.Model(&Token{}).Where("id = ?", id.TokenID).Update("key", "synthetic-replacement-key").Error)
				_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, op)
				require.Error(t, err)
				token := finiteKeyDevReadOriginal(t, db, id)
				require.Equal(t, 230, token.RemainQuota)
				require.Equal(t, 20, token.UsedQuota)
				require.Equal(t, 70, func() int { _, key := childStoreBalances(t, db, a); return key.Available }())
			} else {
				require.NoError(t, db.Delete(&Token{}, id.TokenID).Error)
				_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, op)
				require.NoError(t, err)
				token := finiteKeyDevReadOriginal(t, db, id)
				require.True(t, token.DeletedAt.Valid)
				require.Equal(t, 300, token.RemainQuota)
				require.Equal(t, 20, token.UsedQuota)
				require.Equal(t, common.TokenStatusEnabled, token.Status, "reclaim is accounting-only")
			}
		})
	}
}

func TestLaneBillingFiniteKeyDevRollbackAndReclaimReplayPostgres(t *testing.T) {
	db, a, b := finiteKeyDevFixture(t)
	ctx := t.Context()
	id := finiteKeyDevIdentity(a)
	require.ErrorIs(t, InstallLaneBillingFiniteKeyDevSchema(ctx, DB), ErrLaneBillingOwnerUnavailable)
	// A different old epoch with preseeded counters is not a trustworthy
	// starting point, even if its requested successor would have no bucket.
	preseed := laneBillingFiniteKeyShareV1{UserID: id.UserID, TokenID: id.TokenID, Epoch: id.Epoch - 1,
		Slot: a.KeySlot, TokenFingerprint: id.TokenFingerprint, Granted: 12, Available: 12, Version: 1}
	require.NoError(t, db.Create(&preseed).Error)
	require.ErrorIs(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id), ErrLaneBillingOwnerEvidence)
	require.NoError(t, db.Delete(&preseed).Error)
	require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
	// Fail after the original raw counter and Key bucket were both written.
	require.NoError(t, db.Exec(`CREATE FUNCTION fail_finite_key_transfer() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.operation_id = 'rollback-grant' THEN RAISE EXCEPTION 'test'; END IF; RETURN NEW; END $$`).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_finite_key_transfer BEFORE INSERT ON lane_billing_key_transfers_v1
		FOR EACH ROW EXECUTE FUNCTION fail_finite_key_transfer()`).Error)
	grant := LaneBillingFiniteKeyTransfer{Identity: id, OperationID: "rollback-grant", Slot: a.KeySlot, Direction: "grant", Amount: 70}
	before := finiteKeyDevReadOriginal(t, db, id)
	_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, grant)
	require.ErrorIs(t, err, ErrLaneBillingOwnerStore)
	after := finiteKeyDevReadOriginal(t, db, id)
	require.Equal(t, before.RemainQuota, after.RemainQuota)
	require.Equal(t, before.CacheRevision, after.CacheRevision)
	var shares, transfers int64
	require.NoError(t, db.Model(&laneBillingFiniteKeyShareV1{}).Count(&shares).Error)
	require.NoError(t, db.Model(&laneBillingFiniteKeyTransferV1{}).Count(&transfers).Error)
	require.Zero(t, shares)
	require.Zero(t, transfers)
	grant.OperationID = "durable-grant"
	original, replay, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, grant)
	require.NoError(t, err)
	require.False(t, replay)
	require.NoError(t, ChangeLaneBillingFiniteKeyDevState(ctx, db, id, LaneBillingFiniteKeyRevoked))
	reclaim := grant
	reclaim.OperationID, reclaim.Direction = "durable-reclaim", "reclaim"
	pool, err := db.DB()
	require.NoError(t, err)
	lost := db.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{pool}
	out, replay, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, lost, reclaim)
	require.ErrorIs(t, err, ErrLaneBillingOwnerCommitUncertain)
	require.Empty(t, out)
	require.False(t, replay)
	out, replay, err = ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, reclaim)
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, before.RemainQuota, out.RawAfter)
	old, replay, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, grant)
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, original, old, "a historical replay does not reinterpret newer quota")
	require.Equal(t, 170, finiteKeyDevReadOriginal(t, db, finiteKeyDevIdentity(b)).RemainQuota)
}

func TestLaneBillingFiniteKeyDevLegacyChildNeverAdoptedPostgres(t *testing.T) {
	db, a, _ := finiteKeyDevFixture(t)
	ctx := t.Context()
	id := finiteKeyDevIdentity(a)
	require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
	_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, LaneBillingFiniteKeyTransfer{Identity: id,
		OperationID: "actual-grant", Slot: a.KeySlot, Direction: "grant", Amount: 70})
	require.NoError(t, err)
	legacy := &laneBillingPGChildStore{binding: a}
	reservation, err := ReserveLaneBillingOwner(ctx, db, a.Owner, 15, legacy)
	require.NoError(t, err)
	require.True(t, reservation.Fresh)
	verified := &laneBillingEnrolledPGChildStore{laneBillingPGChildStore: *legacy}
	_, err = ReserveLaneBillingOwner(ctx, db, a.Owner, 15, verified)
	require.ErrorIs(t, err, ErrLaneBillingOwnerEvidence, "old synthetic custody cannot be replayed as enrolled")
	require.ErrorIs(t, claimEnrolledLaneBillingChildDispatch(ctx, db, a, a.FinalWireSHA256), ErrLaneBillingOwnerEvidence)
	childStoreEvidence(t, db, a, 15, 0, LaneBillingReceiptHeld)
	// Leave the original historical owner available for its actual old contract.
	require.NoError(t, finishLaneBillingChild(ctx, db, a, true))
	childStoreEvidence(t, db, a, 15, 0, laneBillingChildRefunded)
}
