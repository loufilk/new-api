package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type groupRenameRequest struct {
	Renames  []model.GroupRenameMapping `json:"renames"`
	Options  map[string]string          `json:"options"`
	Expected map[string]string          `json:"expected"`
}

func RenameGroups(c *gin.Context) {
	var request groupRenameRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "invalid request body",
		})
		return
	}
	result, err := model.ApplyGroupRename(model.GroupRenameApplyRequest{
		Mappings: request.Renames,
		Options:  request.Options,
		Expected: request.Expected,
	})
	if err != nil {
		status := http.StatusBadRequest
		code := "GROUP_RENAME_REJECTED"
		switch {
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
		"renames": request.Renames,
		"options": len(request.Options),
	})
	common.ApiSuccess(c, result)
}
