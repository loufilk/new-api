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

func writeAgentCreditError(c *gin.Context, err error) {
	status, code, message := http.StatusBadRequest, "AGENT_REQUEST_INVALID", "Invalid agent credit or receivable request"
	switch {
	case errors.Is(err, model.ErrAgentCreditRisk):
		status, code, message = http.StatusConflict, "AGENT_CREDIT_RESTRICTED", err.Error()
	case errors.Is(err, model.ErrAgentCreditState):
		status, code, message = http.StatusConflict, "AGENT_STATE_CHANGED", err.Error()
	case errors.Is(err, model.ErrAgentCreditInvariant), errors.Is(err, model.ErrAgentDepositInvariant):
		status, code, message = http.StatusConflict, "AGENT_RECONCILIATION_REQUIRED", "Financial reconciliation requires independent review"
	case errors.Is(err, model.ErrAgentCreditInvalid), errors.Is(err, model.ErrAgentDepositInvalid):
		message = err.Error()
	default:
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": code})
}

func AgentCreditAction(c *gin.Context) {
	id, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var command model.AgentCreditCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentCreditError(c, model.ErrAgentCreditInvalid)
		return
	}
	action := "credit." + c.Param("action")
	payload, err := common.Marshal(command)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	result, found, err := model.ReplayAgentCreditOperation(id, action, command.RequestID, payload)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	if found {
		common.ApiSuccess(c, result)
		return
	}
	prepared, err := model.PrepareAgentCreditOperation(id, action, command.RequestID, payload)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	if prepared == nil {
		writeAgentCreditError(c, model.ErrAgentCreditInvalid)
		return
	}
	auth, err := service.PrepareAgentTransactionAuthorization(strings.TrimSpace(c.GetHeader("X-Security-Proof")), id, prepared.VerificationContext)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	result, err = model.ExecuteAgentCreditOperation(auth, action, command)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func agentFinancePageQuery(c *gin.Context) (int, int, int, bool) {
	owner, offset, limit := 0, 0, 25
	for key, target := range map[string]*int{"agent_user_id": &owner, "offset": &offset, "limit": &limit} {
		if raw, ok := c.GetQuery(key); ok {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 0 || (key == "limit" && (v < 1 || v > 100)) {
				writeAgentCreditError(c, model.ErrAgentCreditInvalid)
				return 0, 0, 0, false
			}
			*target = v
		}
	}
	if offset > 1000000 {
		writeAgentCreditError(c, model.ErrAgentCreditInvalid)
		return 0, 0, 0, false
	}
	return owner, offset, limit, true
}
func GetAgentCredit(c *gin.Context) {
	id, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	owner, offset, limit, ok := agentFinancePageQuery(c)
	if !ok {
		return
	}
	result, err := model.GetAgentCreditView(id, owner, offset, limit)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func GetAgentCreditReconciliation(c *gin.Context) {
	id, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	owner, _, _, ok := agentFinancePageQuery(c)
	if !ok {
		return
	}
	findings, err := model.ReconcileAgentCreditLiabilities(id, owner)
	if err != nil {
		writeAgentCreditError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"findings": findings, "external_bank_reconciled": false})
}
