package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"strconv"
)

func agentDeliveryCommand(c *gin.Context) (model.AgentDeliveryCommand, bool) {
	var command model.AgentDeliveryCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentPurchaseError(c, model.ErrRedemptionInvalidRequest)
		return command, false
	}
	if c.Param("id") != "" {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 || (command.DeliveryID != 0 && command.DeliveryID != id) {
			writeAgentPurchaseError(c, model.ErrRedemptionInvalidRequest)
			return command, false
		}
		command.DeliveryID = id
	}
	return command, true
}
func CreateAgentDelivery(c *gin.Context) {
	command, ok := agentDeliveryCommand(c)
	if !ok {
		return
	}
	auth, ok := agentPurchaseAuthorization(c, "delivery.create", command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.CreateAgentDelivery(auth, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func ClaimAgentDelivery(c *gin.Context) {
	identity, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	command, ok := agentDeliveryCommand(c)
	if !ok {
		return
	}
	result, err := model.ClaimAgentDelivery(identity, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func ConfirmAgentDelivery(c *gin.Context) {
	identity, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	command, ok := agentDeliveryCommand(c)
	if !ok {
		return
	}
	result, err := model.ConfirmAgentDelivery(identity, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func CancelAgentDelivery(c *gin.Context) {
	command, ok := agentDeliveryCommand(c)
	if !ok {
		return
	}
	auth, ok := agentPurchaseAuthorization(c, "delivery.cancel", command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.CancelAgentDelivery(auth, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAgentDeliveries(c *gin.Context)         { getAgentDeliveries(c, false) }
func GetReceivedAgentDeliveries(c *gin.Context) { getAgentDeliveries(c, true) }
func getAgentDeliveries(c *gin.Context, received bool) {
	identity, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	page := redemptionPage(c)
	rows, total, err := model.GetAgentDeliveries(identity, received, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
func CancelAgentLegacyCode(c *gin.Context) {
	command, ok := agentDeliveryCommand(c)
	if !ok {
		return
	}
	auth, ok := agentPurchaseAuthorization(c, "delivery.cancel", command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.CancelAgentLegacyCode(auth, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
