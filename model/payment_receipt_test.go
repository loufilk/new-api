package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func paymentReceiptFixture(t *testing.T) PaymentReceipt {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&PaymentReceipt{}))
	require.NoError(t, DB.Exec("DELETE FROM agent_payment_receipts").Error)
	t.Cleanup(func() { require.NoError(t, DB.Exec("DELETE FROM agent_payment_receipts").Error) })
	return PaymentReceipt{Provider: PaymentProviderStripe, ProviderReference: "pi_synthetic_receipt", MerchantScope: "synthetic-merchant", Environment: "live", Purpose: "topup", LocalOrderKey: "synthetic-topup-order", OwnerUserID: 770001, Currency: "USD", CurrencyExponent: 2, PaidMinor: 1234, PaidAt: 100, VerifiedAt: 101}
}

func TestPaymentReceiptExcludesDoublePurposeAndOrder(t *testing.T) {
	receipt := paymentReceiptFixture(t)
	var first *PaymentReceipt
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = ClaimPaymentReceiptTx(tx, &receipt)
		return err
	}))
	for _, mutate := range []func(*PaymentReceipt){
		func(r *PaymentReceipt) { r.Purpose = "purchase"; r.LocalOrderKey = "purchase-order" },
		func(r *PaymentReceipt) { r.LocalOrderKey = "another-topup" },
		func(r *PaymentReceipt) { r.OwnerUserID++ },
		func(r *PaymentReceipt) { r.PaidMinor++ },
		func(r *PaymentReceipt) { r.Currency = "EUR" },
		func(r *PaymentReceipt) { r.CurrencyExponent = 3 },
		func(r *PaymentReceipt) { r.MerchantScope = "another-label" },
		func(r *PaymentReceipt) { r.Environment = "test" },
		func(r *PaymentReceipt) { r.ProviderReference = "pi_another-payment" },
	} {
		changed := receipt
		mutate(&changed)
		err := DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimPaymentReceiptTx(tx, &changed); return err })
		assert.ErrorIs(t, err, ErrPaymentReceiptConflict)
	}
	receipt.VerifiedAt = 102
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		replay, err := ClaimPaymentReceiptTx(tx, &receipt)
		if err == nil {
			assert.Equal(t, first.ID, replay.ID)
			assert.EqualValues(t, 101, replay.VerifiedAt)
		}
		return err
	}))
	require.ErrorIs(t, DB.Model(first).Update("paid_minor", 999).Error, ErrLedgerImmutable)
	require.ErrorIs(t, DB.Delete(first).Error, ErrLedgerImmutable)
	var count int64
	require.NoError(t, DB.Model(&PaymentReceipt{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestPaymentReceiptRollsBackWithPrincipalFailure(t *testing.T) {
	receipt := paymentReceiptFixture(t)
	injected := errors.New("synthetic principal failure")
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := ClaimPaymentReceiptTx(tx, &receipt)
		if err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)
	var count int64
	require.NoError(t, DB.Model(&PaymentReceipt{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimPaymentReceiptTx(tx, &receipt); return err }))
}

func TestPaymentReceiptConcurrentOrderClaimsHaveOneWinner(t *testing.T) {
	receipt := paymentReceiptFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, order := range []string{"order-one", "order-two"} {
		group.Go(func() {
			candidate := receipt
			candidate.LocalOrderKey = order
			<-start
			results <- DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimPaymentReceiptTx(tx, &candidate); return err })
		})
	}
	close(start)
	group.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrPaymentReceiptConflict)
			conflict++
		}
	}
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, conflict)
}

func TestPaymentReceiptRejectsAmbiguousEvidence(t *testing.T) {
	receipt := paymentReceiptFixture(t)
	for _, mutate := range []func(*PaymentReceipt){
		func(r *PaymentReceipt) { r.ProviderReference = "cs_checkout_alias" },
		func(r *PaymentReceipt) { r.Provider = "manual" },
		func(r *PaymentReceipt) { r.PaidMinor = 0 },
		func(r *PaymentReceipt) { r.PaidMinor = int64(common.MaxWalletQuota) + 1 },
		func(r *PaymentReceipt) { r.PaidAt = 0 },
		func(r *PaymentReceipt) { r.PaidAt = r.VerifiedAt + 1 },
		func(r *PaymentReceipt) { r.ProviderReference = "pi_bad\nreference" },
		func(r *PaymentReceipt) { r.Currency = "usd" },
	} {
		candidate := receipt
		mutate(&candidate)
		assert.ErrorIs(t, ValidatePaymentReceipt(&candidate), ErrPaymentReceiptConflict)
	}
}

func TestPaymentReceiptHistoricalOtherPurposeCannotBecomeWalletCredit(t *testing.T) {
	receipt := paymentReceiptFixture(t)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimPaymentReceiptTx(tx, &receipt); return err }))
	// Simulate retained immutable evidence written by the previous product. The
	// current application has no write API for that purpose.
	require.NoError(t, DB.Exec("UPDATE agent_payment_receipts SET purpose = ? WHERE local_order_key = ?", "purchase", receipt.LocalOrderKey).Error)
	err := DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimPaymentReceiptTx(tx, &receipt); return err })
	require.ErrorIs(t, err, ErrPaymentReceiptConflict)
	var stored PaymentReceipt
	require.NoError(t, DB.Where("local_order_key = ?", receipt.LocalOrderKey).First(&stored).Error)
	assert.Equal(t, "purchase", stored.Purpose)
	assert.Equal(t, receipt.PaidMinor, stored.PaidMinor)
}

func TestPaymentReceiptSharedWithA07UsesOneImmutableRegistry(t *testing.T) {
	receipt := paymentReceiptFixture(t)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := ClaimPaymentReceiptTx(tx, &receipt)
		return err
	}))
	a07 := receipt
	a07.Purpose, a07.LocalOrderKey = "payment_code", "a07-same-provider-payment"
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := ClaimAgentPaymentReceiptTx(tx, &a07)
		return err
	})
	require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict)
	var count int64
	require.NoError(t, DB.Model(&AgentPaymentReceipt{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	a07.ProviderReference = "pi_synthetic_separate_a07_payment"
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := ClaimAgentPaymentReceiptTx(tx, &a07)
		return err
	}))
	ordinary := a07
	ordinary.Purpose, ordinary.LocalOrderKey = "topup", "ordinary-reuse-a07"
	err = DB.Transaction(func(tx *gorm.DB) error {
		_, err := ClaimPaymentReceiptTx(tx, &ordinary)
		return err
	})
	require.ErrorIs(t, err, ErrPaymentReceiptConflict)
	require.NoError(t, DB.Model(&PaymentReceipt{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}
