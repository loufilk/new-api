package model

import (
	"math"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomerInvoiceWalletFeeQuoteBoundaries(t *testing.T) {
	for _, disabled := range []string{"", "0", "-1", "+1", "001", "0.01", "1e2", " 1", "9007199254740992"} {
		t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", disabled)
		assert.Zero(t, CustomerInvoiceWalletQuotaPerCent())
	}
	t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", "5000")
	assert.EqualValues(t, 5000, CustomerInvoiceWalletQuotaPerCent())
	for _, args := range [][2]int64{{0, 100}, {-1, 5000}, {4, 0}, {1, -1}, {2, math.MaxInt64}, {math.MaxInt64, 2}, {2, common.MaxWalletQuota/2 + 1}} {
		_, err := CustomerInvoiceWalletFeeQuote(args[0], args[1])
		assert.ErrorIs(t, err, ErrCustomerInvoiceFeePaymentUnavailable)
	}
	q, err := CustomerInvoiceWalletFeeQuote(1, common.MaxWalletQuota)
	require.NoError(t, err)
	assert.Equal(t, common.MaxWalletQuota, q)
}

func TestCustomerInvoiceWalletCheckoutAndRefundPostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	require.NoError(t, db.AutoMigrate(BonusSourceModels()...))
	ctx := t.Context()
	owner, other := customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleCommonUser)
	root := customerInvoiceTestUser(t, common.RoleRootUser)
	before := getUserBalances(t, owner)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant, WalletFeeAvailable: true}
	t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", "3")
	terms := defaultCustomerInvoiceSettings()
	terms.Ordinary.FeeEnabled, terms.Ordinary.FeeRateBPS = true, 1000
	saved, err := UpdateCustomerInvoiceSettings(ctx, root, terms)
	require.NoError(t, err)
	create := func(reference string, cents int64) (*CustomerInvoiceDetail, TopUp) {
		t.Helper()
		o := customerInvoiceTestOrder(t, owner, reference, cents, nil)
		in := customerInvoiceTestInput(reference, o.Id)
		in.TitleType, in.TaxID, in.SettingsVersion = "company", "91310000ABCDEFGHIJ", &saved.Version
		d, err := SubmitCustomerInvoice(ctx, owner, policy, in)
		require.NoError(t, err)
		require.Equal(t, "awaiting_payment", d.Status)
		return d, o
	}
	d, _ := create("mixed-sources-paid-only", 120)
	assert.Equal(t, "12", d.FeeMinor)
	assert.Equal(t, "36", d.WalletFeeQuota)
	assert.EqualValues(t, 3, d.WalletQuotaPerCent)
	confirm := CustomerInvoiceWalletConfirmation{FeeMinor: "12", WalletQuota: "36", WalletQuotaPerCent: 3}
	pay := func(id int64, c CustomerInvoiceWalletConfirmation) error {
		_, err := PayCustomerInvoiceWalletFee(ctx, owner, policy, id, c)
		return err
	}
	_, err = PayCustomerInvoiceWalletFee(ctx, other, policy, d.ID, confirm)
	require.ErrorIs(t, err, ErrCustomerInvoiceNotFound)
	for _, changed := range []CustomerInvoiceWalletConfirmation{{"12", "36", 4}, {"12", "35", 3}, {"012", "36", 3}} {
		require.ErrorIs(t, pay(d.ID, changed), ErrCustomerInvoiceConflict)
	}
	// This account has 44 paid + 22 bonus + 11 reward. Total exceeds 45
	// but the invoice may not consume, or convert, bonus/reward.
	short, _ := create("paid-account-short", 150)
	require.ErrorIs(t, pay(short.ID, CustomerInvoiceWalletConfirmation{"15", "45", 3}), ErrLedgerInsufficientBalance)
	otherRail, _ := create("epay-already-pinned", 120)
	_, err = BeginCustomerInvoiceEpay(ctx, owner, otherRail.ID, invoiceTestMerchant, "wxpay", "https://example.test/gateway")
	require.NoError(t, err)
	require.ErrorIs(t, pay(otherRail.ID, confirm), ErrCustomerInvoiceConflict)
	invalid, o := create("refunded-source-before-wallet-charge", 120)
	customerInvoiceTestRefund(t, o)
	require.ErrorIs(t, pay(invalid.ID, confirm), ErrCustomerInvoiceConflict)
	t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", "0")
	require.ErrorIs(t, pay(d.ID, confirm), ErrCustomerInvoiceFeePaymentUnavailable)
	t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", "77")
	policy.WalletFeeAvailable = false // compliance withdrawal blocks new debits even if an exchange rate exists
	require.ErrorIs(t, pay(d.ID, confirm), ErrCustomerInvoiceFeePaymentUnavailable)
	policy.WalletFeeAvailable = true
	policy.Enabled = false // intake off does not invalidate an existing frozen quote
	// Concurrent refresh/retry at a new deployment rate keeps the original
	// quote, settles exactly once, and leaves principal and provider receipts alone.
	st := make(chan struct{})
	done := make(chan error, 2)
	for range 2 {
		go func() { <-st; done <- pay(d.ID, confirm) }()
	}
	close(st)
	for range 2 {
		require.NoError(t, <-done)
	}
	t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", "")
	policy.WalletFeeAvailable = false
	require.NoError(t, pay(d.ID, confirm), "kill switch cannot invalidate an already committed retry")
	history, err := GetCustomerInvoice(ctx, owner, false, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", history.Status)
	assert.Equal(t, "paid", history.FeeState)
	assert.Equal(t, "wallet", history.FeeRail)
	assert.Equal(t, "12", history.FeePaidMinor)
	assert.Equal(t, "120", history.AmountMinor)
	_, err = BeginCustomerInvoiceEpay(ctx, owner, d.ID, invoiceTestMerchant, "wxpay", "https://example.test/gateway")
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict, "a wallet debit excludes a payable external checkout")
	after := getUserBalances(t, owner)
	assert.Equal(t, before.QuotaPaid-36, after.QuotaPaid)
	assert.Equal(t, before.Quota-36, after.Quota)
	assert.Equal(t, before.QuotaBonus, after.QuotaBonus)
	assert.Equal(t, before.QuotaReward, after.QuotaReward)
	var receipts, topups int64
	require.NoError(t, db.Model(&PaymentReceipt{}).Count(&receipts).Error)
	require.NoError(t, db.Model(&TopUp{}).Count(&topups).Error)
	assert.EqualValues(t, 4, receipts)
	assert.EqualValues(t, 4, topups)
	_, err = ClaimCustomerInvoice(ctx, root, policy, d.ID)
	require.NoError(t, err)
	_, err = CancelCustomerInvoice(ctx, owner, d.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	_, err = ReleaseCustomerInvoiceClaim(ctx, root, d.ID)
	require.NoError(t, err)
	// Tampered accounting evidence cannot mint a refund, release the claimed
	// order, or permit issuance. This direct write models damaged historic data.
	ref := strconv.FormatInt(d.ID, 10)
	dk := "invoice_fee:debit:" + ref
	var row BalanceLedger
	require.NoError(t, db.Where("idempotency_key = ?", dk).Take(&row).Error)
	require.NoError(t, db.Table(row.TableName()).Where("id = ?", row.Id).Update("metadata", "changed snapshot").Error)
	_, err = RejectCustomerInvoice(ctx, root, d.ID, "fixture rejection")
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	_, err = ClaimCustomerInvoice(ctx, root, policy, d.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	require.NoError(t, db.Table(row.TableName()).Where("id = ?", row.Id).Update("metadata", row.Metadata).Error)
	for range 2 {
		closed, err := CancelCustomerInvoice(ctx, owner, d.ID)
		require.NoError(t, err)
		require.Equal(t, "cancelled", closed.Status)
		require.Equal(t, "refunded", closed.FeeState)
	}
	require.ErrorIs(t, pay(d.ID, confirm), ErrCustomerInvoiceConflict)
	final := getUserBalances(t, owner)
	assert.Equal(t, before.QuotaPaid, final.QuotaPaid)
	assert.Equal(t, before.Quota, final.Quota)
	assert.Equal(t, before.QuotaBonus, final.QuotaBonus)
	assert.Equal(t, before.QuotaReward, final.QuotaReward)
	assert.EqualValues(t, 1, countLedgerRows(t, dk))
	assert.EqualValues(t, 1, countLedgerRows(t, "invoice_fee:refund:"+ref))
}
