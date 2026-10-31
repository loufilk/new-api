package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"strconv"
)

func RecordAgentFundReclassification(c *gin.Context) {
	var command model.AgentFundReclassificationCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return
	}
	auth, ok := agentPurchaseAuthorization(c, "purchase.legacy.record", command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.RecordAgentFundReclassification(auth, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func ConfirmAgentFundReclassification(c *gin.Context) {
	var command model.AgentFundReclassificationCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 || (command.ReclassificationID != 0 && command.ReclassificationID != id) {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return
	}
	command.ReclassificationID = id
	auth, ok := agentPurchaseAuthorization(c, "purchase.legacy.confirm", command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.ConfirmAgentFundReclassification(auth, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAgentFundReclassifications(c *gin.Context) {
	if _, ok := agentPurchaseIdentity(c); !ok {
		return
	}
	page := redemptionPage(c)
	rows, total, err := model.GetAgentFundReclassifications(redemptionScope(c), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
