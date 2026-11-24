//go:build lane_billing_integration_dev

package model

import (
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLaneNativeAndEpayShareExactWalletHeadroomPostgres(t *testing.T) {
	t.Setenv(UserGroupPricingEnabledEnv, "false")
	require.False(t, common.RedisEnabled, "the native custody fixture deliberately rejects Redis")
	ctx := t.Context()
	release := laneStoreTestRelease()
	db, b, _ := finiteKeyDevFixture(t, release)
	b.Owner.Epoch = release.Epoch
	id := finiteKeyDevIdentity(b)
	oldDB, oldLog := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = oldDB, oldLog })
	require.NoError(t, db.AutoMigrate(&TopUp{}, &PaymentReceipt{}, &AgentCommissionOutbox{}, &SubscriptionOrder{}, &Log{}))
	var schema string
	require.NoError(t, db.Raw("SELECT current_schema()").Scan(&schema).Error)

	var user User
	require.NoError(t, db.Take(&user, id.UserID).Error)
	user.SetSetting(dto.UserSetting{BillingPreference: "wallet_only"})
	require.NoError(t, db.Model(&User{}).Where("id = ?", id.UserID).UpdateColumns(map[string]any{
		"group": "default", "auth_version": 2, "cache_revision": 3, "setting": user.Setting,
	}).Error)
	require.NoError(t, db.Model(&Token{}).Where("id = ?", id.TokenID).
		UpdateColumns(map[string]any{"group": "default", "cache_revision": 5}).Error)
	require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
	_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, LaneBillingFiniteKeyTransfer{
		Identity: id, OperationID: "native-epay-key-grant", Slot: b.KeySlot, Direction: "grant", Amount: 80,
	})
	require.NoError(t, err)

	const room = 7
	credit := func(key string, amount int) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			entry := LedgerEntry{UserId: id.UserID, Account: AccountPaid, Delta: amount,
				Reason: LedgerReasonAdminAdd, IdempotencyKey: key}
			_, err := creditQuotaTx(tx, entry, laneHeadroomApproval(t, tx, schema, b.WalletEpoch, entry))
			return err
		}))
	}
	wallet, _ := childStoreBalances(t, db, b)
	require.Equal(t, 100, wallet.Available)
	credit("fill-native-epay-wallet", common.MaxWalletQuota-room-user.Quota-wallet.Available)
	order := TopUp{UserId: id.UserID, Amount: 1, Money: 1, TradeNo: "SYNTHETIC_NATIVE_EPAY_INTERSECTION",
		PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending,
		QuotedWalletQuota: room, QuotedPaymentMinor: 100, QuotedPaymentCurrency: "CNY",
		QuotedPaymentScope: "631210", QuotedPaymentEnvironment: "test"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := validateTopUpQuotaCapacity(tx, user.Id, room, &laneWalletCustodyScope{tx, schema, user.Id, b.WalletEpoch}); err != nil {
			return err
		}
		return tx.Create(&order).Error
	}))
	credit("use-one-room-after-epay-quote", 1)
	require.NoError(t, db.Take(&user, id.UserID).Error)
	token := finiteKeyDevReadOriginal(t, db, id)
	q, err := CaptureLaneNativeChildQuoteDev(ctx, db, LaneStoreQuoteInputV1{Release: release, RequestID: b.Owner.RequestID,
		UserID: user.Id, UserAuthVersion: user.AuthVersion, UserCacheRevision: user.CacheRevision,
		TokenID: token.Id, TokenFingerprint: id.TokenFingerprint, TokenCacheRevision: token.CacheRevision, Group: "default"})
	require.NoError(t, err)
	const held = 15
	child, err := ReserveLaneNativeChildDev(ctx, db, q, LaneBillingChildDevBinding(b), held)
	require.NoError(t, err)
	require.NoError(t, child.ClaimDispatch(ctx, b.FinalWireSHA256))

	client, err := epay.NewClient(&epay.Config{PartnerID: order.QuotedPaymentScope, Key: "SYNTHETIC_NO_PROVIDER_KEY"}, "http://127.0.0.1:1/")
	require.NoError(t, err)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_NATIVE_CAPTURE")
	scope := laneEpaySettlementScope{db, schema, id.UserID, b.WalletEpoch, order.TradeNo}
	parked, err := parkOrCreditVerifiedLaneEpay(ctx, scope, capture)
	require.NoError(t, err)
	require.Equal(t, laneEpayCaptureWaiting, parked.state, "native hold is still wallet exposure; it has not made room for new money")
	assertLaneEpayEffects(t, db, order, 1, 0)
	_, err = RechargeEpay(order.TradeNo, order.PaymentMethod, capture.money, "127.0.0.1", capture.facts)
	require.ErrorIs(t, err, ErrTopUpStatusInvalid, "ordinary callback cannot credit a parked capture")
	waiting, err := reconcileLaneEpayCapture(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, laneEpayCaptureWaiting, waiting.state)
	require.Equal(t, parked.receiptID, waiting.receiptID)
	wallet, key := childStoreBalances(t, db, b)
	require.Equal(t, held, wallet.Held)
	require.Equal(t, held, key.Held)

	m := LaneBillingMeasuredDev{RequestID: b.Owner.RequestID, ChannelID: b.ChannelID, Model: "fixture",
		Group: "default", PromptTokens: 1, ActualQuota: 1, UsageSHA256: strings.Repeat("1", 64)}
	require.NoError(t, child.SettleMeasured(ctx, m))
	for i := range 2 {
		settled, err := reconcileLaneEpayCapture(ctx, scope)
		require.NoError(t, err)
		require.Equal(t, common.TopUpStatusSuccess, settled.state, "one unit of exact native spend makes exactly enough room")
		require.Equal(t, parked.receiptID, settled.receiptID)
		require.Equal(t, i != 0, settled.replayed)
	}
	require.NoError(t, child.SettleMeasured(ctx, m))
	assertLaneEpayEffects(t, db, order, 1, 1)
	require.NoError(t, db.Take(&user, id.UserID).Error)
	wallet, key = childStoreBalances(t, db, b)
	require.Zero(t, wallet.Held)
	require.Zero(t, key.Held)
	require.Equal(t, 1, wallet.Spent)
	require.Equal(t, 1, key.Spent)
	require.Equal(t, common.MaxWalletQuota, user.QuotaPaid+wallet.Available)
	var owner BillingFinalization
	require.NoError(t, db.Where("request_id = ?", b.Owner.RequestID).Take(&owner).Error)
	require.Equal(t, laneBillingOwnerState, owner.State)
	var n int64
	require.NoError(t, db.Model(&laneBillingUsageOutboxDev{}).Where("request_id = ?", b.Owner.RequestID).Count(&n).Error)
	require.EqualValues(t, 1, n)
}
