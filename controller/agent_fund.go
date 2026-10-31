package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"strconv"
)

func GetAgentFundBatches(c *gin.Context) {
	if _, ok := agentPurchaseIdentity(c); !ok {
		return
	}
	page := redemptionPage(c)
	owner, _ := strconv.Atoi(c.Query("agent_user_id"))
	rows, total, err := model.GetAgentFundBatches(redemptionScope(c), owner, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
func GetAgentFundLedger(c *gin.Context) {
	if _, ok := agentPurchaseIdentity(c); !ok {
		return
	}
	page := redemptionPage(c)
	owner, _ := strconv.Atoi(c.Query("agent_user_id"))
	rows, total, err := model.GetAgentFundLedger(redemptionScope(c), owner, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
func ReconcileAgentFunds(c *gin.Context) {
	if _, ok := agentPurchaseIdentity(c); !ok {
		return
	}
	owner, _ := strconv.Atoi(c.Query("agent_user_id"))
	after, _ := strconv.Atoi(c.Query("after_agent_id"))
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit == 0 {
		limit = 100
	}
	rows, err := model.ReconcileAgentFundsPage(redemptionScope(c), owner, after, limit)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}
func GetAgentPurchaseAudit(c *gin.Context) {
	if _, ok := agentPurchaseIdentity(c); !ok {
		return
	}
	page := redemptionPage(c)
	owner, _ := strconv.Atoi(c.Query("agent_user_id"))
	rows, total, err := model.GetAgentPurchaseAudit(redemptionScope(c), owner, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
