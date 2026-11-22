package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Call only for missing/exited -> active, never for existing personal spending.
// Caller already holds policy then related users/profile locks.
func RequireAgentGovernanceActivationTx(tx *gorm.DB, userID int) error {
	p, err := ReadCurrentAgentPolicyTx(tx)
	if err != nil {
		return err
	}
	if !p.GovernanceEnabled || !p.GovernanceRequiredForActivation {
		return nil
	}
	return requireAgentGovernanceApprovalTx(tx, userID, p, common.GetTimestamp())
}

// Apply only to new credit authorizations/draws, not debt repayment or protected
// redemption of credit-backed codes already delivered to their customers.
func RequireAgentGovernanceCreditTx(tx *gorm.DB, userID int) error {
	p, err := ReadCurrentAgentPolicyTx(tx)
	if err != nil {
		return err
	}
	if !p.GovernanceEnabled || !p.CreditGovernanceRequired {
		return nil
	}
	return requireAgentGovernanceApprovalTx(tx, userID, p, common.GetTimestamp())
}
func requireAgentGovernanceApprovalTx(tx *gorm.DB, userID int, p *AgentPolicy, now int64) error {
	if !p.RiskPolicyApproved {
		return ErrAgentFeatureDisabled
	}
	g, err := readAgentGovernanceTx(tx, userID, true)
	if err != nil {
		return err
	}
	if len(agentGovernanceReviewReasons(g, p, now)) > 0 {
		return ErrAgentGovernanceRequired
	}
	var submission AgentGovernanceSubmission
	if err := tx.First(&submission, g.SubmissionID).Error; err != nil {
		return err
	}
	if submission.UserID != userID || !submission.AcceptTerms || submission.ConsentedAt <= 0 {
		return ErrAgentGovernanceRequired
	}
	return nil
}

// ConsumeAgentGovernanceRiskTx must commit in the SAME transaction as a new
// issuance/transfer. Call after policy -> users/profile locks. It never takes
// fund locks, never charges a wallet, and must never run for cancellation,
// refund, or redemption of an already-bound customer's protected delivery.
func ConsumeAgentGovernanceRiskTx(tx *gorm.DB, agentID int, requestID, kind string, quota int64) error {
	return consumeAgentGovernanceRiskAtTx(tx, agentID, requestID, kind, quota, common.GetTimestamp())
}
func consumeAgentGovernanceRiskAtTx(tx *gorm.DB, agentID int, requestID, kind string, quota, now int64) error {
	if tx == nil || agentID <= 0 || validateAgentRequestID(requestID) != nil || kind != "issuance" && kind != "transfer" || quota <= 0 || quota > int64(common.MaxWalletQuota) || now <= 0 {
		return ErrAgentGovernanceInvalid
	}
	p, err := ReadCurrentAgentPolicyTx(tx)
	if err != nil {
		return err
	}
	if !p.GovernanceEnabled {
		return nil
	}
	if !p.RiskPolicyApproved {
		return ErrAgentFeatureDisabled
	}
	g, err := readAgentGovernanceTx(tx, agentID, true)
	if err != nil {
		return err
	}
	// No enrollment implies no retroactive restriction on established agents.
	if g == nil {
		return nil
	}
	var prior AgentGovernanceRiskUsage
	err = tx.Where("user_id = ? AND request_id = ? AND kind = ?", agentID, requestID, kind).First(&prior).Error
	if err == nil {
		if prior.Quota != quota {
			return ErrAgentGovernanceConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if len(agentGovernanceReviewReasons(g, p, now)) > 0 {
		return ErrAgentGovernanceRequired
	}
	var submission AgentGovernanceSubmission
	if err := tx.First(&submission, g.SubmissionID).Error; err != nil {
		return err
	}
	if submission.UserID != agentID || !submission.AcceptTerms || submission.ConsentedAt <= 0 {
		return ErrAgentGovernanceRequired
	}
	day := time.Unix(now, 0).UTC().Truncate(24 * time.Hour).Unix()
	if g.RiskLimits.Enabled {
		cap := g.RiskLimits.DailyIssuanceQuota
		if kind == "transfer" {
			cap = g.RiskLimits.DailyTransferQuota
		}
		var used int64
		if err := tx.Model(&AgentGovernanceRiskUsage{}).Select("COALESCE(SUM(quota), 0)").Where("user_id = ? AND kind = ? AND day_start_utc = ?", agentID, kind, day).Scan(&used).Error; err != nil {
			return err
		}
		if used < 0 || cap <= 0 || quota > cap || used > cap-quota {
			return ErrAgentGovernanceRiskLimit
		}
	}
	usage := AgentGovernanceRiskUsage{UserID: agentID, RequestID: requestID, Kind: kind, DayStartUTC: day, Quota: quota, GovernanceVersion: g.Version, PolicyVersion: p.ID, CreatedAt: now}
	if err := tx.Create(&usage).Error; err != nil {
		return err
	}
	// Activity is not an eligibility revision: an ordinary operation must not
	// invalidate all unrelated proofs. Review/status/limits changes do bump it.
	if now > g.LastBusinessActivityAt {
		return tx.Model(&AgentGovernance{}).Where("user_id = ?", agentID).Updates(map[string]any{"last_business_activity_at": now, "updated_at": now}).Error
	}
	return nil
}

// AgentGovernanceRiskOperationID separates business-operation namespaces before
// their shared risk usage is deduplicated. Different actions may legitimately
// have the same client request ID; they must never share a risk charge. The
// hash is bounded even for a maximum-length external request ID.
func AgentGovernanceRiskOperationID(action, requestID string) string {
	return "risk:" + agentGovernanceDigest(action+"\x00"+requestID)
}
