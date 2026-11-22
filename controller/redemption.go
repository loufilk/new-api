package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetAllRedemptions(c *gin.Context) {
	if _, ok := redemptionIdentity(c); !ok {
		return
	}
	pageInfo := redemptionPage(c)
	redemptions, total, err := model.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchRedemptions(c *gin.Context) {
	if _, ok := redemptionIdentity(c); !ok {
		return
	}
	keyword := c.Query("keyword")
	status := c.Query("status")
	pageInfo := redemptionPage(c)
	redemptions, total, err := model.SearchRedemptions(keyword, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetRedemption(c *gin.Context) {
	if _, ok := redemptionIdentity(c); !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	redemption, err := model.GetRedemptionById(id)
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    redemption,
	})
	return
}

func AddRedemption(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		return
	}

	var redemption model.RedemptionCreateRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &redemption); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	identity, ok := redemptionIdentity(c)
	if !ok {
		return
	}
	keys, err := model.CreateRedemptions(identity, redemption)
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	recordManageAudit(c, "redemption.create", map[string]any{
		"name":  redemption.Name,
		"count": redemption.Count,
		"quota": logger.FormatQuota(redemption.Quota),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    keys,
	})
	return
}

func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	identity, ok := redemptionIdentity(c)
	if !ok {
		return
	}
	_, err := model.DeleteRedemptions([]int{id}, false, identity)
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateRedemption(c *gin.Context) {
	var redemption model.RedemptionUpdateRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &redemption); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	identity, ok := redemptionIdentity(c)
	if !ok {
		return
	}
	cleanRedemption, err := model.UpdateRedemption(redemption, c.Query("status_only") != "", identity)
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanRedemption,
	})
	return
}

func DeleteInvalidRedemption(c *gin.Context) {
	identity, ok := redemptionIdentity(c)
	if !ok {
		return
	}
	rows, err := model.DeleteRedemptions(nil, true, identity)
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func DeleteRedemptionBatch(c *gin.Context) {
	var request struct {
		RequestID string `json:"request_id"`
		Ids       []int  `json:"ids" binding:"required,min=1,max=1000,dive,gt=0"`
	}
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	identity, ok := redemptionIdentity(c)
	if !ok {
		return
	}
	count, err := model.DeleteRedemptions(request.Ids, false, identity)
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	recordManageAudit(c, "redemption.delete_batch", map[string]any{
		"count":                    count,
		"total":                    len(request.Ids),
		"requested_redemption_ids": request.Ids,
	})
	common.ApiSuccess(c, count)
}

// Browser authentication is independently revalidated in each mutation's transaction.
func redemptionIdentity(c *gin.Context) (model.AuthSessionIdentity, bool) {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || c.GetBool("use_access_token") || identity.UserID != c.GetInt("id") || model.ValidateRedemptionAdmin(identity) != nil {
		redemptionAPIError(c, model.ErrRedemptionPermission)
		return model.AuthSessionIdentity{}, false
	}
	return identity, true
}
func redemptionPage(c *gin.Context) *common.PageInfo {
	page := common.GetPageQuery(c)
	page.Page = min(max(page.Page, 1), 1_000_000)
	page.PageSize = min(max(page.PageSize, 1), 100)
	return page
}

// Database diagnostics may contain usable codes; return only known message keys.
func redemptionAPIError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrRedemptionPermission):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage(err.Error()))
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorT(c, "Redemption code not found")
	case errors.Is(err, model.ErrRedemptionUsedImmutable), errors.Is(err, model.ErrRedemptionInvalidStatus), errors.Is(err, model.ErrRedemptionInvalidRequest), errors.Is(err, model.ErrWalletQuotaLimitExceeded):
		common.ApiErrorT(c, err.Error())
	default:
		common.ApiErrorT(c, "Redemption operation failed, please try again later")
	}
}
