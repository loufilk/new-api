package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const AgentFundCredit = "credit"
const AgentCreditRecognitionSourceSpend = "source_spend"

var (
	ErrAgentCreditInvalid   = errors.New("Invalid agent credit or receivable request")
	ErrAgentCreditState     = errors.New("Agent credit state changed; refresh and prepare again")
	ErrAgentCreditRisk      = errors.New("New credit is restricted by the approved limit, expiry or overdue receivables")
	ErrAgentCreditInvariant = errors.New("Agent credit or receivable reconciliation failed")
)

// No normal wallet balance is stored here. Reserved principal includes all
// unspent credit source intervals, including already-bound customer rights.
// WrittenOffMinor remains risk exposure: a non-cash write-off never renews credit.
type AgentCreditAccount struct {
	AgentUserID      int    `json:"agent_user_id" gorm:"primaryKey;autoIncrement:false"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent"`
	LimitVersionID   int64  `json:"limit_version_id"`
	ReservedMinor    int64  `json:"reserved_minor,string"`
	OutstandingMinor int64  `json:"outstanding_minor,string"`
	WrittenOffMinor  int64  `json:"written_off_minor,string"`
	Version          int64  `json:"version"`
	UpdatedAt        int64  `json:"updated_at" gorm:"autoUpdateTime"`
}
type AgentCreditLimitRequest struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	AgentUserID       int    `json:"agent_user_id" gorm:"not null;index"`
	RequestedBy       int    `json:"requested_by"`
	ReviewedBy        int    `json:"reviewed_by"`
	AmountMinor       int64  `json:"amount_minor,string"`
	Currency          string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent  int    `json:"currency_exponent"`
	TermSeconds       int64  `json:"term_seconds"`
	ExpiresAt         int64  `json:"expires_at"`
	Frozen            bool   `json:"frozen"`
	ExpectedLimitID   int64  `json:"expected_limit_id"`
	PolicyVersion     int64  `json:"policy_version"`
	EvidenceReference string `json:"-" gorm:"type:varchar(255)"`
	Reason            string `json:"reason" gorm:"type:varchar(255)"`
	State             string `json:"state" gorm:"type:varchar(32);not null"`
	Version           int64  `json:"version"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}
type AgentCreditLimitVersion struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	RequestID        int64  `json:"request_id" gorm:"not null;uniqueIndex"`
	AgentUserID      int    `json:"agent_user_id" gorm:"not null;index"`
	PreviousID       int64  `json:"previous_id"`
	AmountMinor      int64  `json:"amount_minor,string"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent"`
	TermSeconds      int64  `json:"term_seconds"`
	ExpiresAt        int64  `json:"expires_at"`
	Frozen           bool   `json:"frozen"`
	RequestedBy      int    `json:"requested_by"`
	ApprovedBy       int    `json:"approved_by"`
	PolicyVersion    int64  `json:"policy_version"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCreditLimitVersion) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCreditLimitVersion) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentCreditDraw struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	AgentUserID      int    `json:"agent_user_id" gorm:"not null;index"`
	BatchID          int    `json:"batch_id" gorm:"uniqueIndex"`
	LimitVersionID   int64  `json:"limit_version_id"`
	OriginalQuota    int    `json:"original_quota" gorm:"type:bigint"`
	PrincipalMinor   int64  `json:"principal_minor,string"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent"`
	TermSeconds      int64  `json:"term_seconds"`
	RecognitionMode  string `json:"recognition_mode" gorm:"type:varchar(32)"`
	PolicyVersion    int64  `json:"policy_version"`
	PolicySnapshot   string `json:"-" gorm:"type:text"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCreditDraw) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCreditDraw) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentCreditLedger struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	EventKey         string `json:"event_key" gorm:"type:varchar(200);not null;uniqueIndex"`
	AgentUserID      int    `json:"agent_user_id" gorm:"not null;index"`
	ActorUserID      int    `json:"actor_user_id"`
	Kind             string `json:"kind" gorm:"type:varchar(32);not null"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent"`
	ReservedDelta    int64  `json:"reserved_delta,string"`
	OutstandingDelta int64  `json:"outstanding_delta,string"`
	WrittenOffDelta  int64  `json:"written_off_delta,string"`
	ReservedAfter    int64  `json:"reserved_after,string"`
	OutstandingAfter int64  `json:"outstanding_after,string"`
	WrittenOffAfter  int64  `json:"written_off_after,string"`
	ReceivableID     int64  `json:"receivable_id"`
	DrawID           int64  `json:"draw_id"`
	EvidenceHash     string `json:"evidence_hash" gorm:"type:varchar(64)"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCreditLedger) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCreditLedger) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

// The same module receipt stores the exact committed response. Retrying a lost
// ACK never re-evaluates a newer amount or creates a second business number.
type AgentCreditOperation struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	RequestID   string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	Action      string `json:"action" gorm:"type:varchar(64);not null"`
	ActorUserID int    `json:"actor_user_id"`
	AgentUserID int    `json:"agent_user_id" gorm:"index"`
	Signature   string `json:"-" gorm:"type:varchar(64);not null"`
	Result      string `json:"-" gorm:"type:text;not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCreditOperation) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCreditOperation) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

// Integer amounts are decimal strings on the wire. Terms and recognition are
// approved explicitly; zero terms are never interpreted as a default contract.
type AgentCreditCommand struct {
	RequestID         string                           `json:"request_id"`
	AgentUserID       int                              `json:"agent_user_id,omitempty"`
	ResourceID        int64                            `json:"resource_id,omitempty"`
	Version           int64                            `json:"version,omitempty"`
	ExpectedLimitID   int64                            `json:"expected_limit_id,omitempty"`
	AmountMinor       string                           `json:"amount_minor,omitempty"`
	Quota             int                              `json:"quota,omitempty"`
	TermSeconds       int64                            `json:"term_seconds,omitempty"`
	ExpiresAt         int64                            `json:"expires_at,omitempty"`
	Frozen            bool                             `json:"frozen,omitempty"`
	RiskApproved      bool                             `json:"risk_approved,omitempty"`
	Reason            string                           `json:"reason,omitempty"`
	EvidenceReference string                           `json:"evidence_reference,omitempty"`
	Currency          string                           `json:"currency,omitempty"`
	CurrencyExponent  int                              `json:"currency_exponent,omitempty"`
	Provider          string                           `json:"provider,omitempty"`
	MerchantScope     string                           `json:"merchant_scope,omitempty"`
	Environment       string                           `json:"environment,omitempty"`
	ExternalReference string                           `json:"external_reference,omitempty"`
	PaidAt            int64                            `json:"paid_at,omitempty"`
	Allocations       []AgentReceivableAllocationInput `json:"allocations,omitempty"`
	InvoiceNumber     string                           `json:"invoice_number,omitempty"`
	AdjustmentKind    string                           `json:"adjustment_kind,omitempty"`
}

func AgentCreditModels() []any {
	return []any{&AgentCreditAudit{}, &AgentCreditAccount{}, &AgentCreditLimitRequest{}, &AgentCreditLimitVersion{}, &AgentCreditDraw{}, &AgentCreditLedger{}, &AgentCreditOperation{}, &AgentReceivable{}, &AgentReceivableInvoice{}, &AgentReceivableAdjustment{}, &AgentCreditRepayment{}, &AgentCreditRepaymentAllocation{}}
}

func ValidateAgentCreditPolicy(p *AgentPolicy) error {
	if p.CreditCurrency != "" && !validAgentCurrency(p.CreditCurrency, p.CreditCurrencyExponent) {
		return ErrAgentPolicyInvalid
	}
	for _, v := range []int64{p.CreditMaximumLimitMinor, p.CreditQuotaNumerator, p.CreditQuotaDenominator} {
		if v < 0 || v > int64(common.MaxWalletQuota) {
			return ErrAgentPolicyInvalid
		}
	}
	if p.CreditRateBPS < 0 || p.CreditRateBPS > 10000 || p.CreditTermSeconds < 0 || p.CreditMaximumTermSeconds < 0 || p.CreditMaximumTermSeconds > 10*366*86400 {
		return ErrAgentPolicyInvalid
	}
	if p.CreditInterestMode != "" && p.CreditInterestMode != "none" {
		return ErrAgentPolicyInvalid
	}
	if p.CreditRecognitionMode != "" && p.CreditRecognitionMode != AgentCreditRecognitionSourceSpend {
		return ErrAgentPolicyInvalid
	}
	if p.CreditEnabled || p.CreditSelfUseEnabled {
		if !p.RiskPolicyApproved || !p.CreditTermsApproved || p.CreditRecognitionMode != AgentCreditRecognitionSourceSpend || p.CreditInterestMode != "none" || !validAgentCurrency(p.CreditCurrency, p.CreditCurrencyExponent) || p.CreditMaximumLimitMinor <= 0 || p.CreditTermSeconds <= 0 || p.CreditMaximumTermSeconds < p.CreditTermSeconds || p.CreditQuotaNumerator <= 0 || p.CreditQuotaDenominator <= 0 || p.CreditRateBPS <= 0 {
			return ErrAgentPolicyInvalid
		}
	}
	if p.CreditSelfUseEnabled && (!p.CreditEnabled || !p.ProcurementSelfUseEnabled) {
		return ErrAgentPolicyInvalid
	}
	return nil
}

func (*AgentCreditLimitRequest) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
