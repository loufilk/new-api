//go:build lane_billing_integration_dev

package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func laneWalletHeadroomFixture(t *testing.T, held int) (*gorm.DB, int, int64, string) {
	t.Helper()
	db, _, _ := childStoreFixture(t)
	user := User{Id: 441, Username: "synthetic-headroom", AffCode: "headroom-441", Status: common.UserStatusEnabled,
		Quota: common.MaxWalletQuota - 100, QuotaPaid: common.MaxWalletQuota - 100}
	const epoch = int64(2)
	require.NoError(t, db.Create(&user).Error)
	_, _, err := ApplySharedWalletTransfer(t.Context(), db, SharedWalletTransfer{UserID: user.Id, Epoch: epoch,
		Slot: 4, OperationID: "fixture-headroom-grant", Direction: "grant", Amount: 80})
	require.NoError(t, err)
	if held != 0 {
		require.NoError(t, db.Model(&SharedWalletShare{}).Where("user_id = ?", user.Id).
			Updates(map[string]any{"available": 80 - held, "held": held}).Error)
	}
	var schema string
	require.NoError(t, db.Raw("SELECT current_schema()").Scan(&schema).Error)
	return db, user.Id, epoch, schema
}

func laneHeadroomApproval(t *testing.T, tx *gorm.DB, schema string, epoch int64, entry LedgerEntry) *laneNetCreditApproval {
	t.Helper()
	key, err := entry.resolveKey(ledgerOpCredit)
	require.NoError(t, err)
	return &laneNetCreditApproval{
		scope:     laneWalletCustodyScope{tx: tx, schema: schema, userID: entry.UserId, walletEpoch: epoch},
		ledgerKey: key, signature: ledgerOperationSignature(entry, ledgerOpCredit),
	}
}

func TestLaneWalletHeadroomIncludesHeldAndSeparatesNeutralReturnPostgres(t *testing.T) {
	for _, held := range []int{0, 47} {
		t.Run(fmt.Sprintf("held_%d", held), func(t *testing.T) {
			db, user, epoch, schema := laneWalletHeadroomFixture(t, held)
			preflight := func(n int) error {
				return db.Transaction(func(tx *gorm.DB) error {
					return validateTopUpQuotaCapacity(tx, user, n, &laneWalletCustodyScope{tx, schema, user, epoch})
				})
			}
			require.NoError(t, preflight(100))
			require.ErrorIs(t, preflight(101), ErrTopUpQuotaLimitExceeded)
			require.ErrorIs(t, preflight(150), ErrTopUpQuotaLimitExceeded)
			// Nil preserves current ordinary raw semantics. It is intentionally
			// not a routing choice for a wallet under actual custody.
			require.NoError(t, validateTopUpQuotaCapacity(db, user, 150, nil))
			net := LedgerEntry{UserId: user, Account: AccountPaid, Delta: 150, Reason: "wallet_share_reclaim",
				IdempotencyKey: "net-not-a-neutral-return"}
			credit := func(e LedgerEntry) (*LedgerResult, error) {
				var result *LedgerResult
				err := db.Transaction(func(tx *gorm.DB) error {
					var err error
					result, err = creditQuotaTx(tx, e, laneHeadroomApproval(t, tx, schema, epoch, e))
					return err
				})
				return result, err
			}
			_, err := credit(net)
			require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded, "a reason string does not make new value a neutral reclaim")
			var n int64
			require.NoError(t, db.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", net.IdempotencyKey).Count(&n).Error)
			require.Zero(t, n)
			net.Delta, net.Reason = 100, LedgerReasonAdminAdd
			result, err := credit(net)
			require.NoError(t, err)
			require.Equal(t, common.MaxWalletQuota-80, result.TotalAfter, "credit ledger receipts still use raw mirrors")
			replay, err := credit(net)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, result.Rows, replay.Rows)
			require.ErrorIs(t, preflight(1), ErrTopUpQuotaLimitExceeded)
			net.Delta++
			_, err = credit(net)
			require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)
			net.Delta--
			require.NoError(t, db.Exec("DELETE FROM balance_ledger WHERE id = ?", result.Rows[0].Id).Error)
			_, err = credit(net)
			require.ErrorIs(t, err, ErrSharedWalletEvidence)
			// Verified neutral return frees idle only, even with zero true
			// headroom and a live held balance. Combined wallet remains M.
			returned, already, err := ApplySharedWalletTransfer(t.Context(), db, SharedWalletTransfer{UserID: user, Epoch: epoch,
				Slot: 4, OperationID: "fixture-headroom-return", Direction: "reclaim", Amount: 80 - held})
			require.NoError(t, err)
			require.False(t, already)
			require.Equal(t, 80-held, returned.ReturnedAfter)
			var raw User
			require.NoError(t, db.First(&raw, user).Error)
			require.Equal(t, common.MaxWalletQuota-held, raw.Quota)
			require.ErrorIs(t, preflight(1), ErrTopUpQuotaLimitExceeded)
		})
	}
}

func TestLaneWalletHeadroomVerifiesScopeAndLatestReceiptPostgres(t *testing.T) {
	db, user, epoch, schema := laneWalletHeadroomFixture(t, 47)
	entry := LedgerEntry{UserId: user, Account: AccountPaid, Delta: 1, Reason: LedgerReasonAdminAdd, IdempotencyKey: "proof"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		approval := laneHeadroomApproval(t, tx, schema, epoch, entry)
		_, err := creditQuotaTx(db, entry, approval)
		require.ErrorIs(t, err, ErrSharedWalletScope)
		changed := entry
		changed.Delta++
		_, err = creditQuotaTx(tx, changed, approval)
		require.ErrorIs(t, err, ErrSharedWalletScope)
		return nil
	}))
	var share SharedWalletShare
	require.NoError(t, db.Where("user_id = ?", user).Take(&share).Error)
	require.NoError(t, db.Create(&SharedWalletTransfer{UserID: user, Epoch: epoch, Slot: share.Slot,
		OperationID: "orphan-headroom-tip", Direction: "grant", Amount: 1, ShareVersion: share.Version + 1}).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := creditQuotaTx(tx, entry, laneHeadroomApproval(t, tx, schema, epoch, entry))
		return err
	}), ErrSharedWalletEvidence)
}

func TestLaneWalletHeadroomRechecksSyntheticVerifiedEpayPaymentPostgres(t *testing.T) {
	db, user, epoch, schema := laneWalletHeadroomFixture(t, 24)
	require.NoError(t, db.AutoMigrate(&TopUp{}, &PaymentReceipt{}, &AgentCommissionOutbox{}, &SubscriptionOrder{}))
	const orderKey = "SYNTHETIC_HEADROOM_ORDER"
	order := TopUp{UserId: user, Amount: 1, Money: 1, TradeNo: orderKey, PaymentMethod: "alipay",
		PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending, QuotedWalletQuota: 80,
		QuotedPaymentMinor: 100, QuotedPaymentCurrency: "CNY", QuotedPaymentScope: "SYNTHETIC", QuotedPaymentEnvironment: "test"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := validateTopUpQuotaCapacity(tx, user, order.QuotedWalletQuota, &laneWalletCustodyScope{tx, schema, user, epoch}); err != nil {
			return err
		}
		return tx.Create(&order).Error
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		entry := LedgerEntry{UserId: user, Account: AccountPaid, Delta: 25, Reason: LedgerReasonAdminAdd, IdempotencyKey: "uses-headroom-before-capture"}
		_, err := creditQuotaTx(tx, entry, laneHeadroomApproval(t, tx, schema, epoch, entry))
		return err
	}))
	facts, ok := NewVerifiedPaymentFacts(PaymentProviderEpay, "SYNTHETIC_CAPTURE", "SYNTHETIC", "test", "CNY", "1.00", false)
	require.True(t, ok)
	postVerified := func(f VerifiedPaymentFacts) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var current TopUp
			if err := lockForUpdate(tx).First(&current, order.Id).Error; err != nil {
				return err
			}
			if err := current.validateQuotedPayment([]VerifiedPaymentFacts{f}); err != nil {
				return err
			}
			if err := applyVerifiedPaymentTx(tx, &current, []VerifiedPaymentFacts{f}); err != nil {
				return err
			}
			current.CompleteTime, current.Status = common.GetTimestamp(), common.TopUpStatusSuccess
			if err := tx.Save(&current).Error; err != nil {
				return err
			}
			entry := LedgerEntry{UserId: current.UserId, Account: AccountPaid, Delta: current.QuotedWalletQuota, Reason: LedgerReasonTopup,
				ReferenceType: ReferralReferenceTopUp, ReferenceId: current.TradeNo}
			_, err := creditTopUpQuotaWithHeadroom(tx, entry, laneHeadroomApproval(t, tx, schema, epoch, entry))
			return err
		})
	}
	changed := facts
	changed.PaidMinor++
	require.ErrorIs(t, postVerified(changed), ErrPaymentAmountMismatch)
	require.ErrorIs(t, postVerified(facts), ErrTopUpQuotaLimitExceeded)
	var blocked TopUp
	require.NoError(t, db.First(&blocked, order.Id).Error)
	require.Equal(t, common.TopUpStatusPending, blocked.Status)
	require.Zero(t, blocked.PaymentReceiptID)
	// This isolated splice is not captured-pending handling. Existing
	// callback semantics roll back the new receipt, so leave the live handler
	// unwired until the verified captured liability has durable disposition.
	var n int64
	require.NoError(t, db.Model(&PaymentReceipt{}).Where("local_order_key = ?", orderKey).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := DebitQuotaTx(tx, LedgerEntry{UserId: user, Delta: 5, Reason: LedgerReasonAdminSubtract, IdempotencyKey: "releases-headroom"}, AccountPaid)
		return err
	}))
	require.NoError(t, postVerified(facts))
	require.NoError(t, postVerified(facts))
	var final User
	require.NoError(t, db.First(&final, user).Error)
	require.Equal(t, common.MaxWalletQuota-80, final.Quota)
	require.NoError(t, db.Model(&PaymentReceipt{}).Where("local_order_key = ?", orderKey).Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, db.Model(&AgentCommissionOutbox{}).Where("reference_id = ?", orderKey).Count(&n).Error)
	require.EqualValues(t, 1, n)
}
