package model

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Lock order is policy, actor/subject users (ID order), profile, governance,
// session/proof. Governance never acquires fund/account locks.
func readAgentGovernanceAuthorityTx(tx *gorm.DB, identity AuthSessionIdentity, subject int, finance, locked bool) (*AgentProfile, *AgentGovernance, error) {
	if identity.UserID <= 0 || subject <= 0 || identity.SessionID == "" {
		return nil, nil, ErrAgentProfilePermission
	}
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return nil, nil, ErrAgentProfilePermission
	}
	q := tx.Where("id IN ?", []int{identity.UserID, subject}).Order("id")
	if locked {
		q = lockForUpdate(q)
	}
	var users []User
	if err := q.Find(&users).Error; err != nil {
		return nil, nil, err
	}
	var actor, target *User
	for i := range users {
		if users[i].Id == identity.UserID {
			actor = &users[i]
		}
		if users[i].Id == subject {
			target = &users[i]
		}
	}
	if actor == nil || target == nil || actor.Status != common.UserStatusEnabled {
		return nil, nil, ErrAgentProfilePermission
	}
	if finance {
		if actor.Role != common.RoleAdminUser && actor.Role != common.RoleRootUser || actor.Id == target.Id || target.Role >= actor.Role {
			return nil, nil, ErrAgentProfilePermission
		}
	} else if actor.Id != target.Id {
		return nil, nil, ErrAgentProfilePermission
	}
	profile, err := readAgentProfile(tx, subject, locked)
	if err != nil {
		return nil, nil, err
	}
	governance, err := readAgentGovernanceTx(tx, subject, locked)
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return nil, nil, err
	}
	return profile, governance, nil
}
func readAgentGovernanceTx(tx *gorm.DB, userID int, locked bool) (*AgentGovernance, error) {
	q := tx.Where("user_id = ?", userID)
	if locked {
		q = lockForUpdate(q)
	}
	var row AgentGovernance
	if err := q.First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}
func agentGovernanceContextTx(tx *gorm.DB, identity AuthSessionIdentity, policy *AgentPolicy, action string, c AgentGovernanceCommand, locked bool) (AgentTransactionContext, *AgentGovernance, error) {
	profile, g, err := readAgentGovernanceAuthorityTx(tx, identity, c.UserID, action != "governance.submit", locked)
	if err != nil {
		return AgentTransactionContext{}, nil, err
	}
	if !policy.GovernanceEnabled || !policy.RiskPolicyApproved {
		return AgentTransactionContext{}, nil, ErrAgentFeatureDisabled
	}
	version, pversion := int64(0), int64(0)
	if g != nil {
		version = g.Version
	}
	if profile != nil {
		pversion = profile.Version
	}
	if version != c.ExpectedVersion {
		return AgentTransactionContext{}, nil, ErrAgentGovernanceConflict
	}
	if action != "governance.submit" && pversion != c.ExpectedProfileVersion {
		return AgentTransactionContext{}, nil, ErrAgentProfileConflict
	}
	if action != "governance.submit" {
		var submission AgentGovernanceSubmission
		if g == nil || g.SubmissionID <= 0 {
			return AgentTransactionContext{}, nil, ErrAgentGovernanceReview
		}
		if err := tx.First(&submission, g.SubmissionID).Error; err != nil {
			return AgentTransactionContext{}, nil, err
		}
		if submission.UserID != c.UserID || !submission.AcceptTerms || submission.ConsentedAt <= 0 {
			return AgentTransactionContext{}, nil, ErrAgentGovernanceReview
		}
		if action == "governance.override" {
			var actor User
			if err := tx.Select("role").First(&actor, identity.UserID).Error; err != nil {
				return AgentTransactionContext{}, nil, err
			}
			if actor.Role != common.RoleRootUser {
				return AgentTransactionContext{}, nil, ErrAgentProfilePermission
			}
		}
		now := common.GetTimestamp()
		if c.Decision == "rejected" {
			if c.ReviewDueAt != 0 {
				return AgentTransactionContext{}, nil, ErrAgentGovernanceInvalid
			}
		} else if c.ReviewDueAt <= now || c.ReviewDueAt > now+10*366*24*60*60 {
			return AgentTransactionContext{}, nil, ErrAgentGovernanceInvalid
		}
	}
	context, err := NewAgentTransactionContext(action, c.RequestID, c.UserID, pversion, policy.ID, "", "0", 0, "agent_governance", strconv.Itoa(c.UserID), version, c)
	return context, g, err
}
func PrepareAgentGovernanceOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	var c AgentGovernanceCommand
	if common.DecodeJsonStrict(bytes.NewReader(payload), &c) != nil {
		return nil, ErrAgentGovernanceInvalid
	}
	c.RequestID = requestID
	c, err := normalizeAgentGovernanceCommand(identity, action, c)
	if err != nil {
		return nil, err
	}
	var context AgentTransactionContext
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		p, err := ReadCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		context, _, err = agentGovernanceContextTx(tx, identity, p, action, c, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	label := fmt.Sprintf("Subject %d; governance version %d; no profile activation or wallet change", c.UserID, c.ExpectedVersion)
	switch action {
	case "governance.submit":
		label += fmt.Sprintf("; explicit consent to agreement SHA256 %s; dossier reference SHA256 %s", c.AgreementDigest, agentGovernanceDigest(c.IdentityDossierReference))
	case "governance.review":
		label += fmt.Sprintf("; decision %s; identity verified=%t; agreement verified=%t; review due=%d; gross UTC daily limits enabled=%t issuance=%d transfer=%d; reason: %s", c.Decision, c.IdentityVerified, c.AgreementVerified, c.ReviewDueAt, c.RiskLimits.Enabled, c.RiskLimits.DailyIssuanceQuota, c.RiskLimits.DailyTransferQuota, c.Reason)
	case "governance.override":
		label += fmt.Sprintf("; ROOT EXCEPTION, not verification; exception reference SHA256 %s; review due=%d; gross UTC daily limits enabled=%t issuance=%d transfer=%d; reason: %s", agentGovernanceDigest(c.ExceptionReference), c.ReviewDueAt, c.RiskLimits.Enabled, c.RiskLimits.DailyIssuanceQuota, c.RiskLimits.DailyTransferQuota, c.Reason)
	}
	return &AgentPreparedOperation{VerificationContext: context, Summary: AgentOperationSummary{Action: action, AgentUserID: c.UserID, AmountMinor: "0", FeeMinor: "0", NetMinor: "0", TargetLabel: label, PolicyVersion: context.PolicyVersion}, StatusURL: "/api/agent-platform/operations/" + requestID}, nil
}

// A profile version bump invalidates pending financial proofs without changing
// a business freeze, user status, login role, credential, or customer binding.
func bumpAgentGovernanceProfileTx(tx *gorm.DB, userID, actorID int) (int64, error) {
	previous, err := LockAgentProfileTx(tx, userID)
	if err != nil {
		return 0, err
	}
	if previous == nil {
		return 0, nil
	}
	if previous.Version >= math.MaxInt64 {
		return 0, ErrAgentProfileConflict
	}
	next := previous.Version + 1
	result := tx.Model(&AgentProfile{}).Where("user_id = ? AND version = ?", userID, previous.Version).Updates(map[string]any{"version": next, "updated_at": common.GetTimestamp(), "updated_by": actorID, "reason": "Governance evidence or risk limits changed"})
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, ErrAgentProfileConflict
	}
	audit := AgentProfileAudit{UserID: userID, Version: next, ActorUserID: actorID, PreviousStatus: previous.Status, Status: previous.Status, PreviousSalesFrozen: previous.SalesFrozen, SalesFrozen: previous.SalesFrozen, PreviousSettlementFrozen: previous.SettlementFrozen, SettlementFrozen: previous.SettlementFrozen, Reason: "Governance evidence or risk limits changed"}
	return next, tx.Create(&audit).Error
}
func ExecuteAgentGovernance(auth AgentTransactionAuthorization, action string, c AgentGovernanceCommand) (*AgentGovernanceReceipt, error) {
	identity := auth.Identity()
	c, err := normalizeAgentGovernanceCommand(identity, action, c)
	if err != nil {
		return nil, err
	}
	signature, err := AgentTransactionParametersHash(c)
	if err != nil {
		return nil, err
	}
	var result AgentGovernanceReceipt
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		// All governance writers share the policy lock; no unique-violation retry
		// is needed, and failed proofs leave neither submissions nor audit claims.
		var prior AgentGovernanceAudit
		err = tx.Where("request_id = ?", c.RequestID).First(&prior).Error
		if err == nil {
			return replayAgentGovernanceAuditTx(tx, identity, action, c, signature, prior, &result)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		context, g, err := agentGovernanceContextTx(tx, identity, policy, action, c, true)
		if err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		if g == nil {
			g = &AgentGovernance{UserID: c.UserID}
		}
		if g.Version >= math.MaxInt64 {
			return ErrAgentGovernanceConflict
		}
		previousStatus := g.Status
		g.Version++
		now := common.GetTimestamp()
		if action == "governance.submit" {
			submission := AgentGovernanceSubmission{UserID: c.UserID, Version: g.Version, IdentityDossierReference: c.IdentityDossierReference, IdentityDossierDigest: agentGovernanceDigest(c.IdentityDossierReference), AgreementReference: c.AgreementReference, AgreementReferenceDigest: agentGovernanceDigest(c.AgreementReference), AgreementDigest: c.AgreementDigest, AcceptTerms: true, ConsentedAt: now, RequestID: c.RequestID}
			if err := tx.Create(&submission).Error; err != nil {
				return err
			}
			g.SubmissionID = submission.ID
			g.Status = "pending"
			g.IdentityVerified = false
			g.AgreementVerified = false
			g.Exceptional = false
			g.ReviewedBy = 0
			g.ReviewedAt = 0
			g.ReviewDueAt = 0
		} else {
			if action == "governance.override" {
				g.Status = "exception_approved"
				g.Exceptional = true
			} else {
				g.Status = c.Decision
				g.Exceptional = false
				g.IdentityVerified = c.IdentityVerified
				g.AgreementVerified = c.AgreementVerified
			}
			g.ReviewedBy = identity.UserID
			g.ReviewedAt = now
			g.ReviewDueAt = c.ReviewDueAt
			g.RiskLimits = c.RiskLimits
		}
		if c.ExpectedVersion == 0 {
			err = tx.Create(g).Error
		} else {
			err = tx.Save(g).Error
		}
		if err != nil {
			return err
		}
		pversion, err := bumpAgentGovernanceProfileTx(tx, c.UserID, identity.UserID)
		if err != nil {
			return err
		}
		result = AgentGovernanceReceipt{UserID: c.UserID, GovernanceVersion: g.Version, ProfileVersion: pversion, SubmissionID: g.SubmissionID, Status: g.Status, Exceptional: g.Exceptional}
		encoded, err := common.Marshal(result)
		if err != nil {
			return err
		}
		audit := AgentGovernanceAudit{UserID: c.UserID, Version: g.Version, ProfileVersion: pversion, SubmissionID: g.SubmissionID, RequestID: c.RequestID, Action: action, ActorUserID: identity.UserID, PreviousStatus: previousStatus, Status: g.Status, IdentityVerified: g.IdentityVerified, AgreementVerified: g.AgreementVerified, Exceptional: g.Exceptional, ExceptionReference: c.ExceptionReference, ReviewDueAt: g.ReviewDueAt, RiskLimits: g.RiskLimits, Reason: c.Reason, PolicyVersion: policy.ID, Signature: signature, Result: string(encoded)}
		if c.ExceptionReference != "" {
			audit.ExceptionDigest = agentGovernanceDigest(c.ExceptionReference)
		}
		return tx.Create(&audit).Error
	})
	return &result, err
}
func replayAgentGovernanceAuditTx(tx *gorm.DB, identity AuthSessionIdentity, action string, c AgentGovernanceCommand, signature string, audit AgentGovernanceAudit, result *AgentGovernanceReceipt) error {
	if audit.ActorUserID != identity.UserID || audit.UserID != c.UserID || audit.Action != action || audit.Signature != signature {
		return ErrAgentGovernanceConflict
	}
	if _, _, err := readAgentGovernanceAuthorityTx(tx, identity, audit.UserID, action != "governance.submit", true); err != nil {
		return err
	}
	if action == "governance.override" {
		var actor User
		if err := tx.First(&actor, identity.UserID).Error; err != nil {
			return err
		}
		if actor.Role != common.RoleRootUser {
			return ErrAgentProfilePermission
		}
	}
	if common.UnmarshalJsonStr(audit.Result, result) != nil {
		return ErrAgentGovernanceConflict
	}
	return nil
}
func ReplayAgentGovernanceOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (any, bool, error) {
	var c AgentGovernanceCommand
	if common.DecodeJsonStrict(bytes.NewReader(payload), &c) != nil {
		return nil, false, ErrAgentGovernanceInvalid
	}
	c.RequestID = requestID
	c, err := normalizeAgentGovernanceCommand(identity, action, c)
	if err != nil {
		return nil, false, err
	}
	signature, err := AgentTransactionParametersHash(c)
	if err != nil {
		return nil, false, err
	}
	var result AgentGovernanceReceipt
	found := false
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		var audit AgentGovernanceAudit
		if err := tx.Where("request_id = ?", requestID).First(&audit).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := replayAgentGovernanceAuditTx(tx, identity, action, c, signature, audit, &result); err != nil {
			return err
		}
		found = true
		return nil
	})
	return &result, found, err
}
func FindAgentGovernanceOperation(identity AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	if validateAgentRequestID(requestID) != nil {
		return nil, ErrAgentGovernanceInvalid
	}
	var result AgentGovernanceReceipt
	var audit AgentGovernanceAudit
	found := false
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := tx.Where("request_id = ? AND actor_user_id = ?", requestID, identity.UserID).First(&audit).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if _, _, err := readAgentGovernanceAuthorityTx(tx, identity, audit.UserID, audit.Action != "governance.submit", true); err != nil {
			return err
		}
		if audit.Action == "governance.override" {
			var actor User
			if err := tx.First(&actor, identity.UserID).Error; err != nil {
				return err
			}
			if actor.Role != common.RoleRootUser {
				return ErrAgentProfilePermission
			}
		}
		if common.UnmarshalJsonStr(audit.Result, &result) != nil {
			return ErrAgentGovernanceConflict
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return nil, err
	}
	return &AgentOperationStatus{RequestID: requestID, Action: audit.Action, Status: "completed", ResourceType: "agent_governance", ResourceID: strconv.Itoa(audit.UserID), ResourceState: result.Status, Resource: result}, nil
}
