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

func agentReceiptFixture(t *testing.T) AgentPaymentReceipt {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&AgentPaymentReceipt{}))
	require.NoError(t, DB.Exec("DELETE FROM agent_payment_receipts").Error)
	t.Cleanup(func() { require.NoError(t, DB.Exec("DELETE FROM agent_payment_receipts").Error) })
	return AgentPaymentReceipt{Provider: PaymentProviderStripe, ProviderReference: "pi_synthetic_receipt", MerchantScope: "synthetic-merchant", Environment: "live", Purpose: "topup", LocalOrderKey: "synthetic-topup-order", OwnerUserID: 770001, Currency: "USD", CurrencyExponent: 2, PaidMinor: 1234, PaidAt: 100, VerifiedAt: 101}
}

func TestAgentPaymentReceiptExcludesDoublePurposeAndOrder(t *testing.T) {
	receipt := agentReceiptFixture(t)
	var first *AgentPaymentReceipt
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = ClaimAgentPaymentReceiptTx(tx, &receipt)
		return err
	}))
	for _, mutate := range []func(*AgentPaymentReceipt){
		func(r *AgentPaymentReceipt) { r.Purpose = "purchase"; r.LocalOrderKey = "purchase-order" },
		func(r *AgentPaymentReceipt) { r.LocalOrderKey = "another-topup" },
		func(r *AgentPaymentReceipt) { r.OwnerUserID++ },
		func(r *AgentPaymentReceipt) { r.PaidMinor++ },
		func(r *AgentPaymentReceipt) { r.Currency = "EUR" },
		func(r *AgentPaymentReceipt) { r.CurrencyExponent = 3 },
		func(r *AgentPaymentReceipt) { r.MerchantScope = "another-label" },
		func(r *AgentPaymentReceipt) { r.Environment = "test" },
		func(r *AgentPaymentReceipt) { r.ProviderReference = "pi_another-payment" },
	} {
		changed := receipt
		mutate(&changed)
		err := DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentPaymentReceiptTx(tx, &changed); return err })
		assert.ErrorIs(t, err, ErrAgentPaymentReceiptConflict)
	}
	receipt.VerifiedAt = 102
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		replay, err := ClaimAgentPaymentReceiptTx(tx, &receipt)
		if err == nil {
			assert.Equal(t, first.ID, replay.ID)
			assert.EqualValues(t, 101, replay.VerifiedAt)
		}
		return err
	}))
	require.ErrorIs(t, DB.Model(first).Update("paid_minor", 999).Error, ErrAgentLedgerImmutable)
	require.ErrorIs(t, DB.Delete(first).Error, ErrAgentLedgerImmutable)
	var count int64
	require.NoError(t, DB.Model(&AgentPaymentReceipt{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestAgentPaymentReceiptRollsBackWithPrincipalFailure(t *testing.T) {
	receipt := agentReceiptFixture(t)
	injected := errors.New("synthetic principal failure")
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := ClaimAgentPaymentReceiptTx(tx, &receipt)
		if err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)
	var count int64
	require.NoError(t, DB.Model(&AgentPaymentReceipt{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentPaymentReceiptTx(tx, &receipt); return err }))
}

func TestAgentPaymentReceiptConcurrentPurposeClaimsHaveOneWinner(t *testing.T) {
	receipt := agentReceiptFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, purpose := range []string{"topup", "purchase"} {
		group.Go(func() {
			candidate := receipt
			candidate.Purpose = purpose
			candidate.LocalOrderKey = purpose + "-synthetic"
			<-start
			results <- DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentPaymentReceiptTx(tx, &candidate); return err })
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
			require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict)
			conflict++
		}
	}
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, conflict)
}

func TestAgentPaymentReceiptRejectsAmbiguousEvidence(t *testing.T) {
	receipt := agentReceiptFixture(t)
	for _, mutate := range []func(*AgentPaymentReceipt){
		func(r *AgentPaymentReceipt) { r.ProviderReference = "cs_checkout_alias" },
		func(r *AgentPaymentReceipt) { r.Provider = "manual" },
		func(r *AgentPaymentReceipt) { r.PaidMinor = 0 },
		func(r *AgentPaymentReceipt) { r.PaidMinor = int64(common.MaxWalletQuota) + 1 },
		func(r *AgentPaymentReceipt) { r.PaidAt = 0 },
		func(r *AgentPaymentReceipt) { r.PaidAt = r.VerifiedAt + 1 },
		func(r *AgentPaymentReceipt) { r.ProviderReference = "pi_bad\nreference" },
		func(r *AgentPaymentReceipt) { r.Currency = "usd" },
	} {
		candidate := receipt
		mutate(&candidate)
		assert.ErrorIs(t, ValidateAgentPaymentReceipt(&candidate), ErrAgentPaymentReceiptConflict)
	}
}
