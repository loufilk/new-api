package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func campaignDedupeHashTx(tx *gorm.DB, value string, create bool) (string, error) {
	private := tx.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var key AgentCampaignPrivacyKey
	err := private.First(&key, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var retained int64
		if err := private.Model(&AgentCampaignDedupe{}).Count(&retained).Error; err != nil {
			return "", err
		}
		if retained > 0 {
			return "", ErrBonusSourceInvariant
		}
		if !create {
			return "", nil
		}
		var entropy [32]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return "", err
		}
		key = AgentCampaignPrivacyKey{ID: 1, Secret: hex.EncodeToString(entropy[:])}
		if err := private.Clauses(clause.OnConflict{DoNothing: true}).Create(&key).Error; err != nil {
			return "", err
		}
		if err := private.First(&key, 1).Error; err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	decoded, decodeErr := hex.DecodeString(key.Secret)
	if len(key.Secret) != 64 || decodeErr != nil || len(decoded) != 32 {
		return "", ErrBonusSourceInvariant
	}
	return common.GenerateHMACWithKey([]byte("campaign-dedupe-v1:"+key.Secret), value), nil
}

// Only a current supported provider ownership claim is a verified signal.
// A bare historical field/email/IP is not treated as strong identity evidence.
func campaignLinuxDOSubjectTx(tx *gorm.DB, user *User) (string, error) {
	if user.LinuxDOId == "" {
		return "", nil
	}
	var binding ExternalIdentityClaim
	err := tx.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Where("provider = ? AND user_id = ? AND subject = ?", ExternalIdentityProviderLinuxDO, user.Id, user.LinuxDOId).Limit(1).Find(&binding).Error
	if err != nil {
		return "", err
	}
	if binding.Id == 0 {
		return "", nil
	}
	return binding.Subject, nil
}

type AgentCampaignClaimRequest struct {
	RequestID      string `json:"request_id"`
	CampaignID     int64  `json:"campaign_id"`
	TermsHash      string `json:"terms_hash"`
	ConfirmNoncash bool   `json:"confirm_noncash"`
}

func requireCampaignBrowserTx(tx *gorm.DB, identity AuthSessionIdentity, staff, root bool) error {
	if identity.UserID <= 0 || identity.SessionID == "" {
		return ErrAgentProfilePermission
	}
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return ErrAgentProfilePermission
	}
	var user User
	if err := lockForUpdate(tx).First(&user, identity.UserID).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled || staff && user.Role != common.RoleAdminUser && user.Role != common.RoleRootUser || root && user.Role != common.RoleRootUser {
		return ErrAgentProfilePermission
	}
	return ValidateAuthSessionWithTx(tx, identity)
}

func campaignBudgetTransitionTx(tx *gorm.DB, campaign *AgentCampaign, claim *AgentCampaignClaim, transition string) error {
	scopes := []struct {
		kind, key string
		limit     int
	}{{"global", "all", campaign.TotalBudgetQuota}, {"utc_day", claim.BudgetDay, campaign.DailyBudgetQuota}, {"agent", strconv.Itoa(claim.AgentUserID), campaign.PerAgentBudgetQuota}}
	for _, scope := range scopes {
		budget := AgentCampaignBudget{CampaignID: campaign.ID, Scope: scope.kind, ScopeKey: scope.key, LimitQuota: scope.limit}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&budget).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("campaign_id = ? AND scope = ? AND scope_key = ?", campaign.ID, scope.kind, scope.key).First(&budget).Error; err != nil {
			return err
		}
		if budget.LimitQuota != scope.limit || budget.ReservedQuota < 0 || budget.SpentQuota < 0 || budget.SpentQuota > budget.LimitQuota-budget.ReservedQuota || budget.ReleasedQuota < 0 {
			return ErrBonusSourceInvariant
		}
		switch transition {
		case "reserve":
			if claim.Quota > budget.LimitQuota-budget.SpentQuota-budget.ReservedQuota {
				return ErrAgentCampaignBudget
			}
			budget.ReservedQuota += claim.Quota
		case "spend":
			if claim.Quota > budget.ReservedQuota {
				return ErrBonusSourceInvariant
			}
			budget.ReservedQuota -= claim.Quota
			budget.SpentQuota += claim.Quota
		case "release":
			if claim.Quota > budget.ReservedQuota || claim.Quota > common.MaxWalletQuota-budget.ReleasedQuota {
				return ErrBonusSourceInvariant
			}
			budget.ReservedQuota -= claim.Quota
			budget.ReleasedQuota += claim.Quota
		default:
			return ErrAgentCampaignInvalid
		}
		if err := tx.Model(&budget).Updates(map[string]any{"reserved_quota": budget.ReservedQuota, "spent_quota": budget.SpentQuota, "released_quota": budget.ReleasedQuota}).Error; err != nil {
			return err
		}
		if err := tx.Create(&AgentCampaignBudgetEntry{BudgetID: budget.ID, ClaimID: claim.ID, Transition: transition, Quota: claim.Quota}).Error; err != nil {
			return err
		}
	}
	return nil
}
func grantAgentCampaignClaimTx(tx *gorm.DB, campaign *AgentCampaign, claim *AgentCampaignClaim) error {
	if campaign.ExpiresAt <= agentCampaignNow() {
		return ErrAgentCampaignIneligible
	}
	if err := campaignBudgetTransitionTx(tx, campaign, claim, "spend"); err != nil {
		return err
	}
	_, err := CreditQuotaTx(tx, LedgerEntry{UserId: claim.UserID, Account: AccountBonus, Delta: claim.Quota, Reason: "campaign_grant", ReferenceType: "campaign_claim", ReferenceId: strconv.FormatInt(claim.ID, 10), IdempotencyKey: fmt.Sprintf("campaign-grant:%d", claim.ID), bonusGrant: &bonusSourceGrant{CampaignID: campaign.ID, ClaimID: claim.ID, ExpiresAt: campaign.ExpiresAt, Revocable: campaign.Revocable}})
	if err != nil {
		return err
	}
	claim.Status = "granted"
	claim.Version++
	return tx.Model(claim).Updates(map[string]any{"status": claim.Status, "version": claim.Version}).Error
}

func ClaimAgentCampaign(identity AuthSessionIdentity, request AgentCampaignClaimRequest) (*AgentCampaignClaim, error) {
	if validateAgentRequestID(request.RequestID) != nil || request.CampaignID <= 0 || len(request.TermsHash) != 64 || !request.ConfirmNoncash {
		return nil, ErrAgentCampaignInvalid
	}
	signature, err := AgentTransactionParametersHash(request)
	if err != nil {
		return nil, err
	}
	var result AgentCampaignClaim
	err = DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if err := requireCampaignBrowserTx(tx, identity, false, false); err != nil {
			return err
		}
		err = tx.Where("request_id = ?", request.RequestID).First(&result).Error
		if err == nil {
			if result.UserID != identity.UserID || result.Signature != signature {
				return ErrAgentCampaignConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var conflicting int64
		if err := tx.Model(&AgentCampaignOperation{}).Where("request_id = ?", request.RequestID).Count(&conflicting).Error; err != nil {
			return err
		}
		if conflicting > 0 {
			return ErrAgentCampaignConflict
		}
		if !policy.CampaignEnabled || !policy.CampaignTermsApproved || !policy.RiskPolicyApproved {
			return ErrAgentFeatureDisabled
		}
		var campaign AgentCampaign
		var publication AgentCampaignPublication
		if err := tx.First(&campaign, request.CampaignID).Error; err != nil {
			return err
		}
		if err := tx.First(&publication, campaign.ID).Error; err != nil {
			return err
		}
		if err := validateAgentCampaignTerms(campaign.AgentCampaignTerms); err != nil {
			return err
		}
		now := agentCampaignNow()
		if publication.Status != "approved" || campaign.TermsHash != request.TermsHash || now < campaign.ClaimStartAt || now >= campaign.ClaimEndAt {
			return ErrAgentCampaignIneligible
		}
		var user User
		if err := tx.First(&user, identity.UserID).Error; err != nil {
			return err
		}
		subject, err := campaignLinuxDOSubjectTx(tx, &user)
		if err != nil {
			return err
		}
		if campaign.RequireLinuxDO && subject == "" {
			return ErrAgentCampaignIneligible
		}
		agentID := 0
		var invitation AgentInvitation
		if err := tx.Where("user_id = ?", user.Id).Limit(1).Find(&invitation).Error; err != nil {
			return err
		}
		if invitation.Id != 0 {
			agentID = invitation.InviterId
		}
		if campaign.RequireAgentInvitation && agentID == 0 {
			return ErrAgentCampaignIneligible
		}
		result = AgentCampaignClaim{RequestID: request.RequestID, Signature: signature, CampaignID: campaign.ID, UserID: user.Id, AgentUserID: agentID, Quota: campaign.GrantQuota, Status: "review_pending", Version: 1, BudgetDay: time.Unix(now, 0).UTC().Format("2006-01-02"), TermsHash: campaign.TermsHash}
		if campaign.ManualReview {
			result.ReviewDeadline = min(now+campaign.ReviewWindowSeconds, campaign.ClaimEndAt)
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		keys := []string{fmt.Sprintf("family:%s:user:%d:event:account_claim", campaign.FamilyKey, user.Id)}
		if subject != "" {
			keys = append(keys, "family:"+campaign.FamilyKey+":linuxdo:"+subject)
		}
		if campaign.ExclusionGroup != "" {
			keys = append(keys, fmt.Sprintf("exclusion:%s:user:%d", campaign.ExclusionGroup, user.Id))
			if subject != "" {
				keys = append(keys, "exclusion:"+campaign.ExclusionGroup+":linuxdo:"+subject)
			}
		}
		for _, key := range keys {
			digest, err := campaignDedupeHashTx(tx, key, true)
			if err != nil {
				return err
			}
			row := AgentCampaignDedupe{Key: digest, ClaimID: result.ID}
			inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if inserted.Error != nil {
				return inserted.Error
			}
			if inserted.RowsAffected != 1 {
				return ErrAgentCampaignIneligible
			}
		}
		if err := campaignBudgetTransitionTx(tx, &campaign, &result, "reserve"); err != nil {
			return err
		}
		if !campaign.ManualReview {
			return grantAgentCampaignClaimTx(tx, &campaign, &result)
		}
		return nil
	})
	if err == nil {
		RefreshUserQuotaCache(identity.UserID, 0)
	}
	result.ReviewReason = ""
	result.ReviewedBy = 0
	return &result, err
}
