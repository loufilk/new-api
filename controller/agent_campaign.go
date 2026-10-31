package controller

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func campaignIdentity(c *gin.Context) (model.AuthSessionIdentity, bool) {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") || c.GetBool("use_access_token") {
		campaignError(c, model.ErrAgentProfilePermission)
		return model.AuthSessionIdentity{}, false
	}
	if _, pat := model.ParseAccessTokenSessionID(identity.SessionID); pat {
		campaignError(c, model.ErrAgentProfilePermission)
		return model.AuthSessionIdentity{}, false
	}
	return identity, true
}
func campaignError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAgentProfilePermission), errors.Is(err, model.ErrAgentTransactionProof), errors.Is(err, model.ErrUserSessionInactive):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
	case errors.Is(err, model.ErrAgentCampaignConflict), errors.Is(err, model.ErrAgentPolicyChanged), errors.Is(err, model.ErrAgentProfileConflict):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Financial operation changed; reload and prepare it again"))
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Financial record not found"))
	case errors.Is(err, model.ErrAgentCampaignInvalid), errors.Is(err, model.ErrAgentCampaignIneligible), errors.Is(err, model.ErrAgentCampaignBudget), errors.Is(err, model.ErrAgentFeatureDisabled):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
	default:
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Financial operation failed; check its status before retrying"))
	}
}
func AgentCampaignListHandler(kind string, admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := campaignIdentity(c)
		if !ok {
			return
		}
		owner := 0
		if raw := c.Query("user_id"); raw != "" {
			var err error
			owner, err = strconv.Atoi(raw)
			if err != nil || owner <= 0 {
				campaignError(c, model.ErrAgentCampaignInvalid)
				return
			}
		}
		page := common.GetPageQuery(c)
		page.Page = max(1, min(page.Page, 1_000_000))
		page.PageSize = max(1, min(page.PageSize, 100))
		result, total, err := model.ListAgentCampaignRecords(identity, kind, owner, page.Page, page.PageSize, c.Query("request_id"), admin)
		if err != nil {
			campaignError(c, err)
			return
		}
		page.SetItems(result)
		page.SetTotal(int(total))
		common.ApiSuccess(c, page)
	}
}
func ClaimAgentCampaign(c *gin.Context) {
	identity, ok := campaignIdentity(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var request model.AgentCampaignClaimRequest
	if common.DecodeJsonStrict(c.Request.Body, &request) != nil {
		campaignError(c, model.ErrAgentCampaignInvalid)
		return
	}
	result, err := model.ClaimAgentCampaign(identity, request)
	if err != nil {
		campaignError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAgentCampaignClaim(c *gin.Context) {
	identity, ok := campaignIdentity(c)
	if !ok {
		return
	}
	result, err := model.FindAgentCampaignClaim(identity, c.Param("request_id"))
	if err != nil {
		campaignError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func AgentCampaignOperationHandler(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		action := action
		identity, ok := campaignIdentity(c)
		if !ok {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
		raw, readErr := io.ReadAll(c.Request.Body)
		if readErr != nil {
			campaignError(c, model.ErrAgentCampaignInvalid)
			return
		}
		var payload any
		requestID := ""
		if action == "campaign.propose" {
			var request model.AgentCampaignProposeRequest
			if common.DecodeJsonStrict(bytes.NewReader(raw), &request) != nil {
				campaignError(c, model.ErrAgentCampaignInvalid)
				return
			}
			payload = request
			requestID = request.RequestID
		} else {
			var request model.AgentCampaignActionRequest
			if common.DecodeJsonStrict(bytes.NewReader(raw), &request) != nil {
				campaignError(c, model.ErrAgentCampaignInvalid)
				return
			}
			payload = request
			requestID = request.RequestID
			if action == "campaign.review" {
				if request.Decision != "approve" && request.Decision != "reject" {
					campaignError(c, model.ErrAgentCampaignInvalid)
					return
				}
				action = "campaign.review." + request.Decision
			}
		}
		if result, replayed, err := model.ReplayAgentCampaignOperation(identity, action, requestID, payload); err != nil {
			campaignError(c, err)
			return
		} else if replayed {
			common.ApiSuccess(c, result)
			return
		}
		encoded, err := raw, error(nil)
		if err != nil {
			campaignError(c, err)
			return
		}
		prepared, err := model.PrepareAgentCampaignOperation(identity, action, requestID, encoded)
		if err != nil {
			campaignError(c, err)
			return
		}
		authorization, err := service.PrepareAgentTransactionAuthorization(c.GetHeader("X-Security-Proof"), identity, prepared.VerificationContext)
		if err != nil {
			campaignError(c, err)
			return
		}
		result, err := model.ExecuteAgentCampaignOperation(authorization, action, requestID, encoded)
		if err != nil {
			campaignError(c, err)
			return
		}
		common.ApiSuccess(c, result)
	}
}
