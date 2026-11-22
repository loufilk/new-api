package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrAgentGovernanceInvalid   = errors.New("Invalid agent governance request")
	ErrAgentGovernanceConflict  = errors.New("Agent governance changed; reload and try again")
	ErrAgentGovernanceReview    = errors.New("Independent agent governance review is required")
	ErrAgentGovernanceRequired  = errors.New("Current agent governance approval is required for new business")
	ErrAgentGovernanceRiskLimit = errors.New("Agent gross daily risk limit exceeded")
)

// Governance is qualification evidence and an operational risk budget, never
// money, a login role, or authority to revoke existing customer rights.
type AgentGovernance struct {
	UserID                 int                       `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Version                int64                     `json:"version" gorm:"not null"`
	Status                 string                    `json:"status" gorm:"type:varchar(24);not null"`
	SubmissionID           int64                     `json:"submission_id" gorm:"not null"`
	IdentityVerified       bool                      `json:"identity_verified" gorm:"not null"`
	AgreementVerified      bool                      `json:"agreement_verified" gorm:"not null"`
	Exceptional            bool                      `json:"exceptional" gorm:"not null"`
	ReviewedBy             int                       `json:"reviewed_by" gorm:"not null"`
	ReviewedAt             int64                     `json:"reviewed_at" gorm:"not null"`
	ReviewDueAt            int64                     `json:"review_due_at" gorm:"not null"`
	LastBusinessActivityAt int64                     `json:"last_business_activity_at" gorm:"not null"`
	RiskLimits             AgentGovernanceRiskLimits `json:"risk_limits" gorm:"embedded;embeddedPrefix:risk_"`
	CreatedAt              int64                     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt              int64                     `json:"updated_at" gorm:"autoUpdateTime"`
}
type AgentGovernanceRiskLimits struct {
	Enabled            bool  `json:"enabled" gorm:"not null"`
	DailyIssuanceQuota int64 `json:"daily_issuance_quota" gorm:"not null"`
	DailyTransferQuota int64 `json:"daily_transfer_quota" gorm:"not null"`
}

// Only opaque dossier references are retained. No identity document/image,
// identity number, bank account, credential, or authentication token is stored.
type AgentGovernanceSubmission struct {
	ID                       int64  `json:"id" gorm:"primaryKey"`
	UserID                   int    `json:"user_id" gorm:"not null;uniqueIndex:idx_agent_gov_submission_version,priority:1"`
	Version                  int64  `json:"version" gorm:"not null;uniqueIndex:idx_agent_gov_submission_version,priority:2"`
	IdentityDossierReference string `json:"-" gorm:"type:varchar(128);not null"`
	IdentityDossierDigest    string `json:"identity_dossier_digest" gorm:"type:char(64);not null"`
	AgreementReference       string `json:"-" gorm:"type:varchar(128);not null"`
	AgreementReferenceDigest string `json:"agreement_reference_digest" gorm:"type:char(64);not null"`
	AgreementDigest          string `json:"agreement_digest" gorm:"type:char(64);not null"`
	AcceptTerms              bool   `json:"accept_terms" gorm:"not null"`
	ConsentedAt              int64  `json:"consented_at" gorm:"not null"`
	RequestID                string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
}

func (*AgentGovernanceSubmission) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentGovernanceSubmission) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentGovernanceAudit struct {
	ID                 int64                     `json:"id" gorm:"primaryKey"`
	UserID             int                       `json:"user_id" gorm:"not null;uniqueIndex:idx_agent_gov_audit_version,priority:1"`
	Version            int64                     `json:"version" gorm:"not null;uniqueIndex:idx_agent_gov_audit_version,priority:2"`
	ProfileVersion     int64                     `json:"profile_version" gorm:"not null"`
	SubmissionID       int64                     `json:"submission_id" gorm:"not null"`
	RequestID          string                    `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	Action             string                    `json:"action" gorm:"type:varchar(32);not null"`
	ActorUserID        int                       `json:"actor_user_id" gorm:"not null"`
	PreviousStatus     string                    `json:"previous_status" gorm:"type:varchar(24);not null"`
	Status             string                    `json:"status" gorm:"type:varchar(24);not null"`
	IdentityVerified   bool                      `json:"identity_verified" gorm:"not null"`
	AgreementVerified  bool                      `json:"agreement_verified" gorm:"not null"`
	Exceptional        bool                      `json:"exceptional" gorm:"not null"`
	ExceptionReference string                    `json:"-" gorm:"type:varchar(128);not null"`
	ExceptionDigest    string                    `json:"exception_digest" gorm:"type:varchar(64);not null"`
	ReviewDueAt        int64                     `json:"review_due_at" gorm:"not null"`
	RiskLimits         AgentGovernanceRiskLimits `json:"risk_limits" gorm:"embedded;embeddedPrefix:risk_"`
	Reason             string                    `json:"reason" gorm:"type:varchar(255);not null"`
	PolicyVersion      int64                     `json:"policy_version" gorm:"not null"`
	Signature          string                    `json:"-" gorm:"type:char(64);not null"`
	Result             string                    `json:"-" gorm:"type:text;not null"`
	CreatedAt          int64                     `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentGovernanceAudit) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentGovernanceAudit) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

// Usage is append-only gross volume. Cancel/refund NEVER subtracts usage.
// An exact replay keeps its original day, even when retried after midnight.
type AgentGovernanceRiskUsage struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	UserID            int    `json:"user_id" gorm:"not null;uniqueIndex:idx_agent_gov_risk_op,priority:1;index:idx_agent_gov_risk_day,priority:1"`
	RequestID         string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex:idx_agent_gov_risk_op,priority:2"`
	Kind              string `json:"kind" gorm:"type:varchar(16);not null;uniqueIndex:idx_agent_gov_risk_op,priority:3;index:idx_agent_gov_risk_day,priority:2"`
	DayStartUTC       int64  `json:"day_start_utc" gorm:"not null;index:idx_agent_gov_risk_day,priority:3"`
	Quota             int64  `json:"quota" gorm:"not null"`
	GovernanceVersion int64  `json:"governance_version" gorm:"not null"`
	PolicyVersion     int64  `json:"policy_version" gorm:"not null"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentGovernanceRiskUsage) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentGovernanceRiskUsage) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

func AgentGovernanceModels() []any {
	return []any{&AgentGovernance{}, &AgentGovernanceSubmission{}, &AgentGovernanceAudit{}, &AgentGovernanceRiskUsage{}}
}

type AgentGovernanceCommand struct {
	RequestID                string                    `json:"request_id"`
	UserID                   int                       `json:"user_id,omitempty"`
	ExpectedVersion          int64                     `json:"expected_version"`
	ExpectedProfileVersion   int64                     `json:"expected_profile_version,omitempty"`
	IdentityDossierReference string                    `json:"identity_dossier_reference,omitempty"`
	AgreementReference       string                    `json:"agreement_reference,omitempty"`
	AgreementDigest          string                    `json:"agreement_digest,omitempty"`
	AcceptTerms              bool                      `json:"accept_terms,omitempty"`
	Decision                 string                    `json:"decision,omitempty"`
	IdentityVerified         bool                      `json:"identity_verified,omitempty"`
	AgreementVerified        bool                      `json:"agreement_verified,omitempty"`
	ExceptionReference       string                    `json:"exception_reference,omitempty"`
	ReviewDueAt              int64                     `json:"review_due_at,omitempty"`
	Reason                   string                    `json:"reason,omitempty"`
	RiskLimits               AgentGovernanceRiskLimits `json:"risk_limits"`
}

type AgentGovernanceReceipt struct {
	UserID            int    `json:"user_id"`
	GovernanceVersion int64  `json:"governance_version"`
	ProfileVersion    int64  `json:"profile_version"`
	SubmissionID      int64  `json:"submission_id"`
	Status            string `json:"status"`
	Exceptional       bool   `json:"exceptional"`
}

func validAgentGovernanceReference(value, prefix string) bool {
	suffix, ok := strings.CutPrefix(value, prefix+":")
	if !ok || len(suffix) < 8 || len(suffix) > 96 {
		return false
	}
	letter := false
	for _, c := range suffix {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			letter = true
			continue
		}
		if !(c >= '0' && c <= '9') && c != '_' && c != '-' {
			return false
		}
	}
	return letter
}
func agentGovernanceDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func normalizeAgentGovernanceCommand(identity AuthSessionIdentity, action string, c AgentGovernanceCommand) (AgentGovernanceCommand, error) {
	if c.UserID == 0 && action == "governance.submit" {
		c.UserID = identity.UserID
	}
	if validateAgentRequestID(c.RequestID) != nil || c.UserID <= 0 || c.ExpectedVersion < 0 || c.ExpectedProfileVersion < 0 {
		return c, ErrAgentGovernanceInvalid
	}
	switch action {
	case "governance.submit":
		digest, err := hex.DecodeString(c.AgreementDigest)
		if c.UserID != identity.UserID || !c.AcceptTerms || !validAgentGovernanceReference(c.IdentityDossierReference, "dossier") || !validAgentGovernanceReference(c.AgreementReference, "agreement") || err != nil || len(digest) != 32 || strings.ToLower(c.AgreementDigest) != c.AgreementDigest || c.Decision != "" || c.IdentityVerified || c.AgreementVerified || c.ExceptionReference != "" || c.ReviewDueAt != 0 || c.Reason != "" || c.RiskLimits != (AgentGovernanceRiskLimits{}) {
			return c, ErrAgentGovernanceInvalid
		}
	case "governance.review", "governance.override":
		if c.ExpectedVersion <= 0 || c.AcceptTerms || c.IdentityDossierReference != "" || c.AgreementReference != "" || c.AgreementDigest != "" || !validAgentCommercialReason(c.Reason) {
			return c, ErrAgentGovernanceInvalid
		}
		if c.RiskLimits.DailyIssuanceQuota < 0 || c.RiskLimits.DailyIssuanceQuota > int64(common.MaxWalletQuota) || c.RiskLimits.DailyTransferQuota < 0 || c.RiskLimits.DailyTransferQuota > int64(common.MaxWalletQuota) {
			return c, ErrAgentGovernanceInvalid
		}
		if c.RiskLimits.Enabled && (c.RiskLimits.DailyIssuanceQuota == 0 || c.RiskLimits.DailyTransferQuota == 0) || !c.RiskLimits.Enabled && (c.RiskLimits.DailyIssuanceQuota != 0 || c.RiskLimits.DailyTransferQuota != 0) {
			return c, ErrAgentGovernanceInvalid
		}
		if action == "governance.review" {
			if c.ExceptionReference != "" || c.Decision != "approved" && c.Decision != "rejected" || c.Decision == "approved" && (!c.IdentityVerified || !c.AgreementVerified) {
				return c, ErrAgentGovernanceInvalid
			}
		} else if !validAgentGovernanceReference(c.ExceptionReference, "exception") || c.Decision != "" || c.IdentityVerified || c.AgreementVerified {
			return c, ErrAgentGovernanceInvalid
		}
		if c.ReviewDueAt < 0 {
			return c, ErrAgentGovernanceInvalid
		}
	default:
		return c, ErrAgentGovernanceInvalid
	}
	return c, nil
}
