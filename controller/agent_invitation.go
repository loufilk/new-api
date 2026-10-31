package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
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
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || c.GetBool("use_access_token") || identity.UserID != c.GetInt("id") || service.ValidateStepUpIdentity(identity) != nil {
		writeAgentInvitationError(c, model.ErrAgentInvitationPermission)
		return
	}
	if _, err := model.GetAgentInvitationPolicy(identity.UserID); err != nil {
		writeAgentInvitationError(c, err)
		return
	}
	var request struct {
		RequestID        *string `json:"request_id"`
		ExpectedRevision *int64  `json:"expected_revision"`
		Enabled          *bool   `json:"enabled"`
		Quota            *int    `json:"quota"`
	}
	if common.DecodeJsonStrict(c.Request.Body, &request) != nil || request.RequestID == nil || request.ExpectedRevision == nil || request.Enabled == nil || request.Quota == nil {
		writeAgentInvitationError(c, model.ErrAgentInvitationPolicy)
		return
	}
	mutation := model.AgentInvitationPolicyMutation{ExpectedRevision: *request.ExpectedRevision, Enabled: *request.Enabled, Quota: *request.Quota}
	current, err := model.GetCurrentAgentPolicy()
	if err != nil {
		writeAgentInvitationError(c, err)
		return
	}
	profile, err := model.GetAgentProfile(identity.UserID)
	if err != nil {
		writeAgentInvitationError(c, err)
		return
	}
	version := int64(0)
	if profile != nil {
		version = profile.Version
	}
	context, err := model.AgentInvitationPolicyTransactionContext(identity.UserID, version, current.ID, *request.RequestID, mutation)
	if err != nil {
		writeAgentInvitationError(c, err)
		return
	}
	authorization, err := service.PrepareAgentTransactionAuthorization(strings.TrimSpace(c.GetHeader("X-Security-Proof")), identity, context)
	if err != nil {
		writeAgentProfileError(c, err)
		return
	}
	policy, err := model.UpdateAgentInvitationPolicyAuthorized(authorization, *request.RequestID, mutation)
	if err != nil {
		if errors.Is(err, model.ErrAgentInvitationPermission) || errors.Is(err, model.ErrAgentInvitationPolicy) {
			writeAgentInvitationError(c, err)
		} else {
			writeAgentProfileError(c, err)
		}
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
