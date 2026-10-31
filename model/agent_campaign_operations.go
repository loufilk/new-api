package model

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type AgentCampaignProposeRequest struct {
	RequestID string             `json:"request_id"`
	Terms     AgentCampaignTerms `json:"terms"`
}
type AgentCampaignActionRequest struct {
	RequestID       string `json:"request_id"`
	CampaignID      int64  `json:"campaign_id,omitempty"`
	ClaimID         int64  `json:"claim_id,omitempty"`
	ExpectedVersion int64  `json:"expected_version"`
	Decision        string `json:"decision,omitempty"`
	Reason          string `json:"reason"`
}
type AgentCampaignOutcome struct {
	Campaign    *AgentCampaign            `json:"campaign,omitempty"`
	Publication *AgentCampaignPublication `json:"publication,omitempty"`
	Claim       *AgentCampaignClaim       `json:"claim,omitempty"`
}

func decodeAgentCampaignOperation(action, requestID string, payload []byte) (AgentCampaignProposeRequest, AgentCampaignActionRequest, error) {
	var proposal AgentCampaignProposeRequest
	var command AgentCampaignActionRequest
	if validateAgentRequestID(requestID) != nil {
		return proposal, command, ErrAgentCampaignInvalid
	}
	if action == "campaign.propose" {
		if common.DecodeJsonStrict(bytes.NewReader(payload), &proposal) != nil || proposal.RequestID != requestID || validateAgentCampaignTerms(proposal.Terms) != nil {
			return proposal, command, ErrAgentCampaignInvalid
		}
		var fields map[string]any
		if common.Unmarshal(payload, &fields) != nil {
			return proposal, command, ErrAgentCampaignInvalid
		}
		terms, ok := fields["terms"].(map[string]any)
		if !ok {
			return proposal, command, ErrAgentCampaignInvalid
		}
		for _, key := range []string{"name", "family_key", "exclusion_group", "grant_quota", "total_budget_quota", "daily_budget_quota", "per_agent_budget_quota", "claim_start_at", "claim_end_at", "expires_at", "require_linuxdo", "require_agent_invitation", "manual_review", "review_window_seconds", "refund_terms", "revocable", "reason"} {
			if terms[key] == nil {
				return proposal, command, ErrAgentCampaignInvalid
			}
		}
	} else {
		if common.DecodeJsonStrict(bytes.NewReader(payload), &command) != nil || command.RequestID != requestID || command.ExpectedVersion <= 0 || strings.TrimSpace(command.Reason) == "" || len(command.Reason) > 255 {
			return proposal, command, ErrAgentCampaignInvalid
		}
		switch action {
		case "campaign.approve":
			if command.CampaignID <= 0 || command.ClaimID != 0 || command.Decision != "" {
				return proposal, command, ErrAgentCampaignInvalid
			}
		case "campaign.review.approve", "campaign.review.reject":
			if command.ClaimID <= 0 || command.CampaignID != 0 || command.Decision != strings.TrimPrefix(action, "campaign.review.") {
				return proposal, command, ErrAgentCampaignInvalid
			}
		case "campaign.revoke":
			if command.ClaimID <= 0 || command.CampaignID != 0 || command.Decision != "" {
				return proposal, command, ErrAgentCampaignInvalid
			}
		default:
			return proposal, command, ErrAgentCampaignInvalid
		}
	}
	return proposal, command, nil
}

func agentCampaignContextTx(tx *gorm.DB, identity AuthSessionIdentity, policy *AgentPolicy, action, requestID string, proposal AgentCampaignProposeRequest, command AgentCampaignActionRequest) (AgentTransactionContext, AgentCampaignOutcome, error) {
	var out AgentCampaignOutcome
	root := action == "campaign.propose"
	if err := requireCampaignBrowserTx(tx, identity, true, root); err != nil {
		return AgentTransactionContext{}, out, err
	}
	profile, err := readAgentProfile(tx, identity.UserID, false)
	if err != nil {
		return AgentTransactionContext{}, out, err
	}
	version := int64(0)
	if profile != nil {
		version = profile.Version
	}
	targetType, targetID, targetVersion, quota := "campaign", requestID, int64(0), proposal.Terms.GrantQuota
	var parameters any = proposal
	if action != "campaign.propose" {
		parameters = command
		if command.CampaignID > 0 {
			var campaign AgentCampaign
			var publication AgentCampaignPublication
			if err := tx.First(&campaign, command.CampaignID).Error; err != nil {
				return AgentTransactionContext{}, out, err
			}
			if err := lockForUpdate(tx).First(&publication, campaign.ID).Error; err != nil {
				return AgentTransactionContext{}, out, err
			}
			if publication.Version != command.ExpectedVersion || publication.Status != "proposed" || campaign.ProposedBy == identity.UserID {
				return AgentTransactionContext{}, out, ErrAgentCampaignConflict
			}
			out.Campaign, out.Publication = &campaign, &publication
			quota = campaign.GrantQuota
			targetID = strconv.FormatInt(campaign.ID, 10)
			targetVersion = publication.Version
		} else {
			var claim AgentCampaignClaim
			var campaign AgentCampaign
			if err := lockForUpdate(tx).First(&claim, command.ClaimID).Error; err != nil {
				return AgentTransactionContext{}, out, err
			}
			if err := tx.First(&campaign, claim.CampaignID).Error; err != nil {
				return AgentTransactionContext{}, out, err
			}
			if claim.Version != command.ExpectedVersion || claim.UserID == identity.UserID {
				return AgentTransactionContext{}, out, ErrAgentCampaignConflict
			}
			if strings.HasPrefix(action, "campaign.review.") && (claim.Status != "review_pending" || claim.ReviewDeadline <= agentCampaignNow()) {
				return AgentTransactionContext{}, out, ErrAgentCampaignConflict
			}
			if action == "campaign.revoke" && (claim.Status != "granted" || !campaign.Revocable) {
				return AgentTransactionContext{}, out, ErrAgentCampaignConflict
			}
			out.Campaign, out.Claim = &campaign, &claim
			quota = claim.Quota
			targetType = "campaign_claim"
			targetID = strconv.FormatInt(claim.ID, 10)
			targetVersion = claim.Version
		}
	}
	if action == "campaign.propose" || action == "campaign.approve" || action == "campaign.review.approve" {
		if !policy.CampaignEnabled || !policy.CampaignTermsApproved || !policy.RiskPolicyApproved {
			return AgentTransactionContext{}, out, ErrAgentFeatureDisabled
		}
	}
	context, err := NewAgentTransactionContext(action, requestID, identity.UserID, version, policy.ID, "", "0", int64(quota), targetType, targetID, targetVersion, parameters)
	return context, out, err
}

func PrepareAgentCampaignOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	proposal, command, err := decodeAgentCampaignOperation(action, requestID, payload)
	if err != nil {
		return nil, err
	}
	var result AgentPreparedOperation
	err = DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		context, outcome, err := agentCampaignContextTx(tx, identity, policy, action, requestID, proposal, command)
		if err != nil {
			return err
		}
		result.VerificationContext = context
		terms := proposal.Terms
		if outcome.Campaign != nil {
			terms = outcome.Campaign.AgentCampaignTerms
		}
		label := fmt.Sprintf("%s; grant=%d quota; total=%d; UTC-day=%d; per-agent=%d; claim=%d..%d; expires=%d; refund=%s; revocable=%t; manual_review=%t; LinuxDO=%t; agent_invitation=%t; family=%s; exclusion=%s", terms.Name, terms.GrantQuota, terms.TotalBudgetQuota, terms.DailyBudgetQuota, terms.PerAgentBudgetQuota, terms.ClaimStartAt, terms.ClaimEndAt, terms.ExpiresAt, terms.RefundTerms, terms.Revocable, terms.ManualReview, terms.RequireLinuxDO, terms.RequireAgentInvitation, terms.FamilyKey, terms.ExclusionGroup)
		result.Summary = AgentOperationSummary{Action: action, AgentUserID: identity.UserID, AmountMinor: "0", FeeMinor: "0", NetMinor: "0", Quota: context.Quota, TargetLabel: label, PolicyVersion: policy.ID}
		result.StatusURL = "/api/agent-platform/operations/" + requestID
		return nil
	})
	return &result, err
}

func ReplayAgentCampaignOperation(identity AuthSessionIdentity, action, requestID string, payload any) (*AgentCampaignOutcome, bool, error) {
	signature, err := AgentTransactionParametersHash(payload)
	if err != nil {
		return nil, false, err
	}
	var result AgentCampaignOutcome
	found := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := requireCampaignBrowserTx(tx, identity, true, action == "campaign.propose"); err != nil {
			return err
		}
		var operation AgentCampaignOperation
		err := tx.Where("request_id = ?", requestID).First(&operation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if operation.ActorUserID != identity.UserID || operation.Action != action || operation.Signature != signature {
			return ErrAgentCampaignConflict
		}
		if common.UnmarshalJsonStr(operation.Result, &result) != nil {
			return ErrAgentCampaignConflict
		}
		found = true
		return nil
	})
	return &result, found, err
}

func ExecuteAgentCampaignOperation(authorization AgentTransactionAuthorization, action, requestID string, payload []byte) (*AgentCampaignOutcome, error) {
	proposal, command, err := decodeAgentCampaignOperation(action, requestID, payload)
	if err != nil {
		return nil, err
	}
	var parameters any = command
	if action == "campaign.propose" {
		parameters = proposal
	}
	if result, replayed, err := ReplayAgentCampaignOperation(authorization.Identity(), action, requestID, parameters); err != nil || replayed {
		return result, err
	}
	signature, err := AgentTransactionParametersHash(parameters)
	if err != nil {
		return nil, err
	}
	var result AgentCampaignOutcome
	identity := authorization.Identity()
	err = DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		var existing AgentCampaignOperation
		if err := tx.Where("request_id = ?", requestID).Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		if existing.ID != 0 {
			if existing.ActorUserID != identity.UserID || existing.Action != action || existing.Signature != signature {
				return ErrAgentCampaignConflict
			}
			if err := requireCampaignBrowserTx(tx, identity, true, action == "campaign.propose"); err != nil {
				return err
			}
			return common.UnmarshalJsonStr(existing.Result, &result)
		}
		var conflicting int64
		if err := tx.Model(&AgentCampaignClaim{}).Where("request_id = ?", requestID).Count(&conflicting).Error; err != nil {
			return err
		}
		if conflicting > 0 {
			return ErrAgentCampaignConflict
		}
		context, out, err := agentCampaignContextTx(tx, identity, policy, action, requestID, proposal, command)
		if err != nil {
			return err
		}
		result = out
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, context); err != nil {
			return err
		}
		resourceID := int64(0)
		switch action {
		case "campaign.propose":
			hash, err := AgentTransactionParametersHash(proposal.Terms)
			if err != nil {
				return err
			}
			campaign := AgentCampaign{RequestID: requestID, AgentCampaignTerms: proposal.Terms, TermsHash: hash, ProposedBy: identity.UserID, PolicyVersion: policy.ID}
			if err := tx.Create(&campaign).Error; err != nil {
				return err
			}
			publication := AgentCampaignPublication{CampaignID: campaign.ID, Status: "proposed", Version: 1}
			if err := tx.Create(&publication).Error; err != nil {
				return err
			}
			result.Campaign, result.Publication = &campaign, &publication
			resourceID = campaign.ID
		case "campaign.approve":
			if result.Campaign.ClaimEndAt <= agentCampaignNow() {
				return ErrAgentCampaignInvalid
			}
			publication := result.Publication
			publication.Status = "approved"
			publication.Version++
			publication.ApprovedBy = identity.UserID
			publication.ApprovedAt = agentCampaignNow()
			if err := tx.Save(publication).Error; err != nil {
				return err
			}
			resourceID = result.Campaign.ID
		case "campaign.review.approve", "campaign.review.reject":
			claim := result.Claim
			claim.ReviewedBy = identity.UserID
			claim.ReviewReason = command.Reason
			if action == "campaign.review.approve" {
				if err := grantAgentCampaignClaimTx(tx, result.Campaign, claim); err != nil {
					return err
				}
			} else {
				if err := campaignBudgetTransitionTx(tx, result.Campaign, claim, "release"); err != nil {
					return err
				}
				claim.Status = "rejected"
				claim.Version++
			}
			if err := tx.Save(claim).Error; err != nil {
				return err
			}
			resourceID = claim.ID
		case "campaign.revoke":
			claim := result.Claim
			var lot BonusSourceLot
			if err := tx.Where("claim_id = ? AND kind = ?", claim.ID, "campaign").First(&lot).Error; err != nil {
				return err
			}
			var user User
			if err := lockForUpdate(tx).First(&user, claim.UserID).Error; err != nil {
				return err
			}
			if err := ensureBonusSourceAccountTx(tx, &user); err != nil {
				return err
			}
			var state BonusSourceState
			if err := tx.First(&state, lot.ID).Error; err != nil {
				return err
			}
			if err := tx.Model(&state).Update("revocation_active", true).Error; err != nil {
				return err
			}
			if state.Available > 0 {
				_, err := DebitQuotaTx(tx, LedgerEntry{UserId: claim.UserID, Account: AccountBonus, Delta: state.Available, Reason: "campaign_revoke", ReferenceType: "bonus_source", ReferenceId: strconv.FormatInt(lot.ID, 10), IdempotencyKey: "campaign-revoke:" + requestID, bonusExpireLot: lot.ID}, AccountBonus)
				if err != nil {
					return err
				}
			}
			claim.Status = "revoked"
			claim.Version++
			claim.ReviewedBy = identity.UserID
			claim.ReviewReason = command.Reason
			if err := tx.Save(claim).Error; err != nil {
				return err
			}
			resourceID = claim.ID
		}
		encoded, err := common.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Create(&AgentCampaignOperation{RequestID: requestID, ActorUserID: identity.UserID, Action: action, Signature: signature, ResourceID: resourceID, Result: string(encoded)}).Error
	})
	if err == nil && result.Claim != nil {
		RefreshUserQuotaCache(result.Claim.UserID, 0)
	}
	return &result, err
}

func FindAgentCampaignOperation(identity AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	var operation AgentCampaignOperation
	if err := DB.Where("request_id = ? AND actor_user_id = ?", requestID, identity.UserID).First(&operation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			claim, claimErr := FindAgentCampaignClaim(identity, requestID)
			if errors.Is(claimErr, gorm.ErrRecordNotFound) {
				return nil, nil
			}
			if claimErr != nil {
				return nil, claimErr
			}
			return &AgentOperationStatus{RequestID: requestID, Action: "campaign.claim", Status: "completed", ResourceType: "campaign_claim", ResourceID: fmt.Sprint(claim.ID), ResourceState: claim.Status, Resource: claim}, nil
		}
		return nil, err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return requireCampaignBrowserTx(tx, identity, true, operation.Action == "campaign.propose")
	}); err != nil {
		return nil, err
	}
	var result AgentCampaignOutcome
	if common.UnmarshalJsonStr(operation.Result, &result) != nil {
		return nil, ErrAgentCampaignConflict
	}
	return &AgentOperationStatus{RequestID: requestID, Action: operation.Action, Status: "completed", ResourceType: "campaign", ResourceID: fmt.Sprint(operation.ResourceID), ResourceState: "committed", Resource: result}, nil
}
