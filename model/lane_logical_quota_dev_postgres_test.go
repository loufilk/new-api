//go:build lane_billing_integration_dev

package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func laneLogicalQuotaFixture(t *testing.T) (*gorm.DB, LaneLogicalQuotaDevScope, LaneLogicalQuotaDevScope) {
	t.Helper()
	db, a, b := finiteKeyDevFixture(t)
	var user User
	user.SetSetting(dto.UserSetting{BillingPreference: "wallet_first"})
	require.NoError(t, db.Model(&User{}).Where("id = ?", a.Owner.UserID).Update("setting", user.Setting).Error)
	for i, binding := range []laneBillingChildBinding{a, b} {
		id := finiteKeyDevIdentity(binding)
		require.NoError(t, EnrollLaneBillingFiniteKeyDev(t.Context(), db, id))
		_, replay, err := ApplyLaneBillingFiniteKeyDevTransfer(t.Context(), db, LaneBillingFiniteKeyTransfer{
			Identity: id, OperationID: "logical-key-grant-" + binding.Owner.RequestID, Slot: binding.KeySlot,
			Direction: "grant", Amount: []int{280, 60}[i],
		})
		require.NoError(t, err)
		require.False(t, replay)
	}
	settled, unknown := a, a
	settled.Owner.RequestID = "logical-settled"
	unknown.Owner.RequestID = "logical-unknown"
	for _, binding := range []laneBillingChildBinding{settled, unknown} {
		store := &laneBillingEnrolledPGChildStore{laneBillingPGChildStore: laneBillingPGChildStore{binding: binding}}
		_, err := ReserveLaneBillingOwner(t.Context(), db, binding.Owner, 25, store)
		require.NoError(t, err)
		require.NoError(t, claimEnrolledLaneBillingChildDispatch(t.Context(), db, binding, binding.FinalWireSHA256))
	}
	require.NoError(t, freezeLaneBillingChildUsage(t.Context(), db, settled, 20, strings.Repeat("e", 64)))
	require.NoError(t, finishLaneBillingChild(t.Context(), db, settled, false))
	require.NoError(t, markLaneBillingChildUnknown(t.Context(), db, unknown))
	return db, LaneLogicalQuotaDevScope{Key: finiteKeyDevIdentity(a), WalletEpoch: a.WalletEpoch},
		LaneLogicalQuotaDevScope{Key: finiteKeyDevIdentity(b), WalletEpoch: b.WalletEpoch}
}

func readLogicalQuotaFixture(t *testing.T, db *gorm.DB, scope LaneLogicalQuotaDevScope) LaneLogicalQuotaDevView {
	t.Helper()
	view, err := ReadLaneLogicalQuotaDev(t.Context(), db, scope)
	require.NoError(t, err)
	return view
}

func TestLaneLogicalQuotaDevReadAndReservePostgres(t *testing.T) {
	db, a, b := laneLogicalQuotaFixture(t)
	before := readLogicalQuotaFixture(t, db, a)
	require.Equal(t, LaneLogicalQuotaDevView{
		WalletPaidRaw: 100, WalletPaidIdle: 55, WalletPaidAvailable: 155,
		KeyRemainRaw: 20, KeyIdle: 235, KeyHeld: 25, KeyUnprojectedSpent: 20, KeyRemainAvailable: 255,
		KeyUsedRaw: 20, KeyUsedCompatible: 65,
	}, before)
	sibling := readLogicalQuotaFixture(t, db, b)
	require.Equal(t, 155, sibling.WalletPaidAvailable, "same wallet funds are not duplicated per Key")
	require.Equal(t, 170, sibling.KeyRemainAvailable)
	require.Equal(t, 21, sibling.KeyUsedCompatible)
	r, attempt, err := ReserveLaneLogicalWalletFirstDev(t.Context(), db, a, "ordinary-logical", 120)
	require.NoError(t, err)
	require.True(t, attempt.Applied)
	require.Equal(t, BillingHeld, r.State)
	require.Equal(t, 120, r.Paid)
	require.Equal(t, 120, r.SourceHeld)
	require.Equal(t, 120, r.TokenHeld)
	after := readLogicalQuotaFixture(t, db, a)
	require.Equal(t, LaneLogicalQuotaDevView{
		WalletPaidRaw: 0, WalletPaidIdle: 35, WalletPaidAvailable: 35,
		KeyRemainRaw: 0, KeyIdle: 135, KeyHeld: 25, KeyUnprojectedSpent: 20, KeyRemainAvailable: 135,
		KeyUsedRaw: 140, KeyUsedCompatible: 185,
	}, after)
	peer := readLogicalQuotaFixture(t, db, b)
	require.Equal(t, 35, peer.WalletPaidAvailable)
	require.Equal(t, sibling.KeyRemainAvailable, peer.KeyRemainAvailable)
	require.Equal(t, sibling.KeyUsedCompatible, peer.KeyUsedCompatible)
	var history int64
	require.NoError(t, db.Model(&BillingFinalization{}).Where("state = ?", BillingSettled).Count(&history).Error)
	require.EqualValues(t, 2, history)
}

func TestLaneLogicalQuotaDevRejectionsAndAtomicityPostgres(t *testing.T) {
	db, a, b := laneLogicalQuotaFixture(t)
	ctx := t.Context()
	assertNoOwner := func(id string) {
		t.Helper()
		var n int64
		require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", id).Count(&n).Error)
		require.Zero(t, n)
	}
	assertUnchanged := func(scope LaneLogicalQuotaDevScope, before LaneLogicalQuotaDevView, id string, amount int) {
		t.Helper()
		_, attempt, err := ReserveLaneLogicalWalletFirstDev(ctx, db, scope, id, amount)
		require.Error(t, err)
		require.False(t, attempt.Applied)
		require.Equal(t, before, readLogicalQuotaFixture(t, db, scope))
		assertNoOwner(id)
	}
	assertUnchanged(a, readLogicalQuotaFixture(t, db, a), "logical-no-wallet", 156)
	// The sibling's original finite limit is isolated: the first Key's large
	// remaining slices cannot fund it. Make the wallet comfortably sufficient.
	require.NoError(t, db.Model(&User{}).Where("id = ?", a.Key.UserID).
		Updates(map[string]any{"quota": 200, "quota_paid": 200}).Error)
	assertUnchanged(b, readLogicalQuotaFixture(t, db, b), "logical-no-key", 171)
	for _, account := range []string{"quota_bonus", "quota_reward"} {
		require.NoError(t, db.Model(&User{}).Where("id = ?", a.Key.UserID).
			Updates(map[string]any{"quota": 201, account: 1}).Error)
		assertUnchanged(a, readLogicalQuotaFixture(t, db, a), "logical-mixed-"+account, 10)
		require.NoError(t, db.Model(&User{}).Where("id = ?", a.Key.UserID).
			Updates(map[string]any{"quota": 200, account: 0}).Error)
	}
	require.NoError(t, db.Model(&User{}).Where("id = ?", a.Key.UserID).
		Updates(map[string]any{"quota": 100, "quota_paid": 100}).Error)
	before := readLogicalQuotaFixture(t, db, a)
	// The terminal hold save occurs after wallet/Key reclaim and both raw
	// debits. A real SQL trigger aborts the outer PostgreSQL transaction here.
	require.NoError(t, db.Exec(`CREATE FUNCTION reject_logical_test_hold() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.request_id = 'logical-trigger-rollback' AND NEW.state = 'held_v2' THEN
			RAISE EXCEPTION 'intentional failure after reclaim and debit';
		END IF;
		RETURN NEW;
	END $$`).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_logical_test_hold BEFORE UPDATE ON billing_finalizations
	FOR EACH ROW EXECUTE FUNCTION reject_logical_test_hold()`).Error)
	_, attempt, err := ReserveLaneLogicalWalletFirstDev(ctx, db, a, "logical-trigger-rollback", 120)
	require.ErrorIs(t, err, ErrLaneBillingOwnerStore)
	require.False(t, attempt.Applied)
	require.Equal(t, before, readLogicalQuotaFixture(t, db, a))
	assertNoOwner("logical-trigger-rollback")
	var receipts int64
	require.NoError(t, db.Model(&SharedWalletTransfer{}).Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)

	pool, err := db.DB()
	require.NoError(t, err)
	lost := db.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{pool}
	unacknowledged, attempt, err := ReserveLaneLogicalWalletFirstDev(ctx, lost, a, "logical-ack-lost", 120)
	require.ErrorIs(t, err, ErrLaneBillingOwnerCommitUncertain)
	require.Empty(t, unacknowledged)
	require.False(t, attempt.Applied)
	durable := readLogicalQuotaFixture(t, db, a)
	require.Equal(t, 35, durable.WalletPaidAvailable)
	require.Equal(t, 135, durable.KeyRemainAvailable)
	r, attempt, err := ReserveLaneLogicalWalletFirstDev(ctx, db, a, "logical-ack-lost", 120)
	require.NoError(t, err)
	require.False(t, attempt.Applied)
	require.Equal(t, 120, r.Paid)
	require.Equal(t, durable, readLogicalQuotaFixture(t, db, a))
}

type laneOrdinarySQLCapture struct {
	logger.Interface
	queries *[]string
}

func (l laneOrdinarySQLCapture) Trace(_ context.Context, _ time.Time, sql func() (string, int64), _ error) {
	q, _ := sql()
	*l.queries = append(*l.queries, strings.ToLower(q))
}

func TestLaneLogicalQuotaDevOrdinarySQLUnchangedPostgres(t *testing.T) {
	db, a, _ := laneLogicalQuotaFixture(t)
	var queries []string
	original := DB
	DB = db.Session(&gorm.Session{Logger: laneOrdinarySQLCapture{Interface: logger.Default, queries: &queries}})
	defer func() { DB = original }()
	r, attempt, err := ReserveBillingFinalization(t.Context(), BillingFinalization{
		UserID: a.Key.UserID, TokenID: a.Key.TokenID, TokenFingerprint: a.Key.TokenFingerprint,
		RequestID: "ordinary-unmodified-query-budget", Source: "wallet",
	}, nil, 0, func(*gorm.DB, *BillingFinalization) error { return nil })
	require.NoError(t, err)
	require.True(t, attempt.Applied)
	require.Equal(t, BillingHeld, r.State)
	require.Len(t, queries, 4, "ordinary zero hold runs only insert, owner lookup, user lock, owner update")
	for _, query := range queries {
		require.Regexp(t, `^(insert into "billing_finalizations"|select .* from "(billing_finalizations|users)"|update "billing_finalizations")`, query)
	}
}
