package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Refund events carry a monotonic cumulative amount from a verified provider
// or an independently audited reconciliation record. They record an already
// observed refund; this module never sends a refund or payment to a provider.
type AgentCommissionRefundFact struct {
	ID                      int64  `json:"id" gorm:"primaryKey"`
	EventKey                string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	TopUpReferenceID        string `json:"topup_reference_id" gorm:"type:varchar(255);not null;index"`
	Provider                string `json:"provider" gorm:"type:varchar(64);not null"`
	Environment             string `json:"environment" gorm:"type:varchar(32);not null"`
	MerchantScope           string `json:"merchant_scope" gorm:"type:varchar(128);not null"`
	PaymentReference        string `json:"payment_reference" gorm:"type:varchar(255);not null"`
	RefundReference         string `json:"refund_reference" gorm:"type:varchar(255);not null"`
	Currency                string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent        int    `json:"currency_exponent" gorm:"not null"`
	CumulativeRefundedMinor int64  `json:"cumulative_refunded_minor,string" gorm:"not null"`
	EvidenceReference       string `json:"evidence_reference" gorm:"type:varchar(255);not null"`
	RecordedBy              int    `json:"recorded_by" gorm:"not null"`
	CreatedAt               int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCommissionRefundFact) BeforeUpdate(*gorm.DB) error { return ErrAgentCommissionImmutable }
func (*AgentCommissionRefundFact) BeforeDelete(*gorm.DB) error { return ErrAgentCommissionImmutable }

func RecordAgentCommissionRefundTx(tx *gorm.DB, input AgentCommissionRefundFact) (*AgentCommissionRefundFact, error) {
	if tx == nil || !validAgentCurrency(input.Currency, input.CurrencyExponent) || input.CumulativeRefundedMinor < 0 || input.CumulativeRefundedMinor > MaxAgentMoneyMinor || input.TopupReferenceMissing() || strings.TrimSpace(input.RefundReference) == "" || len(input.RefundReference) > 255 || len(input.EvidenceReference) > 255 || strings.TrimSpace(input.EvidenceReference) == "" {
		return nil, ErrAgentCommissionInvalid
	}
	key, err := agentCommissionHash([]string{input.Provider, input.Environment, input.MerchantScope, input.RefundReference})
	if err != nil {
		return nil, err
	}
	input.ID, input.CreatedAt, input.EventKey = 0, 0, key
	var existing AgentCommissionRefundFact
	err = tx.Where("event_key = ?", key).First(&existing).Error
	if err == nil {
		if existing.TopUpReferenceID != input.TopUpReferenceID || existing.Provider != input.Provider || existing.Environment != input.Environment || existing.MerchantScope != input.MerchantScope || existing.PaymentReference != input.PaymentReference || existing.Currency != input.Currency || existing.CurrencyExponent != input.CurrencyExponent || existing.CumulativeRefundedMinor != input.CumulativeRefundedMinor {
			return nil, ErrAgentCommissionConflict
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&input)
	if created.Error != nil {
		return nil, created.Error
	}
	if created.RowsAffected == 0 {
		return RecordAgentCommissionRefundTx(tx, input)
	}
	payload, err := common.Marshal(input)
	if err != nil {
		return nil, err
	}
	event := AgentCommissionOutbox{EventKey: key, Kind: "refund", ReferenceID: input.TopUpReferenceID, Route: "cash", Payload: string(payload), Status: "pending", NextAttemptAt: common.GetTimestamp()}
	if err := tx.Create(&event).Error; err != nil {
		return nil, err
	}
	return &input, nil
}

func (f AgentCommissionRefundFact) TopupReferenceMissing() bool {
	return strings.TrimSpace(f.TopUpReferenceID) == "" || len(f.TopUpReferenceID) > 255 || strings.TrimSpace(f.Provider) == "" || len(f.Provider) > 64 || strings.TrimSpace(f.PaymentReference) == "" || len(f.PaymentReference) > 255 || len(f.Environment) > 32 || len(f.MerchantScope) > 128
}

func reverseAgentCommissionEventTx(tx *gorm.DB, event *AgentCommissionOutbox) (string, error) {
	var fact AgentCommissionRefundFact
	if err := common.UnmarshalJsonStr(event.Payload, &fact); err != nil {
		return "", err
	}
	var paymentEvent AgentCommissionOutbox
	if err := tx.Where("kind = ? AND reference_id = ?", "payment", fact.TopUpReferenceID).First(&paymentEvent).Error; err != nil {
		return "", err
	}
	if paymentEvent.Status != "done" {
		return "", errors.New("commission payment event must be processed before refund")
	}
	if paymentEvent.Route != "cash" {
		return "legacy_not_cashable", nil
	}
	var snapshot agentCommissionPaymentSnapshot
	if err := common.UnmarshalJsonStr(paymentEvent.Payload, &snapshot); err != nil {
		return "", err
	}
	payment := snapshot.Payment
	if payment.Provider != fact.Provider || payment.Environment != fact.Environment || payment.MerchantScope != fact.MerchantScope || payment.ExternalReference != fact.PaymentReference || payment.Currency != fact.Currency || payment.CurrencyExponent != fact.CurrencyExponent || fact.CumulativeRefundedMinor > payment.PaidMinor {
		return "", ErrAgentCommissionConflict
	}
	var award AgentCommissionAward
	err := tx.Where("event_id = ?", paymentEvent.ID).First(&award).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "no_original_cash_award", nil
	}
	if err != nil {
		return "", err
	}
	account, err := lockAgentCommissionAccount(tx, award.UserID, award.Currency, award.CurrencyExponent)
	if err != nil {
		return "", err
	}
	if err := lockForUpdate(tx).First(&award, award.ID).Error; err != nil {
		return "", err
	}
	if fact.CumulativeRefundedMinor <= award.RefundedMinor {
		return "refund_duplicate_or_stale", nil
	}
	target, err := agentMinorFraction(award.RewardMinor, fact.CumulativeRefundedMinor, award.PaidMinor)
	if err != nil {
		return "", err
	}
	if target > award.RewardMinor || target < award.ReversedMinor {
		return "", ErrAgentCommissionInvariant
	}
	reverse := target - award.ReversedMinor
	before := *account
	remaining := reverse
	take := min(remaining, award.PendingMinor)
	award.PendingMinor -= take
	account.PendingMinor -= take
	remaining -= take
	take = min(remaining, award.AvailableMinor)
	award.AvailableMinor -= take
	account.AvailableMinor -= take
	remaining -= take
	take = min(remaining, award.ReservedMinor)
	award.ReservedMinor -= take
	account.ReservedMinor -= take
	remaining -= take
	// Already paid, converted, or reserved for an uncertain payout cannot be
	// clawed back by mutating its hold. Preserve that independent obligation and
	// create a separate recovery debt. Future value transfers are blocked.
	if remaining > 0 {
		if account.DebtMinor > MaxAgentMoneyMinor-remaining {
			return "", ErrAgentCommissionInvariant
		}
		debt := AgentCommissionDebt{UserID: award.UserID, AwardID: award.ID, EventID: event.ID, Currency: award.Currency, AmountMinor: remaining}
		if err := tx.Create(&debt).Error; err != nil {
			return "", err
		}
		account.DebtMinor += remaining
	}
	account.ReversedMinor += reverse
	award.RefundedMinor = fact.CumulativeRefundedMinor
	award.ReversedMinor = target
	award.Version++
	if err := tx.Save(&award).Error; err != nil {
		return "", err
	}
	if err := saveAgentCommissionAccount(tx, before, account, "refund", "event", fmt.Sprint(event.ID), fact.RefundReference, fact.RecordedBy); err != nil {
		return "", err
	}
	return "cash_reversed", nil
}

// AgentVerifiedProviderRefund is accepted only after provider signature
// verification. It deliberately has no caller-selected local order or user.
// ChargePaidMinor must equal the immutable original receipt; charge-local
// cumulative amounts must never be guessed across unrelated payment attempts.
type AgentVerifiedProviderRefund struct {
	Provider                string `json:"provider"`
	EventReference          string `json:"event_reference"`
	PaymentReference        string `json:"payment_reference"`
	MerchantScope           string `json:"merchant_scope"`
	Environment             string `json:"environment"`
	Currency                string `json:"currency"`
	CurrencyExponent        int    `json:"currency_exponent"`
	ChargePaidMinor         int64  `json:"charge_paid_minor,string"`
	CumulativeRefundedMinor int64  `json:"cumulative_refunded_minor,string"`
}

func EnqueueAgentCommissionProviderRefundTx(tx *gorm.DB, input AgentVerifiedProviderRefund) error {
	if input.Provider != PaymentProviderStripe || !strings.HasPrefix(input.EventReference, "evt_") || !strings.HasPrefix(input.PaymentReference, "pi_") || len(input.EventReference) > 255 || len(input.PaymentReference) > 255 || input.MerchantScope == "" || len(input.MerchantScope) > 128 || (input.Environment != "live" && input.Environment != "test") || !validAgentCurrency(input.Currency, input.CurrencyExponent) || input.ChargePaidMinor <= 0 || input.ChargePaidMinor > MaxAgentMoneyMinor || input.CumulativeRefundedMinor <= 0 || input.CumulativeRefundedMinor > input.ChargePaidMinor {
		return ErrAgentCommissionInvalid
	}
	key, err := agentCommissionHash([]string{"provider_refund", input.Provider, input.EventReference})
	if err != nil {
		return err
	}
	payload, err := common.Marshal(input)
	if err != nil {
		return err
	}
	event := AgentCommissionOutbox{EventKey: key, Kind: "provider_refund", ReferenceID: input.EventReference, Route: "cash", Payload: string(payload), Status: "pending", NextAttemptAt: common.GetTimestamp()}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var existing AgentCommissionOutbox
	if err := tx.Where("event_key = ?", key).First(&existing).Error; err != nil {
		return err
	}
	if existing.Kind != event.Kind || existing.Payload != event.Payload {
		return ErrAgentCommissionConflict
	}
	return nil
}

func resolveAgentCommissionProviderRefundTx(tx *gorm.DB, event *AgentCommissionOutbox) (string, error) {
	var fact AgentVerifiedProviderRefund
	if err := common.UnmarshalJsonStr(event.Payload, &fact); err != nil {
		return "", err
	}
	var receipt AgentPaymentReceipt
	if err := tx.Where("provider = ? AND provider_reference = ?", fact.Provider, fact.PaymentReference).First(&receipt).Error; err != nil {
		// A verified refund may precede checkout fulfillment. Keep the durable
		// event retryable rather than dropping it or inventing historic cash rights.
		return "", err
	}
	if receipt.MerchantScope != fact.MerchantScope || receipt.Environment != fact.Environment || receipt.Currency != fact.Currency || receipt.CurrencyExponent != fact.CurrencyExponent || receipt.PaidMinor != fact.ChargePaidMinor || fact.CumulativeRefundedMinor > receipt.PaidMinor {
		return "", ErrAgentCommissionConflict
	}
	if receipt.Purpose != "topup" {
		return "non_topup_refund", nil
	}
	_, err := RecordAgentCommissionRefundTx(tx, AgentCommissionRefundFact{TopUpReferenceID: receipt.LocalOrderKey, Provider: receipt.Provider, Environment: receipt.Environment, MerchantScope: receipt.MerchantScope, PaymentReference: receipt.ProviderReference, RefundReference: fact.EventReference, Currency: receipt.Currency, CurrencyExponent: receipt.CurrencyExponent, CumulativeRefundedMinor: fact.CumulativeRefundedMinor, EvidenceReference: "verified:stripe:" + fact.EventReference})
	if err != nil {
		return "", err
	}
	return "verified_refund_queued", nil
}
