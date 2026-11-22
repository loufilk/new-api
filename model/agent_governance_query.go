package model

import (
	"math/big"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type AgentGovernanceSubmissionView struct {
	AgentGovernanceSubmission
	IdentityDossierReference string `json:"identity_dossier_reference,omitempty"`
	AgreementReference       string `json:"agreement_reference,omitempty"`
}
type AgentGovernanceDailyRisk struct {
	DayStartUTC            int64   `json:"day_start_utc"`
	DayEndUTC              int64   `json:"day_end_utc"`
	GrossIssuanceQuota     string  `json:"gross_issuance_quota"`
	GrossTransferQuota     string  `json:"gross_transfer_quota"`
	RemainingIssuanceQuota *string `json:"remaining_issuance_quota"`
	RemainingTransferQuota *string `json:"remaining_transfer_quota"`
}

type AgentGovernanceView struct {
	DailyRisk         AgentGovernanceDailyRisk       `json:"daily_risk"`
	Profile           *AgentProfile                  `json:"profile"`
	Governance        *AgentGovernance               `json:"governance"`
	Submission        *AgentGovernanceSubmissionView `json:"submission"`
	ReviewRequired    bool                           `json:"review_required"`
	ReviewReasons     []string                       `json:"review_reasons"`
	Audit             []AgentGovernanceAudit         `json:"audit"`
	GovernanceEnabled bool                           `json:"governance_enabled"`
	ObservedAt        int64                          `json:"observed_at"`
}

// Expiry/inactivity are derived at every gate, so a stopped worker cannot keep
// an approval alive. Re-review restarts the inactivity window without inventing
// a business operation. It never marks existing deliveries or keys invalid.
func agentGovernanceReviewReasons(g *AgentGovernance, p *AgentPolicy, now int64) []string {
	reasons := []string{}
	if g == nil {
		return append(reasons, "not_submitted")
	}
	approved := g.Status == "approved" && g.IdentityVerified && g.AgreementVerified && !g.Exceptional || g.Status == "exception_approved" && g.Exceptional
	if !approved || g.ReviewedAt <= 0 || g.ReviewDueAt <= 0 {
		reasons = append(reasons, "approval_required")
	}
	if g.ReviewDueAt > 0 && g.ReviewDueAt <= now {
		reasons = append(reasons, "review_expired")
	}
	if p.GovernanceInactivitySeconds > 0 && g.ReviewedAt > 0 && now-max(g.ReviewedAt, g.LastBusinessActivityAt) >= p.GovernanceInactivitySeconds {
		reasons = append(reasons, "long_inactivity")
	}
	return reasons
}
func GetAgentGovernance(identity AuthSessionIdentity, userID int, finance bool) (*AgentGovernanceView, error) {
	result := AgentGovernanceView{Audit: []AgentGovernanceAudit{}, ReviewReasons: []string{}, ObservedAt: common.GetTimestamp()}
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		// Same lock order as writers; never acquire the policy after an actor lock.
		p, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		profile, g, err := readAgentGovernanceAuthorityTx(tx, identity, userID, finance, true)
		if err != nil {
			return err
		}
		day := time.Unix(result.ObservedAt, 0).UTC().Truncate(24 * time.Hour).Unix()
		result.DailyRisk = AgentGovernanceDailyRisk{DayStartUTC: day, DayEndUTC: day + 86400, GrossIssuanceQuota: "0", GrossTransferQuota: "0"}
		type gross struct {
			Kind  string
			Quota string
		}
		var usage []gross
		if err := tx.Model(&AgentGovernanceRiskUsage{}).Select("kind, SUM(quota) AS quota").Where("user_id = ? AND day_start_utc = ?", userID, day).Group("kind").Scan(&usage).Error; err != nil {
			return err
		}
		for _, u := range usage {
			if u.Kind == "issuance" {
				result.DailyRisk.GrossIssuanceQuota = u.Quota
			} else if u.Kind == "transfer" {
				result.DailyRisk.GrossTransferQuota = u.Quota
			}
		}
		if g != nil && g.RiskLimits.Enabled {
			for _, v := range []struct {
				cap       int64
				used      string
				remaining **string
			}{{g.RiskLimits.DailyIssuanceQuota, result.DailyRisk.GrossIssuanceQuota, &result.DailyRisk.RemainingIssuanceQuota}, {g.RiskLimits.DailyTransferQuota, result.DailyRisk.GrossTransferQuota, &result.DailyRisk.RemainingTransferQuota}} {
				used, ok := new(big.Int).SetString(v.used, 10)
				if !ok || used.Sign() < 0 {
					return ErrAgentGovernanceConflict
				}
				remainder := new(big.Int).Sub(big.NewInt(v.cap), used)
				n := "0"
				if remainder.Sign() > 0 {
					n = strconv.FormatInt(remainder.Int64(), 10)
				}
				*v.remaining = &n
			}
		}
		result.Profile = profile
		result.Governance = g
		result.GovernanceEnabled = p.GovernanceEnabled
		result.ReviewReasons = agentGovernanceReviewReasons(g, p, result.ObservedAt)
		result.ReviewRequired = len(result.ReviewReasons) > 0
		if g == nil {
			return nil
		}
		var submission AgentGovernanceSubmission
		if err := tx.First(&submission, g.SubmissionID).Error; err != nil {
			return err
		}
		result.Submission = &AgentGovernanceSubmissionView{AgentGovernanceSubmission: submission}
		if finance {
			result.Submission.IdentityDossierReference = submission.IdentityDossierReference
			result.Submission.AgreementReference = submission.AgreementReference
		}
		// Audit is bounded; every immutable operation remains recoverable by its
		// request ID. Do not expose opaque exception references even to self.
		return tx.Where("user_id = ?", userID).Order("version DESC").Limit(100).Find(&result.Audit).Error
	})
	return &result, err
}
