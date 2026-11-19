package model

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const invoiceTestMerchant = "invoice-fixture-merchant"

// TestMain verifies that TEST_MODEL_POSTGRES_DSN is loopback PostgreSQL 18.6.
// Use an independent schema on every connection, including concurrent writers.
func setupCustomerInvoicePG(t *testing.T, beforeInvoiceMigration ...func()) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_MODEL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL 18.6 TEST_MODEL_POSTGRES_DSN")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	schema := "invoice_" + common.GetUUID()
	base := DB
	require.NoError(t, base.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { require.NoError(t, base.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: u.String(), PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(6)
	prevLog := LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = base, prevLog; require.NoError(t, conn.Close()) })
	require.NoError(t, db.AutoMigrate(&User{}, &BalanceLedger{}, &BalanceLedgerOperation{}, &TopUp{}, &PaymentReceipt{}, &AgentCommissionRefundFact{}, &ReferralCommission{}))
	for _, seed := range beforeInvoiceMigration {
		seed()
	}
	require.NoError(t, db.AutoMigrate(CustomerInvoiceModels()...))
	return db
}

func customerInvoiceTestUser(t *testing.T, role int) int {
	t.Helper()
	name := common.GetUUID()[:12]
	u := User{Username: name, AffCode: name, Role: role, Status: common.UserStatusEnabled, Quota: 77, QuotaPaid: 44, QuotaBonus: 22, QuotaReward: 11}
	require.NoError(t, DB.Create(&u).Error)
	return u.Id
}

// Direct insertion can represent incomplete/mismatched historical evidence,
// not just valid payment facts that a current provider callback would accept.
func customerInvoiceTestOrder(t *testing.T, owner int, reference string, minor int64, change func(*TopUp, *PaymentReceipt)) TopUp {
	t.Helper()
	now := time.Now().Unix()
	r := PaymentReceipt{Provider: PaymentProviderEpay, ProviderReference: "provider-" + reference, LocalOrderKey: reference,
		Purpose: "topup", Environment: "live", MerchantScope: invoiceTestMerchant, OwnerUserID: owner,
		Currency: "CNY", CurrencyExponent: 2, PaidMinor: minor, PaidAt: now - 60, VerifiedAt: now - 30}
	o := TopUp{TradeNo: reference, UserId: owner, Amount: 500000, Money: float64(minor) / 100, PaymentProvider: r.Provider, PaidMerchantScope: r.MerchantScope,
		PaidProviderReference: r.ProviderReference, PaidEnvironment: r.Environment, PaidCurrency: r.Currency, PaidCurrencyExponent: 2,
		PaidAmountMinor: minor, PaymentMethod: "wxpay", Status: common.TopUpStatusSuccess, CreateTime: now - 90, CompleteTime: now - 30}
	if change != nil {
		change(&o, &r)
	}
	r.ReceiptKey = fmt.Sprintf("%x", sha256.Sum256([]byte(r.Provider+"\x00"+r.ProviderReference)))
	require.NoError(t, DB.Create(&r).Error)
	o.PaymentReceiptID = r.ID
	require.NoError(t, DB.Create(&o).Error)
	return o
}

func customerInvoiceTestInput(key string, ids ...int) CustomerInvoiceInput {
	return CustomerInvoiceInput{IdempotencyKey: key, OrderIDs: ids, InvoiceType: "ordinary", TitleType: "company", TaxID: "TEST00000000000123",
		Title: "Invoice Fixture", Email: "receipt@example.test", Category: "technical_service"}
}

func customerInvoiceTestRefund(t *testing.T, o TopUp) {
	t.Helper()
	f := AgentCommissionRefundFact{EventKey: fmt.Sprintf("%x", sha256.Sum256([]byte(o.TradeNo))), TopUpReferenceID: o.TradeNo, Provider: o.PaymentProvider,
		MerchantScope: o.PaidMerchantScope, Environment: o.PaidEnvironment, PaymentReference: o.PaidProviderReference, Currency: o.PaidCurrency,
		CurrencyExponent: 2, CumulativeRefundedMinor: 1, RefundReference: "refund-" + o.TradeNo, EvidenceReference: "fixture-observed-refund-" + o.TradeNo}
	require.NoError(t, DB.Create(&f).Error)
}

func TestCustomerInvoiceVerifiedCashEligibilityPostgres(t *testing.T) {
	// The first migration must preserve a pre-existing promotional Epay
	// payment and must not grant the much larger credited quota as invoice cash.
	var owner, other int
	var promo TopUp
	db := setupCustomerInvoicePG(t, func() {
		owner, other = customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleCommonUser)
		promo = customerInvoiceTestOrder(t, owner, "discounted", 10, func(o *TopUp, _ *PaymentReceipt) {
			o.NewUserBenefit = true
			o.Money = 17.5 /* old floating quote is not a receipt */
		})
	})
	ctx := t.Context()
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	ineligible := []int{}
	for _, tc := range []struct {
		name   string
		change func(*TopUp, *PaymentReceipt)
	}{
		{"test", func(o *TopUp, r *PaymentReceipt) { r.Environment, o.PaidEnvironment = "test", "test" }},
		{"merchant", func(o *TopUp, r *PaymentReceipt) {
			r.MerchantScope, o.PaidMerchantScope = "another-account", "another-account"
		}},
		{"currency", func(o *TopUp, r *PaymentReceipt) { r.Currency, o.PaidCurrency = "USD", "USD" }},
		{"exponent", func(o *TopUp, r *PaymentReceipt) { r.CurrencyExponent, o.PaidCurrencyExponent = 0, 0 }},
		{"provider", func(o *TopUp, r *PaymentReceipt) {
			r.Provider, o.PaymentProvider = PaymentProviderCreem, PaymentProviderCreem
		}},
		{"purpose", func(_ *TopUp, r *PaymentReceipt) { r.Purpose = "deposit" }},
		{"agent", func(o *TopUp, _ *PaymentReceipt) { o.AgentSimpleTopUp = true }},
		{"unpaid", func(o *TopUp, _ *PaymentReceipt) { o.Status = common.TopUpStatusPending }},
		{"expired", func(_ *TopUp, r *PaymentReceipt) { r.PaidAt = time.Now().Add(-31 * 24 * time.Hour).Unix() }},
		{"unverified", func(_ *TopUp, r *PaymentReceipt) { r.VerifiedAt = 0 }},
		{"owner-mismatch", func(_ *TopUp, r *PaymentReceipt) { r.OwnerUserID = other }},
		{"order-mismatch", func(_ *TopUp, r *PaymentReceipt) { r.LocalOrderKey = "wrong-order" }},
		{"provider-ref-mismatch", func(o *TopUp, _ *PaymentReceipt) { o.PaidProviderReference = "wrong-reference" }},
		{"amount-mismatch", func(o *TopUp, _ *PaymentReceipt) { o.PaidAmountMinor++ }},
	} {
		o := customerInvoiceTestOrder(t, owner, tc.name, 1234, tc.change)
		ineligible = append(ineligible, o.Id)
	}
	old := TopUp{UserId: owner, TradeNo: "legacy-without-receipt", PaymentProvider: PaymentProviderEpay, Money: 100, Status: common.TopUpStatusSuccess}
	require.NoError(t, db.Create(&old).Error)
	refunded := customerInvoiceTestOrder(t, owner, "partial-refund", 1234, nil)
	customerInvoiceTestRefund(t, refunded)
	foreign := customerInvoiceTestOrder(t, other, "other-owner", 9876, nil)
	legacyRefund := customerInvoiceTestOrder(t, owner, "legacy-partial-refund", 999, nil)
	require.NoError(t, db.Create(&ReferralCommission{ReferenceType: ReferralReferenceTopUp, ReferenceId: legacyRefund.TradeNo, SourceUserId: owner, UserId: other, RefundedMinor: 1}).Error)
	ineligible = append(ineligible, old.Id, refunded.Id, foreign.Id, legacyRefund.Id)
	orders, count, err := ListCustomerInvoiceOrders(ctx, owner, policy, CustomerInvoiceQuery{Page: 1, Size: 100})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.Len(t, orders, 1)
	assert.Equal(t, promo.Id, orders[0].ID)
	assert.Equal(t, "10", orders[0].AmountMinor)
	assert.Equal(t, "CNY", orders[0].Currency)
	totals, err := SummarizeCustomerInvoices(ctx, owner, policy)
	require.NoError(t, err)
	assert.Equal(t, "10", totals.AvailableMinor)
	assert.EqualValues(t, 1, totals.AvailableCount)
	// Selecting one bad and one good order must not reserve the good order.
	for _, id := range ineligible {
		_, err = SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput(fmt.Sprintf("mixed-fixture-%d", id), promo.Id, id))
		require.Error(t, err, "ineligible order %d", id)
	}
	for _, p := range []CustomerInvoicePolicy{{MerchantScope: invoiceTestMerchant}, {Enabled: true}} {
		_, err = SubmitCustomerInvoice(ctx, owner, p, customerInvoiceTestInput("disabled-fixture", promo.Id))
		require.ErrorIs(t, err, ErrCustomerInvoiceDisabled)
	}
	doc, err := SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("discounted-fixture", promo.Id))
	require.NoError(t, err)
	assert.Equal(t, "10", doc.AmountMinor)
	totals, err = SummarizeCustomerInvoices(ctx, owner, policy)
	require.NoError(t, err)
	assert.Equal(t, "0", totals.AvailableMinor)
	assert.Equal(t, "10", totals.PendingMinor)
	assert.Equal(t, "0", totals.IssuedMinor)
}

func TestCustomerInvoiceConcurrentClaimsAndReleasePostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	owner, foreign, root := customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleRootUser)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	one, two := customerInvoiceTestOrder(t, owner, "first", 19, nil), customerInvoiceTestOrder(t, owner, "second", 220, nil)
	a := customerInvoiceTestInput("parallel-first-request", one.Id, two.Id)
	b := customerInvoiceTestInput("parallel-second-request", two.Id, one.Id)
	type result struct {
		input  CustomerInvoiceInput
		detail *CustomerInvoiceDetail
		err    error
	}
	parallelSubmissions := func(first, second CustomerInvoiceInput) (result, result) {
		ready := make(chan struct{}, 2)
		start := make(chan struct{})
		done := make(chan result, 2)
		for _, input := range []CustomerInvoiceInput{first, second} {
			go func() {
				ready <- struct{}{}
				<-start
				d, err := SubmitCustomerInvoice(ctx, owner, policy, input)
				done <- result{input, d, err}
			}()
		}
		<-ready
		<-ready
		close(start)
		return <-done, <-done
	}
	winner, loser := parallelSubmissions(a, b)
	if winner.err != nil {
		winner, loser = loser, winner
	}
	require.NoError(t, winner.err)
	require.ErrorIs(t, loser.err, ErrCustomerInvoiceConflict)
	require.NotNil(t, winner.detail)
	assert.Equal(t, "239", winner.detail.AmountMinor)
	assert.EqualValues(t, 2, winner.detail.OrderCount)
	require.NoError(t, db.AutoMigrate(CustomerInvoiceModels()...))
	replay := winner.input
	replay.OrderIDs = slices.Clone(replay.OrderIDs)
	slices.Reverse(replay.OrderIDs)
	same, err := SubmitCustomerInvoice(ctx, owner, policy, replay)
	require.NoError(t, err)
	assert.Equal(t, winner.detail.ID, same.ID)
	replay.Email = "different@example.test"
	_, err = SubmitCustomerInvoice(ctx, owner, policy, replay)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	_, err = GetCustomerInvoice(ctx, foreign, false, same.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceNotFound)
	_, err = CancelCustomerInvoice(ctx, foreign, same.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceNotFound)
	_, err = RejectCustomerInvoice(ctx, foreign, same.ID, "not authorized")
	require.Error(t, err)
	_, err = GetCustomerInvoice(ctx, foreign, true, same.ID)
	require.Error(t, err, "admin scope must require a real root account")
	others, total, err := ListCustomerInvoices(ctx, foreign, false, CustomerInvoiceQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	assert.Empty(t, others)
	assert.Zero(t, total)
	cancelled, err := CancelCustomerInvoice(ctx, owner, same.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", cancelled.Status)
	oldReplay, err := SubmitCustomerInvoice(ctx, owner, policy, winner.input)
	require.NoError(t, err)
	assert.Equal(t, cancelled.ID, oldReplay.ID)
	assert.Equal(t, "cancelled", oldReplay.Status)
	first, second := parallelSubmissions(loser.input, loser.input)
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Equal(t, first.detail.ID, second.detail.ID, "simultaneous retries must return the same application")
	reapplied := first.detail
	assert.NotEqual(t, cancelled.ID, reapplied.ID)
	// Even an independent database writer cannot turn the historical line
	// back into a second active claim after migration and resubmission.
	duplicate := db.Model(&CustomerInvoiceItem{}).Where("application_id = ? AND top_up_id = ?", cancelled.ID, one.Id).Update("active_top_up_id", one.Id).Error
	require.Error(t, duplicate)
	require.True(t, isDuplicateKeyError(duplicate), "duplicate active order must violate a database unique constraint")
	rejected, err := RejectCustomerInvoice(ctx, root, reapplied.ID, "fixture customer requested a different title")
	require.NoError(t, err)
	assert.Equal(t, "rejected", rejected.Status)
	history, err := GetCustomerInvoice(ctx, owner, false, cancelled.ID)
	require.NoError(t, err)
	assert.Len(t, history.Orders, 2)
	assert.Equal(t, "239", history.AmountMinor)
	orders, total, err := ListCustomerInvoiceOrders(ctx, owner, policy, CustomerInvoiceQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	assert.Len(t, orders, 2)
	assert.EqualValues(t, 2, total)
}

func TestCustomerInvoiceIssueRechecksEvidenceAndLeavesWalletUntouchedPostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	owner, root := customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleRootUser)
	before := getUserBalances(t, owner)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	bad, good := customerInvoiceTestOrder(t, owner, "refunded-after-request", 401, nil), customerInvoiceTestOrder(t, owner, "unrefunded", 2099, nil)
	pdf, err := NewCustomerInvoicePDF(bytes.NewReader(customerInvoicePDFTestData()))
	require.NoError(t, err)
	blocked, err := SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("refund-check-fixture", bad.Id))
	require.NoError(t, err)
	_, err = ClaimCustomerInvoice(ctx, root, policy, blocked.ID)
	require.NoError(t, err)
	customerInvoiceTestRefund(t, bad)
	_, err = IssueCustomerInvoice(ctx, root, policy, blocked.ID, "INV-QA-101", pdf)
	require.Error(t, err, "a partial refund after application must block recording the invoice")
	current, err := GetCustomerInvoice(ctx, root, true, blocked.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", current.Status)
	assert.False(t, current.HasDocument)
	_, err = ReleaseCustomerInvoiceClaim(ctx, root, blocked.ID)
	require.NoError(t, err)
	_, err = RejectCustomerInvoice(ctx, root, blocked.ID, "refund found")
	require.NoError(t, err)
	toIssue, err := SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("verified-issue-fixture", good.Id))
	require.NoError(t, err)
	_, err = IssueCustomerInvoice(ctx, root, policy, toIssue.ID, "INV-QA-102", pdf)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict, "finance must reserve the order before external issuance")
	_, err = ClaimCustomerInvoice(ctx, root, policy, toIssue.ID)
	require.NoError(t, err)
	_, err = IssueCustomerInvoice(ctx, owner, policy, toIssue.ID, "INV-QA-102", pdf)
	require.Error(t, err)
	for _, p := range []CustomerInvoicePolicy{{Enabled: true}, {Enabled: true, MerchantScope: "different-issuer"}} {
		_, err = IssueCustomerInvoice(ctx, root, p, toIssue.ID, "INV-QA-102", pdf)
		require.Error(t, err)
	}
	issued, err := IssueCustomerInvoice(ctx, root, policy, toIssue.ID, "INV-QA-102", pdf)
	require.NoError(t, err)
	assert.Equal(t, "issued", issued.Status)
	assert.True(t, issued.HasDocument)
	view, err := GetCustomerInvoice(ctx, owner, false, toIssue.ID)
	require.NoError(t, err)
	assert.Equal(t, "INV-QA-102", view.InvoiceNumber)
	document, err := LoadCustomerInvoiceDocument(ctx, issued.ID)
	require.NoError(t, err)
	assert.Equal(t, pdf, document)
	customerInvoiceTestRefund(t, good)
	const adminNote = "observed partial refund after external issue"
	_, err = FlagCustomerInvoiceReconciliation(ctx, owner, issued.ID, adminNote)
	require.Error(t, err)
	_, err = FlagCustomerInvoiceReconciliation(ctx, root, issued.ID, adminNote)
	require.NoError(t, err)
	flagged, n, err := ListCustomerInvoices(ctx, root, true, CustomerInvoiceQuery{Page: 1, Size: 20, Status: "refund_review"})
	require.NoError(t, err)
	require.Len(t, flagged, 1)
	assert.EqualValues(t, 1, n)
	assert.Equal(t, issued.ID, flagged[0].ID)
	adminView, err := GetCustomerInvoice(ctx, root, true, issued.ID)
	require.NoError(t, err)
	assert.Equal(t, adminNote, adminView.ReconciliationReason)
	view, err = GetCustomerInvoice(ctx, owner, false, issued.ID)
	require.NoError(t, err)
	assert.Empty(t, view.ReconciliationReason)
	assert.Equal(t, "issued", view.Status)
	_, err = CancelCustomerInvoice(ctx, owner, issued.ID)
	require.Error(t, err)
	_, err = RejectCustomerInvoice(ctx, root, issued.ID, "do not reopen")
	require.Error(t, err)
	_, err = SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("repeat-issued-fixture", good.Id))
	require.Error(t, err)
	totals, err := SummarizeCustomerInvoices(ctx, owner, policy)
	require.NoError(t, err)
	assert.Equal(t, "0", totals.AvailableMinor)
	assert.Equal(t, "0", totals.PendingMinor)
	assert.Equal(t, "2099", totals.IssuedMinor)
	after := getUserBalances(t, owner)
	assert.Equal(t, []int{before.Quota, before.QuotaPaid, before.QuotaBonus, before.QuotaReward}, []int{after.Quota, after.QuotaPaid, after.QuotaBonus, after.QuotaReward})
	for _, ledger := range []any{&BalanceLedger{}, &BalanceLedgerOperation{}} {
		var count int64
		require.NoError(t, db.Model(ledger).Count(&count).Error)
		assert.Zero(t, count, "invoicing must not post a ledger entry or create a balance operation")
	}
}

func TestCustomerInvoiceSelectionLimitAndPaginationPostgres(t *testing.T) {
	setupCustomerInvoicePG(t)
	ctx := t.Context()
	owner := customerInvoiceTestUser(t, common.RoleCommonUser)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	ids := make([]int, 101)
	for i := range ids {
		order := customerInvoiceTestOrder(t, owner, fmt.Sprintf("batch-%03d", i), 1, nil)
		ids[i] = order.Id
	}
	first, total, err := ListCustomerInvoiceOrders(ctx, owner, policy, CustomerInvoiceQuery{Page: 1, Size: 100})
	require.NoError(t, err)
	require.Len(t, first, 100)
	require.EqualValues(t, 101, total)
	last, total, err := ListCustomerInvoiceOrders(ctx, owner, policy, CustomerInvoiceQuery{Page: 2, Size: 100})
	require.NoError(t, err)
	require.Len(t, last, 1)
	assert.EqualValues(t, 101, total)
	assert.NotContains(t, first, last[0])
	_, err = SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("over-limit", ids...))
	require.ErrorIs(t, err, ErrCustomerInvoiceInvalid)
	_, err = SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("duplicate-order", ids[0], ids[0]))
	require.ErrorIs(t, err, ErrCustomerInvoiceInvalid)
	maxRequest, err := SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("exact-limit", ids[:100]...))
	require.NoError(t, err)
	assert.EqualValues(t, 100, maxRequest.OrderCount)
	assert.Equal(t, "100", maxRequest.AmountMinor)
	totals, err := SummarizeCustomerInvoices(ctx, owner, policy)
	require.NoError(t, err)
	assert.Equal(t, "1", totals.AvailableMinor)
	assert.Equal(t, "100", totals.PendingMinor)
}

func TestCustomerInvoiceCompanyTypesAndPrivateDetailsPostgres(t *testing.T) {
	setupCustomerInvoicePG(t)
	ctx := t.Context()
	owner, root := customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleRootUser)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	order := customerInvoiceTestOrder(t, owner, "company-special", 100, nil)
	company := customerInvoiceTestInput("company-special-fixture", order.Id)
	company.InvoiceType, company.TitleType, company.Title, company.TaxID = "special", "company", "Synthetic Invoice Company", "TEST00000000000123"
	company.Address, company.Phone, company.BankName, company.BankAccount = "Synthetic office", "000-0000", "Synthetic bank", "000000000123"
	for _, tc := range []struct {
		name   string
		change func(*CustomerInvoiceInput)
	}{
		{"personal special", func(i *CustomerInvoiceInput) { i.TitleType = "personal"; i.TaxID = "" }},
		{"missing tax ID", func(i *CustomerInvoiceInput) { i.TaxID = "" }},
		{"missing address", func(i *CustomerInvoiceInput) { i.Address = "" }},
		{"missing bank", func(i *CustomerInvoiceInput) { i.BankName = "" }},
		{"invalid account", func(i *CustomerInvoiceInput) { i.BankAccount = "unavailable" }},
		{"missing phone", func(i *CustomerInvoiceInput) { i.Phone = "" }},
		{"invalid email", func(i *CustomerInvoiceInput) { i.Email = "recipient" }},
		{"unknown category", func(i *CustomerInvoiceInput) { i.Category = "other" }},
		{"unknown invoice type", func(i *CustomerInvoiceInput) { i.InvoiceType = "other" }},
	} {
		invalid := company
		tc.change(&invalid)
		_, err := SubmitCustomerInvoice(ctx, owner, policy, invalid)
		require.ErrorIs(t, err, ErrCustomerInvoiceInvalid, tc.name)
	}
	request, err := SubmitCustomerInvoice(ctx, owner, policy, company)
	require.NoError(t, err)
	assert.Equal(t, company.TaxID, request.TaxID)
	assert.Equal(t, company.BankAccount, request.BankAccount)
	adminRows, total, err := ListCustomerInvoices(ctx, root, true, CustomerInvoiceQuery{Status: "pending"})
	require.NoError(t, err)
	require.Len(t, adminRows, 1)
	assert.EqualValues(t, 1, total)
	listJSON, err := common.Marshal(adminRows[0])
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, common.Unmarshal(listJSON, &fields))
	for _, private := range []string{"title", "tax_id", "email", "phone", "address", "bank_name", "bank_account", "reason", "invoice_number", "document_sha256"} {
		assert.NotContains(t, fields, private)
	}
	adminDetail, err := GetCustomerInvoice(ctx, root, true, request.ID)
	require.NoError(t, err)
	assert.Equal(t, company.Address, adminDetail.Address)
	_, err = CancelCustomerInvoice(ctx, owner, request.ID)
	require.NoError(t, err)
	ordinaryCompany := customerInvoiceTestInput("company-ordinary-fixture", order.Id)
	ordinaryCompany.TitleType, ordinaryCompany.TaxID = "company", company.TaxID
	ordinaryCompany.Category = "software_development"
	corrected, err := SubmitCustomerInvoice(ctx, owner, policy, ordinaryCompany)
	require.NoError(t, err)
	assert.Equal(t, "ordinary", corrected.InvoiceType)
	assert.Equal(t, "software_development", corrected.Category)
}

func TestCustomerInvoiceClaimVersusCancelPostgres(t *testing.T) {
	setupCustomerInvoicePG(t)
	ctx := t.Context()
	owner, root := customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleRootUser)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	o := customerInvoiceTestOrder(t, owner, "cancel-vs-claim", 25, nil)
	app, err := SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("cancel-vs-claim-fixture", o.Id))
	require.NoError(t, err)
	start := make(chan struct{})
	cancelled, claimed := make(chan error, 1), make(chan error, 1)
	go func() { <-start; _, err := CancelCustomerInvoice(ctx, owner, app.ID); cancelled <- err }()
	go func() { <-start; _, err := ClaimCustomerInvoice(ctx, root, policy, app.ID); claimed <- err }()
	close(start)
	cancelErr, claimErr := <-cancelled, <-claimed
	result, err := GetCustomerInvoice(ctx, root, true, app.ID)
	require.NoError(t, err)
	orders, count, err := ListCustomerInvoiceOrders(ctx, owner, policy, CustomerInvoiceQuery{})
	require.NoError(t, err)
	if cancelErr == nil {
		require.ErrorIs(t, claimErr, ErrCustomerInvoiceConflict)
		assert.Equal(t, "cancelled", result.Status)
		assert.False(t, result.Processing)
		require.Len(t, orders, 1)
		assert.Equal(t, o.Id, orders[0].ID)
	} else {
		require.ErrorIs(t, cancelErr, ErrCustomerInvoiceConflict)
		require.NoError(t, claimErr)
		assert.Equal(t, "pending", result.Status)
		assert.True(t, result.Processing)
		assert.Equal(t, root, result.ProcessingBy)
		assert.Zero(t, count)
	}
}

func TestCustomerInvoiceExclusiveFinanceClaimAndReleasePostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	owner, a, b := customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleRootUser), customerInvoiceTestUser(t, common.RoleRootUser)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	o := customerInvoiceTestOrder(t, owner, "competing-finance", 175, nil)
	app, err := SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("competing-finance-fixture", o.Id))
	require.NoError(t, err)
	type outcome struct {
		actor int
		err   error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for _, actor := range []int{a, b} {
		go func() {
			<-start
			_, err := ClaimCustomerInvoice(ctx, actor, policy, app.ID)
			results <- outcome{actor, err}
		}()
	}
	close(start)
	winner, loser := <-results, <-results
	if winner.err != nil {
		winner, loser = loser, winner
	}
	require.NoError(t, winner.err)
	require.ErrorIs(t, loser.err, ErrCustomerInvoiceConflict)
	// A process restart/migration cannot clear finance ownership.
	require.NoError(t, db.AutoMigrate(CustomerInvoiceModels()...))
	mine, err := GetCustomerInvoice(ctx, owner, false, app.ID)
	require.NoError(t, err)
	assert.True(t, mine.Processing)
	assert.Zero(t, mine.ProcessingBy, "don't disclose finance identity to customer")
	claim, err := GetCustomerInvoice(ctx, winner.actor, true, app.ID)
	require.NoError(t, err)
	assert.Equal(t, winner.actor, claim.ProcessingBy)
	_, err = CancelCustomerInvoice(ctx, owner, app.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	for _, actor := range []int{winner.actor, loser.actor} {
		_, err = RejectCustomerInvoice(ctx, actor, app.ID, "cannot reopen a claim")
		require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	}
	pdf, err := NewCustomerInvoicePDF(bytes.NewReader(customerInvoicePDFTestData()))
	require.NoError(t, err)
	_, err = IssueCustomerInvoice(ctx, loser.actor, policy, app.ID, "INV-QA-CLAIM", pdf)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	_, err = ReleaseCustomerInvoiceClaim(ctx, loser.actor, app.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	stillLocked, err := GetCustomerInvoice(ctx, winner.actor, true, app.ID)
	require.NoError(t, err)
	assert.True(t, stillLocked.Processing)
	assert.False(t, stillLocked.HasDocument)
	unlocked, err := ReleaseCustomerInvoiceClaim(ctx, winner.actor, app.ID)
	require.NoError(t, err)
	assert.False(t, unlocked.Processing)
	cancelled, err := CancelCustomerInvoice(ctx, owner, app.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", cancelled.Status)
	orders, n, err := ListCustomerInvoiceOrders(ctx, owner, policy, CustomerInvoiceQuery{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	require.Len(t, orders, 1)
	assert.Equal(t, o.Id, orders[0].ID)
}
