package controller

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func userGroupPricingTarget(c *gin.Context, permission authz.Permission) (*model.User, bool) {
	c.Header("Cache-Control", "no-store")
	actor, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil || actor.Status != common.UserStatusEnabled || actor.Role < common.RoleAdminUser || !common.IsValidateRole(actor.Role) || !authz.Can(actor.Id, actor.Role, permission) {
		userGroupPricingAPIError(c, model.ErrUserGroupPricingPermission)
		return nil, false
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		userGroupPricingAPIError(c, model.ErrUserGroupPricingInvalid)
		return nil, false
	}
	target, err := model.GetUserById(id, false)
	if err != nil {
		userGroupPricingAPIError(c, err)
		return nil, false
	}
	if !canManageTargetRole(actor.Role, target.Role) {
		userGroupPricingAPIError(c, model.ErrUserGroupPricingPermission)
		return nil, false
	}
	return target, true
}

func userGroupPricingAPIError(c *gin.Context, err error) {
	status, message, code := http.StatusServiceUnavailable, model.ErrUserGroupPricingUnavailable.Error(), "user_group_pricing_unavailable"
	switch {
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

type userGroupPricingGroup struct {
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	BaseRatio      float64 `json:"base_ratio"`
	EffectiveRatio float64 `json:"effective_ratio"`
	Available      bool    `json:"available"`
}

func respondUserGroupPricing(c *gin.Context, permission authz.Permission) {
	guard, err := model.BeginGroupConfigRead(c.Request.Context())
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	defer guard.Close()
	target, ok := userGroupPricingTarget(c, permission)
	if !ok {
		return
	}
	policy, err := model.GetUserGroupPricing(c.Request.Context(), target.Id)
	if err != nil {
		userGroupPricingAPIError(c, err)
		return
	}
	usable := service.GetUserUsableGroups(target.Group)
	groups := make([]userGroupPricingGroup, 0)
	for name := range ratio_setting.GetGroupRatioCopy() {
		if name == "auto" {
			continue
		}
		description, available := usable[name]
		price := policy.ResolveGroupRatio(target.Group, name)
		groups = append(groups, userGroupPricingGroup{Name: name, Description: description, BaseRatio: price.InheritedGroupRatio, EffectiveRatio: price.GroupRatio, Available: available})
	}
	slices.SortFunc(groups, func(a, b userGroupPricingGroup) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	if err := guard.Finish(); err != nil {
		groupConfigReadFailure(c)
		return
	}
	common.ApiSuccess(c, struct {
		*model.UserGroupPricing
		Groups []userGroupPricingGroup `json:"groups"`
	}{UserGroupPricing: policy, Groups: groups})
}

func GetUserGroupPricing(c *gin.Context) {
	respondUserGroupPricing(c, authz.UserGroupPricingRead)
}

func UpdateUserGroupPricing(c *gin.Context) {
	target, ok := userGroupPricingTarget(c, authz.UserGroupPricingUpdate)
	if !ok {
		return
	}
	// A dedicated DTO prevents generic User updates and self-profile binding
	// from changing financial policy. Null is not a spelling of a zero rate.
	var request struct {
		ExpectedRevision *int64              `json:"expected_revision"`
		Ratios           map[string]*float64 `json:"ratios"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024))
	if err != nil || common.Unmarshal(body, &request) != nil || request.ExpectedRevision == nil || request.Ratios == nil {
		userGroupPricingAPIError(c, model.ErrUserGroupPricingInvalid)
		return
	}
	ratios := make(map[string]float64, len(request.Ratios))
	for group, ratio := range request.Ratios {
		if ratio == nil {
			userGroupPricingAPIError(c, model.ErrUserGroupPricingInvalid)
			return
		}
		ratios[group] = *ratio
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = common.NewRequestId()
	}
	_, err = model.SaveUserGroupPricing(c.Request.Context(), c.GetInt("id"), target.Id, model.UserGroupPricingUpdate{Ratios: ratios, ExpectedRevision: *request.ExpectedRevision, RequestID: requestID}, func(tx *gorm.DB, actor *model.User) error {
		allowed, err := authz.CanInTx(tx, actor.Id, actor.Role, authz.UserGroupPricingUpdate)
		if err != nil {
			return err
		}
		if !allowed {
			return model.ErrUserGroupPricingPermission
		}
		return nil
	})
	if err != nil {
		userGroupPricingAPIError(c, err)
		return
	}
	markAuditLogged(c)
	respondUserGroupPricing(c, authz.UserGroupPricingUpdate)
}
