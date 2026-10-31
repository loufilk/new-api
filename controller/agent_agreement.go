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

func writeAgentAgreementError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAgentAgreementInvalid), errors.Is(err, model.ErrAgentStatementInvalid):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
	case errors.Is(err, model.ErrAgentAgreementConflict), errors.Is(err, model.ErrAgentStatementConflict), errors.Is(err, model.ErrAgentStatementUnresolved):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage(err.Error()))
	case errors.Is(err, model.ErrAgentAgreementReview), errors.Is(err, model.ErrAgentStatementReview):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage(err.Error()))
	default:
		writeAgentPurchaseError(c, err)
	}
}
func ExecuteAgentAgreement(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentAgreementError(c, model.ErrAgentProfilePermission)
		return
	}
	action := "agreement." + c.Param("action")
	var command model.AgentAgreementCommand
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if common.DecodeJsonStrict(c.Request.Body, &command) != nil {
		writeAgentAgreementError(c, model.ErrAgentAgreementInvalid)
		return
	}
	payload, err := common.Marshal(command)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	replay, found, err := model.ReplayAgentAgreementOperation(identity, action, command.RequestID, payload)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	if found {
		common.ApiSuccess(c, replay)
		return
	}
	prepared, err := model.PrepareAgentAgreementOperation(identity, action, command.RequestID, payload)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	auth, err := service.PrepareAgentTransactionAuthorization(strings.TrimSpace(c.GetHeader("X-Security-Proof")), identity, prepared.VerificationContext)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	result, err := model.ExecuteAgentAgreement(auth, action, command)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAgentAgreements(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentAgreementError(c, model.ErrAgentProfilePermission)
		return
	}
	page := redemptionPage(c)
	owner, _ := strconv.Atoi(c.Query("agent_user_id"))
	rows, total, err := model.ListAgentAgreements(identity, owner, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
func GetAgentAgreement(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentAgreementError(c, model.ErrAgentProfilePermission)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAgentAgreementError(c, model.ErrAgentAgreementInvalid)
		return
	}
	result, err := model.GetAgentAgreement(identity, id)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
