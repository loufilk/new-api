package model

import (
	"context"
	"errors"
	"math"
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func invoiceFeeFixture(t *testing.T, trade string) *InvoiceApplication {
	t.Helper()
	setupCommerceTest(t)
	oldPrice, oldUnit, oldMethods := operation_setting.Price, common.QuotaPerUnit, operation_setting.PayMethods
	operation_setting.Price, common.QuotaPerUnit = 1, 1000
	operation_setting.PayMethods = []map[string]string{{"name": "WeChat Pay", "type": "wxpay"}}
	t.Cleanup(func() {
		operation_setting.Price, common.QuotaPerUnit, operation_setting.PayMethods = oldPrice, oldUnit, oldMethods
	})
	config, err := GetCommerceConfig(t.Context())
	require.NoError(t, err)
	config.GeneralFeeBPS = 500
	_, err = SaveCommerceConfig(t.Context(), commerceAdmin, *config)
	require.NoError(t, err)
	order := seedCommerceOrder(t, commerceAlice, trade, 10000)
	invoice, err := CreateInvoiceApplication(t.Context(), commerceAlice, "create-"+trade, invoiceRequest(order.Id))
	require.NoError(t, err)
	require.EqualValues(t, 500, invoice.FeeMinor)
	return invoice
}

func TestCommercePostgresInvoiceFeeWalletConcurrencyReplayAndOriginalRefund(t *testing.T) {
	invoice := invoiceFeeFixture(t, "fee-wallet")
	input := InvoiceFeePayRequest{Method: "wallet", ExpectedRevision: invoice.Revision, ExpectedWalletQuota: "5000"}
	start := make(chan struct{})
	results := make(chan *InvoiceFeePayment, 3)
	failures := make(chan error, 3)
	var workers sync.WaitGroup
	for i := range 3 {
		workers.Go(func() {
			<-start
			_, payment, err := StartInvoiceFeePayment(context.Background(), commerceAlice, invoice.ID, "wallet-click-"+strconv.Itoa(i), input, "")
			results <- payment
			failures <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var paymentID int64
	for payment := range results {
		require.NotNil(t, payment)
		if paymentID != 0 {
			assert.Equal(t, paymentID, payment.ID)
		}
		paymentID = payment.ID
	}
	paid, bonus, reward, err := GetUserAccountBalances(commerceAlice)
	require.NoError(t, err)
	assert.Equal(t, 5000, paid)
	assert.Zero(t, bonus)
	assert.Zero(t, reward)
	invoice, err = GetInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "submitted", invoice.Status)
	require.NotNil(t, invoice.FeePayment)
	assert.Equal(t, "1", invoice.FeePayment.WalletPrice)
	assert.Equal(t, "1000", invoice.FeePayment.QuotaPerUnit)
	trade := invoice.FeePayment.TradeNo
	// Refund the frozen original debit even when purchase price changes later.
	operation_setting.Price = 9
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false, CommerceReview{ExpectedRevision: invoice.Revision, Action: "cancel"})
	require.NoError(t, err)
	assert.Equal(t, "refunded", invoice.FeeRefundStatus)
	assert.Equal(t, "refunded", invoice.FeePayment.Status)
	assert.Equal(t, "wallet:"+trade, invoice.FeeRefundReference)
	paid, _, _, err = GetUserAccountBalances(commerceAlice)
	require.NoError(t, err)
	assert.Equal(t, 10000, paid)
	_, _, err = StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "wallet-again", input, "")
	require.ErrorIs(t, err, ErrCommerceConflict)
	var ledger []BalanceLedger
	require.NoError(t, DB.Where("reference_type = ? AND reference_id = ?", "invoice_fee", trade).Order("id").Find(&ledger).Error)
	require.Len(t, ledger, 2)
	assert.Equal(t, -5000, ledger[0].Delta)
	assert.Equal(t, 5000, ledger[1].Delta)
	for _, row := range ledger {
		assert.Equal(t, string(AccountPaid), row.Account)
	}
}

func TestCommercePostgresInvoiceFeeQuoteChangesOwnershipAndInsufficientPaid(t *testing.T) {
	invoice := invoiceFeeFixture(t, "fee-boundaries")
	input := InvoiceFeePayRequest{Method: "wallet", ExpectedRevision: invoice.Revision, ExpectedWalletQuota: "5000"}
	_, _, err := StartInvoiceFeePayment(t.Context(), commerceBob, invoice.ID, "other-owner", input, "")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	operation_setting.Price = 2
	_, _, err = StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "changed-quote", input, "")
	require.ErrorIs(t, err, ErrInvoicePaymentChanged)
	operation_setting.Price = 1
	// Large reward balance must not fund a paid-wallet fee.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", commerceAlice).Updates(map[string]any{"quota_paid": 1, "quota_reward": 10000, "quota": 10001}).Error)
	_, _, err = StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "insufficient-paid", input, "")
	require.ErrorIs(t, err, ErrInvoiceWalletInsufficient)
	var count int64
	require.NoError(t, DB.Model(&InvoiceFeePayment{}).Where("invoice_id = ?", invoice.ID).Count(&count).Error)
	assert.Zero(t, count)
	current, err := GetInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "awaiting_fee", current.Status)
	paid, _, reward, err := GetUserAccountBalances(commerceAlice)
	require.NoError(t, err)
	assert.Equal(t, 1, paid)
	assert.Equal(t, 10000, reward)
	var reservation InvoiceReservation
	require.NoError(t, DB.Where("invoice_id = ?", invoice.ID).First(&reservation).Error)
}

func TestCommercePostgresInvoiceFeeAuditFailureRollsBackDebitAndRefund(t *testing.T) {
	invoice := invoiceFeeFixture(t, "fee-atomic")
	injected := errors.New("synthetic invoice audit failure")
	blockedAction := "fee_paid"
	callback := "test:invoice_fee_audit"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if audit, ok := tx.Statement.Dest.(*CommerceAudit); ok && audit.Action == blockedAction {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { assert.NoError(t, DB.Callback().Create().Remove(callback)) })
	input := InvoiceFeePayRequest{Method: "wallet", ExpectedRevision: invoice.Revision, ExpectedWalletQuota: "5000"}
	_, _, err := StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "fee-atomic-pay", input, "")
	require.ErrorIs(t, err, injected)
	paid, _, _, err := GetUserAccountBalances(commerceAlice)
	require.NoError(t, err)
	assert.Equal(t, 10000, paid)
	var count int64
	require.NoError(t, DB.Model(&InvoiceFeePayment{}).Where("invoice_id = ?", invoice.ID).Count(&count).Error)
	assert.Zero(t, count)
	blockedAction = "cancel"
	invoice, _, err = StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "fee-atomic-pay", input, "")
	require.NoError(t, err)
	_, err = ReviewInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false, CommerceReview{ExpectedRevision: invoice.Revision, Action: "cancel"})
	require.ErrorIs(t, err, injected)
	paid, _, _, err = GetUserAccountBalances(commerceAlice)
	require.NoError(t, err)
	assert.Equal(t, 5000, paid)
	current, err := GetInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "submitted", current.Status)
	assert.Equal(t, "paid", current.FeePayment.Status)
	assert.Empty(t, current.FeeRefundStatus)
}

func TestCommercePostgresInvoiceFeeEpayRejectsMismatchesAndTracksLateRefund(t *testing.T) {
	invoice := invoiceFeeFixture(t, "fee-epay-late")
	input := InvoiceFeePayRequest{Method: "wxpay", ExpectedRevision: invoice.Revision}
	invoice, payment, err := StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "online-payment", input, "merchant-1")
	require.NoError(t, err)
	assert.Equal(t, "awaiting_fee", invoice.Status)
	_, _, err = StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "switch-wallet", InvoiceFeePayRequest{Method: "wallet", ExpectedRevision: invoice.Revision, ExpectedWalletQuota: "5000"}, "")
	require.ErrorIs(t, err, ErrInvoicePaymentActive)
	_, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{ExpectedRevision: invoice.Revision, Action: "confirm_fee", FeeReceiptReference: "manual-double-payment"})
	require.ErrorIs(t, err, ErrInvoicePaymentActive)
	facts := VerifiedPaymentFacts{Provider: PaymentProviderEpay, ExternalReference: "epay-late-real-receipt", MerchantScope: "merchant-1", Environment: "live", Currency: "CNY", CurrencyExponent: 2, PaidMinor: 500}
	for _, mutate := range []func(*VerifiedPaymentFacts){
		func(f *VerifiedPaymentFacts) { f.PaidMinor++ },
		func(f *VerifiedPaymentFacts) { f.MerchantScope = "different-merchant" },
		func(f *VerifiedPaymentFacts) { f.Currency = "USD" },
		func(f *VerifiedPaymentFacts) { f.Environment = "test" },
	} {
		changed := facts
		mutate(&changed)
		require.ErrorIs(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", changed), ErrPaymentReceiptConflict)
	}
	require.ErrorIs(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "alipay", facts), ErrPaymentReceiptConflict)
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false, CommerceReview{ExpectedRevision: invoice.Revision, Action: "cancel"})
	require.NoError(t, err)
	assert.Empty(t, invoice.FeeRefundStatus)
	require.NoError(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", facts))
	require.NoError(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", facts))
	invoice, err = GetInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", invoice.Status)
	assert.Equal(t, "pending", invoice.FeeRefundStatus)
	assert.Equal(t, "paid", invoice.FeePayment.Status)
	assert.Equal(t, "pending", invoice.FeePayment.RefundStatus)
	paid, _, _, err := GetUserAccountBalances(commerceAlice)
	require.NoError(t, err)
	assert.Equal(t, 10000, paid, "external fee payment must never credit the wallet")
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{ExpectedRevision: invoice.Revision, Action: "record_fee_refund", FeeRefundReference: "manual-provider-refund-1"})
	require.NoError(t, err)
	assert.Equal(t, "refunded", invoice.FeePayment.Status)
	assert.Equal(t, "refunded", invoice.FeePayment.RefundStatus)
	require.NoError(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", facts))
	var receipts int64
	require.NoError(t, DB.Model(&PaymentReceipt{}).Where("provider_reference = ?", facts.ExternalReference).Count(&receipts).Error)
	assert.EqualValues(t, 1, receipts)
}

func TestCommercePostgresInvoiceFeeEpayEvidenceCannotCreditTopUpOrSecondInvoice(t *testing.T) {
	invoice := invoiceFeeFixture(t, "fee-evidence")
	invoice, payment, err := StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "evidence-payment", InvoiceFeePayRequest{Method: "wxpay", ExpectedRevision: invoice.Revision}, "merchant-1")
	require.NoError(t, err)
	facts := VerifiedPaymentFacts{Provider: PaymentProviderEpay, ExternalReference: "fee-evidence-reference", MerchantScope: "merchant-1", Environment: "live", Currency: "CNY", CurrencyExponent: 2, PaidMinor: 500}
	require.NoError(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", facts))
	var receipt PaymentReceipt
	require.NoError(t, DB.Where("provider_reference = ?", facts.ExternalReference).First(&receipt).Error)
	assert.Equal(t, "invoice_fee", receipt.Purpose)
	// A valid verified receipt may have only one financial purpose globally.
	receipt.Purpose, receipt.LocalOrderKey = "topup", "unrelated-wallet-credit"
	err = DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimPaymentReceiptTx(tx, &receipt); return err })
	require.ErrorIs(t, err, ErrPaymentReceiptConflict)
	order2 := seedCommerceOrder(t, commerceAlice, "fee-evidence-second", 10000)
	second, err := CreateInvoiceApplication(t.Context(), commerceAlice, "fee-evidence-second", invoiceRequest(order2.Id))
	require.NoError(t, err)
	_, _, err = StartInvoiceFeePayment(t.Context(), commerceAlice, second.ID, "evidence-payment", InvoiceFeePayRequest{Method: "wxpay", ExpectedRevision: second.Revision}, "merchant-1")
	require.ErrorIs(t, err, ErrCommerceConflict)
	_, secondPayment, err := StartInvoiceFeePayment(t.Context(), commerceAlice, second.ID, "evidence-payment-two", InvoiceFeePayRequest{Method: "wxpay", ExpectedRevision: second.Revision}, "merchant-1")
	require.NoError(t, err)
	require.ErrorIs(t, SettleInvoiceEpayFee(t.Context(), secondPayment.TradeNo, "wxpay", facts), ErrPaymentReceiptConflict)
	current, err := GetInvoiceApplication(t.Context(), commerceAlice, second.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "awaiting_fee", current.Status)
	// Rerunning the affected migrations preserves the quote and unique payment.
	for range 2 {
		require.NoError(t, DB.AutoMigrate(CommerceModels()...))
	}
	var retained InvoiceFeePayment
	require.NoError(t, DB.First(&retained, payment.ID).Error)
	assert.Equal(t, payment.TradeNo, retained.TradeNo)
	assert.Equal(t, "paid", retained.Status)
	duplicate := retained
	duplicate.ID, duplicate.RequestKey, duplicate.TradeNo = 0, "duplicate-fee-record", "IFDUPLICATE"
	require.Error(t, DB.Create(&duplicate).Error)
}

func TestCommercePostgresInvoiceFeeQuoteUsesConfiguredPriceAndStrictBounds(t *testing.T) {
	invoiceFeeFixture(t, "fee-quote")
	oldDisplay := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 7.3
	t.Cleanup(func() { operation_setting.USDExchangeRate = oldDisplay })
	quota, price, unit, err := InvoiceWalletFeeQuote("CNY", 2, 123)
	require.NoError(t, err)
	assert.Equal(t, 1230, quota)
	assert.Equal(t, "1", price)
	assert.Equal(t, "1000", unit)
	operation_setting.Price = 3
	quota, _, _, err = InvoiceWalletFeeQuote("CNY", 2, 1)
	require.NoError(t, err)
	assert.Equal(t, 4, quota, "round up the sub-quota fraction")
	quota, _, _, err = InvoiceWalletFeeQuote("USD", 2, 100)
	require.NoError(t, err)
	assert.Equal(t, 1000, quota)
	_, _, _, err = InvoiceWalletFeeQuote("EUR", 2, 100)
	require.ErrorIs(t, err, ErrInvoiceWalletUnavailable)
	for _, minor := range []int64{-1, commerceMaxMinor + 1} {
		_, _, _, err = InvoiceWalletFeeQuote("CNY", 2, minor)
		require.ErrorIs(t, err, ErrCommerceInvalid)
	}
	for _, invalid := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		operation_setting.Price = invalid
		_, _, _, err = InvoiceWalletFeeQuote("CNY", 2, 100)
		require.ErrorIs(t, err, ErrInvoiceWalletUnavailable)
	}
}
