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
	"gorm.io/gorm"
)

func GetAgentPlatformSelf(c *gin.Context) {
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		writeAgentProfileError(c, err)
		return
	}
	profile, err := model.GetAgentProfile(user.Id)
	if err != nil {
		writeAgentProfileError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"profile": profile, "capabilities": model.AgentCapabilitiesFor(user, profile)})
}

func GetAgentProfiles(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		writeAgentProfileError(c, model.ErrAgentProfilePermission)
		return
	}
	var actor model.User
	if err := model.DB.First(&actor, identity.UserID).Error; err != nil {
		writeAgentProfileError(c, err)
		return
	}
	if actor.Status != common.UserStatusEnabled || (actor.Role != common.RoleAdminUser && actor.Role != common.RoleRootUser) || service.ValidateStepUpIdentity(identity) != nil {
		writeAgentProfileError(c, model.ErrAgentProfilePermission)
		return
	}
	page := common.GetPageQuery(c)
	page.Page, page.PageSize = max(1, min(page.Page, 1_000_000)), max(1, min(page.PageSize, 100))
	var total int64
	var profiles []model.AgentProfile
	if err := model.DB.Model(&model.AgentProfile{}).Count(&total).Error; err != nil {
		writeAgentProfileError(c, err)
		return
	}
	if err := model.DB.Order("user_id").Offset((page.Page - 1) * page.PageSize).Limit(page.PageSize).Find(&profiles).Error; err != nil {
		writeAgentProfileError(c, err)
		return
	}
	page.SetItems(profiles)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func GetAgentProfileDetail(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if err != nil || userID <= 0 || !ok {
		writeAgentProfileError(c, model.ErrAgentProfileInvalid)
		return
	}
	current, err := model.GetAgentProfile(userID)
	if err != nil {
		writeAgentProfileError(c, err)
		return
	}
	version := int64(0)
	if current != nil {
		version = current.Version
	}
	var profile *model.AgentProfile
	var user model.User
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		profile, err = model.RequireAgentCapabilityTx(tx, identity, userID, version, model.AgentCapabilityAdmin)
		if err != nil {
			return err
		}
		return tx.Select("id", "username", "role", "status").First(&user, userID).Error
	})
	if err != nil {
		writeAgentProfileError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"user_id": user.Id, "username": user.Username, "profile": profile})
}

func UpdateAgentProfile(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		writeAgentProfileError(c, model.ErrAgentProfileInvalid)
		return
	}
	var request struct {
		RequestID        *string `json:"request_id"`
		ExpectedVersion  *int64  `json:"expected_version"`
		Status           *string `json:"status"`
		SalesFrozen      *bool   `json:"sales_frozen"`
		SettlementFrozen *bool   `json:"settlement_frozen"`
		Reason           *string `json:"reason"`
	}
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil || request.RequestID == nil || request.ExpectedVersion == nil || request.Status == nil || request.SalesFrozen == nil || request.SettlementFrozen == nil || request.Reason == nil {
		writeAgentProfileError(c, model.ErrAgentProfileInvalid)
		return
	}
	mutation := model.AgentProfileMutation{ExpectedVersion: *request.ExpectedVersion, Status: *request.Status, SalesFrozen: *request.SalesFrozen, SettlementFrozen: *request.SettlementFrozen, Reason: *request.Reason}
	executeAgentProfileUpdate(c, *request.RequestID, model.AgentProfileCommand{UserID: userID, AgentProfileMutation: mutation})
}

func executeAgentProfileUpdate(c *gin.Context, requestID string, command model.AgentProfileCommand) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || c.GetBool("use_access_token") {
		writeAgentProfileError(c, model.ErrAgentProfilePermission)
		return
	}
	policy, err := model.GetCurrentAgentPolicy()
	if err != nil {
		writeAgentProfileError(c, err)
		return
	}
	context, err := model.NewAgentTransactionContext("profile.update", requestID, command.UserID, command.ExpectedVersion, policy.ID, "", "0", 0, "agent_profile", strconv.Itoa(command.UserID), command.ExpectedVersion, command)
	if err != nil {
		writeAgentProfileError(c, err)
		return
	}
	authorization, err := service.PrepareAgentTransactionAuthorization(strings.TrimSpace(c.GetHeader("X-Security-Proof")), identity, context)
	if err != nil {
		writeAgentProfileError(c, err)
		return
	}
	profile, err := model.UpdateAgentProfileAuthorized(authorization, requestID, command)
	if err != nil {
		writeAgentProfileError(c, err)
		return
	}
	common.ApiSuccess(c, profile)
}

func writeAgentProfileError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, "AGENT_INTERNAL_ERROR", "Agent operation failed, please try again later"
	switch {
	case errors.Is(err, model.ErrAgentProfilePermission), errors.Is(err, service.ErrVerificationForbidden):
		status, code, message = http.StatusForbidden, "AGENT_PERMISSION_DENIED", model.ErrAgentProfilePermission.Error()
	case errors.Is(err, model.ErrAgentProfileConflict):
		status, code, message = http.StatusConflict, "AGENT_PROFILE_CHANGED", model.ErrAgentProfileConflict.Error()
	case errors.Is(err, model.ErrAgentProfileInvalid), errors.Is(err, model.ErrAgentTransactionContext):
		status, code, message = http.StatusBadRequest, "AGENT_REQUEST_INVALID", "Invalid agent profile request"
	case errors.Is(err, model.ErrAgentTransactionMethod):
		status, code, message = http.StatusForbidden, "SECURITY_METHOD_UNAVAILABLE", model.ErrAgentTransactionMethod.Error()
	case errors.Is(err, model.ErrAgentTransactionProof), errors.Is(err, model.ErrAuthFlowInvalid), errors.Is(err, model.ErrAuthFlowExpired), errors.Is(err, model.ErrAuthFlowConsumed), errors.Is(err, model.ErrUserSessionInactive), errors.Is(err, service.ErrLoginSessionRevoked), errors.Is(err, service.ErrAuthTokenInvalid):
		status, code, message = http.StatusForbidden, "SECURITY_PROOF_INVALID", "Financial authorization is invalid or no longer matches this transaction"
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code, message = http.StatusNotFound, "AGENT_RESOURCE_NOT_FOUND", "User does not exist"
	}
	common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": code})
}
