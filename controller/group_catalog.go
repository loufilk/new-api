package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetGroupCatalog(c *gin.Context) {
	result, err := model.ReadGroupCatalog(c.Request.Context())
	if err != nil {
		groupCatalogError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func PatchGroupCatalog(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") {
		groupCatalogError(c, model.ErrGroupRenamePermission)
		return
	}
	var request struct {
		Revision *int64                             `json:"revision"`
		Groups   map[string]model.GroupCatalogEntry `json:"groups"`
	}
	if err := common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024), &request); err != nil || request.Revision == nil {
		groupCatalogError(c, model.ErrInvalidGroupCatalog)
		return
	}
	result, err := model.UpdateGroupCatalog(c.Request.Context(), model.GroupCatalogSnapshot{Revision: *request.Revision, Groups: request.Groups}, identity)
	if err != nil {
		groupCatalogError(c, err)
		return
	}
	recordManageAudit(c, "option.group_catalog.update", map[string]any{"revision": result.Revision, "count": len(result.Groups)})
	common.ApiSuccess(c, result)
}

func GetUserGroupEntitlements(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || id <= 0 {
		groupCatalogError(c, model.ErrInvalidGroupCatalog)
		return
	}
	result, err := model.ReadUserGroupEntitlements(c.Request.Context(), id)
	if err != nil {
		groupCatalogError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func PutUserGroupEntitlements(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") {
		groupCatalogError(c, model.ErrGroupRenamePermission)
		return
	}
	id, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || id <= 0 {
		groupCatalogError(c, model.ErrInvalidGroupCatalog)
		return
	}
	var request struct {
		Revision *int64   `json:"revision"`
		Groups   []string `json:"groups"`
	}
	if err := common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024), &request); err != nil {
		groupCatalogError(c, model.ErrInvalidGroupCatalog)
		return
	}
	result, err := model.UpdateUserGroupEntitlements(c.Request.Context(), id, request.Revision, request.Groups, identity)
	if err != nil {
		groupCatalogError(c, err)
		return
	}
	recordManageAuditFor(c, id, "option.group_entitlements.replace", map[string]any{"revision": result.Revision, "groups": result.Groups})
	common.ApiSuccess(c, result)
}

func groupCatalogError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrInvalidGroupCatalog), errors.Is(err, model.ErrGroupCatalogGrants):
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
	case errors.Is(err, model.ErrGroupRenamePermission):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Group catalog changes require a current root identity"))
	case errors.Is(err, model.ErrGroupConfigChanged):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Group settings changed or are temporarily unavailable. Retry the request."))
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("User not found"))
	default:
		groupConfigReadFailure(c)
	}
}
