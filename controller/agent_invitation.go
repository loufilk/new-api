package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetAgentInvitations(c *gin.Context) {
	inviterID := 0
	if value := c.Query("agent_user_id"); value != "" {
		var err error
		inviterID, err = strconv.Atoi(value)
		if err != nil || inviterID <= 0 {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
			return
		}
	}
	page := common.GetPageQuery(c)
	page.Page = max(1, min(page.Page, 1_000_000))
	page.PageSize = max(1, min(page.PageSize, 100))
	items, total, err := model.ListAgentInvitations(c.GetInt("id"), page.Page, page.PageSize, inviterID)
	if err != nil {
		writeAgentInvitationError(c, err)
		return
	}
	page.SetItems(items)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func GetAgentInvitationPolicy(c *gin.Context) {
	policy, err := model.GetAgentInvitationPolicy(c.GetInt("id"))
	if err != nil {
		writeAgentInvitationError(c, err)
		return
	}
	common.ApiSuccess(c, policy)
}

func UpdateAgentInvitationPolicy(c *gin.Context) {
	// Bind only these fields, never a model, actor ID, role or ledger entry.
	var request struct {
		Enabled *bool `json:"enabled"`
		Quota   *int  `json:"quota"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Enabled == nil || request.Quota == nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	policy, err := model.UpdateAgentInvitationPolicy(c.GetInt("id"), *request.Enabled, *request.Quota)
	if err != nil {
		writeAgentInvitationError(c, err)
		return
	}
	common.ApiSuccess(c, policy)
}

func writeAgentInvitationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAgentInvitationPermission):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
	case errors.Is(err, model.ErrAgentInvitationPolicy):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
	default:
		// Database details and audit payloads are never returned to the caller.
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Database error, please contact the administrator"))
	}
}
