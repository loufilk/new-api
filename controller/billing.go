package controller

import (
	"net"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// A nil source leaves the ordinary request and query path unchanged. The
// scope is server-selected; never derive it from request headers or user IDs.
func readLaneBillingToken(c *gin.Context, source *model.LaneBillingDisplayV1) (model.LaneTokenBillingDisplay, bool, error) {
	if source == nil {
		return model.LaneTokenBillingDisplay{}, false, nil
	}
	return source.ReadToken(c.Request.Context(), c.GetInt("id"), c.GetInt("token_id"),
		model.BillingTokenFingerprint(c.GetString("token_key")), net.ParseIP(c.ClientIP()))
}

func readLaneLegacyBilling(c *gin.Context, source *model.LaneBillingDisplayV1) (remain, used int, expires int64, scoped bool, err error) {
	view, scoped, err := readLaneBillingToken(c, source)
	if err != nil || !scoped {
		return 0, 0, 0, scoped, err
	}
	if common.DisplayTokenStatEnabled {
		return view.Available, view.Used, view.Token.ExpiredTime, true, nil
	}
	if view.Account == nil {
		return 0, 0, 0, true, model.ErrTokenInvalid
	}
	return view.Account.Available, view.Account.Used, 0, true, nil
}

func GetSubscription(c *gin.Context) {
	getSubscription(c, nil)
}

func getSubscription(c *gin.Context, source *model.LaneBillingDisplayV1) {
	remainQuota, usedQuota, expiredTime, scoped, err := readLaneLegacyBilling(c, source)
	var token *model.Token
	if err == nil && !scoped {
		if common.DisplayTokenStatEnabled {
			token, err = model.GetTokenById(c.GetInt("token_id"))
			if token != nil {
				expiredTime, remainQuota, usedQuota = token.ExpiredTime, token.RemainQuota, token.UsedQuota
			}
		} else {
			userID := c.GetInt("id")
			remainQuota, err = model.GetUserQuota(userID, false)
			// The legacy route reports the second DB result. Keep that
			// policy here only; a scoped read error never reaches it.
			usedQuota, err = model.GetUserUsedQuota(userID)
		}
	}
	if expiredTime <= 0 {
		expiredTime = 0
	}
	if err != nil {
		message := err.Error()
		if scoped {
			message = i18n.T(c, i18n.MsgDatabaseError)
		}
		openAIError := types.OpenAIError{
			Message: message,
			Type:    "upstream_error",
		}
		c.JSON(200, gin.H{"error": openAIError})
		return
	}

	quota := remainQuota + usedQuota
	amount := float64(quota)
	// OpenAI 兼容接口中的 *_USD 字段含义保持“额度单位”对应值：
	// 我们将其解释为以“站点展示类型”为准：
	// - USD: 直接除以 QuotaPerUnit
	// - CNY: 先转 USD 再乘汇率
	// - TOKENS: 直接使用 tokens 数量
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeCNY:
		amount = amount / common.QuotaPerUnit * operation_setting.USDExchangeRate
	case operation_setting.QuotaDisplayTypeTokens:
		// amount 保持 tokens 数值
	default:
		amount = amount / common.QuotaPerUnit
	}
	if token != nil && token.UnlimitedQuota {
		amount = 100000000
	}
	subscription := OpenAISubscriptionResponse{
		Object:             "billing_subscription",
		HasPaymentMethod:   true,
		SoftLimitUSD:       amount,
		HardLimitUSD:       amount,
		SystemHardLimitUSD: amount,
		AccessUntil:        expiredTime,
	}
	c.JSON(200, subscription)
	return
}

func GetUsage(c *gin.Context) {
	getUsage(c, nil)
}

func getUsage(c *gin.Context, source *model.LaneBillingDisplayV1) {
	_, quota, _, scoped, err := readLaneLegacyBilling(c, source)
	if err == nil && !scoped {
		if common.DisplayTokenStatEnabled {
			var token *model.Token
			token, err = model.GetTokenById(c.GetInt("token_id"))
			if token != nil {
				quota = token.UsedQuota
			}
		} else {
			quota, err = model.GetUserUsedQuota(c.GetInt("id"))
		}
	}
	if err != nil {
		message := err.Error()
		if scoped {
			message = i18n.T(c, i18n.MsgDatabaseError)
		}
		openAIError := types.OpenAIError{
			Message: message,
			Type:    "new_api_error",
		}
		c.JSON(200, gin.H{"error": openAIError})
		return
	}
	amount := float64(quota)
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeCNY:
		amount = amount / common.QuotaPerUnit * operation_setting.USDExchangeRate
	case operation_setting.QuotaDisplayTypeTokens:
		// tokens 保持原值
	default:
		amount = amount / common.QuotaPerUnit
	}
	usage := OpenAIUsageResponse{
		Object:     "list",
		TotalUsage: amount * 100,
	}
	c.JSON(200, usage)
	return
}
