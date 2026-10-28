package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func redemptionScope(c *gin.Context) model.RedemptionScope {
	return model.RedemptionScope{UserID: c.GetInt("id"), Role: c.GetInt("role")}
}

// Never expose raw database errors from code creation/lookup: unique violations
// and SQL diagnostics may contain the plaintext redemption credential.
func redemptionAPIError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAgentRedemptionPermission):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage(err.Error()))
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorT(c, "Redemption code not found")
	case errors.Is(err, model.ErrAgentRedemptionUnavailable), errors.Is(err, model.ErrAgentQuotaInvalid), errors.Is(err, model.ErrAgentQuotaInsufficient), errors.Is(err, model.ErrAgentQuotaConflict), errors.Is(err, model.ErrRedemptionUsedImmutable), errors.Is(err, model.ErrRedemptionInvalidStatus), errors.Is(err, model.ErrRedemptionInvalidRequest), errors.Is(err, model.ErrWalletQuotaLimitExceeded):
		common.ApiErrorT(c, err.Error())
	default:
		common.ApiErrorT(c, "Redemption operation failed, please try again later")
	}
}

func GetAgentRedemptionAccounts(c *gin.Context) {
	page := common.GetPageQuery(c)
	agentID, _ := strconv.Atoi(c.Query("agent_user_id"))
	rows, total, err := model.GetAgentRedemptionAccounts(redemptionScope(c), agentID, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func GetAgentRedemptionLedger(c *gin.Context) {
	agentID, err := strconv.Atoi(c.Param("id"))
	if err != nil || agentID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	page := common.GetPageQuery(c)
	rows, total, err := model.GetAgentRedemptionLedger(redemptionScope(c), agentID, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func AllocateAgentRedemptionQuota(c *gin.Context) {
	var request model.AgentQuotaAllocationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	result, err := model.AllocateAgentRedemptionQuota(redemptionScope(c), request)
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	// The durable budget ledger is the audit trail. Do not repeat request IDs or
	// any credential material into the separately configured management logs.
	common.ApiSuccess(c, result)
}
