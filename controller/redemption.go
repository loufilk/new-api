package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func GetAllRedemptions(c *gin.Context) {
	pageInfo := redemptionPage(c)
	redemptions, total, err := model.GetRedemptionsScoped(redemptionScope(c), redemptionAgentFilter(c), "", "", pageInfo.GetStartIdx(), pageInfo.GetPageSize())
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
	keyword := c.Query("keyword")
	status := c.Query("status")
	pageInfo := redemptionPage(c)
	redemptions, total, err := model.GetRedemptionsScoped(redemptionScope(c), redemptionAgentFilter(c), keyword, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
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
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		redemptionAPIError(c, err)
		return
	}
	redemption, err := model.GetRedemptionScoped(redemptionScope(c), id)
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
	scope := redemptionScope(c)
	action := "redemption.create"
	if scope.IsAdmin() {
		action = "redemption.admin.create"
	}
	auth, ok := agentPurchaseAuthorization(c, action, redemption.RequestID, redemption)
	if !ok {
		return
	}
	scope.Authorization = &auth
	keys, err := model.CreateRedemptionsScoped(scope, redemption)
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
	scope := redemptionScope(c)
	scope.RequestID = c.Query("request_id")
	action := "redemption.delete"
	if scope.IsAdmin() {
		action = "redemption.admin.delete"
	}
	command := model.AgentLegacyRedemptionDeleteRequest{RequestID: scope.RequestID, IDs: []int{id}}
	auth, ok := agentPurchaseAuthorization(c, action, scope.RequestID, command)
	if !ok {
		return
	}
	scope.Authorization = &auth
	_, err := model.DeleteRedemptionsScoped(scope, []int{id}, false)
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
	scope := redemptionScope(c)
	action := "redemption.update"
	if scope.IsAdmin() {
		action = "redemption.admin.update"
	}
	statusOnly := c.Query("status_only") != ""
	command := model.AgentLegacyRedemptionUpdateRequest{RedemptionUpdateRequest: redemption, StatusOnly: statusOnly}
	auth, ok := agentPurchaseAuthorization(c, action, redemption.RequestID, command)
	if !ok {
		return
	}
	scope.Authorization = &auth
	cleanRedemption, err := model.UpdateRedemptionScoped(scope, redemption, statusOnly)
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
	scope := redemptionScope(c)
	scope.RequestID = c.Query("request_id")
	action := "redemption.delete"
	if scope.IsAdmin() {
		action = "redemption.admin.delete"
	}
	command := model.AgentLegacyRedemptionDeleteRequest{RequestID: scope.RequestID, InvalidOnly: true, AgentUserID: redemptionAgentFilter(c)}
	auth, ok := agentPurchaseAuthorization(c, action, scope.RequestID, command)
	if !ok {
		return
	}
	scope.Authorization = &auth
	rows, err := model.DeleteRedemptionsScoped(scope, nil, true, redemptionAgentFilter(c))
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
	scope := redemptionScope(c)
	scope.RequestID = request.RequestID
	action := "redemption.delete"
	if scope.IsAdmin() {
		action = "redemption.admin.delete"
	}
	command := model.AgentLegacyRedemptionDeleteRequest{RequestID: scope.RequestID, IDs: request.Ids}
	auth, ok := agentPurchaseAuthorization(c, action, scope.RequestID, command)
	if !ok {
		return
	}
	scope.Authorization = &auth
	count, err := model.DeleteRedemptionsScoped(scope, request.Ids, false)
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

// Filtering never supplies the authority: the model always enforces the scope.
func redemptionAgentFilter(c *gin.Context) int {
	id, _ := strconv.Atoi(c.Query("agent_user_id"))
	return id
}
