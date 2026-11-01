package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrPaymentReceiptConflict = errors.New("Payment evidence is already assigned or does not match this order")

// PaymentReceipt is the shared payment-purpose registry, not a balance.
// Only a verified provider callback or independent financial review can claim
// it. ProviderReference is the provider's stable financial transaction ID, NOT
// a webhook event, internal order ID, user-entered nickname or random request ID.
// Uniqueness deliberately excludes merchant/environment aliases: relabeling the
// same provider payment must never make it reusable for another purpose.
type PaymentReceipt struct {
	ID                 int64  `json:"id" gorm:"primaryKey"`
	ReceiptKey         string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	Provider           string `json:"provider" gorm:"type:varchar(32);not null"`
	ProviderReference  string `json:"-" gorm:"type:varchar(255);not null"`
	MerchantScope      string `json:"merchant_scope" gorm:"type:varchar(128);not null"`
	Environment        string `json:"environment" gorm:"type:varchar(16);not null"`
	Purpose            string `json:"purpose" gorm:"type:varchar(24);not null;uniqueIndex:idx_agent_payment_purpose_order,priority:1"`
	LocalOrderKey      string `json:"local_order_key" gorm:"type:varchar(128);not null;uniqueIndex:idx_agent_payment_purpose_order,priority:2"`
	OwnerUserID        int    `json:"owner_user_id" gorm:"not null;index"`
	Currency           string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent   int    `json:"currency_exponent" gorm:"not null"`
	PaidMinor          int64  `json:"paid_minor,string" gorm:"not null"`
	CounterpartyDigest string `json:"counterparty_digest" gorm:"type:varchar(64)"`
	PaidAt             int64  `json:"paid_at" gorm:"not null"`
	VerifiedAt         int64  `json:"verified_at" gorm:"not null"`
	VerifiedBy         int    `json:"verified_by"`
	EvidenceHash       string `json:"evidence_hash" gorm:"type:varchar(64)"`
	CreatedAt          int64  `json:"created_at" gorm:"autoCreateTime"`
}

// The historical registry also contains other purposes. Keep its physical
// identity so an existing provider payment can never be credited a second time.
// Non-topup records remain immutable evidence, not executable business objects.
func (PaymentReceipt) TableName() string { return "agent_payment_receipts" }

func (*PaymentReceipt) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*PaymentReceipt) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

func ValidatePaymentReceipt(receipt *PaymentReceipt) error {
	if receipt == nil {
		return ErrPaymentReceiptConflict
	}
	switch receipt.Provider {
	case PaymentProviderStripe, PaymentProviderEpay, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake:
	default:
		return ErrPaymentReceiptConflict
	}
	if receipt.Provider == PaymentProviderStripe && !strings.HasPrefix(receipt.ProviderReference, "pi_") {
		return ErrPaymentReceiptConflict
	}
	if receipt.Purpose != "topup" {
		return ErrPaymentReceiptConflict
	}
	if receipt.Environment != "live" && receipt.Environment != "test" {
		return ErrPaymentReceiptConflict
	}
	if receipt.OwnerUserID <= 0 || receipt.PaidMinor <= 0 || receipt.PaidMinor > int64(common.MaxWalletQuota) || !validPaymentCurrency(receipt.Currency, receipt.CurrencyExponent) || receipt.VerifiedAt <= 0 || receipt.PaidAt <= 0 || receipt.PaidAt > receipt.VerifiedAt {
		return ErrPaymentReceiptConflict
	}
	for _, value := range []struct {
		value string
		max   int
	}{{receipt.ProviderReference, 255}, {receipt.MerchantScope, 128}, {receipt.LocalOrderKey, 128}} {
		if value.value == "" || len(value.value) > value.max || strings.TrimSpace(value.value) != value.value {
			return ErrPaymentReceiptConflict
		}
		for _, ch := range value.value {
			if ch < 32 || ch == 127 {
				return ErrPaymentReceiptConflict
			}
		}
	}
	if receipt.CounterpartyDigest != "" {
		if decoded, err := hex.DecodeString(receipt.CounterpartyDigest); err != nil || len(decoded) != 32 || strings.ToLower(receipt.CounterpartyDigest) != receipt.CounterpartyDigest {
			return ErrPaymentReceiptConflict
		}
	}
	if receipt.EvidenceHash != "" {
		if decoded, err := hex.DecodeString(receipt.EvidenceHash); err != nil || len(decoded) != 32 {
			return ErrPaymentReceiptConflict
		}
	}
	return nil
}

// ClaimPaymentReceiptTx atomically excludes double-purpose and double-order
// credit. A replay must identify the SAME local order and the SAME economic facts.
// Callers must not suppress an error: the corresponding principal credit and
// event write belong in this transaction and must roll back with the receipt.
func ClaimPaymentReceiptTx(tx *gorm.DB, receipt *PaymentReceipt) (*PaymentReceipt, error) {
	if err := ValidatePaymentReceipt(receipt); err != nil {
		return nil, err
	}
	copy := *receipt
	copy.ID = 0
	digest := sha256.Sum256([]byte(copy.Provider + "\x00" + copy.ProviderReference))
	copy.ReceiptKey = hex.EncodeToString(digest[:])
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&copy)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 1 {
		return &copy, nil
	}
	var existing PaymentReceipt
	if err := tx.Where("receipt_key = ?", copy.ReceiptKey).First(&existing).Error; err != nil {
		return nil, ErrPaymentReceiptConflict
	}
	if existing.Provider != copy.Provider || existing.ProviderReference != copy.ProviderReference || existing.MerchantScope != copy.MerchantScope || existing.Environment != copy.Environment || existing.Purpose != copy.Purpose || existing.LocalOrderKey != copy.LocalOrderKey || existing.OwnerUserID != copy.OwnerUserID || existing.Currency != copy.Currency || existing.CurrencyExponent != copy.CurrencyExponent || existing.PaidMinor != copy.PaidMinor || existing.CounterpartyDigest != copy.CounterpartyDigest {
		return nil, ErrPaymentReceiptConflict
	}
	return &existing, nil
}

// PaymentCurrencyExponent pins provider minor units; never guess an unknown currency.
func PaymentCurrencyExponent(currency string) (int, bool) {
	switch currency {
	case "USD", "CNY", "EUR", "GBP", "HKD", "CAD", "AUD", "SGD", "NZD", "CHF":
		return 2, true
	case "JPY", "KRW":
		return 0, true
	case "BHD", "JOD", "KWD", "OMR", "TND":
		return 3, true
	default:
		return 0, false
	}
}
func validPaymentCurrency(currency string, exponent int) bool {
	expected, ok := PaymentCurrencyExponent(currency)
	return ok && exponent == expected
}
