package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type AgentCampaignView struct {
	AgentCampaign
	Status            string   `json:"status"`
	Version           int64    `json:"version"`
	ApprovedBy        int      `json:"approved_by"`
	ApprovedAt        int64    `json:"approved_at"`
	CanClaim          bool     `json:"can_claim"`
	EligibilityReason string   `json:"eligibility_reason"`
	AllowedActions    []string `json:"allowed_actions"`
}
type AgentCampaignClaimView struct {
	AgentCampaignClaim
	Revocable      bool     `json:"revocable"`
	ExpiresAt      int64    `json:"expires_at"`
	RefundTerms    string   `json:"refund_terms"`
	AllowedActions []string `json:"allowed_actions"`
}
type BonusSourceAllocationView struct {
	BonusSourceAllocation
	Reason       string `json:"reason"`
	BillingState string `json:"billing_state"`
	TaskState    string `json:"task_state"`
}
type BonusSourceView struct {
	Lot   BonusSourceLot   `json:"lot"`
	State BonusSourceState `json:"state"`
}

func ListAgentCampaignRecords(identity AuthSessionIdentity, kind string, owner, page, size int, requestID string, admin bool) (any, int64, error) {
	page = max(1, min(page, 1_000_000))
	size = max(1, min(size, 100))
	var result any
	var total int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireCampaignBrowserTx(tx, identity, admin, false); err != nil {
			return err
		}
		if !admin {
			if owner != 0 && owner != identity.UserID {
				return ErrAgentProfilePermission
			}
			owner = identity.UserID
		}
		query := tx
		switch kind {
		case "campaigns":
			query = query.Model(&AgentCampaign{})
			if !admin {
				query = query.Where("id IN (?)", tx.Model(&AgentCampaignPublication{}).Select("campaign_id").Where("status = ?", "approved"))
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			var rows []AgentCampaign
			if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
				return err
			}
			policy, err := GetCurrentAgentPolicyWithDB(tx)
			if err != nil {
				return err
			}
			var actor User
			if err := tx.First(&actor, identity.UserID).Error; err != nil {
				return err
			}
			subject, err := campaignLinuxDOSubjectTx(tx, &actor)
			if err != nil {
				return err
			}
			views := make([]AgentCampaignView, 0, len(rows))
			now := agentCampaignNow()
			for _, row := range rows {
				var publication AgentCampaignPublication
				if err := tx.First(&publication, row.ID).Error; err != nil {
					return err
				}
				view := AgentCampaignView{AgentCampaign: row, Status: publication.Status, Version: publication.Version, ApprovedBy: publication.ApprovedBy, ApprovedAt: publication.ApprovedAt, AllowedActions: []string{}}
				switch {
				case !policy.CampaignEnabled:
					view.EligibilityReason = "feature_disabled"
				case publication.Status != "approved":
					view.EligibilityReason = "not_approved"
				case now < row.ClaimStartAt:
					view.EligibilityReason = "not_started"
				case now >= row.ClaimEndAt:
					view.EligibilityReason = "claim_window_closed"
				case row.RequireLinuxDO && subject == "":
					view.EligibilityReason = "linuxdo_binding_required"
				default:
					view.CanClaim = true
					view.EligibilityReason = "eligible_subject_to_budget"
				}
				if view.CanClaim && row.RequireAgentInvitation {
					var count int64
					if err := tx.Model(&AgentInvitation{}).Where("user_id = ?", actor.Id).Count(&count).Error; err != nil {
						return err
					}
					if count == 0 {
						view.CanClaim = false
						view.EligibilityReason = "agent_invitation_required"
					}
				}
				if view.CanClaim {
					key, err := campaignDedupeHashTx(tx, fmt.Sprintf("family:%s:user:%d:event:account_claim", row.FamilyKey, actor.Id), false)
					if err != nil {
						return err
					}
					var count int64
					if err := tx.Model(&AgentCampaignDedupe{}).Where("key = ?", key).Count(&count).Error; err != nil {
						return err
					}
					if count > 0 {
						view.CanClaim = false
						view.EligibilityReason = "already_claimed"
					}
				}
				if admin && publication.Status == "proposed" && row.ProposedBy != actor.Id && policy.CampaignEnabled {
					view.AllowedActions = append(view.AllowedActions, "campaign.approve")
				}
				views = append(views, view)
			}
			result = views
		case "claims":
			query = query.Model(&AgentCampaignClaim{})
			if owner > 0 {
				query = query.Where("user_id = ?", owner)
			}
			if requestID != "" {
				query = query.Where("request_id = ?", requestID)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []AgentCampaignClaim{}
			if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
				return err
			}
			views := make([]AgentCampaignClaimView, 0, len(rows))
			policy, err := GetCurrentAgentPolicyWithDB(tx)
			if err != nil {
				return err
			}
			for _, row := range rows {
				var campaign AgentCampaign
				if err := tx.First(&campaign, row.CampaignID).Error; err != nil {
					return err
				}
				view := AgentCampaignClaimView{AgentCampaignClaim: row, Revocable: campaign.Revocable, ExpiresAt: campaign.ExpiresAt, RefundTerms: campaign.RefundTerms, AllowedActions: []string{}}
				if !admin {
					view.ReviewReason = ""
					view.ReviewedBy = 0
				} else if identity.UserID != row.UserID {
					if row.Status == "review_pending" && row.ReviewDeadline > agentCampaignNow() {
						view.AllowedActions = append(view.AllowedActions, "campaign.review.reject")
						if policy.CampaignEnabled && campaign.ExpiresAt > agentCampaignNow() {
							view.AllowedActions = append(view.AllowedActions, "campaign.review.approve")
						}
					}
					if row.Status == "granted" && campaign.Revocable {
						view.AllowedActions = append(view.AllowedActions, "campaign.revoke")
					}
				}
				views = append(views, view)
			}
			result = views
		case "sources":
			query = query.Model(&BonusSourceLot{})
			if owner > 0 {
				query = query.Where("user_id = ?", owner)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			var rows []BonusSourceLot
			if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
				return err
			}
			views := make([]BonusSourceView, 0, len(rows))
			for _, row := range rows {
				var state BonusSourceState
				if err := tx.First(&state, row.ID).Error; err != nil {
					return err
				}
				views = append(views, BonusSourceView{Lot: row, State: state})
			}
			result = views
		case "allocations":
			query = query.Model(&BonusSourceAllocation{})
			if owner > 0 {
				query = query.Where("user_id = ?", owner)
			}
			if requestID != "" {
				query = query.Where("reference_id = ?", requestID)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			var rows []BonusSourceAllocation
			if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
				return err
			}
			views := make([]BonusSourceAllocationView, 0, len(rows))
			for _, row := range rows {
				view := BonusSourceAllocationView{BonusSourceAllocation: row, BillingState: "unclassified_debit"}
				var ledger BalanceLedger
				if err := tx.First(&ledger, row.LedgerID).Error; err != nil {
					return err
				}
				view.Reason = ledger.Reason
				if row.ReferenceType == "relay" {
					var finalization BillingFinalization
					if err := tx.Where("request_id = ? AND user_id = ?", row.ReferenceID, row.UserID).Limit(1).Find(&finalization).Error; err != nil {
						return err
					}
					if finalization.RequestID != "" {
						view.BillingState = finalization.State
					}
					if finalization.TaskID > 0 {
						var task Task
						if err := tx.Where("id = ? AND user_id = ?", finalization.TaskID, row.UserID).Limit(1).Find(&task).Error; err != nil {
							return err
						}
						if task.ID > 0 {
							view.TaskState = string(task.Status)
						}
					}
				} else if row.ReferenceType == "task" {
					var task Task
					if err := tx.Where("task_id = ? AND user_id = ?", row.ReferenceID, row.UserID).Limit(1).Find(&task).Error; err != nil {
						return err
					}
					if task.ID > 0 {
						view.TaskState = string(task.Status)
					}
				}
				views = append(views, view)
			}
			result = views
		case "restorations":
			query = query.Model(&BonusSourceRestoration{})
			if owner > 0 {
				query = query.Where("allocation_id IN (?)", tx.Model(&BonusSourceAllocation{}).Select("id").Where("user_id = ?", owner))
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []BonusSourceRestoration{}
			if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
				return err
			}
			result = rows
		case "removals":
			query = query.Model(&BonusSourceRemoval{})
			if owner > 0 {
				query = query.Where("user_id = ?", owner)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []BonusSourceRemoval{}
			if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
				return err
			}
			result = rows
		case "budgets":
			if !admin {
				return ErrAgentProfilePermission
			}
			query = query.Model(&AgentCampaignBudget{})
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []AgentCampaignBudget{}
			if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
				return err
			}
			result = rows
		case "anomalies":
			if !admin {
				return ErrAgentProfilePermission
			}
			query = query.Model(&BonusSourceAnomaly{})
			if owner > 0 {
				query = query.Where("user_id = ?", owner)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []BonusSourceAnomaly{}
			if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
				return err
			}
			result = rows
		default:
			return ErrAgentCampaignInvalid
		}
		return nil
	})
	return result, total, err
}

// A read does not lock/publish policy and remains available while new campaigns
// are disabled. Existing bonus sources and old claim receipts stay accessible.
func GetCurrentAgentPolicyWithDB(tx *gorm.DB) (*AgentPolicy, error) {
	var state AgentPolicyState
	if err := tx.First(&state, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &AgentPolicy{}, nil
		}
		return nil, err
	}
	if state.CurrentPolicyID == 0 {
		return &AgentPolicy{}, nil
	}
	var p AgentPolicy
	err := tx.First(&p, state.CurrentPolicyID).Error
	return &p, err
}

func FindAgentCampaignClaim(identity AuthSessionIdentity, requestID string) (*AgentCampaignClaim, error) {
	if validateAgentRequestID(requestID) != nil {
		return nil, ErrAgentCampaignInvalid
	}
	var claim AgentCampaignClaim
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireCampaignBrowserTx(tx, identity, false, false); err != nil {
			return err
		}
		return tx.Where("request_id = ? AND user_id = ?", requestID, identity.UserID).First(&claim).Error
	})
	claim.ReviewReason = ""
	claim.ReviewedBy = 0
	return &claim, err
}
