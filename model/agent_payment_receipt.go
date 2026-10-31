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

var ErrAgentPaymentReceiptConflict = errors.New("Payment evidence is already assigned or does not match this order")

// AgentPaymentReceipt is the shared payment-purpose registry, not a balance.
// Only a verified provider callback or independent financial review can claim
// it. ProviderReference is the provider's stable financial transaction ID, NOT
// a webhook event, internal order ID, user-entered nickname or random request ID.
// Uniqueness deliberately excludes merchant/environment aliases: relabeling the
// same provider payment must never make it reusable for another purpose.
type AgentPaymentReceipt struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	ReceiptKey        string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	Provider          string `json:"provider" gorm:"type:varchar(32);not null"`
	ProviderReference string `json:"-" gorm:"type:varchar(255);not null"`
	MerchantScope     string `json:"merchant_scope" gorm:"type:varchar(128);not null"`
	Environment       string `json:"environment" gorm:"type:varchar(16);not null"`
	Purpose           string `json:"purpose" gorm:"type:varchar(24);not null;uniqueIndex:idx_agent_payment_purpose_order,priority:1"`
	LocalOrderKey     string `json:"local_order_key" gorm:"type:varchar(128);not null;uniqueIndex:idx_agent_payment_purpose_order,priority:2"`
	OwnerUserID       int    `json:"owner_user_id" gorm:"not null;index"`
	Currency          string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent  int    `json:"currency_exponent" gorm:"not null"`
	PaidMinor         int64  `json:"paid_minor,string" gorm:"not null"`
	PaidAt            int64  `json:"paid_at" gorm:"not null"`
	VerifiedAt        int64  `json:"verified_at" gorm:"not null"`
	VerifiedBy        int    `json:"verified_by"`
	EvidenceHash      string `json:"evidence_hash" gorm:"type:varchar(64)"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentPaymentReceipt) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentPaymentReceipt) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

func ValidateAgentPaymentReceipt(receipt *AgentPaymentReceipt) error {
	if receipt == nil {
		return ErrAgentPaymentReceiptConflict
	}
	switch receipt.Provider {
	case PaymentProviderStripe, PaymentProviderEpay, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake, "bank_transfer":
	default:
		return ErrAgentPaymentReceiptConflict
	}
	if receipt.Provider == PaymentProviderStripe && !strings.HasPrefix(receipt.ProviderReference, "pi_") {
		return ErrAgentPaymentReceiptConflict
	}
	if receipt.Purpose != "topup" && receipt.Purpose != "purchase" {
		return ErrAgentPaymentReceiptConflict
	}
	if receipt.Environment != "live" && receipt.Environment != "test" {
		return ErrAgentPaymentReceiptConflict
	}
	if receipt.OwnerUserID <= 0 || receipt.PaidMinor <= 0 || receipt.PaidMinor > int64(common.MaxWalletQuota) || !validAgentCurrency(receipt.Currency, receipt.CurrencyExponent) || receipt.VerifiedAt <= 0 || receipt.PaidAt <= 0 || receipt.PaidAt > receipt.VerifiedAt {
		return ErrAgentPaymentReceiptConflict
	}
	for _, value := range []struct {
		value string
		max   int
	}{{receipt.ProviderReference, 255}, {receipt.MerchantScope, 128}, {receipt.LocalOrderKey, 128}} {
		if value.value == "" || len(value.value) > value.max || strings.TrimSpace(value.value) != value.value {
			return ErrAgentPaymentReceiptConflict
		}
		for _, ch := range value.value {
			if ch < 32 || ch == 127 {
				return ErrAgentPaymentReceiptConflict
			}
		}
	}
	if receipt.EvidenceHash != "" {
		if decoded, err := hex.DecodeString(receipt.EvidenceHash); err != nil || len(decoded) != 32 {
			return ErrAgentPaymentReceiptConflict
		}
	}
	return nil
}

// ClaimAgentPaymentReceiptTx atomically excludes double-purpose and double-order
// credit. A replay must identify the SAME local order and the SAME economic facts.
// Callers must not suppress an error: the corresponding principal credit and
// event write belong in this transaction and must roll back with the receipt.
func ClaimAgentPaymentReceiptTx(tx *gorm.DB, receipt *AgentPaymentReceipt) (*AgentPaymentReceipt, error) {
	if err := ValidateAgentPaymentReceipt(receipt); err != nil {
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
	var existing AgentPaymentReceipt
	if err := tx.Where("receipt_key = ?", copy.ReceiptKey).First(&existing).Error; err != nil {
		return nil, ErrAgentPaymentReceiptConflict
	}
	if existing.Provider != copy.Provider || existing.ProviderReference != copy.ProviderReference || existing.MerchantScope != copy.MerchantScope || existing.Environment != copy.Environment || existing.Purpose != copy.Purpose || existing.LocalOrderKey != copy.LocalOrderKey || existing.OwnerUserID != copy.OwnerUserID || existing.Currency != copy.Currency || existing.CurrencyExponent != copy.CurrencyExponent || existing.PaidMinor != copy.PaidMinor {
		return nil, ErrAgentPaymentReceiptConflict
	}
	return &existing, nil
}
