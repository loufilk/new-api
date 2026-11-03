package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrAgentDepositInvalid = errors.New("Invalid agent deposit request")
var ErrAgentDepositInvariant = errors.New("Agent deposit liability reconciliation failed")

type AgentDepositAccount struct {
	AgentUserID      int    `json:"agent_user_id" gorm:"primaryKey;autoIncrement:false"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent"`
	ReceivedMinor    int64  `json:"received_minor,string"`
	AvailableMinor   int64  `json:"available_minor,string"`
	HeldMinor        int64  `json:"held_minor,string"`
	OffsetMinor      int64  `json:"offset_minor,string"`
	RefundedMinor    int64  `json:"refunded_minor,string"`
	Version          int64  `json:"version"`
	UpdatedAt        int64  `json:"updated_at" gorm:"autoUpdateTime"`
}
type AgentDepositReceipt struct {
	PayerReference   string `json:"-" gorm:"type:varchar(128)"`
	PayerDigest      string `json:"payer_digest" gorm:"type:varchar(64)"`
	ID               int64  `json:"id" gorm:"primaryKey"`
	AgentUserID      int    `json:"agent_user_id" gorm:"index"`
	PaymentReceiptID int64  `json:"payment_receipt_id" gorm:"not null;uniqueIndex"`
	AmountMinor      int64  `json:"amount_minor,string"`
	AvailableMinor   int64  `json:"available_minor,string"`
	HeldMinor        int64  `json:"held_minor,string"`
	OffsetMinor      int64  `json:"offset_minor,string"`
	RefundedMinor    int64  `json:"refunded_minor,string"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent"`
	RecordedBy       int    `json:"recorded_by"`
	ConfirmedBy      int    `json:"confirmed_by"`
	State            string `json:"state" gorm:"type:varchar(32);not null"`
	PolicyVersion    int64  `json:"policy_version"`
	Version          int64  `json:"version"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
}
type AgentDepositOperationRequest struct {
	SubmittedAt        int64                            `json:"submitted_at"`
	ResolvedAt         int64                            `json:"resolved_at"`
	RecipientDigest    string                           `json:"recipient_digest" gorm:"type:varchar(64)"`
	RecipientVersion   int64                            `json:"recipient_version"`
	Allocations        []AgentReceivableAllocationInput `json:"allocations" gorm:"-"`
	ID                 int64                            `json:"id" gorm:"primaryKey"`
	AgentUserID        int                              `json:"agent_user_id" gorm:"index"`
	DepositID          int64                            `json:"deposit_id" gorm:"index"`
	Kind               string                           `json:"kind" gorm:"type:varchar(24);not null"`
	AmountMinor        int64                            `json:"amount_minor,string"`
	Currency           string                           `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent   int                              `json:"currency_exponent"`
	State              string                           `json:"state" gorm:"type:varchar(32);not null"`
	Version            int64                            `json:"version"`
	BusinessKey        string                           `json:"business_key" gorm:"type:varchar(128);not null;uniqueIndex"`
	RequestedBy        int                              `json:"requested_by"`
	ApprovedBy         int                              `json:"approved_by"`
	SubmittedBy        int                              `json:"submitted_by"`
	ResolvedBy         int                              `json:"resolved_by"`
	PayoutReceiptID    int64                            `json:"payout_receipt_id"`
	AllocationSnapshot string                           `json:"-" gorm:"type:text"`
	Reason             string                           `json:"reason" gorm:"type:varchar(255)"`
	PolicyVersion      int64                            `json:"policy_version"`
	CreatedAt          int64                            `json:"created_at" gorm:"autoCreateTime"`
}
type AgentDepositLedger struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	EventKey         string `json:"event_key" gorm:"type:varchar(200);not null;uniqueIndex"`
	AgentUserID      int    `json:"agent_user_id" gorm:"index"`
	DepositID        int64  `json:"deposit_id" gorm:"index"`
	RequestID        int64  `json:"request_id"`
	ActorUserID      int    `json:"actor_user_id"`
	Kind             string `json:"kind" gorm:"type:varchar(32)"`
	Currency         string `json:"currency" gorm:"type:varchar(3)"`
	CurrencyExponent int    `json:"currency_exponent"`
	ReceivedDelta    int64  `json:"received_delta,string"`
	AvailableDelta   int64  `json:"available_delta,string"`
	HeldDelta        int64  `json:"held_delta,string"`
	OffsetDelta      int64  `json:"offset_delta,string"`
	RefundedDelta    int64  `json:"refunded_delta,string"`
	ReceivedAfter    int64  `json:"received_after,string"`
	AvailableAfter   int64  `json:"available_after,string"`
	HeldAfter        int64  `json:"held_after,string"`
	OffsetAfter      int64  `json:"offset_after,string"`
	RefundedAfter    int64  `json:"refunded_after,string"`
	EvidenceHash     string `json:"evidence_hash" gorm:"type:varchar(64)"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentDepositLedger) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentDepositLedger) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentDepositCommand struct {
	PayerReference     string                           `json:"payer_reference,omitempty"`
	RequestID          string                           `json:"request_id"`
	AgentUserID        int                              `json:"agent_user_id,omitempty"`
	ResourceID         int64                            `json:"resource_id,omitempty"`
	DepositID          int64                            `json:"deposit_id,omitempty"`
	Version            int64                            `json:"version,omitempty"`
	AmountMinor        string                           `json:"amount_minor,omitempty"`
	Currency           string                           `json:"currency,omitempty"`
	CurrencyExponent   int                              `json:"currency_exponent,omitempty"`
	Provider           string                           `json:"provider,omitempty"`
	MerchantScope      string                           `json:"merchant_scope,omitempty"`
	Environment        string                           `json:"environment,omitempty"`
	ExternalReference  string                           `json:"external_reference,omitempty"`
	PaidAt             int64                            `json:"paid_at,omitempty"`
	EvidenceReference  string                           `json:"evidence_reference,omitempty"`
	Reason             string                           `json:"reason,omitempty"`
	Allocations        []AgentReceivableAllocationInput `json:"allocations,omitempty"`
	BusinessKey        string                           `json:"business_key,omitempty"`
	Outcome            string                           `json:"outcome,omitempty"`
	NoPaymentConfirmed bool                             `json:"no_payment_confirmed,omitempty"`
}

func AgentDepositModels() []any {
	return []any{&AgentDepositAccount{}, &AgentDepositReceipt{}, &AgentDepositOperationRequest{}, &AgentDepositLedger{}}
}
func ValidateAgentDepositPolicy(p *AgentPolicy) error {
	if p.DepositCurrency != "" && !validAgentCurrency(p.DepositCurrency, p.DepositCurrencyExponent) {
		return ErrAgentPolicyInvalid
	}
	if p.DepositMaximumMinor < 0 || p.DepositMaximumMinor > int64(common.MaxWalletQuota) {
		return ErrAgentPolicyInvalid
	}
	if p.DepositEnabled || p.DepositOffsetEnabled || p.DepositRefundEnabled {
		if !p.DepositEnabled || !p.DepositTermsApproved || !p.RiskPolicyApproved || !validAgentCurrency(p.DepositCurrency, p.DepositCurrencyExponent) || p.DepositMaximumMinor <= 0 {
			return ErrAgentPolicyInvalid
		}
	}
	return nil
}

func (*AgentDepositReceipt) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

func (*AgentDepositOperationRequest) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
