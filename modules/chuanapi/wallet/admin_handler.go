package wallet

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// BatchUpdateModelBonusEligibleRequest 批量调整 bonus_eligible 请求。
// bonus_eligible 为 nil 表示清除资格（回到未配置三态）。
type BatchUpdateModelBonusEligibleRequest struct {
	ModelIDs       []int  `json:"model_ids" binding:"required"`
	BonusEligible  *bool  `json:"bonus_eligible"`
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
}

// BatchUpdateModelBonusEligible 批量调整模型赠金资格并写独立审计记录。
func BatchUpdateModelBonusEligible(c *gin.Context) {
	var req BatchUpdateModelBonusEligibleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	changed, err := model.BatchUpdateModelBonusEligible(
		req.ModelIDs,
		req.BonusEligible,
		c.GetInt("id"),
		c.GetString("username"),
		req.IdempotencyKey,
	)
	if err != nil {
		if errors.Is(err, model.ErrModelAuditConflict) {
			common.ApiErrorMsg(c, "该幂等键已用于不同参数的请求，请更换请求 ID 后重试")
			return
		}
		if errors.Is(err, model.ErrModelBonusEligibleNoRows) {
			common.ApiErrorMsg(c, "未指定有效的模型")
			return
		}
		common.ApiError(c, err)
		return
	}
	model.RefreshPricing()

	target := "null"
	if req.BonusEligible != nil {
		if *req.BonusEligible {
			target = "true"
		} else {
			target = "false"
		}
	}
	recordBonusEligibilityAudit(c, map[string]interface{}{
		"count":           changed,
		"bonus_eligible":  target,
		"idempotency_key": req.IdempotencyKey,
	})
	common.ApiSuccess(c, gin.H{
		"changed":         changed,
		"idempotency_key": req.IdempotencyKey,
	})
}

func recordBonusEligibilityAudit(c *gin.Context, params map[string]interface{}) {
	action := "model.bonus_eligible_batch"
	content := fmt.Sprintf("Batch set bonus eligibility to %v for %v models", params["bonus_eligible"], params["count"])
	authMethod := "session"
	if c.GetBool("use_access_token") {
		authMethod = "access_token"
	}
	model.RecordOperationAuditLog(
		c.GetInt("id"),
		c.GetInt("role"),
		content,
		c.ClientIP(),
		action,
		params,
		&model.AuditAdminInfo{
			AdminID:       c.GetInt("id"),
			AdminUsername: c.GetString("username"),
			AdminRole:     c.GetInt("role"),
			AuthMethod:    authMethod,
		},
		nil,
		c,
	)
	common.SetContextKey(c, constant.ContextKeyAuditLogged, true)
}

// GetModelBonusEligibleAudits 查询赠金资格调整的独立审计记录（管理员）。
func GetModelBonusEligibleAudits(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	modelID, _ := strconv.Atoi(c.Query("model_id"))
	operatorID, _ := strconv.Atoi(c.Query("operator_id"))
	audits, total, err := model.SearchModelBonusEligibleAudits(modelID, operatorID, c.Query("keyword"), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(audits)
	common.ApiSuccess(c, gin.H{
		"items":     audits,
		"total":     total,
		"page":      pageInfo.GetPage(),
		"page_size": pageInfo.GetPageSize(),
	})
}
