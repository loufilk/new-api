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

var ErrAgentOutboundPaymentConflict = errors.New("Outgoing payment evidence is already assigned or does not match this business number")

type AgentOutboundPaymentReceipt struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	ReceiptKey        string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	Provider          string `json:"provider" gorm:"type:varchar(32);not null"`
	ProviderReference string `json:"-" gorm:"type:varchar(255);not null"`
	MerchantScope     string `json:"merchant_scope" gorm:"type:varchar(128);not null"`
	Environment       string `json:"environment" gorm:"type:varchar(16);not null"`
	Purpose           string `json:"purpose" gorm:"type:varchar(24);not null"`
	BusinessKey       string `json:"business_key" gorm:"type:varchar(128);not null;uniqueIndex"`
	OwnerUserID       int    `json:"owner_user_id" gorm:"not null;index"`
	Currency          string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent  int    `json:"currency_exponent"`
	PaidMinor         int64  `json:"paid_minor,string" gorm:"not null"`
	VerifiedBy        int    `json:"verified_by" gorm:"not null"`
	EvidenceHash      string `json:"evidence_hash" gorm:"type:char(64);not null"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentOutboundPaymentReceipt) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentOutboundPaymentReceipt) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
func ClaimAgentOutboundPaymentReceiptTx(tx *gorm.DB, receipt *AgentOutboundPaymentReceipt) (*AgentOutboundPaymentReceipt, error) {
	if receipt == nil || (receipt.Purpose != "purchase_refund" && receipt.Purpose != "withdrawal") || receipt.OwnerUserID <= 0 || receipt.VerifiedBy <= 0 || receipt.PaidMinor <= 0 || receipt.PaidMinor > int64(common.MaxWalletQuota) || !validAgentCurrency(receipt.Currency, receipt.CurrencyExponent) || (receipt.Environment != "live" && receipt.Environment != "test") {
		return nil, ErrAgentOutboundPaymentConflict
	}
	switch receipt.Provider {
	case PaymentProviderStripe, PaymentProviderEpay, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake, "bank_transfer":
	default:
		return nil, ErrAgentOutboundPaymentConflict
	}
	if receipt.Provider == PaymentProviderStripe {
		if receipt.Purpose == "purchase_refund" && !strings.HasPrefix(receipt.ProviderReference, "re_") {
			return nil, ErrAgentOutboundPaymentConflict
		}
		if receipt.Purpose == "withdrawal" && !strings.HasPrefix(receipt.ProviderReference, "po_") && !strings.HasPrefix(receipt.ProviderReference, "tr_") {
			return nil, ErrAgentOutboundPaymentConflict
		}
	}
	for _, field := range []struct {
		value string
		limit int
	}{{receipt.ProviderReference, 255}, {receipt.MerchantScope, 128}, {receipt.BusinessKey, 128}} {
		if field.value == "" || len(field.value) > field.limit || strings.TrimSpace(field.value) != field.value {
			return nil, ErrAgentOutboundPaymentConflict
		}
		for _, ch := range field.value {
			if ch < 32 || ch == 127 {
				return nil, ErrAgentOutboundPaymentConflict
			}
		}
	}
	decoded, err := hex.DecodeString(receipt.EvidenceHash)
	if err != nil || len(decoded) != 32 {
		return nil, ErrAgentOutboundPaymentConflict
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
	var existing AgentOutboundPaymentReceipt
	if err := tx.Where("receipt_key = ?", copy.ReceiptKey).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentOutboundPaymentConflict
		}
		return nil, err
	}
	if existing.Provider != copy.Provider || existing.ProviderReference != copy.ProviderReference || existing.MerchantScope != copy.MerchantScope || existing.Environment != copy.Environment || existing.Purpose != copy.Purpose || existing.BusinessKey != copy.BusinessKey || existing.OwnerUserID != copy.OwnerUserID || existing.Currency != copy.Currency || existing.CurrencyExponent != copy.CurrencyExponent || existing.PaidMinor != copy.PaidMinor {
		return nil, ErrAgentOutboundPaymentConflict
	}
	return &existing, nil
}
