package model

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var (
	ErrInvoiceWalletUnavailable  = errors.New("Wallet payment is unavailable for this invoice currency")
	ErrInvoiceWalletInsufficient = errors.New("Insufficient paid wallet balance for the invoice fee")
	ErrInvoicePaymentActive      = errors.New("This invoice already has a payment. Continue the existing payment.")
	ErrInvoicePaymentChanged     = errors.New("The invoice payment quote changed. Refresh and try again.")
)

// One payment per application prevents an outstanding online payment and a
// wallet charge from collecting the same fee. Its immutable quote is independent
// of top-ups, so invoice fees cannot generate credits or referral rewards.
type InvoiceFeePayment struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	InvoiceID         int64  `json:"invoice_id" gorm:"not null;uniqueIndex"`
	UserID            int    `json:"user_id" gorm:"index;not null;uniqueIndex:idx_invoice_fee_user_request,priority:1"`
	RequestKey        string `json:"-" gorm:"type:varchar(96);not null;uniqueIndex:idx_invoice_fee_user_request,priority:2"`
	Method            string `json:"method" gorm:"type:varchar(32);not null"`
	Provider          string `json:"provider" gorm:"type:varchar(32);not null"`
	TradeNo           string `json:"trade_no" gorm:"type:varchar(32);not null;uniqueIndex"`
	Currency          string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent  int    `json:"currency_exponent"`
	AmountMinor       int64  `json:"amount_minor,string"`
	WalletQuota       int    `json:"wallet_quota,string" gorm:"type:bigint"`
	WalletPrice       string `json:"wallet_price" gorm:"type:varchar(100)"`
	QuotaPerUnit      string `json:"quota_per_unit" gorm:"type:varchar(100)"`
	Status            string `json:"status" gorm:"type:varchar(16);index"`
	MerchantScope     string `json:"-" gorm:"type:varchar(128)"`
	Environment       string `json:"-" gorm:"type:varchar(16)"`
	ProviderReference string `json:"-" gorm:"type:varchar(255)"`
	PaymentReceiptID  int64  `json:"-"`
	PaidAt            int64  `json:"paid_at"`
	RefundStatus      string `json:"refund_status" gorm:"type:varchar(16)"`
	RefundReference   string `json:"refund_reference" gorm:"type:varchar(200)"`
	RefundedAt        int64  `json:"refunded_at"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime:false"`
	UpdatedAt         int64  `json:"updated_at" gorm:"autoUpdateTime:false"`
}

type InvoiceFeePayRequest struct {
	Method              string `json:"method"`
	ExpectedRevision    int64  `json:"expected_revision"`
	ExpectedWalletQuota string `json:"expected_wallet_quota"`
}

// InvoiceWalletFeeQuote uses the configured CNY purchase price per system unit,
// not the display exchange rate or a promotional top-up discount. A changed
// price is rejected at payment against the quote shown to the user.
func InvoiceWalletFeeQuote(currency string, exponent int, minor int64) (quota int, price, unit string, err error) {
	if minor < 0 || minor > commerceMaxMinor || !validPaymentCurrency(currency, exponent) {
		return 0, "", "", ErrCommerceInvalid
	}
	value := 1.0
	switch currency {
	case "CNY":
		value = operation_setting.Price
	case "USD":
	default:
		return 0, "", "", ErrInvoiceWalletUnavailable
	}
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) || common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		return 0, "", "", ErrInvoiceWalletUnavailable
	}
	rate := decimal.NewFromFloat(value)
	quotaUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	amount := decimal.NewFromInt(minor).Shift(-int32(exponent)).Mul(quotaUnit).Div(rate).Ceil()
	quota, err = common.WalletQuotaFromDecimalStrict(amount)
	if err != nil {
		return 0, "", "", ErrCommerceInvalid
	}
	return quota, rate.String(), quotaUnit.String(), nil
}

func loadInvoiceFeePaymentTx(tx *gorm.DB, invoice *InvoiceApplication) error {
	var payment InvoiceFeePayment
	err := tx.Where("invoice_id = ?", invoice.ID).First(&payment).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err == nil {
		invoice.FeePayment = &payment
	}
	return err
}

func invoiceFeeDebitEntry(payment *InvoiceFeePayment) LedgerEntry {
	return LedgerEntry{UserId: payment.UserID, Account: AccountPaid, Delta: payment.WalletQuota,
		Reason: "invoice_fee", ReferenceType: "invoice_fee", ReferenceId: payment.TradeNo,
		OperatorId: payment.UserID, IdempotencyKey: "invoice_fee:" + payment.TradeNo,
		DebitOrder: []LedgerAccount{AccountPaid}}
}

func StartInvoiceFeePayment(ctx context.Context, userID int, invoiceID int64, requestKey string, input InvoiceFeePayRequest, merchant string) (*InvoiceApplication, *InvoiceFeePayment, error) {
	if !commerceRequestKey(requestKey) || invoiceID <= 0 || input.ExpectedRevision < 1 || !commerceText(input.Method, 1, 32) {
		return nil, nil, ErrCommerceInvalid
	}
	var payment InvoiceFeePayment
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// All writes lock owner -> application -> payment -> ledger.
		if err := commerceActor(tx, userID, false); err != nil {
			return err
		}
		var invoice InvoiceApplication
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", invoiceID, userID).First(&invoice).Error; err != nil {
			return err
		}
		var sameRequest InvoiceFeePayment
		foundRequest := tx.Where("user_id = ? AND request_key = ?", userID, requestKey).First(&sameRequest)
		if foundRequest.Error == nil && (sameRequest.InvoiceID != invoiceID || sameRequest.Method != input.Method) {
			return ErrCommerceConflict
		}
		if foundRequest.Error != nil && !errors.Is(foundRequest.Error, gorm.ErrRecordNotFound) {
			return foundRequest.Error
		}
		found := lockForUpdate(tx).Where("invoice_id = ?", invoiceID).First(&payment)
		if found.Error == nil {
			if payment.Method != input.Method {
				return ErrInvoicePaymentActive
			}
			if payment.Status == "refunded" || invoice.Status == "cancelled" || invoice.Status == "rejected" {
				return ErrCommerceConflict
			}
			if payment.Provider == "wallet" && input.ExpectedWalletQuota != strconv.Itoa(payment.WalletQuota) {
				return ErrInvoicePaymentChanged
			}
			return nil // Both pending online orders and completed charges replay.
		}
		if !errors.Is(found.Error, gorm.ErrRecordNotFound) {
			return found.Error
		}
		if invoice.Revision != input.ExpectedRevision || invoice.Status != "awaiting_fee" || invoice.FeeMinor <= 0 || invoice.FeeReceiptReference != "" {
			return ErrCommerceConflict
		}
		config := DefaultCommerceConfig()
		if err := tx.Where("id = ?", 1).Limit(1).Find(&config).Error; err != nil {
			return err
		}
		if !config.InvoiceEnabled {
			return ErrCommerceDisabled
		}
		now := common.GetTimestamp()
		payment = InvoiceFeePayment{InvoiceID: invoiceID, UserID: userID, RequestKey: requestKey, Method: input.Method,
			TradeNo: "IF" + strings.ToUpper(common.GetRandomString(28)), Currency: invoice.Currency,
			CurrencyExponent: invoice.CurrencyExponent, AmountMinor: invoice.FeeMinor, Status: "pending", CreatedAt: now, UpdatedAt: now}
		if input.Method == "wallet" {
			quota, price, unit, err := InvoiceWalletFeeQuote(invoice.Currency, invoice.CurrencyExponent, invoice.FeeMinor)
			if err != nil {
				return err
			}
			if quota <= 0 || input.ExpectedWalletQuota != strconv.Itoa(quota) {
				return ErrInvoicePaymentChanged
			}
			payment.Provider, payment.WalletQuota, payment.WalletPrice, payment.QuotaPerUnit = "wallet", quota, price, unit
		} else {
			if invoice.Currency != "CNY" || invoice.CurrencyExponent != 2 || !operation_setting.ContainsPayMethod(input.Method) || !commerceText(merchant, 1, 128) {
				return ErrCommerceInvalid
			}
			payment.Provider, payment.MerchantScope, payment.Environment = PaymentProviderEpay, merchant, "live"
		}
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}
		if payment.Provider != "wallet" {
			return tx.Create(&CommerceAudit{Kind: "invoice", ResourceID: invoiceID, ActorID: userID, UserID: userID, Action: "fee_payment_started", FromStatus: invoice.Status, ToStatus: invoice.Status, CreatedAt: now}).Error
		}
		if _, err := DebitQuotaTx(tx, invoiceFeeDebitEntry(&payment), AccountPaid); err != nil {
			if errors.Is(err, ErrLedgerInsufficientBalance) {
				return ErrInvoiceWalletInsufficient
			}
			return err
		}
		payment.Status, payment.PaidAt = "paid", now
		if err := tx.Save(&payment).Error; err != nil {
			return err
		}
		invoice.Status, invoice.FeeReceiptReference = "submitted", "wallet:"+payment.TradeNo
		invoice.Revision++
		invoice.UpdatedAt = now
		if err := tx.Save(&invoice).Error; err != nil {
			return err
		}
		return tx.Create(&CommerceAudit{Kind: "invoice", ResourceID: invoiceID, ActorID: userID, UserID: userID, Action: "fee_paid", FromStatus: "awaiting_fee", ToStatus: "submitted", CreatedAt: now}).Error
	})
	if err != nil {
		return nil, nil, err
	}
	if payment.Provider == "wallet" {
		RefreshUserQuotaCache(userID, 0)
	}
	invoice, err := GetInvoiceApplication(ctx, userID, invoiceID, false)
	return invoice, &payment, err
}

// SettleInvoiceEpayFee accepts only verified provider facts. It also records
// payments arriving after cancellation as refundable liabilities.
func SettleInvoiceEpayFee(ctx context.Context, tradeNo, method string, facts VerifiedPaymentFacts) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var identity InvoiceFeePayment
		if err := tx.Select("invoice_id", "user_id").Where("trade_no = ?", tradeNo).First(&identity).Error; err != nil {
			return err
		}
		var owner User
		if err := lockForUpdate(tx.Unscoped()).Select("id").First(&owner, identity.UserID).Error; err != nil {
			return err
		}
		var invoice InvoiceApplication
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", identity.InvoiceID, identity.UserID).First(&invoice).Error; err != nil {
			return err
		}
		var payment InvoiceFeePayment
		if err := lockForUpdate(tx).Where("invoice_id = ? AND trade_no = ?", invoice.ID, tradeNo).First(&payment).Error; err != nil {
			return err
		}
		if payment.Provider != PaymentProviderEpay || payment.Method != method || facts.Provider != payment.Provider || facts.MerchantScope != payment.MerchantScope || facts.Environment != payment.Environment || facts.Currency != payment.Currency || facts.CurrencyExponent != payment.CurrencyExponent || facts.PaidMinor != payment.AmountMinor || payment.AmountMinor != invoice.FeeMinor || payment.Currency != invoice.Currency || payment.CurrencyExponent != invoice.CurrencyExponent {
			return ErrPaymentReceiptConflict
		}
		now := common.GetTimestamp()
		receipt, err := ClaimPaymentReceiptTx(tx, &PaymentReceipt{Provider: facts.Provider, ProviderReference: facts.ExternalReference,
			MerchantScope: facts.MerchantScope, Environment: facts.Environment, Purpose: "invoice_fee", LocalOrderKey: payment.TradeNo,
			OwnerUserID: payment.UserID, Currency: payment.Currency, CurrencyExponent: payment.CurrencyExponent,
			PaidMinor: payment.AmountMinor, PaidAt: now, VerifiedAt: now})
		if err != nil {
			return err
		}
		if payment.Status == "paid" || payment.Status == "refunded" {
			if payment.PaymentReceiptID != receipt.ID || payment.ProviderReference != facts.ExternalReference {
				return ErrPaymentReceiptConflict
			}
			return nil
		}
		if payment.Status != "pending" || invoice.FeeReceiptReference != "" || (invoice.Status != "awaiting_fee" && invoice.Status != "cancelled" && invoice.Status != "rejected") {
			return ErrCommerceConflict
		}
		before := invoice.Status
		payment.Status, payment.PaymentReceiptID, payment.ProviderReference = "paid", receipt.ID, facts.ExternalReference
		payment.PaidAt, payment.UpdatedAt = now, now
		invoice.FeeReceiptReference = "epay:" + payment.TradeNo
		if invoice.Status == "awaiting_fee" {
			invoice.Status = "submitted"
		} else {
			invoice.FeeRefundStatus, payment.RefundStatus = "pending", "pending"
		}
		invoice.Revision++
		invoice.UpdatedAt = now
		if err := tx.Save(&payment).Error; err != nil {
			return err
		}
		if err := tx.Save(&invoice).Error; err != nil {
			return err
		}
		return tx.Create(&CommerceAudit{Kind: "invoice", ResourceID: invoice.ID, UserID: invoice.UserID, Action: "fee_paid", FromStatus: before, ToStatus: invoice.Status, CreatedAt: now}).Error
	})
}

func guardInvoiceFeeReviewTx(tx *gorm.DB, invoice *InvoiceApplication, input CommerceReview) error {
	var payment InvoiceFeePayment
	err := lockForUpdate(tx).Where("invoice_id = ?", invoice.ID).First(&payment).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // Legacy manually recorded receipts keep their existing path.
	}
	if err != nil {
		return err
	}
	if input.Action == "confirm_fee" || input.Action == "record_fee_refund" && payment.Provider == "wallet" {
		return ErrInvoicePaymentActive
	}
	return nil
}

// The caller owns the owner/application locks and saves the invoice and audit in
// this transaction. Refund the original paid account and frozen quota exactly.
func refundInvoiceWalletFeeTx(tx *gorm.DB, invoice *InvoiceApplication, actorID int) error {
	if invoice.Status != "cancelled" && invoice.Status != "rejected" {
		return nil
	}
	var payment InvoiceFeePayment
	err := lockForUpdate(tx).Where("invoice_id = ?", invoice.ID).First(&payment).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil || payment.Status != "paid" {
		return err
	}
	if payment.Provider != "wallet" {
		payment.RefundStatus, payment.RefundReference = invoice.FeeRefundStatus, invoice.FeeRefundReference
		if invoice.FeeRefundStatus == "refunded" {
			payment.Status, payment.RefundedAt = "refunded", common.GetTimestamp()
		}
		payment.UpdatedAt = common.GetTimestamp()
		return tx.Save(&payment).Error
	}
	if payment.WalletQuota <= 0 || payment.WalletQuota > common.MaxWalletQuota || payment.UserID != invoice.UserID || payment.AmountMinor != invoice.FeeMinor {
		return ErrLedgerInvalidEntry
	}
	entry := invoiceFeeDebitEntry(&payment)
	operation, rows, err := loadLedgerOperation(tx, entry.IdempotencyKey)
	if err != nil {
		return err
	}
	if !verifyReplay(operation, entry, ledgerOpDebit) || len(rows) != 1 || rows[0].Account != string(AccountPaid) || rows[0].Delta != -payment.WalletQuota {
		return ErrLedgerInvalidEntry
	}
	if _, err := CreditQuotaTx(tx, LedgerEntry{UserId: payment.UserID, Account: AccountPaid, Delta: payment.WalletQuota,
		Reason: "invoice_fee_refund", ReferenceType: "invoice_fee", ReferenceId: payment.TradeNo,
		OperatorId: actorID, IdempotencyKey: "invoice_fee_refund:" + payment.TradeNo}); err != nil {
		return err
	}
	payment.Status, payment.RefundStatus, payment.RefundReference = "refunded", "refunded", "wallet:"+payment.TradeNo
	payment.RefundedAt, payment.UpdatedAt = common.GetTimestamp(), common.GetTimestamp()
	invoice.FeeRefundStatus, invoice.FeeRefundReference = "refunded", payment.RefundReference
	return tx.Save(&payment).Error
}
