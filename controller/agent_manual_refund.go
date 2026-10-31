package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func AgentManualRefundInstructionHandler(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := agentPurchaseIdentity(c)
		if !ok {
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			writeAgentPurchaseError(c, model.ErrAgentRefundInvalid)
			return
		}
		result, err := model.GetAgentManualRefundInstruction(identity, kind, id)
		if err != nil {
			writeAgentPurchaseError(c, err)
			return
		}
		common.ApiSuccess(c, result)
	}
}
