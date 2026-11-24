//go:build lane_billing_integration_dev

package model

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLaneEpayExclusiveScopedOrderWinsLegacyReceiptRacePostgres(t *testing.T) {
	db, scope, order, client, bound := laneEpayCaptureFixtureWithMode(t, false, true)
	require.Equal(t, laneEpayPendingCapture, order.Status)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_MARKED_ORDER")
	// Capture owns the same row that every legacy writer must read under
	// lock. Establish actual PostgreSQL contention before releasing it.
	holder := db.Begin()
	require.NoError(t, holder.Error)
	defer holder.Rollback()
	var current TopUp
	require.NoError(t, lockForUpdate(holder).First(&current, order.Id).Error)
	require.Equal(t, laneEpayPendingCapture, current.Status)
	result := make(chan error, 2)
	go func() { result <- ManualCompleteTopUp(order.TradeNo, "127.0.0.1") }()
	go func() {
		_, err := RechargeEpay(order.TradeNo, order.PaymentMethod, capture.money, "127.0.0.1", capture.facts)
		result <- err
	}()
	assertLaneEpayBlocked(t, db, 2)
	require.NoError(t, holder.Commit().Error)
	require.ErrorIs(t, <-result, ErrLaneEpayExclusiveOrder)
	require.ErrorIs(t, <-result, ErrLaneEpayExclusiveOrder)
	require.ErrorIs(t, UpdatePendingTopUpStatus(order.TradeNo, PaymentProviderEpay, common.TopUpStatusExpired), ErrLaneEpayExclusiveOrder)
	assertLaneEpayEffects(t, db, order, 0, 0)
	// The existing Creem entrypoint deliberately masks internal provider errors.
	require.EqualError(t, RechargeCreem(order.TradeNo, "", "", "127.0.0.1"), "top-up failed")
	assertLaneEpayEffects(t, db, order, 0, 0)

	out, err := bound.ApplyVerified(t.Context(), LaneEpayVerifiedNotification{capture.tradeNo, capture.method, capture.money, capture.facts})
	require.NoError(t, err)
	require.Equal(t, LaneEpayCapturedWaiting, out.State)
	assertLaneEpayEffects(t, db, order, 1, 0)
	releaseLaneEpayHeadroom(t, db, scope.userID)
	out, err = bound.Reconcile(t.Context())
	require.NoError(t, err)
	require.Equal(t, LaneEpayCredited, out.State)
	assertLaneEpayEffects(t, db, order, 1, 1)
}

func TestLaneEpayExclusiveCannotAdoptOrdinaryOrderWhileLegacyCreditsPostgres(t *testing.T) {
	db, scope, order, client, bound := laneEpayCaptureFixtureWithMode(t, false, false)
	releaseLaneEpayHeadroom(t, db, scope.userID)
	// Deterministically pause the real manual handler after it has locked
	// the ordinary order and before its principal ledger can commit.
	require.NoError(t, db.Exec(`CREATE FUNCTION block_fixture_legacy_epay_credit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.idempotency_key = 'credit:topup:SYNTHETIC_EPAY_LANE_ORDER' THEN
				PERFORM pg_advisory_xact_lock(1933372377);
			END IF;
			RETURN NEW;
		END $$`).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER block_fixture_legacy_epay_credit
		BEFORE INSERT ON balance_ledger_operations FOR EACH ROW EXECUTE FUNCTION block_fixture_legacy_epay_credit()`).Error)
	gate := db.Begin()
	require.NoError(t, gate.Error)
	defer gate.Rollback()
	require.NoError(t, gate.Exec("SELECT pg_advisory_xact_lock(1933372377)").Error)
	legacy := make(chan error, 1)
	go func() { legacy <- ManualCompleteTopUp(order.TradeNo, "127.0.0.1") }()
	assertLaneEpayBlocked(t, db, 1)
	draft := order
	draft.Id, draft.Status, draft.CreateTime = 0, "", 0
	managed := make(chan error, 1)
	ctx := t.Context()
	go func() {
		_, err := createBoundLaneEpayOrder(ctx, bound, draft)
		managed <- err
	}()
	assertLaneEpayBlocked(t, db, 2)
	require.NoError(t, gate.Commit().Error)
	require.NoError(t, <-legacy)
	require.ErrorIs(t, <-managed, ErrLaneEpayExclusiveOrder)
	stored := GetTopUpByTradeNo(order.TradeNo)
	require.Equal(t, common.TopUpStatusSuccess, stored.Status)
	require.Zero(t, stored.PaymentReceiptID, "ordinary manual credit is not signed capture")
	var n int64
	require.NoError(t, db.Model(&laneEpayOrderBindingV1{}).Where("trade_no = ?", order.TradeNo).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, db.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", "credit:topup:"+order.TradeNo).Count(&n).Error)
	require.EqualValues(t, 1, n)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_CANNOT_ADOPT")
	out, err := bound.ApplyVerified(ctx, LaneEpayVerifiedNotification{capture.tradeNo, capture.method, capture.money, capture.facts})
	require.ErrorIs(t, err, ErrLaneEpayExclusiveOrder)
	require.Zero(t, out)
	require.NoError(t, db.Model(&PaymentReceipt{}).Where("local_order_key = ?", order.TradeNo).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, db.Model(&User{}).Where("id = ? AND quota = ?", scope.userID, common.MaxWalletQuota-80).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestLaneEpayExclusiveRequiresOriginalSignedQuoteAndNewPreflightPostgres(t *testing.T) {
	db, _, order, client, bound := laneEpayCaptureFixtureWithMode(t, false, true)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_QUOTE_BINDING")
	input := LaneEpayVerifiedNotification{capture.tradeNo, capture.method, capture.money, capture.facts}
	bad := *bound
	wrongKey := *bound.orderSigner
	wrongKey.key[0] ^= 1
	bad.orderSigner = &wrongKey
	_, err := bad.ApplyVerified(t.Context(), input)
	require.ErrorIs(t, err, ErrLaneEpayExclusiveOrder)
	require.NoError(t, db.Model(&TopUp{}).Where("id = ?", order.Id).UpdateColumn("quoted_wallet_quota", 79).Error)
	_, err = bound.ApplyVerified(t.Context(), input)
	require.ErrorIs(t, err, ErrLaneEpayExclusiveOrder)
	require.NoError(t, db.Model(&TopUp{}).Where("id = ?", order.Id).UpdateColumn("quoted_wallet_quota", 80).Error)
	assertLaneEpayEffects(t, db, order, 0, 0)
	out, err := bound.ApplyVerified(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, LaneEpayCapturedWaiting, out.State)
	_, err = bad.Status(t.Context())
	require.ErrorIs(t, err, ErrLaneEpayExclusiveOrder)
	_, err = bad.Reconcile(t.Context())
	require.ErrorIs(t, err, ErrLaneEpayExclusiveOrder)
	assertLaneEpayEffects(t, db, order, 1, 0)

	newScope := *bound
	newScope.scope.tradeNo = "SYNTHETIC_OVER_CEILING_BEFORE_FORM"
	newScope.checkoutRequestID = "SYNTHETIC_OVER_CEILING_REQUEST"
	draft := TopUp{UserId: order.UserId, Amount: 1, Money: 1, TradeNo: newScope.scope.tradeNo,
		PaymentMethod: order.PaymentMethod, PaymentProvider: order.PaymentProvider,
		QuotedWalletQuota: 76, QuotedPaymentMinor: order.QuotedPaymentMinor, QuotedPaymentCurrency: order.QuotedPaymentCurrency,
		QuotedPaymentScope: order.QuotedPaymentScope, QuotedPaymentEnvironment: order.QuotedPaymentEnvironment}
	created, err := createBoundLaneEpayOrder(t.Context(), &newScope, draft)
	require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded)
	require.Zero(t, created)
	var missing TopUp
	require.True(t, errors.Is(db.Where("trade_no = ?", draft.TradeNo).Take(&missing).Error, gorm.ErrRecordNotFound))
	var n int64
	require.NoError(t, db.Model(&laneEpayOrderBindingV1{}).Where("trade_no = ?", draft.TradeNo).Count(&n).Error)
	require.Zero(t, n)
}

func assertLaneEpayBlocked(t *testing.T, db *gorm.DB, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		var blocked int
		err := db.Raw(`SELECT COUNT(*) FROM pg_catalog.pg_stat_activity
			WHERE datname = current_database() AND cardinality(pg_blocking_pids(pid)) > 0`).Scan(&blocked).Error
		return err == nil && blocked >= want
	}, 3*time.Second, 10*time.Millisecond, "real PostgreSQL lock contention must precede release")
}

func TestLaneEpayExclusiveRejectsSubscriptionTopUpCollisionPostgres(t *testing.T) {
	db, scope, order, client, bound := laneEpayCaptureFixtureWithMode(t, false, true)
	upsert := func(trade string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return upsertSubscriptionTopUpTx(tx, &SubscriptionOrder{
				UserId: order.UserId, Money: 1, TradeNo: trade, PaymentMethod: order.PaymentMethod,
			})
		})
	}
	require.ErrorIs(t, upsert(order.TradeNo), ErrLaneEpayExclusiveOrder)
	require.Equal(t, laneEpayPendingCapture, GetTopUpByTradeNo(order.TradeNo).Status)
	releaseLaneEpayHeadroom(t, db, scope.userID)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_SUBSCRIPTION_COLLISION")
	out, err := bound.ApplyVerified(t.Context(), LaneEpayVerifiedNotification{capture.tradeNo, capture.method, capture.money, capture.facts})
	require.NoError(t, err)
	require.Equal(t, LaneEpayCredited, out.State)
	require.ErrorIs(t, upsert(order.TradeNo), ErrPaymentMethodMismatch)
	assertLaneEpayEffects(t, db, order, 1, 1)
	require.NoError(t, upsert("SYNTHETIC_ORDINARY_SUBSCRIPTION_MIRROR"))
	require.NoError(t, upsert("SYNTHETIC_ORDINARY_SUBSCRIPTION_MIRROR"))

	blocked := *bound
	blocked.scope.tradeNo = "SYNTHETIC_EXISTING_SUBSCRIPTION_BEFORE_FORM"
	blocked.checkoutRequestID = "SYNTHETIC_EXISTING_SUBSCRIPTION_REQUEST"
	require.NoError(t, db.Create(&SubscriptionOrder{
		UserId: order.UserId, PlanId: 1, Money: 1, TradeNo: blocked.scope.tradeNo, PaymentMethod: order.PaymentMethod,
		PaymentProvider: order.PaymentProvider, Status: common.TopUpStatusPending,
	}).Error)
	draft := order
	draft.TradeNo = blocked.scope.tradeNo
	draft.Id, draft.CreateTime, draft.Status = 0, 0, ""
	prior, err := createBoundLaneEpayOrder(t.Context(), &blocked, draft)
	require.ErrorIs(t, err, ErrLaneEpayExclusiveOrder)
	require.Zero(t, prior)
	require.Nil(t, GetTopUpByTradeNo(draft.TradeNo))
}
