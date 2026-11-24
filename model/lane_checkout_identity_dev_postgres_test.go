//go:build lane_billing_integration_dev

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func installLaneCheckoutNamespaceFixture(t *testing.T, db *gorm.DB, bound *BoundLaneEpayOrder) *laneCheckoutNamespaceV1 {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&laneCheckoutIssuerV1{}, &laneCheckoutIdentityV1{}))
	// Synthetic migration rehearsal only. An application writer cannot
	// rewrite or erase a granted identity using ordinary DML.
	require.NoError(t, db.Exec(`CREATE FUNCTION block_lane_checkout_fixture_identity_change() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'immutable checkout identity'; END $$`).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER lane_checkout_fixture_identity_immutable
		BEFORE UPDATE OR DELETE OR TRUNCATE ON lane_checkout_identities_v1
		FOR EACH STATEMENT EXECUTE FUNCTION block_lane_checkout_fixture_identity_change()`).Error)
	n := &laneCheckoutNamespaceV1{primary: db, signer: bound.orderSigner, custody: bound.authority,
		issuerID: "synthetic-checkout-issuer", generation: 1, walletEpoch: bound.scope.walletEpoch}
	var err error
	n.pool, err = db.DB()
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		n.space, err = laneCustodySpaceTx(tx)
		if err != nil {
			return err
		}
		n.roster, err = readLaneCheckoutRosterTx(tx, n.space)
		if err != nil {
			return err
		}
		return tx.Create(&laneCheckoutIssuerV1{Singleton: 1, IssuerID: n.issuerID, Generation: n.generation,
			WalletEpoch: n.walletEpoch, KeyID: n.signer.keyID, KeyFingerprint: laneCheckoutKeyFingerprint(n.signer)}).Error
	}))
	return n
}

func laneCheckoutFresh(base *BoundLaneEpayOrder, prototype TopUp, trade, request string) (*BoundLaneEpayOrder, TopUp) {
	out := *base
	out.checkoutRequestID, out.scope.tradeNo = request, trade
	prototype.Id, prototype.CreateTime, prototype.Status, prototype.TradeNo = 0, 0, "", trade
	prototype.QuotedWalletQuota = 10
	return &out, prototype
}

func laneCheckoutSubscriptionRequest(b *BoundLaneEpayOrder, requestID string) laneCheckoutRequestV1 {
	q := sha256.Sum256([]byte("synthetic-subscription-plan:1|CNY|100"))
	return laneCheckoutRequestV1{tradeNo: b.scope.tradeNo, requestID: requestID,
		family: laneCheckoutEpaySubscription, quote: hex.EncodeToString(q[:]), userID: b.scope.userID}
}

func laneCheckoutSubscriptionTx(tx *gorm.DB, b *BoundLaneEpayOrder, r laneCheckoutRequestV1) error {
	_, replay, err := b.checkoutNamespace.allocateTx(tx, b.scope.db, b.scope, r)
	if err != nil || replay {
		return err
	}
	return tx.Create(&SubscriptionOrder{UserId: r.userID, PlanId: 1, Money: 1, TradeNo: r.tradeNo,
		PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending}).Error
}

func assertLaneCheckoutRows(t *testing.T, db *gorm.DB, trade string, identities, topups, subscriptions int64) {
	t.Helper()
	for _, item := range []struct {
		model any
		n     int64
	}{
		{&laneCheckoutIdentityV1{}, identities},
		{&TopUp{}, topups},
		{&SubscriptionOrder{}, subscriptions},
	} {
		var n int64
		require.NoError(t, db.Model(item.model).Where("trade_no = ?", trade).Count(&n).Error)
		require.Equal(t, item.n, n, "%T", item.model)
	}
}

func TestLaneCheckoutIdentitySubscriptionCommitOrRollbackPrecedesEpayPostgres(t *testing.T) {
	for _, commit := range []bool{true, false} {
		name := "subscription_rolls_back"
		if commit {
			name = "subscription_commits"
		}
		t.Run(name, func(t *testing.T) {
			db, _, sample, _, base := laneEpayCaptureFixtureWithMode(t, false, false)
			b, draft := laneCheckoutFresh(base, sample, "SYNTHETIC_SHARED_TRADE", "SYNTHETIC_EPAY_REQ")
			r := laneCheckoutSubscriptionRequest(b, "SYNTHETIC_SUB_REQ")
			leader := db.Begin()
			require.NoError(t, leader.Error)
			defer leader.Rollback()
			require.NoError(t, laneCheckoutSubscriptionTx(leader, b, r))
			ctx := t.Context()
			done := make(chan error, 1)
			go func() { _, err := createBoundLaneEpayOrder(ctx, b, draft); done <- err }()
			assertLaneEpayBlocked(t, db, 1)
			if commit {
				require.NoError(t, leader.Commit().Error)
				require.ErrorIs(t, <-done, errLaneCheckoutConflict)
				assertLaneCheckoutRows(t, db, draft.TradeNo, 1, 0, 1)
			} else {
				require.NoError(t, leader.Rollback().Error)
				require.NoError(t, <-done)
				assertLaneCheckoutRows(t, db, draft.TradeNo, 1, 1, 0)
			}
		})
	}
}

func TestLaneCheckoutIdentityEpayFirstPrecedesSubscriptionPostgres(t *testing.T) {
	db, _, sample, _, base := laneEpayCaptureFixtureWithMode(t, false, false)
	b, draft := laneCheckoutFresh(base, sample, "SYNTHETIC_SHARED_EPAY_FIRST", "SYNTHETIC_EPAY_FIRST_REQUEST")
	require.NoError(t, db.Exec(`CREATE FUNCTION block_fixture_namespace_epay() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.trade_no = 'SYNTHETIC_SHARED_EPAY_FIRST' THEN PERFORM pg_advisory_xact_lock(1933372391); END IF;
			RETURN NEW;
		END $$`).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER block_fixture_namespace_epay BEFORE INSERT ON top_ups
		FOR EACH ROW EXECUTE FUNCTION block_fixture_namespace_epay()`).Error)
	gate := db.Begin()
	require.NoError(t, gate.Error)
	defer gate.Rollback()
	require.NoError(t, gate.Exec("SELECT pg_advisory_xact_lock(1933372391)").Error)
	created, competing := make(chan error, 1), make(chan error, 1)
	ctx := t.Context()
	go func() { _, err := createBoundLaneEpayOrder(ctx, b, draft); created <- err }()
	assertLaneEpayBlocked(t, db, 1)
	go func() {
		competing <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return laneCheckoutSubscriptionTx(tx, b, laneCheckoutSubscriptionRequest(b, "SYNTHETIC_SUB_SECOND"))
		})
	}()
	assertLaneEpayBlocked(t, db, 2)
	require.NoError(t, gate.Commit().Error)
	require.NoError(t, <-created)
	require.ErrorIs(t, <-competing, errLaneCheckoutConflict)
	assertLaneCheckoutRows(t, db, draft.TradeNo, 1, 1, 0)
}

func TestLaneCheckoutIdentityExactReplayAndCrossScopeConflictPostgres(t *testing.T) {
	db, _, order, _, b := laneEpayCaptureFixtureWithMode(t, false, true)
	n := b.checkoutNamespace
	exact, err := laneEpayCheckoutRequest(b.checkoutRequestID, order)
	require.NoError(t, err)
	allocate := func(scope laneEpaySettlementScope, r laneCheckoutRequestV1) (stored laneCheckoutIdentityV1, replay bool, err error) {
		err = db.Transaction(func(tx *gorm.DB) error {
			var e error
			stored, replay, e = n.allocateTx(tx, db, scope, r)
			return e
		})
		return
	}
	first, replay, err := allocate(b.scope, exact)
	require.NoError(t, err)
	require.True(t, replay)
	second, replay, err := allocate(b.scope, exact)
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, first, second)
	draft := order
	draft.Id, draft.Status, draft.CreateTime = 0, "", 0
	blank, err := createBoundLaneEpayOrder(t.Context(), b, draft)
	require.ErrorIs(t, err, errLaneCheckoutConflict, "exact replay is not a second form authorization")
	require.Zero(t, blank)
	for _, c := range []string{"family", "request", "wallet_user", "quote", "trade", "epoch"} {
		t.Run(c, func(t *testing.T) {
			r, s := exact, b.scope
			var expected error = errLaneCheckoutConflict
			switch c {
			case "family":
				r.family = laneCheckoutEpaySubscription
			case "request":
				r.requestID = "SYNTHETIC_OTHER_REQUEST"
			case "wallet_user":
				r.userID++
				s.userID++
			case "quote":
				r.quote = laneCheckoutSubscriptionRequest(b, "SYNTHETIC_UNUSED").quote
			case "trade":
				r.tradeNo = "SYNTHETIC_CHANGED_TRADE"
				s.tradeNo = r.tradeNo
			case "epoch":
				s.walletEpoch++
				expected = errLaneCheckoutIssuer
			}
			x, replayed, e := allocate(s, r)
			require.ErrorIs(t, e, expected)
			require.False(t, replayed)
			require.Zero(t, x)
		})
	}
	assertLaneCheckoutRows(t, db, order.TradeNo, 1, 1, 0)
	assertLaneCheckoutRows(t, db, "SYNTHETIC_CHANGED_TRADE", 0, 0, 0)
	require.ErrorContains(t, db.Exec("UPDATE lane_checkout_identities_v1 SET request_id = 'SYNTHETIC_REWRITE'").Error,
		"immutable checkout identity")
	require.ErrorContains(t, db.Exec("DELETE FROM lane_checkout_identities_v1").Error, "immutable checkout identity")
}

func TestLaneCheckoutIdentityOldIssuerDeniedHistoricalCaptureRetainedPostgres(t *testing.T) {
	db, scope, order, client, bound := laneEpayCaptureFixtureWithMode(t, false, true)
	fresh, draft := laneCheckoutFresh(bound, order, "SYNTHETIC_NEXT_ISSUER_TRADE", "SYNTHETIC_NEXT_ISSUER_REQUEST")
	ns := *fresh.checkoutNamespace
	fresh.checkoutNamespace = &ns
	ns.issuerID = "unregistered-old-issuer"
	_, err := createBoundLaneEpayOrder(t.Context(), fresh, draft)
	require.ErrorIs(t, err, errLaneCheckoutIssuer)
	ns.issuerID = bound.checkoutNamespace.issuerID
	key := *ns.signer
	key.key[0] ^= 1 // same key ID is not the same enrolled key
	ns.signer, fresh.orderSigner = &key, &key
	_, err = createBoundLaneEpayOrder(t.Context(), fresh, draft)
	require.ErrorIs(t, err, errLaneCheckoutIssuer)
	ns.signer, fresh.orderSigner = bound.orderSigner, bound.orderSigner
	require.NoError(t, db.Model(&laneCheckoutIssuerV1{}).Where("singleton=1").UpdateColumn("generation", 2).Error)
	_, err = createBoundLaneEpayOrder(t.Context(), fresh, draft)
	require.ErrorIs(t, err, errLaneCheckoutIssuer)
	assertLaneCheckoutRows(t, db, draft.TradeNo, 0, 0, 0)
	ns.generation = 2
	newer, err := createBoundLaneEpayOrder(t.Context(), fresh, draft)
	require.NoError(t, err)
	require.Positive(t, newer.Id)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_HISTORICAL_ISSUER")
	out, err := bound.ApplyVerified(t.Context(), LaneEpayVerifiedNotification{capture.tradeNo, capture.method, capture.money, capture.facts})
	require.NoError(t, err)
	require.Equal(t, LaneEpayCapturedWaiting, out.State)
	releaseLaneEpayHeadroom(t, db, scope.userID)
	out, err = bound.Reconcile(t.Context())
	require.NoError(t, err)
	require.Equal(t, LaneEpayCredited, out.State)
	assertLaneEpayEffects(t, db, order, 1, 1)
}

func TestLaneCheckoutIdentityMissingProofBlocksCaptureAndRollbackPostgres(t *testing.T) {
	db, _, order, client, b := laneEpayCaptureFixtureWithMode(t, false, true)
	ctx := t.Context()
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_MISSING_IDENTITY")
	// Deliberate fixture corruption, not an available application path.
	require.NoError(t, db.Exec("ALTER TABLE lane_checkout_identities_v1 DISABLE TRIGGER lane_checkout_fixture_identity_immutable").Error)
	require.NoError(t, db.Exec("DELETE FROM lane_checkout_identities_v1 WHERE trade_no = ?", order.TradeNo).Error)
	out, err := b.ApplyVerified(ctx, LaneEpayVerifiedNotification{capture.tradeNo, capture.method, capture.money, capture.facts})
	require.ErrorIs(t, err, errLaneCheckoutConflict)
	require.Zero(t, out)
	assertLaneEpayEffects(t, db, order, 0, 0)
	fresh, draft := laneCheckoutFresh(b, order, "SYNTHETIC_ROLLBACK_TRADE", "SYNTHETIC_ROLLBACK_REQUEST")
	draft.QuotedWalletQuota = 76 // only 75 units of logical room remain
	_, err = createBoundLaneEpayOrder(ctx, fresh, draft)
	require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded)
	assertLaneCheckoutRows(t, db, draft.TradeNo, 0, 0, 0)
	r := laneCheckoutSubscriptionRequest(fresh, "SYNTHETIC_AFTER_ROLLBACK")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return laneCheckoutSubscriptionTx(tx, fresh, r)
	}))
	assertLaneCheckoutRows(t, db, draft.TradeNo, 1, 0, 1)
}
