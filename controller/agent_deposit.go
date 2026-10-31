package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func AgentDepositAction(c *gin.Context) {
	id, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var command model.AgentDepositCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentCreditError(c, model.ErrAgentDepositInvalid)
		return
	}
	action := "deposit." + c.Param("action")
	payload, err := common.Marshal(command)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	result, found, err := model.ReplayAgentDepositOperation(id, action, command.RequestID, payload)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	if found {
		common.ApiSuccess(c, result)
		return
	}
	prepared, err := model.PrepareAgentDepositOperation(id, action, command.RequestID, payload)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	if prepared == nil {
		writeAgentCreditError(c, model.ErrAgentDepositInvalid)
		return
	}
	auth, err := service.PrepareAgentTransactionAuthorization(strings.TrimSpace(c.GetHeader("X-Security-Proof")), id, prepared.VerificationContext)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	result, err = model.ExecuteAgentDepositOperation(auth, action, command)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAgentDeposit(c *gin.Context) {
	id, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	owner, offset, limit, ok := agentFinancePageQuery(c)
	if !ok {
		return
	}
	result, err := model.GetAgentDepositView(id, owner, offset, limit)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
