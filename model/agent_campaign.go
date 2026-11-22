package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// The campaign accounting clock is server-owned, never a client request field.
var agentCampaignNow = common.GetTimestamp

var (
	ErrAgentCampaignInvalid    = errors.New("Invalid campaign request")
	ErrAgentCampaignConflict   = errors.New("Campaign operation changed; reload and prepare it again")
	ErrAgentCampaignIneligible = errors.New("This account is not eligible for this campaign")
	ErrAgentCampaignBudget     = errors.New("Campaign quota budget is unavailable")
)

// Campaign terms are immutable and quota-denominated. New versions are new
// records, independently approved. A family/exclusion key applies only to new
// campaign claims, never to legacy signup or agent invitation gifts.
type AgentCampaignTerms struct {
	Name                   string `json:"name" gorm:"type:varchar(96)"`
	FamilyKey              string `json:"family_key" gorm:"type:varchar(64);index"`
	ExclusionGroup         string `json:"exclusion_group" gorm:"type:varchar(64)"`
	GrantQuota             int    `json:"grant_quota" gorm:"type:bigint"`
	TotalBudgetQuota       int    `json:"total_budget_quota" gorm:"type:bigint"`
	DailyBudgetQuota       int    `json:"daily_budget_quota" gorm:"type:bigint"`
	PerAgentBudgetQuota    int    `json:"per_agent_budget_quota" gorm:"type:bigint"`
	ClaimStartAt           int64  `json:"claim_start_at"`
	ClaimEndAt             int64  `json:"claim_end_at"`
	ExpiresAt              int64  `json:"expires_at"`
	RequireLinuxDO         bool   `json:"require_linuxdo"`
	RequireAgentInvitation bool   `json:"require_agent_invitation"`
	ManualReview           bool   `json:"manual_review"`
	ReviewWindowSeconds    int64  `json:"review_window_seconds"`
	RefundTerms            string `json:"refund_terms" gorm:"type:varchar(32)"`
	Revocable              bool   `json:"revocable"`
	Reason                 string `json:"reason" gorm:"type:varchar(255)"`
}
type AgentCampaign struct {
	ID                 int64  `json:"id" gorm:"primaryKey"`
	RequestID          string `json:"request_id" gorm:"type:varchar(96);uniqueIndex;not null"`
	AgentCampaignTerms `gorm:"embedded"`
	TermsHash          string `json:"terms_hash" gorm:"type:char(64);not null"`
	ProposedBy         int    `json:"proposed_by"`
	PolicyVersion      int64  `json:"policy_version"`
	CreatedAt          int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCampaign) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCampaign) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentCampaignPublication struct {
	CampaignID int64  `json:"campaign_id" gorm:"primaryKey;autoIncrement:false"`
	Status     string `json:"status" gorm:"type:varchar(24);index"`
	Version    int64  `json:"version"`
	ApprovedBy int    `json:"approved_by"`
	ApprovedAt int64  `json:"approved_at"`
}
type AgentCampaignBudget struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	CampaignID    int64  `json:"campaign_id" gorm:"uniqueIndex:idx_campaign_budget,priority:1"`
	Scope         string `json:"scope" gorm:"type:varchar(24);uniqueIndex:idx_campaign_budget,priority:2"`
	ScopeKey      string `json:"scope_key" gorm:"type:varchar(64);uniqueIndex:idx_campaign_budget,priority:3"`
	LimitQuota    int    `json:"limit_quota" gorm:"type:bigint"`
	ReservedQuota int    `json:"reserved_quota" gorm:"type:bigint"`
	SpentQuota    int    `json:"spent_quota" gorm:"type:bigint"`
	ReleasedQuota int    `json:"released_quota" gorm:"type:bigint"`
}
type AgentCampaignBudgetEntry struct {
	ID         int64  `json:"id" gorm:"primaryKey"`
	BudgetID   int64  `json:"budget_id" gorm:"uniqueIndex:idx_campaign_budget_entry,priority:1"`
	ClaimID    int64  `json:"claim_id" gorm:"uniqueIndex:idx_campaign_budget_entry,priority:2"`
	Transition string `json:"transition" gorm:"type:varchar(16);uniqueIndex:idx_campaign_budget_entry,priority:3"`
	Quota      int    `json:"quota" gorm:"type:bigint"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCampaignBudgetEntry) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCampaignBudgetEntry) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentCampaignClaim struct {
	ID             int64  `json:"id" gorm:"primaryKey"`
	RequestID      string `json:"request_id" gorm:"type:varchar(96);uniqueIndex;not null"`
	Signature      string `json:"-" gorm:"type:char(64)"`
	CampaignID     int64  `json:"campaign_id" gorm:"index"`
	UserID         int    `json:"user_id" gorm:"index"`
	AgentUserID    int    `json:"agent_user_id" gorm:"index"`
	Quota          int    `json:"quota" gorm:"type:bigint"`
	Status         string `json:"status" gorm:"type:varchar(24);index"`
	Version        int64  `json:"version"`
	BudgetDay      string `json:"budget_day" gorm:"type:varchar(10)"`
	ReviewDeadline int64  `json:"review_deadline" gorm:"index"`
	ReviewedBy     int    `json:"reviewed_by"`
	ReviewReason   string `json:"review_reason" gorm:"type:varchar(255)"`
	TermsHash      string `json:"terms_hash" gorm:"type:char(64)"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

// Dedupe keys contain only HMACs of verified supported identities. They remain
// after denial/deletion and are not a reusable credential or a device identity.
// This random privacy key is stable across login/session-secret rotation. It
// is never returned by APIs or audit views and must not be rotated independently
// of the retained dedupe records.
type AgentCampaignPrivacyKey struct {
	ID     int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Secret string `json:"-" gorm:"type:char(64);not null"`
}

func (*AgentCampaignPrivacyKey) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCampaignPrivacyKey) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentCampaignDedupe struct {
	Key       string `json:"-" gorm:"type:char(64);primaryKey"`
	ClaimID   int64  `json:"claim_id" gorm:"index"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}
type AgentCampaignOperation struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	RequestID   string `json:"request_id" gorm:"type:varchar(96);uniqueIndex;not null"`
	ActorUserID int    `json:"actor_user_id"`
	Action      string `json:"action" gorm:"type:varchar(48)"`
	Signature   string `json:"-" gorm:"type:char(64)"`
	ResourceID  int64  `json:"resource_id"`
	Result      string `json:"-" gorm:"type:text"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCampaignOperation) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCampaignOperation) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
func AgentCampaignModels() []any {
	return []any{&AgentCampaign{}, &AgentCampaignPublication{}, &AgentCampaignBudget{}, &AgentCampaignBudgetEntry{}, &AgentCampaignClaim{}, &AgentCampaignDedupe{}, &AgentCampaignPrivacyKey{}, &AgentCampaignOperation{}}
}

func validateAgentCampaignTerms(t AgentCampaignTerms) error {
	if strings.TrimSpace(t.Name) == "" || len(t.Name) > 96 || strings.TrimSpace(t.Reason) == "" || len(t.Reason) > 255 || !validCampaignKey(t.FamilyKey) || t.ExclusionGroup != "" && !validCampaignKey(t.ExclusionGroup) {
		return ErrAgentCampaignInvalid
	}
	for _, n := range []int{t.GrantQuota, t.TotalBudgetQuota, t.DailyBudgetQuota, t.PerAgentBudgetQuota} {
		if n <= 0 || n > common.MaxWalletQuota {
			return ErrAgentCampaignInvalid
		}
	}
	if t.GrantQuota > t.DailyBudgetQuota || t.DailyBudgetQuota > t.TotalBudgetQuota || t.GrantQuota > t.PerAgentBudgetQuota || t.PerAgentBudgetQuota > t.TotalBudgetQuota {
		return ErrAgentCampaignInvalid
	}
	if t.ClaimStartAt <= 0 || t.ClaimEndAt <= t.ClaimStartAt || t.ClaimEndAt-t.ClaimStartAt > 366*86400 || t.ExpiresAt <= t.ClaimEndAt || t.ExpiresAt-t.ClaimEndAt > 366*86400 || t.RefundTerms != "restore_original_expiry" {
		return ErrAgentCampaignInvalid
	}
	if t.ReviewWindowSeconds < 0 || t.ReviewWindowSeconds > 30*86400 || t.ManualReview && t.ReviewWindowSeconds < 60 || !t.ManualReview && t.ReviewWindowSeconds != 0 {
		return ErrAgentCampaignInvalid
	}
	return nil
}
func validCampaignKey(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
