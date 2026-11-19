package model

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func invoiceDeletionIdentity(t *testing.T, userID int) AuthSessionIdentity {
	t.Helper()
	var user User
	require.NoError(t, DB.First(&user, userID).Error)
	identity := AuthSessionIdentity{UserID: userID, SessionID: "invoice-delete-" + common.GetRandomString(16), UserAuthVersion: user.AuthVersion, SessionVersion: 1}
	require.NoError(t, DB.Create(&UserSession{SID: identity.SessionID, UserID: userID, UserAuthVersion: user.AuthVersion,
		Version: 1, Status: UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: identity.SessionID, LoginMethod: "password"}).Error)
	t.Cleanup(func() { assert.NoError(t, DB.Where("sid = ?", identity.SessionID).Delete(&UserSession{}).Error) })
	return identity
}

func TestCommercePostgresInvoiceDeletionPreservesWalletRefundAndAuthentication(t *testing.T) {
	invoice := invoiceFeeFixture(t, "delete-wallet")
	admin, applicant := invoiceDeletionIdentity(t, commerceAdmin), invoiceDeletionIdentity(t, commerceAlice)
	invoice, _, err := StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "delete-wallet-pay", InvoiceFeePayRequest{
		Method: "wallet", ExpectedRevision: invoice.Revision, ExpectedWalletQuota: "5000",
	}, "")
	require.NoError(t, err)
	var owner User
	require.NoError(t, DB.First(&owner, commerceAlice).Error)
	_, err = DeleteUserForSession(applicant)
	require.ErrorIs(t, err, ErrInvoiceUnsettled)
	for _, hard := range []bool{false, true} {
		_, err = DeleteUserByAdministrator(admin, owner, hard)
		require.ErrorIs(t, err, ErrInvoiceUnsettled)
	}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, applicant) }))
	var retained User
	require.NoError(t, DB.First(&retained, owner.Id).Error)
	assert.Equal(t, owner.AuthVersion, retained.AuthVersion)
	assert.Equal(t, 5000, retained.QuotaPaid)
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{
		ExpectedRevision: invoice.Revision, Action: "reject", Note: "Correct the invoice details",
	})
	require.NoError(t, err)
	assert.Equal(t, "refunded", invoice.FeeRefundStatus)
	assert.Equal(t, "refunded", invoice.FeePayment.Status)
	paid, _, _, err := GetUserAccountBalances(commerceAlice)
	require.NoError(t, err)
	assert.Equal(t, 10000, paid)
	_, err = DeleteUserForSession(applicant)
	require.NoError(t, err)
	assert.ErrorIs(t, DB.First(&User{}, commerceAlice).Error, gorm.ErrRecordNotFound)
	assert.Error(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, applicant) }))
}

func TestCommercePostgresInvoiceDeletionRetainsPendingAndLateEpayPayments(t *testing.T) {
	invoice := invoiceFeeFixture(t, "delete-epay")
	invoice, payment, err := StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "delete-epay-pay", InvoiceFeePayRequest{
		Method: "wxpay", ExpectedRevision: invoice.Revision,
	}, "merchant-1")
	require.NoError(t, err)
	owner := User{Id: commerceAlice}
	_, err = owner.HardDelete()
	require.ErrorIs(t, err, ErrInvoiceUnsettled)
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false, CommerceReview{ExpectedRevision: invoice.Revision, Action: "cancel"})
	require.NoError(t, err)
	_, err = owner.HardDelete()
	require.ErrorIs(t, err, ErrInvoiceUnsettled, "cancelling the application does not close the external payment")
	facts := VerifiedPaymentFacts{Provider: PaymentProviderEpay, ExternalReference: "delete-epay-reference", MerchantScope: "merchant-1",
		Environment: "live", Currency: "CNY", CurrencyExponent: 2, PaidMinor: 500}
	require.NoError(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", facts))
	invoice, err = GetInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "pending", invoice.FeeRefundStatus)
	_, err = owner.Delete()
	require.ErrorIs(t, err, ErrInvoiceUnsettled)
	_, err = owner.HardDelete()
	require.ErrorIs(t, err, ErrInvoiceUnsettled)
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{
		ExpectedRevision: invoice.Revision, Action: "record_fee_refund", FeeRefundReference: "provider-refund-delete-epay",
	})
	require.NoError(t, err)
	assert.Equal(t, "refunded", invoice.FeePayment.Status)
	_, err = owner.HardDelete()
	require.NoError(t, err)
	require.NoError(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", facts), "settled evidence remains replayable after account removal")
	conflicting := facts
	conflicting.ExternalReference = "another-provider-payment"
	require.ErrorIs(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", conflicting), ErrPaymentReceiptConflict)
	var receipts int64
	require.NoError(t, DB.Model(&PaymentReceipt{}).Where("local_order_key = ?", payment.TradeNo).Count(&receipts).Error)
	assert.EqualValues(t, 1, receipts)
}

func TestCommercePostgresInvoiceDeletionAllowsIssuedEpayReceiptReplay(t *testing.T) {
	invoice := invoiceFeeFixture(t, "delete-issued")
	_, payment, err := StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "delete-issued-pay", InvoiceFeePayRequest{
		Method: "wxpay", ExpectedRevision: invoice.Revision,
	}, "merchant-1")
	require.NoError(t, err)
	facts := VerifiedPaymentFacts{Provider: PaymentProviderEpay, ExternalReference: "delete-issued-reference", MerchantScope: "merchant-1",
		Environment: "live", Currency: "CNY", CurrencyExponent: 2, PaidMinor: 500}
	require.NoError(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", facts))
	invoice, err = GetInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false)
	require.NoError(t, err)
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{ExpectedRevision: invoice.Revision, Action: "approve"})
	require.NoError(t, err)
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{
		ExpectedRevision: invoice.Revision, Action: "issue", InvoiceNumber: "DELETE-ISSUED", DeliveryURL: "https://billing.example.com/issued",
	})
	require.NoError(t, err)
	owner := User{Id: commerceAlice}
	_, err = owner.HardDelete()
	require.NoError(t, err)
	require.NoError(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", facts))
	for _, change := range []func(*VerifiedPaymentFacts){
		func(f *VerifiedPaymentFacts) { f.PaidMinor++ },
		func(f *VerifiedPaymentFacts) { f.Currency = "USD" },
		func(f *VerifiedPaymentFacts) { f.CurrencyExponent = 0 },
		func(f *VerifiedPaymentFacts) { f.Provider = "other-provider" },
		func(f *VerifiedPaymentFacts) { f.MerchantScope = "other-merchant" },
		func(f *VerifiedPaymentFacts) { f.Environment = "test" },
	} {
		mismatch := facts
		change(&mismatch)
		require.ErrorIs(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", mismatch), ErrPaymentReceiptConflict)
	}
	require.ErrorIs(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "alipay", facts), ErrPaymentReceiptConflict)
}

func TestCommercePostgresInvoiceDeletionRetainsManualRefundLiability(t *testing.T) {
	invoice := invoiceFeeFixture(t, "delete-manual")
	invoice, err := ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{
		ExpectedRevision: invoice.Revision, Action: "confirm_fee", FeeReceiptReference: "manual-fee-delete",
	})
	require.NoError(t, err)
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{
		ExpectedRevision: invoice.Revision, Action: "reject", Note: "Refund the manual payment",
	})
	require.NoError(t, err)
	require.Nil(t, invoice.FeePayment)
	owner := User{Id: commerceAlice}
	_, err = owner.HardDelete()
	require.ErrorIs(t, err, ErrInvoiceUnsettled)
	_, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{
		ExpectedRevision: invoice.Revision, Action: "record_fee_refund", FeeRefundReference: "manual-refund-delete",
	})
	require.NoError(t, err)
	_, err = owner.HardDelete()
	require.NoError(t, err)
}

func TestCommercePostgresInvoiceDeletionAllowsUnpaidCancellationAndRejectsNewManualPayment(t *testing.T) {
	invoice := invoiceFeeFixture(t, "delete-unpaid")
	owner := User{Id: commerceAlice}
	_, err := owner.Delete()
	require.ErrorIs(t, err, ErrInvoiceUnsettled)
	invoice, err = ReviewInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false, CommerceReview{ExpectedRevision: invoice.Revision, Action: "cancel"})
	require.NoError(t, err)
	_, err = owner.HardDelete()
	require.NoError(t, err)
	_, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{
		ExpectedRevision: invoice.Revision, Action: "confirm_fee", FeeReceiptReference: "manual-after-delete",
	})
	require.ErrorIs(t, err, ErrCommerceConflict)
	invoice, err = GetInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true)
	require.NoError(t, err)
	assert.Empty(t, invoice.FeeReceiptReference)
	assert.Empty(t, invoice.FeeRefundStatus)
}

func TestCommercePostgresInvoiceCreationAndDeletionCannotOrphanAnApplication(t *testing.T) {
	setupCommerceTest(t)
	order := seedCommerceOrder(t, commerceAlice, "delete-concurrent", 10000)
	var invoice *InvoiceApplication
	var createErr, deleteErr error
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Go(func() {
		<-start
		invoice, createErr = CreateInvoiceApplication(context.Background(), commerceAlice, "delete-concurrent-create", invoiceRequest(order.Id))
	})
	workers.Go(func() {
		<-start
		owner := User{Id: commerceAlice}
		_, deleteErr = owner.HardDelete()
	})
	close(start)
	workers.Wait()
	var owner User
	ownerErr := DB.First(&owner, commerceAlice).Error
	if createErr == nil {
		require.NotNil(t, invoice)
		require.ErrorIs(t, deleteErr, ErrInvoiceUnsettled)
		require.NoError(t, ownerErr)
		assert.Equal(t, commerceAlice, invoice.UserID)
	} else {
		require.ErrorIs(t, createErr, ErrCommerceForbidden)
		require.NoError(t, deleteErr)
		require.ErrorIs(t, ownerErr, gorm.ErrRecordNotFound)
		var count int64
		require.NoError(t, DB.Model(&InvoiceApplication{}).Where("user_id = ?", commerceAlice).Count(&count).Error)
		assert.Zero(t, count)
	}
}

func TestCommercePostgresInvoiceDeletionRejectsUnknownHistoricalSettlementState(t *testing.T) {
	t.Run("manual refund state is null", func(t *testing.T) {
		invoice := invoiceFeeFixture(t, "delete-null-refund")
		invoice, err := ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{
			ExpectedRevision: invoice.Revision, Action: "confirm_fee", FeeReceiptReference: "manual-null-refund",
		})
		require.NoError(t, err)
		invoice, err = ReviewInvoiceApplication(t.Context(), commerceAdmin, invoice.ID, true, CommerceReview{
			ExpectedRevision: invoice.Revision, Action: "reject", Note: "Refund the manual payment",
		})
		require.NoError(t, err)
		require.NoError(t, DB.Model(&InvoiceApplication{}).Where("id = ?", invoice.ID).Update("fee_refund_status", nil).Error)
		owner := User{Id: commerceAlice}
		_, err = owner.HardDelete()
		require.ErrorIs(t, err, ErrInvoiceUnsettled)
	})
	t.Run("external payment state is null", func(t *testing.T) {
		invoice := invoiceFeeFixture(t, "delete-null-payment")
		invoice, payment, err := StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "delete-null-payment-start", InvoiceFeePayRequest{
			Method: "wxpay", ExpectedRevision: invoice.Revision,
		}, "merchant-1")
		require.NoError(t, err)
		_, err = ReviewInvoiceApplication(t.Context(), commerceAlice, invoice.ID, false, CommerceReview{ExpectedRevision: invoice.Revision, Action: "cancel"})
		require.NoError(t, err)
		require.NoError(t, DB.Model(&InvoiceFeePayment{}).Where("id = ?", payment.ID).Update("status", nil).Error)
		owner := User{Id: commerceAlice}
		_, err = owner.HardDelete()
		require.ErrorIs(t, err, ErrInvoiceUnsettled)
	})
}

func TestCommercePostgresInvoiceEpayRejectsNewSettlementWithoutOwner(t *testing.T) {
	invoice := invoiceFeeFixture(t, "delete-orphan")
	_, payment, err := StartInvoiceFeePayment(t.Context(), commerceAlice, invoice.ID, "delete-orphan-pay", InvoiceFeePayRequest{
		Method: "wxpay", ExpectedRevision: invoice.Revision,
	}, "merchant-1")
	require.NoError(t, err)
	// Represent an orphan left by an older version, bypassing today's deletion
	// guard only in the fixture. Replaying settled evidence must not settle it.
	require.NoError(t, DB.Unscoped().Delete(&User{Id: commerceAlice}).Error)
	facts := VerifiedPaymentFacts{Provider: PaymentProviderEpay, ExternalReference: "delete-orphan-reference", MerchantScope: "merchant-1",
		Environment: "live", Currency: "CNY", CurrencyExponent: 2, PaidMinor: 500}
	require.ErrorIs(t, SettleInvoiceEpayFee(t.Context(), payment.TradeNo, "wxpay", facts), gorm.ErrRecordNotFound)
	require.NoError(t, DB.First(&payment, payment.ID).Error)
	assert.Equal(t, "pending", payment.Status)
	assert.Zero(t, payment.PaymentReceiptID)
	var receipts int64
	require.NoError(t, DB.Model(&PaymentReceipt{}).Where("local_order_key = ?", payment.TradeNo).Count(&receipts).Error)
	assert.Zero(t, receipts)
}
