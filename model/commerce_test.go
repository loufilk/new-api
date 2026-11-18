package model

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const commerceAdmin, commerceAlice, commerceBob = 98401, 98402, 98403

func setupCommerceTest(t *testing.T) {
	t.Helper()
	require.NotEmpty(t, os.Getenv("TEST_MODEL_POSTGRES_DSN"), "requires disposable PostgreSQL 18.6")
	require.True(t, common.UsingMainDatabase(common.DatabaseTypePostgreSQL))
	require.NoError(t, DB.Migrator().DropTable(CommerceModels()...))
	require.NoError(t, DB.AutoMigrate(CommerceModels()...))
	require.NoError(t, DB.Unscoped().Where("id IN ?", []int{commerceAdmin, commerceAlice, commerceBob}).Delete(&User{}).Error)
	require.NoError(t, DB.Where("user_id IN ?", []int{commerceAdmin, commerceAlice, commerceBob}).Delete(&TopUp{}).Error)
	require.NoError(t, DB.Create(&[]User{
		{Id: commerceAdmin, Username: "commerce-admin", AffCode: "commerce-admin", Role: common.RoleRootUser, Status: common.UserStatusEnabled},
		{Id: commerceAlice, Username: "commerce-alice", AffCode: "commerce-alice", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 10000, QuotaPaid: 10000},
		{Id: commerceBob, Username: "commerce-bob", AffCode: "commerce-bob", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
	}).Error)
	config := DefaultCommerceConfig()
	config.InvoiceEnabled, config.SpecialInvoiceEnabled = true, true
	_, err := SaveCommerceConfig(context.Background(), commerceAdmin, config)
	require.NoError(t, err)
}

func seedCommerceOrder(t *testing.T, userID int, trade string, minor int64) TopUp {
	t.Helper()
	row := TopUp{UserId: userID, TradeNo: trade, Status: common.TopUpStatusSuccess, PaidCurrency: "CNY", PaidCurrencyExponent: 2, PaidAmountMinor: minor, CompleteTime: common.GetTimestamp(), Amount: 900, Money: 999}
	require.NoError(t, DB.Create(&row).Error)
	return row
}

func invoiceRequest(ids ...int) InvoiceCreate {
	return InvoiceCreate{OrderIDs: ids, Kind: "general", TitleType: "company", Title: "Example Company", TaxID: "TAX-EXAMPLE", Email: "invoice@example.com"}
}

func TestCommercePostgresInvoiceLifecycleAndMigration(t *testing.T) {
	setupCommerceTest(t)
	ctx := context.Background()
	paid := seedCommerceOrder(t, commerceAlice, "commerce-paid", 12345)
	other := seedCommerceOrder(t, commerceBob, "commerce-other", 9900)
	legacy := seedCommerceOrder(t, commerceAlice, "commerce-legacy", 0)
	config, err := GetCommerceConfig(ctx)
	require.NoError(t, err)
	config.GeneralFeeBPS = 400
	_, err = SaveCommerceConfig(ctx, commerceAdmin, *config)
	require.NoError(t, err)
	eligible, _, err := EligibleInvoiceOrders(ctx, commerceAlice)
	require.NoError(t, err)
	require.Len(t, eligible, 1)
	assert.Equal(t, paid.Id, eligible[0].TopUpID)
	_, err = CreateInvoiceApplication(ctx, commerceAlice, "wrong-owner", invoiceRequest(other.Id))
	require.ErrorIs(t, err, ErrInvoiceIneligible)
	_, err = CreateInvoiceApplication(ctx, commerceAlice, "legacy-order", invoiceRequest(legacy.Id))
	require.ErrorIs(t, err, ErrInvoiceIneligible)
	invoice, err := CreateInvoiceApplication(ctx, commerceAlice, "create-first", invoiceRequest(paid.Id))
	require.NoError(t, err)
	assert.EqualValues(t, 12345, invoice.AmountMinor, "use settled CNY receipt, not quota or Money")
	assert.EqualValues(t, 494, invoice.FeeMinor, "rounded once in minor units")
	assert.Equal(t, "awaiting_fee", invoice.Status)
	_, err = GetInvoiceApplication(ctx, commerceBob, invoice.ID, false)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = ReviewInvoiceApplication(ctx, commerceAdmin, invoice.ID, true, CommerceReview{ExpectedRevision: 1, Action: "approve"})
	require.ErrorIs(t, err, ErrCommerceConflict)
	invoice, err = ReviewInvoiceApplication(ctx, commerceAdmin, invoice.ID, true, CommerceReview{ExpectedRevision: 1, Action: "confirm_fee", FeeReceiptReference: "manual-receipt-01"})
	require.NoError(t, err)
	invoice, err = ReviewInvoiceApplication(ctx, commerceAdmin, invoice.ID, true, CommerceReview{ExpectedRevision: 2, Action: "approve"})
	require.NoError(t, err)
	_, err = ReviewInvoiceApplication(ctx, commerceAdmin, invoice.ID, true, CommerceReview{ExpectedRevision: 3, Action: "issue", InvoiceNumber: "INV-01", DeliveryURL: "javascript:alert(1)"})
	require.ErrorIs(t, err, ErrCommerceInvalid)
	invoice, err = ReviewInvoiceApplication(ctx, commerceAdmin, invoice.ID, true, CommerceReview{ExpectedRevision: 3, Action: "issue", InvoiceNumber: "INV-01", DeliveryURL: "https://billing.example.com/private/INV-01"})
	require.NoError(t, err)
	assert.Equal(t, "issued", invoice.Status)
	assert.Len(t, invoice.Events, 4)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(CommerceModels()...))
	}
	retained, err := GetInvoiceApplication(ctx, commerceAlice, invoice.ID, false)
	require.NoError(t, err)
	assert.Equal(t, invoice, retained)
	var user User
	require.NoError(t, DB.First(&user, commerceAlice).Error)
	assert.Equal(t, 10000, user.Quota, "invoicing never moves wallet money")
	eligible, _, err = EligibleInvoiceOrders(ctx, commerceAlice)
	require.NoError(t, err)
	assert.Empty(t, eligible)
}

func TestCommercePostgresReservationsReplayAndAuditRollback(t *testing.T) {
	setupCommerceTest(t)
	ctx := context.Background()
	paid := seedCommerceOrder(t, commerceAlice, "commerce-race", 10000)
	start := make(chan struct{})
	results := make(chan *InvoiceApplication, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			row, err := CreateInvoiceApplication(ctx, commerceAlice, "same-request", invoiceRequest(paid.Id))
			results <- row
			errors <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var invoice *InvoiceApplication
	for row := range results {
		if invoice != nil {
			assert.Equal(t, invoice.ID, row.ID)
		}
		invoice = row
	}
	require.NotNil(t, invoice)
	changed := invoiceRequest(paid.Id)
	changed.Title = "Another company"
	_, err := CreateInvoiceApplication(ctx, commerceAlice, "same-request", changed)
	require.ErrorIs(t, err, ErrCommerceConflict)
	_, err = CreateInvoiceApplication(ctx, commerceAlice, "different-request", invoiceRequest(paid.Id))
	require.ErrorIs(t, err, ErrInvoiceIneligible)
	_, err = ReviewInvoiceApplication(ctx, commerceBob, invoice.ID, false, CommerceReview{ExpectedRevision: 1, Action: "cancel"})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = ReviewInvoiceApplication(ctx, commerceAlice, invoice.ID, false, CommerceReview{ExpectedRevision: 1, Action: "cancel"})
	require.NoError(t, err)
	replacement, err := CreateInvoiceApplication(ctx, commerceAlice, "after-cancel", invoiceRequest(paid.Id))
	require.NoError(t, err)
	assert.NotEqual(t, invoice.ID, replacement.ID)
	_, err = ReviewInvoiceApplication(ctx, commerceAdmin, replacement.ID, true, CommerceReview{ExpectedRevision: 1, Action: "reject", Note: "Correct the title"})
	require.NoError(t, err)
	require.NoError(t, DB.Migrator().DropTable(&CommerceAudit{}))
	_, err = CreateInvoiceApplication(ctx, commerceAlice, "audit-must-commit", invoiceRequest(paid.Id))
	require.Error(t, err)
	var count int64
	require.NoError(t, DB.Model(&InvoiceApplication{}).Where("request_key = ?", "audit-must-commit").Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&InvoiceReservation{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.AutoMigrate(&CommerceAudit{}))
}

func TestCommercePostgresPartnerReviewOwnershipAndImmutableSubmission(t *testing.T) {
	setupCommerceTest(t)
	ctx := context.Background()
	input := PartnerCreate{SiteName: "Example API", SiteURL: "https://supplier.example.com", ContactName: "Supplier", ContactEmail: "partner@example.com", Description: "API supply and availability details", Quotes: []PartnerQuote{{Group: "Claude stable", Models: "claude-example", Rate: "0.35", Description: "Native protocol"}}}
	row, err := CreatePartnerApplication(ctx, commerceAlice, "partner-first", input)
	require.NoError(t, err)
	replay, err := CreatePartnerApplication(ctx, commerceAlice, "partner-first", input)
	require.NoError(t, err)
	assert.Equal(t, row.ID, replay.ID)
	_, err = CreatePartnerApplication(ctx, commerceAlice, "partner-another", input)
	require.ErrorIs(t, err, ErrCommerceConflict)
	_, err = GetPartnerApplication(ctx, commerceBob, row.ID, false)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = ReviewPartnerApplication(ctx, commerceAlice, row.ID, true, CommerceReview{ExpectedRevision: 1, Action: "approve", Note: "Approved"})
	require.ErrorIs(t, err, ErrCommerceForbidden)
	row, err = ReviewPartnerApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 1, Action: "review"})
	require.NoError(t, err)
	_, err = ReviewPartnerApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 1, Action: "reject", Note: "Old version"})
	require.ErrorIs(t, err, ErrCommerceConflict)
	row, err = ReviewPartnerApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 2, Action: "approve", Note: "Contact will follow by email"})
	require.NoError(t, err)
	assert.Equal(t, "approved", row.Status)
	assert.Equal(t, input.Quotes, row.Quotes)
	assert.Len(t, row.Events, 3)
	_, err = ReviewPartnerApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 3, Action: "reject", Note: "Cannot rewrite terminal state"})
	require.ErrorIs(t, err, ErrCommerceConflict)
	input.Quotes[0].Rate = "-0.1"
	_, err = CreatePartnerApplication(ctx, commerceBob, "negative-rate", input)
	require.ErrorIs(t, err, ErrCommerceInvalid)
	for _, invalid := range []string{"1e2", "1e1000000000", "0.0000001", "1000.01"} {
		input.Quotes[0].Rate = invalid
		_, err = CreatePartnerApplication(ctx, commerceBob, "invalid-rate", input)
		require.ErrorIs(t, err, ErrCommerceInvalid, invalid)
	}
}

func TestCommercePostgresConfigPermissionConflictAndFreshUpgrade(t *testing.T) {
	setupCommerceTest(t)
	ctx := context.Background()
	config, err := GetCommerceConfig(ctx)
	require.NoError(t, err)
	_, err = SaveCommerceConfig(ctx, commerceAlice, *config)
	require.ErrorIs(t, err, ErrCommerceForbidden)
	updated, err := SaveCommerceConfig(ctx, commerceAdmin, *config)
	require.NoError(t, err)
	_, err = SaveCommerceConfig(ctx, commerceAdmin, *config)
	require.ErrorIs(t, err, ErrCommerceConflict)
	updated.InvoiceEnabled = false
	_, err = SaveCommerceConfig(ctx, commerceAdmin, *updated)
	require.NoError(t, err)
	paid := seedCommerceOrder(t, commerceAlice, "disabled-invoice", 100)
	_, err = CreateInvoiceApplication(ctx, commerceAlice, "when-disabled", invoiceRequest(paid.Id))
	require.ErrorIs(t, err, ErrCommerceDisabled)
	// Upgrade an existing account/order database with no commerce tables.
	require.NoError(t, DB.Migrator().DropTable(CommerceModels()...))
	for range 2 {
		require.NoError(t, DB.AutoMigrate(CommerceModels()...))
	}
	var retained TopUp
	require.NoError(t, DB.First(&retained, paid.Id).Error)
	assert.Equal(t, paid, retained)
	config, err = GetCommerceConfig(ctx)
	require.NoError(t, err)
	assert.False(t, config.InvoiceEnabled, "merchant must configure invoicing before accepting applications")
	assert.Equal(t, 30, config.InvoiceWindowDays)
}

func TestCommercePostgresReceivedFeeCancellationRequiresManualRefundRecord(t *testing.T) {
	setupCommerceTest(t)
	ctx := context.Background()
	config, err := GetCommerceConfig(ctx)
	require.NoError(t, err)
	config.GeneralFeeBPS = 100
	_, err = SaveCommerceConfig(ctx, commerceAdmin, *config)
	require.NoError(t, err)
	paid := seedCommerceOrder(t, commerceAlice, "fee-refund-order", 10000)
	row, err := CreateInvoiceApplication(ctx, commerceAlice, "fee-refund-application", invoiceRequest(paid.Id))
	require.NoError(t, err)
	row, err = ReviewInvoiceApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 1, Action: "confirm_fee", FeeReceiptReference: "received-fee-01"})
	require.NoError(t, err)
	row, err = ReviewInvoiceApplication(ctx, commerceAlice, row.ID, false, CommerceReview{ExpectedRevision: 2, Action: "cancel"})
	require.NoError(t, err)
	assert.Equal(t, "pending", row.FeeRefundStatus)
	_, err = ReviewInvoiceApplication(ctx, commerceAlice, row.ID, false, CommerceReview{ExpectedRevision: 3, Action: "record_fee_refund", FeeRefundReference: "forged-refund"})
	require.ErrorIs(t, err, ErrCommerceInvalid)
	row, err = ReviewInvoiceApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 3, Action: "record_fee_refund", FeeRefundReference: "manual-refund-01"})
	require.NoError(t, err)
	assert.Equal(t, "refunded", row.FeeRefundStatus)
	assert.Equal(t, "cancelled", row.Status)
	assert.Equal(t, "manual-refund-01", row.FeeRefundReference)
	_, err = ReviewInvoiceApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 4, Action: "record_fee_refund", FeeRefundReference: "duplicate-refund"})
	require.ErrorIs(t, err, ErrCommerceInvalid)
}

func TestCommercePostgresLateFeeReceiptPreservesTerminalStateAndRequiresRefund(t *testing.T) {
	setupCommerceTest(t)
	ctx := context.Background()
	config, err := GetCommerceConfig(ctx)
	require.NoError(t, err)
	config.GeneralFeeBPS = 100
	_, err = SaveCommerceConfig(ctx, commerceAdmin, *config)
	require.NoError(t, err)
	for _, action := range []string{"cancel", "reject"} {
		t.Run(action, func(t *testing.T) {
			paid := seedCommerceOrder(t, commerceAlice, "late-fee-"+action, 10000)
			row, err := CreateInvoiceApplication(ctx, commerceAlice, "late-fee-"+action, invoiceRequest(paid.Id))
			require.NoError(t, err)
			actor, admin := commerceAlice, false
			if action == "reject" {
				actor, admin = commerceAdmin, true
			}
			row, err = ReviewInvoiceApplication(ctx, actor, row.ID, admin, CommerceReview{ExpectedRevision: row.Revision, Action: action, Note: "Incorrect tax identifier"})
			require.NoError(t, err)
			terminal := row.Status
			row, err = ReviewInvoiceApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: row.Revision, Action: "confirm_fee", FeeReceiptReference: "late-receipt-" + action})
			require.NoError(t, err)
			assert.Equal(t, terminal, row.Status)
			assert.Equal(t, "pending", row.FeeRefundStatus)
			var reserved int64
			require.NoError(t, DB.Model(&InvoiceReservation{}).Where("invoice_id = ?", row.ID).Count(&reserved).Error)
			assert.Zero(t, reserved)
			_, err = ReviewInvoiceApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: row.Revision, Action: "confirm_fee", FeeReceiptReference: "duplicate-receipt"})
			require.ErrorIs(t, err, ErrCommerceInvalid)
			row, err = ReviewInvoiceApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: row.Revision, Action: "record_fee_refund", FeeRefundReference: "late-refund-" + action})
			require.NoError(t, err)
			assert.Equal(t, "refunded", row.FeeRefundStatus)
			assert.Equal(t, terminal, row.Status)
		})
	}
}

func TestCommercePostgresRefundRecordingRetainsRejectionReason(t *testing.T) {
	setupCommerceTest(t)
	ctx := context.Background()
	config, err := GetCommerceConfig(ctx)
	require.NoError(t, err)
	config.GeneralFeeBPS = 100
	_, err = SaveCommerceConfig(ctx, commerceAdmin, *config)
	require.NoError(t, err)
	paid := seedCommerceOrder(t, commerceAlice, "refund-note-order", 10000)
	row, err := CreateInvoiceApplication(ctx, commerceAlice, "refund-note-app", invoiceRequest(paid.Id))
	require.NoError(t, err)
	row, err = ReviewInvoiceApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 1, Action: "confirm_fee", FeeReceiptReference: "note-fee-receipt"})
	require.NoError(t, err)
	row, err = ReviewInvoiceApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 2, Action: "reject", Note: "Correct the tax identifier and apply again"})
	require.NoError(t, err)
	row, err = ReviewInvoiceApplication(ctx, commerceAdmin, row.ID, true, CommerceReview{ExpectedRevision: 3, Action: "record_fee_refund", FeeRefundReference: "note-fee-refund"})
	require.NoError(t, err)
	assert.Equal(t, "Correct the tax identifier and apply again", row.ReviewNote)
	require.Len(t, row.Events, 4)
	assert.Equal(t, "Correct the tax identifier and apply again", row.Events[2].Note)
}

func TestCommercePostgresPendingInvoiceOrderDoesNotBlockPaymentLock(t *testing.T) {
	setupCommerceTest(t)
	paid := seedCommerceOrder(t, commerceAlice, "invoice-payment-lock-order", 10000)
	require.NoError(t, DB.Model(&paid).Update("status", common.TopUpStatusPending).Error)
	payment := DB.Begin()
	require.NoError(t, payment.Error)
	defer payment.Rollback()
	var locked TopUp
	require.NoError(t, lockForUpdate(payment).First(&locked, paid.Id).Error)
	// A payment callback holds the pending order before creditTopUpQuota locks
	// its user. An invoice must reject that order without waiting for its lock.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := CreateInvoiceApplication(ctx, commerceAlice, "invoice-pending-order", invoiceRequest(paid.Id))
	require.ErrorIs(t, err, ErrInvoiceIneligible)
	_, err = creditTopUpQuota(payment, LedgerEntry{UserId: commerceAlice, Account: AccountPaid, Delta: 1000, Reason: LedgerReasonTopup, ReferenceType: "topup", ReferenceId: paid.TradeNo})
	require.NoError(t, err)
	require.NoError(t, payment.Model(&locked).Updates(map[string]any{"status": common.TopUpStatusSuccess, "complete_time": common.GetTimestamp()}).Error)
	require.NoError(t, payment.Commit().Error)
	row, err := CreateInvoiceApplication(context.Background(), commerceAlice, "invoice-after-payment", invoiceRequest(paid.Id))
	require.NoError(t, err)
	assert.Equal(t, "submitted", row.Status)
}
