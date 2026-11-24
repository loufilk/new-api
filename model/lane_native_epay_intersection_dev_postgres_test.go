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

func TestLaneNativeDisplayAndEpayShareExactWalletHeadroomPostgres(t *testing.T) {
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
	require.NoError(t, db.AutoMigrate(&TopUp{}, &PaymentReceipt{}, &AgentCommissionOutbox{}, &SubscriptionOrder{}, &Log{}, &laneEpayOrderBindingV1{}))
	custody, err := InstallLaneCustodyFixtureV1(ctx, db, db, release, b.WalletEpoch)
	require.NoError(t, err)
	var schema string
	require.NoError(t, db.Raw("SELECT current_schema()").Scan(&schema).Error)

	var user User
	require.NoError(t, db.Take(&user, id.UserID).Error)
	user.SetSetting(dto.UserSetting{BillingPreference: "wallet_only"})
	require.NoError(t, db.Model(&User{}).Where("id = ?", id.UserID).UpdateColumns(map[string]any{
		"group": "default", "auth_version": 2, "cache_revision": 3, "setting": user.Setting,
	}).Error)
	require.NoError(t, db.Model(&Token{}).Where("id = ?", id.TokenID).
		UpdateColumns(map[string]any{"group": "default", "cache_revision": 5, "allow_user_quota_query": true}).Error)
	require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
	_, _, err = ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, LaneBillingFiniteKeyTransfer{
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
		PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay,
		QuotedWalletQuota: room, QuotedPaymentMinor: 100, QuotedPaymentCurrency: "CNY",
		QuotedPaymentScope: "631210", QuotedPaymentEnvironment: "test"}
	bound := &BoundLaneEpayOrder{
		scope: laneEpaySettlementScope{db, schema, id.UserID, b.WalletEpoch, order.TradeNo}, authority: custody,
		orderSigner: laneEpaySyntheticSigner(),
	}
	order, err = createBoundLaneEpayOrder(ctx, bound, order)
	require.NoError(t, err)

	credit("use-one-room-after-epay-quote", 1)
	require.NoError(t, db.Take(&user, id.UserID).Error)
	token := finiteKeyDevReadOriginal(t, db, id)
	display := &LaneBillingDisplayV1{primary: db, log: db, scope: id, walletEpoch: b.WalletEpoch, store: custody}
	assertDisplay := func(available, keyAvailable, keyUsed int) {
		t.Helper()
		account, matched, err := display.ReadUser(ctx, user.Id, user.AuthVersion)
		require.True(t, matched)
		require.NoError(t, err)
		require.Equal(t, LaneAccountBillingDisplay{Available: available, Used: user.UsedQuota, Requests: user.RequestCount}, account)
		keyView, matched, err := display.ReadToken(ctx, user.Id, token.Id, id.TokenFingerprint, nil)
		require.True(t, matched)
		require.NoError(t, err)
		require.Equal(t, keyAvailable, keyView.Available)
		require.Equal(t, keyUsed, keyView.Used)
		require.Empty(t, keyView.Token.Key)
		require.Equal(t, &account, keyView.Account)
	}
	keyAvailable := token.RemainQuota + 80
	assertDisplay(common.MaxWalletQuota-room+1, keyAvailable, token.UsedQuota)
	q, err := CaptureLaneNativeChildQuoteDev(ctx, db, LaneStoreQuoteInputV1{Release: release, RequestID: b.Owner.RequestID,
		UserID: user.Id, UserAuthVersion: user.AuthVersion, UserCacheRevision: user.CacheRevision,
		TokenID: token.Id, TokenFingerprint: id.TokenFingerprint, TokenCacheRevision: token.CacheRevision, Group: "default"}, custody)
	require.NoError(t, err)
	const held = 15
	child, err := ReserveLaneNativeChildDev(ctx, db, q, LaneBillingChildDevBinding(b), held)
	require.NoError(t, err)
	assertDisplay(common.MaxWalletQuota-room+1-held, keyAvailable-held, token.UsedQuota+held)

	client, err := epay.NewClient(&epay.Config{PartnerID: order.QuotedPaymentScope, Key: "SYNTHETIC_NO_PROVIDER_KEY"}, "http://127.0.0.1:1/")
	require.NoError(t, err)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_NATIVE_CAPTURE")
	parked, err := bound.ApplyVerified(ctx, LaneEpayVerifiedNotification{
		TradeNo: order.TradeNo, Method: capture.method, Money: capture.money, Facts: capture.facts,
	})
	require.NoError(t, err)
	require.Equal(t, LaneEpayCapturedWaiting, parked.State, "native hold is still wallet exposure; it has not made room for new money")
	assertLaneEpayEffects(t, db, order, 1, 0)
	recorded, err := bound.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, parked.ReceiptID, recorded.ReceiptID)
	require.Positive(t, recorded.ReceiptRecordedAt)
	require.Zero(t, recorded.CreditCompletedAt)
	_, err = RechargeEpay(order.TradeNo, order.PaymentMethod, capture.money, "127.0.0.1", capture.facts)
	require.ErrorIs(t, err, ErrTopUpStatusInvalid, "ordinary callback cannot credit a parked capture")
	waiting, err := bound.Reconcile(ctx)
	require.NoError(t, err)
	require.Equal(t, LaneEpayCapturedWaiting, waiting.State)
	require.Equal(t, parked.ReceiptID, waiting.ReceiptID)

	// One physical version change blocks all three consumers. The native
	// owner is already held, but cannot dispatch; display cannot fall back
	// to raw balances; payment retains exactly its original pending receipt.
	require.NoError(t, db.Exec("UPDATE lane_custody_catalog_v1 SET wallet_epoch=wallet_epoch+1").Error)
	unavailable, matched, err := display.ReadUser(ctx, user.Id, user.AuthVersion)
	require.True(t, matched)
	require.ErrorIs(t, err, ErrLaneStoreAuthorityEvidence)
	require.Zero(t, unavailable)
	unavailableKey, matched, err := display.ReadToken(ctx, user.Id, token.Id, id.TokenFingerprint, nil)
	require.True(t, matched)
	require.ErrorIs(t, err, ErrLaneStoreAuthorityEvidence)
	require.Zero(t, unavailableKey)
	denied, err := bound.Reconcile(ctx)
	require.ErrorIs(t, err, ErrLaneStoreAuthorityEvidence)
	require.Zero(t, denied)
	require.ErrorIs(t, child.ClaimDispatch(ctx, b.FinalWireSHA256), ErrLaneBillingOwnerEvidence)
	assertLaneEpayEffects(t, db, order, 1, 0)
	require.ErrorIs(t, CheckLaneStoreOrdinaryStartup(ctx, db), ErrLaneStoreAuthorityState)
	// Restore only the deliberate isolated fixture injection; no runtime
	// API exists to roll a real wallet generation backward.
	require.NoError(t, db.Exec("UPDATE lane_custody_catalog_v1 SET wallet_epoch=wallet_epoch-1").Error)
	require.NoError(t, child.ClaimDispatch(ctx, b.FinalWireSHA256))
	wallet, key := childStoreBalances(t, db, b)
	require.Equal(t, held, wallet.Held)
	require.Equal(t, held, key.Held)

	m := LaneBillingMeasuredDev{RequestID: b.Owner.RequestID, ChannelID: b.ChannelID, Model: "fixture",
		Group: "default", PromptTokens: 1, ActualQuota: 1, UsageSHA256: strings.Repeat("1", 64)}
	require.NoError(t, child.SettleMeasured(ctx, m))
	assertDisplay(common.MaxWalletQuota-room, keyAvailable-1, token.UsedQuota+1)
	for i := range 2 {
		settled, err := bound.Reconcile(ctx)
		require.NoError(t, err)
		require.Equal(t, LaneEpayCredited, settled.State, "one unit of exact native spend makes exactly enough room")
		require.Equal(t, parked.ReceiptID, settled.ReceiptID)
		require.Equal(t, i != 0, settled.Replayed)
	}
	finalStatus, err := bound.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, recorded.ReceiptRecordedAt, finalStatus.ReceiptRecordedAt)
	require.GreaterOrEqual(t, finalStatus.CreditCompletedAt, recorded.ReceiptRecordedAt)
	assertDisplay(common.MaxWalletQuota, keyAvailable-1, token.UsedQuota+1)
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
