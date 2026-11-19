package model

import (
	"bytes"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomerInvoiceEpayFeeLifecyclePostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	const method, gateway = "wxpay", "https://provider.example.test/"
	p := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant, EpayFeeAvailable: true}
	root, owner, other := customerInvoiceTestUser(t, common.RoleRootUser), customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleCommonUser)
	before := getUserBalances(t, owner)
	cfg := defaultCustomerInvoiceSettings()
	cfg.Ordinary = CustomerInvoiceTypeSettings{Enabled: true, FeeEnabled: true, FeeRateBPS: 100, TaxRateBPS: 600}
	cfg, err := UpdateCustomerInvoiceSettings(ctx, root, cfg)
	require.NoError(t, err)
	first, second := customerInvoiceTestOrder(t, owner, "epay-fee-line-a", 25, nil), customerInvoiceTestOrder(t, owner, "epay-fee-line-b", 25, nil)
	submit := customerInvoiceTestInput("epay-fixture-fee", first.Id, second.Id)
	submit.SettingsVersion = &cfg.Version
	app, err := SubmitCustomerInvoice(ctx, owner, p, submit)
	require.NoError(t, err)
	assert.Equal(t, "awaiting_payment", app.Status)
	assert.Equal(t, "unpaid", app.FeeState)
	assert.Equal(t, "1", app.FeeMinor)
	assert.Equal(t, "50", app.AmountMinor)
	_, err = ClaimCustomerInvoice(ctx, root, p, app.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	totals, err := SummarizeCustomerInvoices(ctx, owner, p)
	require.NoError(t, err)
	assert.Equal(t, "50", totals.PendingMinor)
	assert.Equal(t, "0", totals.AvailableMinor)
	// Unpinned offers can be cancelled. Once a real Epay order has been created
	// an asynchronous payment might still arrive, so never release/replace it.
	closed, err := CancelCustomerInvoice(ctx, owner, app.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", closed.Status)
	submit.IdempotencyKey = "epay-fixture-reapply"
	app, err = SubmitCustomerInvoice(ctx, owner, p, submit)
	require.NoError(t, err)
	_, err = BeginCustomerInvoiceEpay(ctx, other, app.ID, invoiceTestMerchant, method, gateway)
	require.ErrorIs(t, err, ErrCustomerInvoiceNotFound)
	order, err := BeginCustomerInvoiceEpay(ctx, owner, app.ID, invoiceTestMerchant, method, gateway)
	require.NoError(t, err)
	assert.EqualValues(t, 1, order.FeeMinor)
	require.Regexp(t, `^CIF[0-9a-f]{29}$`, order.TradeNo)
	resume, err := BeginCustomerInvoiceEpay(ctx, owner, app.ID, invoiceTestMerchant, method, gateway)
	require.NoError(t, err)
	assert.Equal(t, *order, *resume)
	for _, args := range [][3]string{{invoiceTestMerchant, "alipay", gateway}, {"new-merchant", method, gateway}, {invoiceTestMerchant, method, "https://another.example.test/"}} {
		_, err = BeginCustomerInvoiceEpay(ctx, owner, app.ID, args[0], args[1], args[2])
		require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	}
	_, err = CancelCustomerInvoice(ctx, owner, app.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	_, err = RejectCustomerInvoice(ctx, root, app.ID, "abandoned cart is not cancellation proof")
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	f := VerifiedPaymentFacts{Provider: PaymentProviderEpay, ExternalReference: "independent-fee-provider-txn", MerchantScope: invoiceTestMerchant, Environment: "live", Currency: "CNY", CurrencyExponent: 2, PaidMinor: 1}
	for _, change := range []func(*VerifiedPaymentFacts){func(f *VerifiedPaymentFacts) { f.PaidMinor = 2 }, func(f *VerifiedPaymentFacts) { f.Currency = "USD" },
		func(f *VerifiedPaymentFacts) { f.Environment = "test" }, func(f *VerifiedPaymentFacts) { f.MerchantScope = "other" }, func(f *VerifiedPaymentFacts) { f.ExternalReference = first.PaidProviderReference }} {
		bad := f
		change(&bad)
		require.Error(t, SettleCustomerInvoiceEpay(ctx, order.TradeNo, method, bad, gateway))
	}
	require.Error(t, SettleCustomerInvoiceEpay(ctx, order.TradeNo, "alipay", f, gateway))
	require.Error(t, SettleCustomerInvoiceEpay(ctx, order.TradeNo, method, f, "https://another.example.test/"))
	var count int64
	require.NoError(t, db.Model(&PaymentReceipt{}).Where("purpose='invoice_fee'").Count(&count).Error)
	assert.Zero(t, count)
	start, result := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() { <-start; result <- SettleCustomerInvoiceEpay(ctx, order.TradeNo, method, f, gateway) }()
	}
	close(start)
	require.NoError(t, <-result)
	require.NoError(t, <-result)
	require.NoError(t, db.Model(&PaymentReceipt{}).Where("purpose='invoice_fee'").Count(&count).Error)
	assert.EqualValues(t, 1, count)
	paid, err := GetCustomerInvoice(ctx, owner, false, app.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", paid.Status)
	assert.Equal(t, "paid", paid.FeeState)
	assert.Equal(t, "epay", paid.FeeRail)
	assert.Equal(t, "1", paid.FeePaidMinor)
	assert.Positive(t, paid.FeePaidAt)
	assert.Empty(t, paid.FeeTradeNo)
	assert.Empty(t, paid.FeeProviderReference)
	claim, err := ClaimCustomerInvoice(ctx, root, p, app.ID)
	require.NoError(t, err)
	assert.True(t, claim.Processing)
	assert.Equal(t, order.TradeNo, claim.FeeTradeNo)
	assert.Equal(t, f.ExternalReference, claim.FeeProviderReference)
	_, err = CancelCustomerInvoice(ctx, owner, app.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	// If the verified fee has since been reversed, no PDF or issued transition
	// may commit even when the principal receipts are still entirely valid.
	reversed := AgentCommissionRefundFact{EventKey: common.GetUUID(), TopUpReferenceID: order.TradeNo, Provider: PaymentProviderEpay, PaymentReference: f.ExternalReference,
		MerchantScope: invoiceTestMerchant, Environment: "live", Currency: "CNY", CurrencyExponent: 2, CumulativeRefundedMinor: 1, RefundReference: "synthetic-fee-reversal", EvidenceReference: "synthetic-evidence"}
	require.NoError(t, db.Create(&reversed).Error)
	pdf, err := NewCustomerInvoicePDF(bytes.NewReader(customerInvoicePDFTestData()))
	require.NoError(t, err)
	_, err = IssueCustomerInvoice(ctx, root, p, app.ID, "TEST-EPAY-FEE", pdf)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	_, err = ReleaseCustomerInvoiceClaim(ctx, root, app.ID)
	require.NoError(t, err)
	rejected, err := RejectCustomerInvoice(ctx, root, app.ID, "manual bank reconciliation needed")
	require.NoError(t, err)
	assert.Equal(t, "refund_review", rejected.FeeState)
	require.NoError(t, SettleCustomerInvoiceEpay(ctx, order.TradeNo, method, f, gateway))
	queue, n, err := ListCustomerInvoices(ctx, root, true, CustomerInvoiceQuery{Status: "fee_refund_review"})
	require.NoError(t, err)
	require.Len(t, queue, 1)
	assert.EqualValues(t, 1, n)
	assert.Equal(t, "rejected", queue[0].Status)
	_, _, err = ListCustomerInvoices(ctx, owner, false, CustomerInvoiceQuery{Status: "fee_refund_review"})
	require.ErrorIs(t, err, ErrCustomerInvoiceInvalid)
	after := getUserBalances(t, owner)
	assert.Equal(t, []int{before.Quota, before.QuotaPaid, before.QuotaBonus, before.QuotaReward}, []int{after.Quota, after.QuotaPaid, after.QuotaBonus, after.QuotaReward})
	for _, entity := range []any{&BalanceLedger{}, &BalanceLedgerOperation{}, &CustomerInvoiceDocument{}} {
		require.NoError(t, db.Model(entity).Count(&count).Error)
		assert.Zero(t, count)
	}
	require.NoError(t, db.Model(&TopUp{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}
