package model

import (
	"context"
	"errors"
	"maps"
	"math"
	"strconv"
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

const UserGroupPricingEnabledEnv = "USER_GROUP_PRICING_ENABLED"

func UserGroupPricingEnabled() bool {
	return common.GetEnvOrDefaultBool(UserGroupPricingEnabledEnv, false)
}

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
	frozen     map[string]types.GroupRatioInfo
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
	Group            string
	Ratio            *float64 // nil explicitly restores inherited pricing
	ExpectedRevision int64
	RequestID        string
}

func validPersonalGroupName(group string) bool {
	_, err := normalizeGroupRenameName(group)
	return err == nil && group != "auto" && group != "all"
}

func validPersonalGroupRatio(ratio float64) bool {
	return ratio >= 0 && ratio <= 10000 && !math.IsNaN(ratio) && !math.IsInf(ratio, 0)
}

func validateUserGroupPricingRatios(ratios map[string]float64) error {
	if ratios == nil {
		return ErrUserGroupPricingInvalid
	}
	for group, ratio := range ratios {
		if !validPersonalGroupName(group) || !validPersonalGroupRatio(ratio) {
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

func LoadEffectiveUserGroupPricing(ctx context.Context, userID int) (*UserGroupPricing, error) {
	if userID <= 0 || !UserGroupPricingEnabled() {
		return &UserGroupPricing{UserID: userID}, nil
	}
	return GetUserGroupPricing(ctx, userID)
}

type requestUserGroupPricing struct {
	userID int
	policy *UserGroupPricing
	err    error
}

const requestUserGroupPricingKey = "request_user_group_pricing"

func GetRequestUserGroupPricing(c *gin.Context, userID int, userGroup string) (*UserGroupPricing, error) {
	if userID <= 0 {
		return &UserGroupPricing{Ratios: map[string]float64{}}, nil
	}
	ctx := context.Background()
	if c != nil {
		if value, exists := c.Get(requestUserGroupPricingKey); exists && value != nil {
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
	policy, err := LoadEffectiveUserGroupPricing(ctx, userID)
	if err == nil {
		groups := ratio_setting.GetGroupRatioCopy()
		frozen := make(map[string]types.GroupRatioInfo, len(groups))
		for group := range groups {
			frozen[group] = policy.ResolveGroupRatio(userGroup, group)
		}
		policy.frozen = frozen
	}
	if c != nil {
		c.Set(requestUserGroupPricingKey, requestUserGroupPricing{userID: userID, policy: policy, err: err})
	}
	return policy, err
}

// A persistent WebSocket transports separate billable response.create calls.
func ResetRequestUserGroupPricing(c *gin.Context) {
	c.Set(requestUserGroupPricingKey, nil)
}

// ResolveGroupRatio selects one final rate. GroupSpecialRatio retains its
// existing account-class meaning for historical log consumers.
func (policy *UserGroupPricing) ResolveGroupRatio(userGroup, callingGroup string) types.GroupRatioInfo {
	if policy != nil {
		if frozen, ok := policy.frozen[callingGroup]; ok {
			return frozen
		}
	}
	result := types.GroupRatioInfo{GroupRatio: ratio_setting.GetGroupRatio(callingGroup), GroupSpecialRatio: -1, RateSource: "group"}
	if ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, callingGroup); ok {
		result.GroupRatio, result.GroupSpecialRatio, result.HasSpecialRatio = ratio, ratio, true
		result.RateSource = "group_override"
	}
	result.InheritedGroupRatio = result.GroupRatio
	if policy != nil {
		result.UserPricingRevision = policy.Revision
		if ratio, ok := policy.Ratios[callingGroup]; ok && validPersonalGroupName(callingGroup) && ratio_setting.ContainsGroupRatio(callingGroup) {
			result.GroupRatio, result.RateSource = ratio, "personal"
			result.UserGroupPriceOverride = &ratio
		}
	}
	return result
}

func SaveUserGroupPricing(ctx context.Context, identity AuthSessionIdentity, userID int, update UserGroupPricingUpdate) (*UserGroupPricing, error) {
	if DB == nil {
		return nil, ErrUserGroupPricingUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	actorID := identity.UserID
	if actorID <= 0 {
		return nil, ErrUserGroupPricingPermission
	}
	if userID <= 0 || update.ExpectedRevision < 0 || !validCallInterceptionMetadata(update.RequestID, 128) || !validPersonalGroupName(update.Group) {
		return nil, ErrUserGroupPricingInvalid
	}
	if update.Ratio != nil && !validPersonalGroupRatio(*update.Ratio) {
		return nil, ErrUserGroupPricingInvalid
	}
	var result UserGroupPricing
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Group writers use namespace -> user locks. Match that order so a
		// concurrent rename cannot leave a rule attached to a retired name.
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		// A stale authorized request may have waited on the namespace lock.
		// Revalidate root role, browser session/PAT, auth epoch, and PAT scope
		// inside the same transaction as the policy and audit.
		if err := authorizeGroupRenameTx(tx, &identity); err != nil {
			return ErrUserGroupPricingPermission
		}
		var target User
		if err := lockForUpdate(tx).Select("id").First(&target, userID).Error; err != nil {
			return err
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
		if _, exists := configured[update.Group]; !exists && update.Ratio != nil {
			return ErrUserGroupPricingInvalid
		}

		previous := UserGroupPricing{UserID: userID, RatiosJSON: "{}"}
		found := tx.Where("user_id = ?", userID).Limit(1).Find(&previous)
		if found.Error != nil {
			return found.Error
		}
		if previous.Revision != update.ExpectedRevision || previous.Revision >= maxPermissionRevision {
			return ErrUserGroupPricingConflict
		}
		if err := previous.decodeRatios(); err != nil {
			return err
		}
		ratios := maps.Clone(previous.Ratios)
		if update.Ratio == nil {
			delete(ratios, update.Group)
		} else {
			ratios[update.Group] = *update.Ratio
		}
		if maps.Equal(previous.Ratios, ratios) {
			result = previous
			return nil
		}
		data, err := common.Marshal(ratios)
		if err != nil {
			return ErrUserGroupPricingInvalid
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

// Parse only bounded base-10 configuration values; no exponent, NaN, negative
// discount, or implicit zero. Stored JSON retains zero separately from absence.
func ParsePersonalGroupRatio(raw string) (float64, error) {
	if raw == "" || len(raw) > 14 {
		return 0, ErrUserGroupPricingInvalid
	}
	point := -1
	for i, ch := range raw {
		if ch == '.' && point < 0 && i > 0 && i < len(raw)-1 {
			point = i
		} else if ch < '0' || ch > '9' {
			return 0, ErrUserGroupPricingInvalid
		}
	}
	if point >= 0 && len(raw)-point-1 > 8 {
		return 0, ErrUserGroupPricingInvalid
	}
	ratio, err := strconv.ParseFloat(raw, 64)
	if err != nil || !validPersonalGroupRatio(ratio) {
		return 0, ErrUserGroupPricingInvalid
	}
	return ratio, nil
}
