package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type groupRenameRequest struct {
	RequestID string                     `json:"request_id"`
	Renames   []model.GroupRenameMapping `json:"renames"`
	Options   map[string]string          `json:"options"`
	Expected  map[string]string          `json:"expected"`
}

func RenameGroups(c *gin.Context) {
	var request groupRenameRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "invalid request body",
		})
		return
	}
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") {
		common.ApiErrorStatus(c, http.StatusForbidden, model.ErrGroupRenamePermission)
		return
	}
	result, err := model.ApplyGroupRename(model.GroupRenameApplyRequest{
		Identity:  &identity,
		Mappings:  request.Renames,
		RequestID: request.RequestID,
		ActorID:   c.GetInt("id"),
		ActorRole: c.GetInt("role"),
		Options:   request.Options,
		Expected:  request.Expected,
	})
	if err != nil {
		status := http.StatusBadRequest
		code := "GROUP_RENAME_REJECTED"
		switch {
		case errors.Is(err, model.ErrGroupRenamePermission):
			status = http.StatusForbidden
			code = "GROUP_RENAME_PERMISSION"
		case errors.Is(err, model.ErrGroupRenameRequestConflict):
			status = http.StatusConflict
			code = "GROUP_RENAME_REQUEST_CONFLICT"
		case errors.Is(err, model.ErrGroupRenameStale):
			status = http.StatusConflict
			code = "GROUP_RENAME_STALE"
		case errors.Is(err, model.ErrGroupRenameUnmappedChange):
			code = "GROUP_RENAME_MAPPING_REQUIRED"
		case errors.Is(err, model.ErrGroupRenameReferencedGroup):
			code = "GROUP_RENAME_REFERENCED"
		case errors.Is(err, model.ErrGroupRenameInvalidAutoGroups):
			code = "GROUP_RENAME_INVALID_AUTO_GROUPS"
		}
		common.ApiErrorStatus(c, status, err, gin.H{"code": code})
		return
	}
	recordManageAudit(c, "option.group_rename", map[string]any{
		"renames":            result.Renames,
		"request_id":         result.RequestID,
		"replayed":           result.Replayed,
		"options":            result.Options,
		"tokens":             result.Tokens,
		"users":              result.Users,
		"channels":           result.Channels,
		"abilities":          result.Abilities,
		"subscription_plans": result.SubscriptionPlans,
		"user_subscriptions": result.UserSubscriptions,
	})
	common.ApiSuccess(c, result)
}
