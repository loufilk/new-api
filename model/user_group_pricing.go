package model

import (
	"context"
	"errors"
	"maps"
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var (
	ErrUserGroupPricingInvalid     = errors.New("Invalid user group pricing")
	ErrUserGroupPricingConflict    = errors.New("User group pricing changed; reload and try again")
	ErrUserGroupPricingPermission  = errors.New("Unauthorized, insufficient privileges")
	ErrUserGroupPricingUnavailable = errors.New("User group pricing is temporarily unavailable. Retry the request.")
)

// UserGroupPricing is deliberately separate from User and its self-editable
// settings. Ratios replace the inherited calling-group rate; they never grant
// access to a group, change User.Group, or multiply another group discount.
type UserGroupPricing struct {
	UserID     int                `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	RatiosJSON string             `json:"-" gorm:"type:text;not null"`
	Ratios     map[string]float64 `json:"ratios" gorm:"-"`
	Revision   int64              `json:"revision" gorm:"not null"`
	UpdatedAt  int64              `json:"updated_at" gorm:"autoUpdateTime:false;not null"`
	UpdatedBy  int                `json:"updated_by" gorm:"not null"`
}

// Pricing audits live with the policy in the primary database, in the same
// transaction. They contain no API keys, session credentials or request bodies.
type UserGroupPricingAudit struct {
	ID                 int64  `json:"id" gorm:"primaryKey;index:idx_ugp_audit_user_time,priority:3"`
	Time               int64  `json:"time" gorm:"not null;index:idx_ugp_audit_user_time,priority:2"`
	UserID             int    `json:"user_id" gorm:"not null;index:idx_ugp_audit_user_time,priority:1"`
	ActorID            int    `json:"actor_id" gorm:"not null"`
	RequestID          string `json:"request_id" gorm:"type:varchar(128);not null"`
	ChangeKind         string `json:"change_kind" gorm:"type:varchar(32);not null"`
	PreviousRatiosJSON string `json:"previous_ratios_json" gorm:"type:text;not null"`
	RatiosJSON         string `json:"ratios_json" gorm:"type:text;not null"`
	Revision           int64  `json:"revision" gorm:"not null"`
}

type UserGroupPricingUpdate struct {
	Ratios           map[string]float64
	ExpectedRevision int64
	RequestID        string
}

func validateUserGroupPricingRatios(ratios map[string]float64) error {
	if ratios == nil {
		return ErrUserGroupPricingInvalid
	}
	for group, ratio := range ratios {
		if _, err := normalizeGroupRenameName(group); err != nil || group == "auto" || ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			return ErrUserGroupPricingInvalid
		}
	}
	return nil
}

func (policy *UserGroupPricing) decodeRatios() error {
	policy.Ratios = map[string]float64{}
	if policy.RatiosJSON != "" {
		if err := common.UnmarshalJsonStr(policy.RatiosJSON, &policy.Ratios); err != nil {
			return ErrUserGroupPricingUnavailable
		}
	}
	if err := validateUserGroupPricingRatios(policy.Ratios); err != nil {
		return ErrUserGroupPricingUnavailable
	}
	return nil
}

// Read the primary DB once per request rather than caching financial policy in
// Redis. A lookup starting after a save commits sees the new revision on every
// node. An admitted request keeps its map through routing retries and settlement.
func GetUserGroupPricing(ctx context.Context, userID int) (*UserGroupPricing, error) {
	if userID <= 0 {
		return nil, ErrUserGroupPricingInvalid
	}
	if DB == nil {
		return nil, ErrUserGroupPricingUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	policy := &UserGroupPricing{UserID: userID, Ratios: map[string]float64{}}
	result := DB.WithContext(bounded).Where("user_id = ?", userID).Limit(1).Find(policy)
	if result.Error != nil {
		return nil, ErrUserGroupPricingUnavailable
	}
	if err := policy.decodeRatios(); err != nil {
		return nil, err
	}
	return policy, nil
}

type requestUserGroupPricing struct {
	userID int
	policy *UserGroupPricing
	err    error
}

const requestUserGroupPricingKey = "request_user_group_pricing"

func GetRequestUserGroupPricing(c *gin.Context, userID int) (*UserGroupPricing, error) {
	if userID <= 0 {
		return &UserGroupPricing{Ratios: map[string]float64{}}, nil
	}
	ctx := context.Background()
	if c != nil {
		if value, exists := c.Get(requestUserGroupPricingKey); exists {
			cached, ok := value.(requestUserGroupPricing)
			if !ok || cached.userID != userID {
				return nil, ErrUserGroupPricingUnavailable
			}
			return cached.policy, cached.err
		}
		if c.Request != nil {
			ctx = c.Request.Context()
		}
	}
	policy, err := GetUserGroupPricing(ctx, userID)
	if c != nil {
		c.Set(requestUserGroupPricingKey, requestUserGroupPricing{userID: userID, policy: policy, err: err})
	}
	return policy, err
}

// ResolveGroupRatio selects one final rate. GroupSpecialRatio retains its
// existing account-class meaning for historical log consumers.
func (policy *UserGroupPricing) ResolveGroupRatio(userGroup, callingGroup string) types.GroupRatioInfo {
	result := types.GroupRatioInfo{GroupRatio: ratio_setting.GetGroupRatio(callingGroup), GroupSpecialRatio: -1, Source: "global"}
	if ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, callingGroup); ok {
		result.GroupRatio, result.GroupSpecialRatio, result.HasSpecialRatio = ratio, ratio, true
		result.Source = "account_group"
	}
	result.InheritedGroupRatio = result.GroupRatio
	if policy != nil {
		result.UserPricingRevision = policy.Revision
		if ratio, ok := policy.Ratios[callingGroup]; ok && callingGroup != "auto" {
			result.GroupRatio, result.Source = ratio, "user"
			result.UserGroupPriceOverride = &ratio
		}
	}
	return result
}

func SaveUserGroupPricing(ctx context.Context, actorID, userID int, update UserGroupPricingUpdate, permissions ...AdminUserPermissionCheck) (*UserGroupPricing, error) {
	if DB == nil {
		return nil, ErrUserGroupPricingUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if actorID <= 0 || userID <= 0 || update.ExpectedRevision < 0 || !validCallInterceptionMetadata(update.RequestID, 128) {
		return nil, ErrUserGroupPricingInvalid
	}
	if err := validateUserGroupPricingRatios(update.Ratios); err != nil {
		return nil, err
	}
	ratios := maps.Clone(update.Ratios)
	data, err := common.Marshal(ratios)
	if err != nil {
		return nil, ErrUserGroupPricingInvalid
	}
	var result UserGroupPricing
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Group writers use namespace -> user locks. Match that order so a
		// concurrent rename cannot leave a rule attached to a retired name.
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		var users []User
		if err := lockForUpdate(tx).Select("id", "role", "status").Where("id IN ?", []int{actorID, userID}).Order("id").Find(&users).Error; err != nil {
			return err
		}
		var actor, target *User
		for i := range users {
			if users[i].Id == actorID {
				actor = &users[i]
			}
			if users[i].Id == userID {
				target = &users[i]
			}
		}
		if actor == nil || actor.Status != common.UserStatusEnabled || actor.Role < common.RoleAdminUser || !common.IsValidateRole(actor.Role) {
			return ErrUserGroupPricingPermission
		}
		if target == nil {
			return gorm.ErrRecordNotFound
		}
		if actor.Role != common.RoleRootUser && actor.Role <= target.Role {
			return ErrUserGroupPricingPermission
		}
		for _, check := range permissions {
			if check == nil {
				return ErrUserGroupPricingPermission
			}
			if err := check(tx, actor); err != nil {
				return err
			}
		}

		values, err := lockAndLoadGroupRenameOptions(tx, []string{"GroupRatio", "group_ratio_setting.group_ratio"})
		if err != nil {
			return err
		}
		if err := validateGroupOptionAliases(values, nil); err != nil {
			return err
		}
		configured := map[string]float64{}
		if err := common.UnmarshalJsonStr(values["GroupRatio"], &configured); err != nil {
			return err
		}
		for group := range ratios {
			if _, exists := configured[group]; !exists {
				return ErrUserGroupPricingInvalid
			}
		}

		previous := UserGroupPricing{UserID: userID, RatiosJSON: "{}"}
		found := tx.Where("user_id = ?", userID).Limit(1).Find(&previous)
		if found.Error != nil {
			return found.Error
		}
		if previous.Revision != update.ExpectedRevision || previous.Revision >= maxPermissionRevision {
			return ErrUserGroupPricingConflict
		}
		result = UserGroupPricing{UserID: userID, RatiosJSON: string(data), Ratios: ratios, Revision: previous.Revision + 1, UpdatedAt: common.GetTimestamp(), UpdatedBy: actorID}
		if found.RowsAffected == 0 {
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
		} else {
			updated := tx.Model(&UserGroupPricing{}).Where("user_id = ? AND revision = ?", userID, previous.Revision).Updates(map[string]any{"ratios_json": result.RatiosJSON, "revision": result.Revision, "updated_at": result.UpdatedAt, "updated_by": actorID})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return ErrUserGroupPricingConflict
			}
		}
		return tx.Create(&UserGroupPricingAudit{Time: result.UpdatedAt, UserID: userID, ActorID: actorID, RequestID: update.RequestID, ChangeKind: "administrator", PreviousRatiosJSON: previous.RatiosJSON, RatiosJSON: result.RatiosJSON, Revision: result.Revision}).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
