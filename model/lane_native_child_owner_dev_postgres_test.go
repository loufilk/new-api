//go:build lane_billing_integration_dev

package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLaneNativeChildOwnerOneTransactionPostgres(t *testing.T) {
	t.Setenv(UserGroupPricingEnabledEnv, "false")
	ctx := t.Context()
	r := laneStoreTestRelease()
	db, b, _ := finiteKeyDevFixture(t, r)
	b.Owner.Epoch = r.Epoch
	id := finiteKeyDevIdentity(b)
	var user User
	require.NoError(t, db.Take(&user, b.Owner.UserID).Error)
	user.SetSetting(dto.UserSetting{BillingPreference: "wallet_only"})
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumns(map[string]any{"group": "default", "auth_version": 2, "cache_revision": 3, "setting": user.Setting}).Error)
	require.NoError(t, db.Model(&Token{}).Where("id = ?", id.TokenID).UpdateColumns(map[string]any{"group": "default", "cache_revision": 5}).Error)
	require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
	_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, LaneBillingFiniteKeyTransfer{Identity: id, OperationID: "native-owner-grant", Slot: b.KeySlot, Direction: "grant", Amount: 80})
	require.NoError(t, err)
	// Keep the selected slots at 100/80, but grant the remaining raw
	// balances into sibling slots through their real cold writers.
	_, _, err = ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, LaneBillingFiniteKeyTransfer{Identity: id, OperationID: "native-owner-sibling-key",
		Slot: b.KeySlot + 1, Direction: "grant", Amount: 220})
	require.NoError(t, err)
	_, _, err = ApplySharedWalletTransfer(ctx, db, SharedWalletTransfer{UserID: b.Owner.UserID, Epoch: b.WalletEpoch,
		Slot: b.WalletSlot + 1, OperationID: "native-owner-sibling-wallet", Direction: "grant", Amount: 100})
	require.NoError(t, err)
	require.NoError(t, db.Take(&user, b.Owner.UserID).Error)
	token := finiteKeyDevReadOriginal(t, db, id)
	require.Zero(t, user.QuotaPaid)
	require.Zero(t, token.RemainQuota)
	quote := func(binding laneBillingChildBinding) LaneStoreQuoteAuthorityV1 {
		q, err := CaptureLaneNativeChildQuoteDev(ctx, db, LaneStoreQuoteInputV1{Release: r, RequestID: binding.Owner.RequestID, UserID: user.Id, TokenID: id.TokenID,
			TokenFingerprint: id.TokenFingerprint, UserAuthVersion: user.AuthVersion, UserCacheRevision: user.CacheRevision,
			TokenCacheRevision: token.CacheRevision, Group: "default"})
		require.NoError(t, err)
		return q
	}
	require.ErrorIs(t, CheckLaneStoreOrdinaryStartup(ctx, db), ErrLaneStoreAuthorityState, "coexisting synthetic custody must never start ordinary runtime")
	q := quote(b)
	wallet, key := childStoreBalances(t, db, b)
	require.NoError(t, db.Model(&SharedWalletShare{}).Where("user_id = ? AND epoch = ? AND slot = ?", user.Id, b.WalletEpoch, b.WalletSlot).
		UpdateColumn("last_operation_id", "unknown-transfer-tip").Error)
	res, err := ReserveLaneNativeChildDev(ctx, db, q, LaneBillingChildDevBinding(b), 15)
	require.ErrorIs(t, err, ErrLaneBillingOwnerEvidence)
	require.Nil(t, res)
	var n int64
	require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", b.Owner.RequestID).Count(&n).Error)
	require.Zero(t, n, "wallet evidence error rolls back the canonical marker")
	require.NoError(t, db.Model(&laneBillingChildReceiptV1{}).Where("request_id = ?", b.Owner.RequestID).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, db.Model(&SharedWalletShare{}).Where("user_id = ? AND epoch = ? AND slot = ?", user.Id, b.WalletEpoch, b.WalletSlot).
		UpdateColumn("last_operation_id", wallet.LastOperationID).Error)
	walletNow, keyNow := childStoreBalances(t, db, b)
	require.Equal(t, wallet, walletNow)
	require.Equal(t, key, keyNow)

	// Wallet's first update succeeds within the transaction; insufficient
	// selected Key (despite sibling custody) must roll it back completely.
	res, err = ReserveLaneNativeChildDev(ctx, db, q, LaneBillingChildDevBinding(b), 90)
	require.Error(t, err)
	require.Nil(t, res)
	walletNow, keyNow = childStoreBalances(t, db, b)
	require.Equal(t, wallet, walletNow)
	require.Equal(t, key, keyNow)
	require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", b.Owner.RequestID).Count(&n).Error)
	require.Zero(t, n, "partial dual hold must not leave an ordinary or lane owner")
	require.NoError(t, db.Model(&laneBillingChildReceiptV1{}).Where("request_id = ?", b.Owner.RequestID).Count(&n).Error)
	require.Zero(t, n)
	pool, err := db.DB()
	require.NoError(t, err)
	lost := db.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{pool}
	res, err = ReserveLaneNativeChildDev(ctx, lost, q, LaneBillingChildDevBinding(b), 15)
	require.ErrorIs(t, err, ErrLaneBillingOwnerCommitUncertain)
	require.Nil(t, res, "no dispatch capability from a lost acknowledgement")
	res, err = ReserveLaneNativeChildDev(ctx, db, q, LaneBillingChildDevBinding(b), 15)
	require.ErrorIs(t, err, ErrBillingRequestOwnerConflict)
	require.Nil(t, res, "caller cannot turn a replay into another send")
	walletNow, keyNow = childStoreBalances(t, db, b)
	require.Equal(t, 15, walletNow.Held)
	require.Equal(t, 15, keyNow.Held)

	waiting, sent := b, b
	waiting.Owner.RequestID, sent.Owner.RequestID = "native-owner-waiting", "native-owner-sent"
	held, err := ReserveLaneNativeChildDev(ctx, db, quote(waiting), LaneBillingChildDevBinding(waiting), 10)
	require.NoError(t, err)
	delivered, err := ReserveLaneNativeChildDev(ctx, db, quote(sent), LaneBillingChildDevBinding(sent), 10)
	require.NoError(t, err)
	require.NoError(t, delivered.ClaimDispatch(ctx, sent.FinalWireSHA256))
	measured := b
	measured.Owner.RequestID = "native-owner-measured"
	measuredChild, err := ReserveLaneNativeChildDev(ctx, db, quote(measured), LaneBillingChildDevBinding(measured), 10)
	require.NoError(t, err)
	require.NoError(t, measuredChild.ClaimDispatch(ctx, measured.FinalWireSHA256))
	// Simulate restored stale enrollment evidence: the prior row still
	// looks active and its slot/tip still match, but its epoch is not current.
	newer := laneBillingFiniteKeyEnrollmentV1{UserID: id.UserID, TokenID: id.TokenID, Epoch: id.Epoch + 1,
		TokenFingerprint: id.TokenFingerprint, State: LaneBillingFiniteKeyPaused}
	require.NoError(t, db.Create(&newer).Error)
	stale := b
	stale.Owner.RequestID = "native-owner-stale-generation"
	res, err = ReserveLaneNativeChildDev(ctx, db, quote(stale), LaneBillingChildDevBinding(stale), 10)
	require.ErrorIs(t, err, ErrLaneBillingOwnerEvidence)
	require.Nil(t, res)
	require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", stale.Owner.RequestID).Count(&n).Error)
	require.Zero(t, n)
	require.ErrorIs(t, held.ClaimDispatch(ctx, waiting.FinalWireSHA256), ErrLaneBillingOwnerEvidence)
	require.NoError(t, db.Delete(&newer).Error)

	require.NoError(t, db.Exec("UPDATE lane_store_authority_v1 SET state='revoked', epoch=epoch+1 WHERE singleton=1").Error)
	require.ErrorIs(t, held.ClaimDispatch(ctx, waiting.FinalWireSHA256), ErrLaneBillingOwnerEvidence)
	require.NoError(t, delivered.PreserveUnknown(ctx), "original already-dispatched owner still records outcome after revocation")
	m := LaneBillingMeasuredDev{RequestID: measured.Owner.RequestID, ChannelID: measured.ChannelID, Model: "fixture",
		Group: "default", PromptTokens: 3, CompletionTokens: 2, ActualQuota: 5, UsageSHA256: strings.Repeat("1", 64)}
	for range 2 {
		require.NoError(t, measuredChild.SettleMeasured(ctx, m), "settlement and exact replay use the original owner, not current release")
	}
	require.NoError(t, db.Model(&laneBillingUsageOutboxDev{}).Where("request_id = ?", m.RequestID).Count(&n).Error)
	require.EqualValues(t, 1, n)

	var waitingReceipt, sentReceipt laneBillingChildReceiptV1
	require.NoError(t, db.Where("request_id = ?", waiting.Owner.RequestID).Take(&waitingReceipt).Error)
	require.NoError(t, db.Where("request_id = ?", sent.Owner.RequestID).Take(&sentReceipt).Error)
	require.Equal(t, LaneBillingReceiptHeld, waitingReceipt.State)
	require.Equal(t, laneBillingChildUnknown, sentReceipt.State)
	walletNow, keyNow = childStoreBalances(t, db, b)
	require.Equal(t, 35, walletNow.Held)
	require.Equal(t, 35, keyNow.Held)
	require.Equal(t, 5, walletNow.Spent)
	require.Equal(t, 5, keyNow.Spent)
}
