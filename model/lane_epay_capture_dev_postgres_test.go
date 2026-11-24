//go:build lane_billing_integration_dev

package model

import (
	"fmt"
	"os"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func laneEpayCaptureFixture(t *testing.T, useRedis bool) (*gorm.DB, laneEpaySettlementScope, TopUp, *epay.Client) {
	t.Helper()
	db, user, epoch, schema := laneWalletHeadroomFixture(t, 24)
	oldMain, oldLog := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = oldMain, oldLog })
	require.NoError(t, db.AutoMigrate(&TopUp{}, &PaymentReceipt{}, &AgentCommissionOutbox{}, &SubscriptionOrder{}, &Log{}))
	merchant := "631210"
	order := TopUp{UserId: user, Amount: 1, Money: 1, TradeNo: "SYNTHETIC_EPAY_LANE_ORDER",
		PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending,
		QuotedWalletQuota: 80, QuotedPaymentMinor: 100, QuotedPaymentCurrency: "CNY",
		QuotedPaymentScope: merchant, QuotedPaymentEnvironment: "test"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := validateTopUpQuotaCapacity(tx, user, order.QuotedWalletQuota, &laneWalletCustodyScope{tx, schema, user, epoch}); err != nil {
			return err
		}
		return tx.Create(&order).Error
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		entry := LedgerEntry{UserId: user, Account: AccountPaid, Delta: 25, Reason: LedgerReasonAdminAdd, IdempotencyKey: "uses-epay-headroom-before-capture"}
		_, err := creditQuotaTx(tx, entry, laneHeadroomApproval(t, tx, schema, epoch, entry))
		return err
	}))
	if useRedis {
		rawDSN := os.Getenv("TEST_LANE_EPAY_REDIS_DSN")
		if rawDSN == "" {
			t.Skip("requires disposable loopback Redis 8.8")
		}
		opt, err := redis.ParseURL(rawDSN)
		require.NoError(t, err)
		require.Regexp(t, "^127[.]0[.]0[.]1:[0-9]+$", opt.Addr)
		rdb := redis.NewClient(opt)
		t.Cleanup(func() { require.NoError(t, rdb.Close()) })
		info, err := rdb.InfoMap(t.Context(), "server").Result()
		require.NoError(t, err)
		require.Equal(t, "8.8.0", info["Server"]["redis_version"])
		n, err := rdb.DBSize(t.Context()).Result()
		require.NoError(t, err)
		require.Zero(t, n)
		oldRedis, oldRDB, oldSync := common.RedisEnabled, common.RDB, common.SyncFrequency
		common.RedisEnabled, common.RDB, common.SyncFrequency = true, rdb, 120
		t.Cleanup(func() { common.RedisEnabled, common.RDB, common.SyncFrequency = oldRedis, oldRDB, oldSync })
		var u User
		require.NoError(t, db.First(&u, user).Error)
		require.NoError(t, populateUserCache(u))
	}
	client, err := epay.NewClient(&epay.Config{PartnerID: merchant, Key: "SYNTHETIC_NO_PROVIDER_KEY"}, "http://127.0.0.1:1/")
	require.NoError(t, err) // neither SignParams nor Verify performs network I/O
	return db, laneEpaySettlementScope{db, schema, user, epoch, order.TradeNo}, order, client
}

func verifiedLaneEpayFixture(t *testing.T, client *epay.Client, order TopUp, price, ref string) (laneVerifiedEpayCapture, map[string]string) {
	t.Helper()
	params, err := client.SignParams(map[string]string{"out_trade_no": order.TradeNo, "type": order.PaymentMethod,
		"trade_no": ref, "trade_status": epay.StatusTradeSuccess, "money": price})
	require.NoError(t, err)
	v, err := client.Verify(params)
	require.NoError(t, err)
	require.True(t, v.VerifyStatus)
	require.Equal(t, epay.StatusTradeSuccess, v.TradeStatus)
	f, ok := NewVerifiedPaymentFacts(PaymentProviderEpay, params["trade_no"], params["pid"], "test", "CNY", v.Money, false)
	require.True(t, ok)
	return laneVerifiedEpayCapture{v.ServiceTradeNo, v.Type, v.Money, f}, params
}

func assertLaneEpayEffects(t *testing.T, db *gorm.DB, order TopUp, receipts, credits int64) {
	t.Helper()
	for _, item := range []struct {
		model any
		query string
		args  []any
		want  int64
	}{
		{&PaymentReceipt{}, "local_order_key = ?", []any{order.TradeNo}, receipts},
		{&BalanceLedgerOperation{}, "idempotency_key = ?", []any{"credit:topup:" + order.TradeNo}, credits},
		{&Log{}, "request_id = ?", []any{laneEpayLogKey(order.Id)}, credits},
		{&AgentCommissionOutbox{}, "reference_id = ?", []any{order.TradeNo}, credits},
	} {
		var n int64
		require.NoError(t, db.Model(item.model).Where(item.query, item.args...).Count(&n).Error)
		require.Equal(t, item.want, n, "%T", item.model)
	}
}

func releaseLaneEpayHeadroom(t *testing.T, db *gorm.DB, user int) {
	t.Helper()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := DebitQuotaTx(tx, LedgerEntry{UserId: user, Delta: 5, Reason: LedgerReasonAdminSubtract, IdempotencyKey: "room-for-captured-epay"}, AccountPaid)
		return err
	}))
}

func TestLaneEpayCaptureParksAndReconcilesExactlyOncePostgresRedis(t *testing.T) {
	db, scope, order, client := laneEpayCaptureFixture(t, true)
	capture, params := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_CAPTURE_A")
	params["money"] = "0.01"
	verified, err := client.Verify(params)
	if err == nil {
		require.False(t, verified.VerifyStatus)
	}
	bad, _ := verifiedLaneEpayFixture(t, client, order, "1.01", "SYNTHETIC_WRONG_AMOUNT")
	_, err = parkOrCreditVerifiedLaneEpay(t.Context(), scope, bad)
	require.ErrorIs(t, err, ErrPaymentAmountMismatch)
	assertLaneEpayEffects(t, db, order, 0, 0)

	parked, err := parkOrCreditVerifiedLaneEpay(t.Context(), scope, capture)
	require.NoError(t, err)
	require.Equal(t, laneEpayCaptureWaiting, parked.state)
	require.False(t, parked.replayed)
	require.Positive(t, parked.receiptID)
	assertLaneEpayEffects(t, db, order, 1, 0)
	current := GetTopUpByTradeNo(order.TradeNo)
	require.Equal(t, laneEpayCaptureWaiting, current.Status)
	require.Equal(t, parked.receiptID, current.PaymentReceiptID)
	require.Zero(t, current.CompleteTime)
	cached, err := cacheGetUserBase(scope.userID)
	require.NoError(t, err)
	require.Equal(t, common.MaxWalletQuota-155, cached.Quota)
	waiting, err := reconcileLaneEpayCapture(t.Context(), scope)
	require.NoError(t, err)
	require.Equal(t, laneEpayCaptureWaiting, waiting.state)
	require.True(t, waiting.replayed)
	require.Equal(t, parked.receiptID, waiting.receiptID)
	conflict, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_CAPTURE_B")
	_, err = parkOrCreditVerifiedLaneEpay(t.Context(), scope, conflict)
	require.ErrorIs(t, err, ErrPaymentReceiptConflict)
	require.ErrorIs(t, UpdatePendingTopUpStatus(order.TradeNo, PaymentProviderEpay, common.TopUpStatusExpired), ErrTopUpStatusInvalid)
	require.Error(t, ManualCompleteTopUp(order.TradeNo, "127.0.0.1"))
	assertLaneEpayEffects(t, db, order, 1, 0)

	releaseLaneEpayHeadroom(t, db, scope.userID)
	type attempt struct {
		out laneEpaySettlementResult
		err error
	}
	ch := make(chan attempt, 4)
	ctx := t.Context()
	for i := range 4 {
		go func() {
			var a attempt
			if i%2 == 0 {
				a.out, a.err = reconcileLaneEpayCapture(ctx, scope)
			} else {
				a.out, a.err = parkOrCreditVerifiedLaneEpay(ctx, scope, capture)
			}
			ch <- a
		}()
	}
	fresh := 0
	for range 4 {
		a := <-ch
		require.NoError(t, a.err)
		require.Equal(t, common.TopUpStatusSuccess, a.out.state)
		require.Equal(t, parked.receiptID, a.out.receiptID)
		if !a.out.replayed {
			fresh++
		}
	}
	require.Equal(t, 1, fresh)
	assertLaneEpayEffects(t, db, order, 1, 1)
	n, err := common.RDB.Exists(ctx, getUserCacheKey(scope.userID)).Result()
	require.NoError(t, err)
	require.Zero(t, n, "only committed credit invalidates Redis")
	refreshed, err := GetUserCache(scope.userID)
	require.NoError(t, err)
	require.Equal(t, common.MaxWalletQuota-80, refreshed.Quota, "existing cache is the raw mirror")
	require.NoError(t, db.Exec("DELETE FROM logs WHERE request_id = ?", laneEpayLogKey(order.Id)).Error)
	_, err = reconcileLaneEpayCapture(ctx, scope)
	require.ErrorIs(t, err, errLaneEpayCaptureEvidence, "success state without its exact log cannot be reasserted")
}

func TestLaneEpayCaptureSQLFailureIsNotParkingPostgres(t *testing.T) {
	db, scope, order, client := laneEpayCaptureFixture(t, false)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_SQL_FAILURE")
	// Failing to read wallet custody must not be reported as waiting. The
	// attempted PaymentReceipt must roll back with the first callback.
	require.NoError(t, db.Exec("ALTER TABLE shared_wallet_shares RENAME TO synthetic_unavailable_wallet_shares").Error)
	_, err := parkOrCreditVerifiedLaneEpay(t.Context(), scope, capture)
	require.Error(t, err)
	require.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
	assertLaneEpayEffects(t, db, order, 0, 0)
	require.NoError(t, db.Exec("ALTER TABLE synthetic_unavailable_wallet_shares RENAME TO shared_wallet_shares").Error)
	parked, err := parkOrCreditVerifiedLaneEpay(t.Context(), scope, capture)
	require.NoError(t, err)
	require.Equal(t, laneEpayCaptureWaiting, parked.state)
	assertLaneEpayEffects(t, db, order, 1, 0)

	// An immutable receipt binds the wallet epoch and the original credit
	// quantity independently of the current recoverable TopUp row.
	otherEpoch := scope
	otherEpoch.walletEpoch++
	_, err = reconcileLaneEpayCapture(t.Context(), otherEpoch)
	require.ErrorIs(t, err, errLaneEpayCaptureEvidence)
	require.NoError(t, db.Model(&TopUp{}).Where("id = ?", order.Id).UpdateColumn("quoted_wallet_quota", 79).Error)
	_, err = reconcileLaneEpayCapture(t.Context(), scope)
	require.ErrorIs(t, err, errLaneEpayCaptureEvidence)
	require.NoError(t, db.Model(&TopUp{}).Where("id = ?", order.Id).UpdateColumn("quoted_wallet_quota", 80).Error)
	releaseLaneEpayHeadroom(t, db, scope.userID)
	require.NoError(t, db.Exec(`CREATE FUNCTION reject_lane_synthetic_log() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic local log failure'; END; $$`).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_lane_synthetic_log BEFORE INSERT ON logs FOR EACH ROW EXECUTE FUNCTION reject_lane_synthetic_log()`).Error)
	_, err = reconcileLaneEpayCapture(t.Context(), scope)
	require.ErrorContains(t, err, "synthetic local log failure")
	still := GetTopUpByTradeNo(order.TradeNo)
	require.Equal(t, laneEpayCaptureWaiting, still.Status)
	require.Equal(t, parked.receiptID, still.PaymentReceiptID)
	require.Zero(t, still.CompleteTime)
	assertLaneEpayEffects(t, db, order, 1, 0)
	require.NoError(t, db.Exec("DROP TRIGGER reject_lane_synthetic_log ON logs").Error)
	credit, err := reconcileLaneEpayCapture(t.Context(), scope)
	require.NoError(t, err)
	require.Equal(t, common.TopUpStatusSuccess, credit.state)
	assertLaneEpayEffects(t, db, order, 1, 1)
}

func TestLaneEpayCaptureRequiresSameLogStoreAndDoesNotAdoptPartialReceiptPostgres(t *testing.T) {
	db, scope, order, client := laneEpayCaptureFixture(t, false)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_RESTORE")
	otherLog := LOG_DB
	LOG_DB = LOG_DB.Session(&gorm.Session{NewDB: true})
	_, err := parkOrCreditVerifiedLaneEpay(t.Context(), scope, capture)
	LOG_DB = otherLog
	require.ErrorIs(t, err, ErrSharedWalletScope)
	_, err = reconcileLaneEpayCapture(t.Context(), scope)
	require.ErrorIs(t, err, ErrTopUpStatusInvalid)
	assertLaneEpayEffects(t, db, order, 0, 0)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var o TopUp
		if err := tx.First(&o, order.Id).Error; err != nil {
			return err
		}
		// Directly construct the partial restore shape without touching the
		// TopUp row; the real callback must not adopt this lone receipt.
		return claimLaneEpayReceiptTx(tx, scope, &o, capture.facts)
	}))
	out, err := parkOrCreditVerifiedLaneEpay(t.Context(), scope, capture)
	require.ErrorIs(t, err, errLaneEpayCaptureEvidence)
	require.Empty(t, out.state, fmt.Sprintf("failure must not return provider acknowledgement: %#v", out))
	require.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
	assertLaneEpayEffects(t, db, order, 1, 0)
}
