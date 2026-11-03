package model

import (
	"bytes"
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func requireAgentCommercialAdminTx(tx *gorm.DB, identity AuthSessionIdentity) error {
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return ErrAgentProfilePermission
	}
	// Session validation locks the current actor row. Read role only AFTER that
	// lock so a concurrent demotion cannot leave a stale privileged read window.
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return err
	}
	var actor User
	if err := tx.First(&actor, identity.UserID).Error; err != nil {
		return err
	}
	if actor.Status != common.UserStatusEnabled || (actor.Role != common.RoleAdminUser && actor.Role != common.RoleRootUser) {
		return ErrAgentProfilePermission
	}
	return nil
}

func normalizeAgentAgreementCommand(action string, c AgentAgreementCommand) (AgentAgreementCommand, error) {
	if validateAgentRequestID(c.RequestID) != nil || !validAgentCommercialReason(c.Reason) {
		return c, ErrAgentAgreementInvalid
	}
	switch action {
	case "agreement.propose":
		if c.AgreementID != 0 || c.ExpectedVersion != 0 {
			return c, ErrAgentAgreementInvalid
		}
		return c, ValidateAgentAgreementTerms(c.Terms)
	case "agreement.approve", "agreement.reject", "agreement.revoke":
		if c.AgreementID <= 0 || c.ExpectedVersion <= 0 {
			return c, ErrAgentAgreementInvalid
		}
	default:
		return c, ErrAgentAgreementInvalid
	}
	return c, nil
}
func validAgentCommercialReason(s string) bool {
	if len(s) < 3 || len(s) > 255 {
		return false
	}
	for _, ch := range s {
		if ch < 32 || ch == 127 {
			return false
		}
	}
	return true
}
func readAgentAgreementTx(tx *gorm.DB, id int64) (AgentAgreementView, error) {
	var v AgentAgreementView
	if err := tx.First(&v.AgentAgreement, id).Error; err != nil {
		return v, err
	}
	if err := tx.First(&v.Approval, "agreement_id = ?", id).Error; err != nil {
		return v, err
	}
	return v, common.UnmarshalJsonStr(v.TermsJSON, &v.Terms)
}
func agentAgreementContextTx(tx *gorm.DB, identity AuthSessionIdentity, p *AgentPolicy, action string, c AgentAgreementCommand) (AgentTransactionContext, error) {
	if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
		return AgentTransactionContext{}, err
	}
	owner := identity.UserID
	version := int64(0)
	target := c.Terms.AgreementKey
	currency := c.Terms.Currency
	if action == "agreement.propose" {
		if !p.AgreementPricingEnabled || !p.PurchaseEnabled || !p.RiskPolicyApproved {
			return AgentTransactionContext{}, ErrAgentFeatureDisabled
		}
		if err := validateAgentAgreementPolicy(c.Terms, p); err != nil {
			return AgentTransactionContext{}, err
		}
		if c.Terms.AgentUserID > 0 {
			owner = c.Terms.AgentUserID
		}
	} else {
		v, err := readAgentAgreementTx(tx, c.AgreementID)
		if err != nil {
			return AgentTransactionContext{}, err
		}
		if v.Approval.Version != c.ExpectedVersion {
			return AgentTransactionContext{}, ErrAgentAgreementConflict
		}
		if v.AgentUserID > 0 {
			owner = v.AgentUserID
		}
		version = v.Approval.Version
		target = strconv.FormatInt(v.ID, 10)
		currency = v.Currency
	}
	if owner == identity.UserID && ((action == "agreement.propose" && c.Terms.AgentUserID > 0) || (action != "agreement.propose" && owner != 0 && target != "")) {
		// Global schedules use the actor as context principal, but personal terms
		// cannot be authored/reviewed by their own commercial beneficiary.
		subject := c.Terms.AgentUserID
		if action != "agreement.propose" {
			var row AgentAgreement
			if err := tx.First(&row, c.AgreementID).Error; err != nil {
				return AgentTransactionContext{}, err
			}
			subject = row.AgentUserID
		}
		if subject == identity.UserID {
			return AgentTransactionContext{}, ErrAgentAgreementReview
		}
	}
	return agentBusinessContextTx(tx, p, owner, action, c.RequestID, currency, "0", 0, "agent_agreement", target, version, c)
}
func PrepareAgentAgreementOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	var c AgentAgreementCommand
	if common.DecodeJsonStrict(bytes.NewReader(payload), &c) != nil {
		return nil, ErrAgentAgreementInvalid
	}
	c.RequestID = requestID
	c, err := normalizeAgentAgreementCommand(action, c)
	if err != nil {
		return nil, err
	}
	var context AgentTransactionContext
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		p, e := ReadCurrentAgentPolicyTx(tx)
		if e != nil {
			return e
		}
		context, e = agentAgreementContextTx(tx, identity, p, action, c)
		return e
	})
	if err != nil {
		return nil, err
	}
	return &AgentPreparedOperation{VerificationContext: context, Summary: AgentOperationSummary{Action: action, AgentUserID: context.AgentUserID, Currency: context.Currency, AmountMinor: "0", FeeMinor: "0", NetMinor: "0", TargetLabel: "Commercial agreement: " + context.TargetID, PolicyVersion: context.PolicyVersion}, StatusURL: "/api/agent-platform/operations/" + requestID}, nil
}
func ExecuteAgentAgreement(auth AgentTransactionAuthorization, action string, c AgentAgreementCommand) (*AgentAgreementView, error) {
	c, err := normalizeAgentAgreementCommand(action, c)
	if err != nil {
		return nil, err
	}
	signature, err := AgentTransactionParametersHash(c)
	if err != nil {
		return nil, err
	}
	var result AgentAgreementView
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		p, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if err := requireAgentCommercialAdminTx(tx, auth.Identity()); err != nil {
			return err
		}
		var prior AgentAgreementAudit
		err = tx.Where("request_id = ?", c.RequestID).First(&prior).Error
		if err == nil {
			if prior.ActorUserID != auth.Identity().UserID || prior.Action != action || prior.Signature != signature {
				return ErrAgentQuotaConflict
			}
			result, err = readAgentAgreementTx(tx, prior.AgreementID)
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		context, err := agentAgreementContextTx(tx, auth.Identity(), p, action, c)
		if err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		if action == "agreement.propose" {
			var last AgentAgreement
			err := tx.Where("agreement_key = ?", c.Terms.AgreementKey).Order("terms_version DESC").First(&last).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if c.Terms.TermsVersion != last.TermsVersion+1 {
				return ErrAgentAgreementConflict
			}
			if last.ID > 0 && (last.AgentUserID != c.Terms.AgentUserID || last.Scope != c.Terms.Scope) {
				return ErrAgentAgreementConflict
			}
			encoded, err := common.Marshal(c.Terms)
			if err != nil {
				return err
			}
			digest, err := AgentTransactionParametersHash(c.Terms)
			if err != nil {
				return err
			}
			row := AgentAgreement{AgreementKey: c.Terms.AgreementKey, TermsVersion: c.Terms.TermsVersion, Scope: c.Terms.Scope, AgentUserID: c.Terms.AgentUserID, PolicyVersion: p.ID, EffectiveFrom: c.Terms.EffectiveFrom, EffectiveUntil: c.Terms.EffectiveUntil, Currency: c.Terms.Currency, TermsJSON: string(encoded), TermsDigest: digest, CreatedBy: auth.Identity().UserID}
			if row.AgentUserID > 0 {
				profile, err := readAgentProfile(tx, row.AgentUserID, false)
				if err != nil {
					return err
				}
				if profile == nil || !profile.Allows(AgentCapabilitySales) {
					return ErrAgentProfilePermission
				}
				row.ProfileVersion = profile.Version
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			approval := AgentAgreementApproval{AgreementID: row.ID, State: "proposed", Version: 1, UpdatedAt: common.GetTimestamp()}
			if err := tx.Create(&approval).Error; err != nil {
				return err
			}
			result = AgentAgreementView{AgentAgreement: row, Terms: c.Terms, Approval: approval}
		} else {
			result, err = readAgentAgreementTx(tx, c.AgreementID)
			if err != nil {
				return err
			}
			next := ""
			switch action {
			case "agreement.approve":
				if !p.AgreementPricingEnabled || !p.RiskPolicyApproved || !p.PurchaseEnabled {
					return ErrAgentFeatureDisabled
				}
				if result.Approval.State != "proposed" || result.PolicyVersion != p.ID || result.EffectiveUntil <= common.GetTimestamp() {
					return ErrAgentAgreementConflict
				}
				if err := validateAgentAgreementPolicy(result.Terms, p); err != nil {
					return err
				}
				if result.CreatedBy == auth.Identity().UserID {
					return ErrAgentAgreementReview
				}
				if result.AgentUserID > 0 {
					profile, err := readAgentProfile(tx, result.AgentUserID, false)
					if err != nil {
						return err
					}
					if profile == nil || profile.Version != result.ProfileVersion || !profile.Allows(AgentCapabilitySales) {
						return ErrAgentProfileConflict
					}
				}
				var overlaps int64
				if err := tx.Model(&AgentAgreement{}).Joins("JOIN agent_agreement_approvals a ON a.agreement_id = agent_agreements.id").Where("a.state = ? AND agent_agreements.scope = ? AND agent_agreements.agent_user_id = ? AND agent_agreements.policy_version = ? AND agent_agreements.currency = ? AND agent_agreements.effective_from < ? AND agent_agreements.effective_until > ?", "approved", result.Scope, result.AgentUserID, p.ID, result.Currency, result.EffectiveUntil, result.EffectiveFrom).Where("agent_agreements.profile_version = ?", result.ProfileVersion).Count(&overlaps).Error; err != nil {
					return err
				}
				if overlaps != 0 {
					return ErrAgentAgreementConflict
				}
				next = "approved"
				result.Approval.ApprovedBy = auth.Identity().UserID
			case "agreement.reject":
				if result.Approval.State != "proposed" {
					return ErrAgentAgreementConflict
				}
				if result.CreatedBy == auth.Identity().UserID {
					return ErrAgentAgreementReview
				}
				next = "rejected"
			case "agreement.revoke":
				if result.Approval.State != "approved" {
					return ErrAgentAgreementConflict
				}
				next = "revoked"
			}
			result.Approval.State = next
			result.Approval.Version++
			result.Approval.UpdatedAt = common.GetTimestamp()
			if err := tx.Save(&result.Approval).Error; err != nil {
				return err
			}
		}
		return tx.Create(&AgentAgreementAudit{AgreementID: result.ID, Action: action, ActorUserID: auth.Identity().UserID, Reason: c.Reason, RequestID: c.RequestID, Signature: signature}).Error
	})
	return &result, err
}
func ReplayAgentAgreementOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (any, bool, error) {
	var c AgentAgreementCommand
	if common.DecodeJsonStrict(bytes.NewReader(payload), &c) != nil {
		return nil, false, ErrAgentAgreementInvalid
	}
	c.RequestID = requestID
	c, err := normalizeAgentAgreementCommand(action, c)
	if err != nil {
		return nil, false, err
	}
	signature, err := AgentTransactionParametersHash(c)
	if err != nil {
		return nil, false, err
	}
	var result AgentAgreementView
	found := false
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		var audit AgentAgreementAudit
		if e := tx.Where("request_id = ?", requestID).First(&audit).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return nil
			}
			return e
		}
		if e := requireAgentCommercialAdminTx(tx, identity); e != nil {
			return e
		}
		if audit.ActorUserID != identity.UserID || audit.Action != action || audit.Signature != signature {
			return ErrAgentQuotaConflict
		}
		var e error
		result, e = readAgentAgreementTx(tx, audit.AgreementID)
		found = e == nil
		return e
	})
	return &result, found, err
}
func FindAgentAgreementOperation(identity AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	var audit AgentAgreementAudit
	if err := DB.Where("request_id = ? AND actor_user_id = ?", requestID, identity.UserID).First(&audit).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	view, err := GetAgentAgreement(identity, audit.AgreementID)
	if err != nil {
		return nil, err
	}
	return &AgentOperationStatus{RequestID: requestID, Action: audit.Action, Status: "completed", ResourceType: "agent_agreement", ResourceID: strconv.FormatInt(view.ID, 10), ResourceState: view.Approval.State, Resource: view}, nil
}
func GetAgentAgreement(identity AuthSessionIdentity, id int64) (*AgentAgreementView, error) {
	var result AgentAgreementView
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		var err error
		result, err = readAgentAgreementTx(tx, id)
		return err
	})
	return &result, err
}
func ListAgentAgreements(identity AuthSessionIdentity, owner, offset, limit int) ([]AgentAgreementView, int64, error) {
	var rows []AgentAgreement
	var total int64
	result := []AgentAgreementView{}
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, 0, ErrAgentAgreementInvalid
	}
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		q := tx.Model(&AgentAgreement{})
		if owner > 0 {
			q = q.Where("agent_user_id = ?", owner)
		}
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			v, err := readAgentAgreementTx(tx, row.ID)
			if err != nil {
				return err
			}
			result = append(result, v)
		}
		return nil
	})
	return result, total, err
}
