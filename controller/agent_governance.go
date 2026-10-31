package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func writeAgentGovernanceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAgentGovernanceInvalid):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
	case errors.Is(err, model.ErrAgentGovernanceConflict), errors.Is(err, model.ErrAgentGovernanceRiskLimit):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage(err.Error()))
	case errors.Is(err, model.ErrAgentGovernanceReview), errors.Is(err, model.ErrAgentGovernanceRequired):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage(err.Error()))
	default:
		writeAgentPlatformError(c, err)
	}
}
func GetAgentGovernanceSelf(c *gin.Context)    { getAgentGovernance(c, false) }
func GetAgentGovernanceFinance(c *gin.Context) { getAgentGovernance(c, true) }
func getAgentGovernance(c *gin.Context, finance bool) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentGovernanceError(c, model.ErrAgentProfilePermission)
		return
	}
	userID := identity.UserID
	if finance {
		var err error
		userID, err = strconv.Atoi(c.Param("user_id"))
		if err != nil || userID <= 0 {
			writeAgentGovernanceError(c, model.ErrAgentGovernanceInvalid)
			return
		}
	}
	view, err := model.GetAgentGovernance(identity, userID, finance)
	if err != nil {
		writeAgentGovernanceError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}
func SubmitAgentGovernance(c *gin.Context)   { executeAgentGovernance(c, "governance.submit") }
func ReviewAgentGovernance(c *gin.Context)   { executeAgentGovernance(c, "governance.review") }
func OverrideAgentGovernance(c *gin.Context) { executeAgentGovernance(c, "governance.override") }
func executeAgentGovernance(c *gin.Context, action string) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentGovernanceError(c, model.ErrAgentProfilePermission)
		return
	}
	var command model.AgentGovernanceCommand
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if common.DecodeJsonStrict(c.Request.Body, &command) != nil {
		writeAgentGovernanceError(c, model.ErrAgentGovernanceInvalid)
		return
	}
	if action != "governance.submit" {
		userID, err := strconv.Atoi(c.Param("user_id"))
		if err != nil || userID <= 0 || command.UserID != userID {
			writeAgentGovernanceError(c, model.ErrAgentGovernanceInvalid)
			return
		}
	}
	payload, err := common.Marshal(command)
	if err != nil {
		writeAgentGovernanceError(c, err)
		return
	}
	prior, found, err := model.ReplayAgentGovernanceOperation(identity, action, command.RequestID, payload)
	if err != nil {
		writeAgentGovernanceError(c, err)
		return
	}
	if found {
		common.ApiSuccess(c, prior)
		return
	}
	prepared, err := model.PrepareAgentGovernanceOperation(identity, action, command.RequestID, payload)
	if err != nil {
		writeAgentGovernanceError(c, err)
		return
	}
	auth, err := service.PrepareAgentTransactionAuthorization(strings.TrimSpace(c.GetHeader("X-Security-Proof")), identity, prepared.VerificationContext)
	if err != nil {
		writeAgentGovernanceError(c, err)
		return
	}
	result, err := model.ExecuteAgentGovernance(auth, action, command)
	if err != nil {
		writeAgentGovernanceError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
