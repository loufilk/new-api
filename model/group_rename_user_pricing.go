package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Only policies owned by existing users follow group renames. Disabled users
// retain their policies; deleted owners' policy and audit history stay intact.
// Admitted tasks and usage logs retain their original financial snapshot.
func renameGroupUserPricing(tx *gorm.DB, plan *groupRenamePlan, actorID int) error {
	if len(plan.mappings) == 0 {
		return nil
	}
	lastUserID := 0
	for {
		var policies []UserGroupPricing
		if err := lockForUpdate(tx).Where("user_id IN (?)", tx.Model(&User{}).Select("id")).Where("user_id > ?", lastUserID).Order("user_id").Limit(256).Find(&policies).Error; err != nil {
			return err
		}
		if len(policies) == 0 {
			return nil
		}
		for i := range policies {
			policy := &policies[i]
			lastUserID = policy.UserID
			if err := policy.decodeRatios(); err != nil {
				return err
			}
			changed := false
			for oldGroup, newGroup := range plan.sources {
				if ratio, exists := policy.Ratios[oldGroup]; exists {
					if _, collision := policy.Ratios[newGroup]; collision {
						return ErrGroupRenameCollision
					}
					delete(policy.Ratios, oldGroup)
					policy.Ratios[newGroup], changed = ratio, true
				}
			}
			if !changed {
				continue
			}
			if policy.Revision >= maxPermissionRevision {
				return ErrUserGroupPricingConflict
			}
			data, err := common.Marshal(policy.Ratios)
			if err != nil {
				return err
			}
			now := common.GetTimestamp()
			if err := tx.Model(&UserGroupPricing{}).Where("user_id = ?", policy.UserID).Updates(map[string]any{"ratios_json": string(data), "revision": policy.Revision + 1, "updated_at": now, "updated_by": actorID}).Error; err != nil {
				return err
			}
			if err := tx.Create(&UserGroupPricingAudit{Time: now, UserID: policy.UserID, ActorID: actorID, ChangeKind: "group_rename", PreviousRatiosJSON: policy.RatiosJSON, RatiosJSON: string(data), Revision: policy.Revision + 1}).Error; err != nil {
				return err
			}
		}
	}
}

func userGroupPricingReferencesGroup(tx *gorm.DB, group string) (bool, error) {
	lastUserID := 0
	for {
		var policies []UserGroupPricing
		if err := tx.Where("user_id IN (?)", tx.Model(&User{}).Select("id")).Where("user_id > ?", lastUserID).Order("user_id").Limit(256).Find(&policies).Error; err != nil {
			return false, err
		}
		if len(policies) == 0 {
			return false, nil
		}
		for i := range policies {
			policy := &policies[i]
			lastUserID = policy.UserID
			if err := policy.decodeRatios(); err != nil {
				return false, err
			}
			if _, exists := policy.Ratios[group]; exists {
				return true, nil
			}
		}
	}
}
