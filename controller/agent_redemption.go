package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Every HTTP scope carries the authenticated receipt. Internal model callers
// may omit one, but controllers cannot accidentally fall back to role-only auth.
func redemptionScope(c *gin.Context) model.RedemptionScope {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || c.GetBool("use_access_token") || identity.UserID != c.GetInt("id") {
		return model.RedemptionScope{}
	}
	if err := model.ValidateAgentPurchaseReadIdentity(identity); err != nil {
		return model.RedemptionScope{}
	}
	scope, err := model.ResolveRedemptionScope(identity.UserID)
	if err != nil {
		return model.RedemptionScope{}
	}
	scope.Identity = &identity
	c.Set("role", scope.Role)
	return scope
}

// Normalize before PageInfo computes (page-1)*size. Negative GORM limits
// otherwise remove pagination entirely; bounding the page also prevents overflow.
func redemptionPage(c *gin.Context) *common.PageInfo {
	page := common.GetPageQuery(c)
	page.Page = min(max(page.Page, 1), 1_000_000)
	page.PageSize = min(max(page.PageSize, 1), 100)
	return page
}

// Never expose raw database errors from code creation/lookup: unique violations
// and SQL diagnostics may contain the plaintext redemption credential.
func redemptionAPIError(c *gin.Context, err error) {
	if errors.Is(err, model.ErrAgentTransactionProof) || errors.Is(err, model.ErrAgentTransactionContext) || errors.Is(err, model.ErrAgentProfilePermission) || errors.Is(err, model.ErrAgentProfileConflict) || errors.Is(err, model.ErrAgentDeliveryProtected) {
		writeAgentPurchaseError(c, err)
		return
	}
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
	page := redemptionPage(c)
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
	page := redemptionPage(c)
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
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	scope := redemptionScope(c)
	auth, ok := agentPurchaseAuthorization(c, "redemption.allocate", request.RequestID, request)
	if !ok {
		return
	}
	scope.Authorization = &auth
	result, err := model.AllocateAgentRedemptionQuota(scope, request)
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	// The durable budget ledger is the audit trail. Do not repeat request IDs or
	// any credential material into the separately configured management logs.
	common.ApiSuccess(c, result)
}
