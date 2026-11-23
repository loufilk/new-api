package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func userGroupPricingAPIError(c *gin.Context, err error) {
	status, message, code := http.StatusServiceUnavailable, model.ErrUserGroupPricingUnavailable.Error(), "user_group_pricing_unavailable"
	switch {
	case errors.Is(err, common.ErrIM21RolloutLocked):
		im21RolloutLocked(c)
		return
	case errors.Is(err, model.ErrUserGroupPricingConflict):
		status, message, code = http.StatusConflict, err.Error(), "user_group_pricing_conflict"
	case errors.Is(err, model.ErrUserGroupPricingInvalid):
		status, message, code = http.StatusBadRequest, err.Error(), "invalid_user_group_pricing"
	case errors.Is(err, model.ErrUserGroupPricingPermission):
		status, message, code = http.StatusForbidden, err.Error(), "forbidden"
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message, code = http.StatusNotFound, "User not found", "user_not_found"
	}
	common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": code})
}

type adminGroupRate struct {
	Group          string  `json:"group"`
	InheritedRatio float64 `json:"inherited_ratio"`
	EffectiveRatio float64 `json:"effective_ratio"`
}

type adminGroupRateRule struct {
	UserID   int    `json:"user_id"`
	Group    string `json:"group"`
	Ratio    string `json:"ratio"`
	Revision int64  `json:"revision"`
	Active   bool   `json:"active"`
}

// GetUserGroupPricing returns only one selected user's billing configuration.
// This is intentionally separate from both public price feeds and the generic
// settings response, which are never allowed to cache or enumerate these rules.
func GetUserGroupPricing(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	actor, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil || actor.Status != common.UserStatusEnabled || actor.Role != common.RoleRootUser {
		userGroupPricingAPIError(c, model.ErrUserGroupPricingPermission)
		return
	}
	userID, err := strconv.Atoi(c.Query("user_id"))
	if err != nil || userID <= 0 {
		userGroupPricingAPIError(c, model.ErrUserGroupPricingInvalid)
		return
	}
	guard, ok := beginControllerGroupConfigRead(c)
	if !ok {
		return
	}
	defer guard.Close()
	target, err := model.GetUserById(userID, false)
	if err != nil {
		userGroupPricingAPIError(c, err)
		return
	}
	policy, err := model.GetUserGroupPricing(c.Request.Context(), userID)
	if err != nil {
		userGroupPricingAPIError(c, err)
		return
	}
	enabled := model.UserGroupPricingEnabled()
	usable, err := service.GetUserUsableGroupsForUser(c.Request.Context(), userID, target.Group)
	if err != nil {
		userGroupPricingAPIError(c, err)
		return
	}
	available := make([]adminGroupRate, 0, len(usable))
	for group := range usable {
		if group == "auto" || group == "all" || !ratio_setting.ContainsGroupRatio(group) {
			continue
		}
		quote := policy.ResolveGroupRatio(target.Group, group)
		rate := adminGroupRate{Group: group, InheritedRatio: quote.InheritedGroupRatio, EffectiveRatio: quote.GroupRatio}
		if !enabled {
			rate.EffectiveRatio = rate.InheritedRatio
		}
		available = append(available, rate)
	}
	slices.SortFunc(available, func(a, b adminGroupRate) int {
		if a.Group < b.Group {
			return -1
		}
		if a.Group > b.Group {
			return 1
		}
		return 0
	})
	rules := make([]adminGroupRateRule, 0, len(policy.Ratios))
	for group, ratio := range policy.Ratios {
		_, allowed := usable[group]
		rules = append(rules, adminGroupRateRule{UserID: userID, Group: group, Ratio: strconv.FormatFloat(ratio, 'f', -1, 64), Revision: policy.Revision, Active: enabled && allowed && ratio_setting.ContainsGroupRatio(group)})
	}
	slices.SortFunc(rules, func(a, b adminGroupRateRule) int {
		if a.Group < b.Group {
			return -1
		}
		if a.Group > b.Group {
			return 1
		}
		return 0
	})
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	common.ApiSuccess(c, gin.H{"user_id": userID, "revision": policy.Revision, "enabled": enabled, "rules": rules, "available_groups": available})
}

func UpdateUserGroupPricing(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") {
		userGroupPricingAPIError(c, model.ErrUserGroupPricingPermission)
		return
	}
	if !common.IM21RolloutActive() {
		im21RolloutLocked(c)
		return
	}
	var request struct {
		UserID           int             `json:"user_id"`
		Group            string          `json:"group"`
		Ratio            json.RawMessage `json:"ratio"`
		ExpectedRevision *int64          `json:"expected_revision"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	if err != nil || common.Unmarshal(body, &request) != nil || request.ExpectedRevision == nil || len(request.Ratio) == 0 {
		userGroupPricingAPIError(c, model.ErrUserGroupPricingInvalid)
		return
	}
	var ratio *float64
	if string(request.Ratio) != "null" {
		var raw string
		if err := common.Unmarshal(request.Ratio, &raw); err != nil {
			userGroupPricingAPIError(c, model.ErrUserGroupPricingInvalid)
			return
		}
		value, err := model.ParsePersonalGroupRatio(raw)
		if err != nil {
			userGroupPricingAPIError(c, err)
			return
		}
		ratio = &value
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = common.NewRequestId()
	}
	result, err := model.SaveUserGroupPricing(c.Request.Context(), identity, request.UserID, model.UserGroupPricingUpdate{
		Group: request.Group, Ratio: ratio, ExpectedRevision: *request.ExpectedRevision, RequestID: requestID,
	})
	if err != nil {
		userGroupPricingAPIError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"user_id": result.UserID, "revision": result.Revision, "enabled": model.UserGroupPricingEnabled()})
}
