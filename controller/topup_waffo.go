package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/paymentcore"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/thanhpk/randstr"
	waffo "github.com/waffo-com/waffo-go"
	"github.com/waffo-com/waffo-go/config"
	"github.com/waffo-com/waffo-go/core"
	"github.com/waffo-com/waffo-go/types/order"
)

func getWaffoSDK() (*waffo.Waffo, error) {
	env := config.Sandbox
	apiKey := setting.WaffoSandboxApiKey
	privateKey := setting.WaffoSandboxPrivateKey
	publicKey := setting.WaffoSandboxPublicCert
	if !setting.WaffoSandbox {
		env = config.Production
		apiKey = setting.WaffoApiKey
		privateKey = setting.WaffoPrivateKey
		publicKey = setting.WaffoPublicCert
	}
	builder := config.NewConfigBuilder().
		APIKey(apiKey).
		PrivateKey(privateKey).
		WaffoPublicKey(publicKey).
		Environment(env)
	if setting.WaffoMerchantId != "" {
		builder = builder.MerchantID(setting.WaffoMerchantId)
	}
	cfg, err := builder.Build()
	if err != nil {
		return nil, err
	}
	return waffo.New(cfg), nil
}

func getWaffoUserEmail(user *model.User) string {
	return fmt.Sprintf("%d@examples.com", user.Id)
}

func getWaffoCurrency() string {
	if setting.WaffoCurrency != "" {
		return setting.WaffoCurrency
	}
	return "USD"
}

func buildWaffoTopUpGoodsInfo(amount int64) *order.GoodsInfo {
	appName := strings.TrimSpace(common.SystemName)
	if appName == "" {
		appName = "New API"
	}
	return &order.GoodsInfo{
		GoodsName: fmt.Sprintf("Recharge %d credits", amount),
		AppName:   appName,
	}
}

// zeroDecimalCurrencies 零小数位币种，金额不能带小数点
var zeroDecimalCurrencies = map[string]bool{
	"IDR": true, "JPY": true, "KRW": true, "VND": true,
}

func formatWaffoAmount(amount float64, currency string) string {
	if zeroDecimalCurrencies[currency] {
		return fmt.Sprintf("%.0f", amount)
	}
	return fmt.Sprintf("%.2f", amount)
}

// getWaffoPayMoney converts the user-facing amount to USD for Waffo payment.
// Waffo only accepts USD, so this function handles the conversion from different
// display types (USD/CNY/TOKENS) to the actual USD amount to charge.
func getWaffoPayMoney(amount float64, group string) float64 {
	originalAmount := amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = amount / common.QuotaPerUnit
	}
	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(originalAmount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	return amount * setting.WaffoUnitPrice * topupGroupRatio * discount
}

type WaffoPayRequest struct {
	Amount         int64  `json:"amount"`
	PayMethodIndex *int   `json:"pay_method_index"` // 服务端支付方式列表的索引，nil 表示由 Waffo 自动选择
	PayMethodType  string `json:"pay_method_type"`  // Deprecated: 兼容旧前端，优先使用 pay_method_index
	PayMethodName  string `json:"pay_method_name"`  // Deprecated: 兼容旧前端，优先使用 pay_method_index
}

func RequestWaffoAmount(c *gin.Context) {
	var req WaffoPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		paymentError(c, common.NewMessage("Invalid parameters"))
		return
	}

	waffoMinTopup := int64(setting.WaffoMinTopUp)
	if req.Amount < waffoMinTopup {
		paymentError(c, common.NewMessage("Top-up amount cannot be less than {{min}}", map[string]any{"min": waffoMinTopup}))
		return
	}
	id := c.GetInt("id")
	if paymentcore.RejectInvalidTopUpQuota(c, id, req.Amount) {
		return
	}

	group, err := model.GetUserGroup(id, true)
	if err != nil {
		paymentError(c, common.NewMessage("Failed to get the user group"))
		return
	}

	payMoney := getWaffoPayMoney(float64(req.Amount), group)
	if payMoney <= 0.01 {
		paymentError(c, common.NewMessage("Payment amount is too low"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

// RequestWaffoPay 创建 Waffo 支付订单
func RequestWaffoPay(c *gin.Context) {
	requestWaffoPay(c, getWaffoSDK)
}

func requestWaffoPay(c *gin.Context, sdkFactory func() (*waffo.Waffo, error)) {
	if !paymentcore.RequireCompliance(c) {
		return
	}

	if !setting.WaffoEnabled {
		paymentError(c, common.NewMessage("Waffo payment is not enabled"))
		return
	}

	var req WaffoPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		paymentError(c, common.NewMessage("Invalid parameters"))
		return
	}
	waffoMinTopup := int64(setting.WaffoMinTopUp)
	if req.Amount < waffoMinTopup {
		paymentError(c, common.NewMessage("Top-up amount cannot be less than {{min}}", map[string]any{"min": waffoMinTopup}))
		return
	}
	id := c.GetInt("id")
	if paymentcore.RejectInvalidTopUpQuota(c, id, req.Amount) {
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		paymentError(c, common.NewMessage("User does not exist"))
		return
	}

	// 从服务端配置查找支付方式，客户端只传索引或旧字段
	var resolvedPayMethodType, resolvedPayMethodName string
	methods := setting.GetWaffoPayMethods()
	if req.PayMethodIndex != nil {
		// 新协议：按索引查找
		idx := *req.PayMethodIndex
		if idx < 0 || idx >= len(methods) {
			logger.LogWarn(c.Request.Context(), common.LogText("Waffo invalid payment method index user_id=%d pay_method_index=%d method_count=%d", id, idx, len(methods)))
			paymentError(c, common.NewMessage("Unsupported payment method"))
			return
		}
		resolvedPayMethodType = methods[idx].PayMethodType
		resolvedPayMethodName = methods[idx].PayMethodName
	} else if req.PayMethodType != "" {
		// 兼容旧前端：验证客户端传的值在服务端列表中
		valid := false
		for _, m := range methods {
			if m.PayMethodType == req.PayMethodType && m.PayMethodName == req.PayMethodName {
				valid = true
				resolvedPayMethodType = m.PayMethodType
				resolvedPayMethodName = m.PayMethodName
				break
			}
		}
		if !valid {
			logger.LogWarn(c.Request.Context(), common.LogText("Waffo invalid payment method user_id=%d pay_method_type=%s pay_method_name=%q", id, req.PayMethodType, req.PayMethodName))
			paymentError(c, common.NewMessage("Unsupported payment method"))
			return
		}
	}
	// resolvedPayMethodType/Name 为空时，Waffo 自动选择支付方式

	group, _ := model.GetUserGroup(id, true)
	payMoney := getWaffoPayMoney(float64(req.Amount), group)
	if payMoney < 0.01 {
		paymentError(c, common.NewMessage("Payment amount is too low"))
		return
	}
	currency := getWaffoCurrency()
	expectedProviderAmount := formatWaffoAmount(payMoney, currency)

	// 生成唯一订单号，paymentRequestId 与 merchantOrderId 保持一致，简化追踪
	merchantOrderId := fmt.Sprintf("WAFFO-%d-%d-%s", id, time.Now().UnixMilli(), randstr.String(6))
	paymentRequestId := merchantOrderId

	// Token 模式下归一化 Amount（存等价美元/CNY 数量，避免 RechargeWaffo 双重放大）
	amount := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = max(int64(float64(req.Amount)/common.QuotaPerUnit), 1)
	}

	// 创建本地订单及不可变 provider 结算快照；两者必须同事务落库。
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         merchantOrderId,
		PaymentMethod:   model.PaymentMethodWaffo,
		PaymentProvider: model.PaymentProviderWaffo,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	binding, err := paymentcore.NewWaffoBinding(merchantOrderId, expectedProviderAmount, currency)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 订单绑定配置失败 user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付配置错误"})
		return
	}
	if err := paymentcore.InsertTopUpWithBinding(topUp, binding); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, merchantOrderId, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	sdk, err := sdkFactory()
	if err != nil {
		logger.LogError(c.Request.Context(), common.LogText("Waffo SDK initialization failed user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		if statusErr := model.UpdatePendingTopUpStatus(merchantOrderId, model.PaymentProviderWaffo, common.TopUpStatusFailed); statusErr != nil && !errors.Is(statusErr, model.ErrTopUpStatusInvalid) {
			logger.LogError(c.Request.Context(), common.LogText("Failed to mark pending payment order as failed trade_no=%s error=%q", merchantOrderId, statusErr.Error()))
		}
		paymentError(c, common.NewMessage("Payment configuration error"))
		return
	}

	callbackAddr := service.GetCallbackAddress()
	notifyUrl := callbackAddr + "/api/waffo/webhook"
	if setting.WaffoNotifyUrl != "" {
		notifyUrl = setting.WaffoNotifyUrl
	}
	returnUrl := paymentReturnPath("/wallet?show_history=true")
	if setting.WaffoReturnUrl != "" {
		returnUrl = setting.WaffoReturnUrl
	}

	goodsInfo := buildWaffoTopUpGoodsInfo(req.Amount)
	createParams := &order.CreateOrderParams{
		PaymentRequestID: paymentRequestId,
		MerchantOrderID:  merchantOrderId,
		OrderAmount:      expectedProviderAmount,
		OrderCurrency:    currency,
		OrderDescription: goodsInfo.GoodsName,
		OrderRequestedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		NotifyURL:        notifyUrl,
		MerchantInfo: &order.MerchantInfo{
			MerchantID: setting.WaffoMerchantId,
		},
		UserInfo: &order.UserInfo{
			UserID:       strconv.Itoa(user.Id),
			UserEmail:    getWaffoUserEmail(user),
			UserTerminal: "WEB",
		},
		PaymentInfo: &order.PaymentInfo{
			ProductName:   "ONE_TIME_PAYMENT",
			PayMethodType: resolvedPayMethodType,
			PayMethodName: resolvedPayMethodName,
		},
		GoodsInfo:          goodsInfo,
		SuccessRedirectURL: returnUrl,
		FailedRedirectURL:  returnUrl,
	}
	resp, err := sdk.Order().Create(c.Request.Context(), createParams, nil)
	if err != nil {
		logger.LogError(c.Request.Context(), common.LogText("Waffo failed to create order user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		if statusErr := model.UpdatePendingTopUpStatus(merchantOrderId, model.PaymentProviderWaffo, common.TopUpStatusFailed); statusErr != nil && !errors.Is(statusErr, model.ErrTopUpStatusInvalid) {
			logger.LogError(c.Request.Context(), common.LogText("Failed to mark pending payment order as failed trade_no=%s error=%q", merchantOrderId, statusErr.Error()))
		}
		paymentError(c, common.NewMessage("Failed to start payment"))
		return
	}
	if !resp.IsSuccess() {
		logger.LogWarn(c.Request.Context(), common.LogText("Waffo order creation rejected user_id=%d trade_no=%s code=%s message=%q response=%q", id, merchantOrderId, resp.Code, resp.Message, common.GetJsonString(resp)))
		if statusErr := model.UpdatePendingTopUpStatus(merchantOrderId, model.PaymentProviderWaffo, common.TopUpStatusFailed); statusErr != nil && !errors.Is(statusErr, model.ErrTopUpStatusInvalid) {
			logger.LogError(c.Request.Context(), common.LogText("Failed to mark pending payment order as failed trade_no=%s error=%q", merchantOrderId, statusErr.Error()))
		}
		paymentError(c, common.NewMessage("Failed to start payment"))
		return
	}

	orderData := resp.GetData()
	if orderData.PaymentRequestID == "" || orderData.PaymentRequestID != paymentRequestId ||
		orderData.MerchantOrderID == "" || orderData.MerchantOrderID != merchantOrderId {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 创建订单身份不匹配 trade_no=%s payment_request_id=%q merchant_order_id=%q", merchantOrderId, orderData.PaymentRequestID, orderData.MerchantOrderID))
		if statusErr := model.UpdatePendingTopUpStatus(merchantOrderId, model.PaymentProviderWaffo, common.TopUpStatusFailed); statusErr != nil && !errors.Is(statusErr, model.ErrTopUpStatusInvalid) {
			logger.LogError(c.Request.Context(), common.LogText("Failed to mark pending payment order as failed trade_no=%s error=%q", merchantOrderId, statusErr.Error()))
		}
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "上游支付订单身份异常"})
		return
	}
	if strings.TrimSpace(orderData.AcquiringOrderID) != "" {
		if err := paymentcore.BindProviderOrderID(merchantOrderId, orderData.AcquiringOrderID); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 保存上游订单身份失败 trade_no=%s acquiring_order_id=%q error=%q", merchantOrderId, orderData.AcquiringOrderID, err.Error()))
			if statusErr := model.UpdatePendingTopUpStatus(merchantOrderId, model.PaymentProviderWaffo, common.TopUpStatusFailed); statusErr != nil && !errors.Is(statusErr, model.ErrTopUpStatusInvalid) {
				logger.LogError(c.Request.Context(), common.LogText("Failed to mark pending payment order as failed trade_no=%s error=%q", merchantOrderId, statusErr.Error()))
			}
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付订单绑定失败"})
			return
		}
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 充值订单创建成功 user_id=%d trade_no=%s amount=%d money=%.2f pay_method_type=%s pay_method_name=%q", id, merchantOrderId, req.Amount, payMoney, resolvedPayMethodType, resolvedPayMethodName))

	paymentUrl := orderData.FetchRedirectURL()
	if paymentUrl == "" {
		paymentUrl = orderData.OrderAction
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"payment_url": paymentUrl,
			"order_id":    merchantOrderId,
		},
	})
}

// webhookPayloadWithSubInfo 扩展 PAYMENT_NOTIFICATION，包含 SDK 未定义的 subscriptionInfo 字段
type webhookPayloadWithSubInfo struct {
	EventType string `json:"eventType"`
	Result    struct {
		core.PaymentNotificationResult
		SubscriptionInfo *webhookSubscriptionInfo `json:"subscriptionInfo,omitempty"`
	} `json:"result"`
}

type webhookSubscriptionInfo struct {
	Period              string `json:"period,omitempty"`
	MerchantRequest     string `json:"merchantRequest,omitempty"`
	SubscriptionID      string `json:"subscriptionId,omitempty"`
	SubscriptionRequest string `json:"subscriptionRequest,omitempty"`
}

// WaffoWebhook 处理 Waffo 回调通知（支付/退款/订阅）
func WaffoWebhook(c *gin.Context) {
	if !isWaffoWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.URL.Path, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	sdk, err := getWaffoSDK()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook SDK 初始化失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	wh := sdk.Webhook()
	bodyStr := string(bodyBytes)
	signature := c.GetHeader("X-SIGNATURE")
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo webhook 收到请求 path=%q client_ip=%s signature_present=%t payload=%s", c.Request.URL.Path, c.ClientIP(), signature != "", paymentcore.PayloadSummary(bodyBytes)))

	// 验证请求签名
	if !wh.VerifySignature(bodyStr, signature) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 验签失败 path=%q client_ip=%s signature_present=%t payload=%s", c.Request.URL.Path, c.ClientIP(), signature != "", paymentcore.PayloadSummary(bodyBytes)))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	var event core.WebhookEvent
	if err := common.Unmarshal(bodyBytes, &event); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook 解析失败 path=%q client_ip=%s error=%q payload=%s", c.Request.URL.Path, c.ClientIP(), err.Error(), paymentcore.PayloadSummary(bodyBytes)))
		sendWaffoWebhookResponse(c, wh, false, "invalid payload")
		return
	}

	switch event.EventType {
	case core.EventPayment:
		// 解析为扩展类型，区分普通支付和订阅支付
		var payload webhookPayloadWithSubInfo
		if err := common.Unmarshal(bodyBytes, &payload); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 支付回调载荷解析失败 event_type=%s client_ip=%s error=%q payload=%s", event.EventType, c.ClientIP(), err.Error(), paymentcore.PayloadSummary(bodyBytes)))
			sendWaffoWebhookResponse(c, wh, false, "invalid payment payload")
			return
		}
		logger.LogInfo(c.Request.Context(), common.LogText("Waffo webhook verified and parsed event_type=%s merchant_order_id=%s order_status=%s client_ip=%s", event.EventType, payload.Result.MerchantOrderID, payload.Result.OrderStatus, c.ClientIP()))
		handleWaffoPayment(c, wh, &payload.Result.PaymentNotificationResult)
	default:
		logger.LogInfo(c.Request.Context(), common.LogText("Waffo webhook ignored event event_type=%s client_ip=%s", event.EventType, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, true, "")
	}
}

func waffoNotificationMerchantID(result *core.PaymentNotificationResult) string {
	if result == nil || result.MerchantInfo == nil {
		return ""
	}
	for _, key := range []string{"merchantId", "merchantID", "merchant_id"} {
		if value, ok := result.MerchantInfo[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

// validateWaffoPaymentCallback 在入账前用订单创建时钉扎的不可变快照校验回调
// 事实（provider/kind/env/merchant/请求号/金额/币种/上游订单号），并首次钉扎
// 上游订单号。缺绑定、错绑定或身份冲突一律拒绝，不进入充值事务。
func validateWaffoPaymentCallback(result *core.PaymentNotificationResult) error {
	if result == nil {
		return errors.New("missing Waffo payment notification")
	}
	tradeNo := strings.TrimSpace(result.MerchantOrderID)
	if tradeNo == "" {
		return errors.New("missing Waffo merchant order id")
	}
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderWaffo {
		return fmt.Errorf("Waffo order not found for tradeNo=%s", tradeNo)
	}
	binding, err := paymentcore.GetPaymentOrderBinding(tradeNo)
	if err != nil {
		return fmt.Errorf("Waffo payment binding not found for tradeNo=%s: %w", tradeNo, err)
	}
	if err := paymentcore.ValidatePaymentOrderBinding(binding, paymentcore.PaymentCallbackFacts{
		Provider:         model.PaymentProviderWaffo,
		OrderKind:        paymentcore.OrderBindingKindTopUp,
		Environment:      paymentcore.CurrentWaffoEnvironment(),
		MerchantID:       waffoNotificationMerchantID(result),
		PaymentRequestID: result.PaymentRequestID,
		ProviderOrderID:  result.AcquiringOrderID,
		ActualAmount:     result.OrderAmount,
		ActualCurrency:   result.OrderCurrency,
	}); err != nil {
		return err
	}
	if binding.ProviderOrderID == "" {
		return paymentcore.BindProviderOrderID(tradeNo, result.AcquiringOrderID)
	}
	return nil
}

// handleWaffoPayment 处理支付完成通知
func handleWaffoPayment(c *gin.Context, wh *core.WebhookHandler, result *core.PaymentNotificationResult) {
	if err := validateWaffoPaymentCallback(result); err != nil {
		tradeNo := ""
		if result != nil {
			tradeNo = result.MerchantOrderID
		}
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 回调订单绑定校验失败 trade_no=%s client_ip=%s error=%q", tradeNo, c.ClientIP(), err.Error()))
		sendWaffoWebhookResponse(c, wh, false, "payment binding mismatch")
		return
	}
	if result.OrderStatus != "PAY_SUCCESS" {
		logger.LogInfo(c.Request.Context(), common.LogText("Waffo order not successful, top-up skipped trade_no=%s order_status=%s client_ip=%s", result.MerchantOrderID, result.OrderStatus, c.ClientIP()))
		// 终态失败订单标记为 failed，避免永远停在 pending
		if result.MerchantOrderID != "" {
			if err := model.UpdatePendingTopUpStatus(result.MerchantOrderID, model.PaymentProviderWaffo, common.TopUpStatusFailed); err != nil &&
				!errors.Is(err, model.ErrTopUpNotFound) &&
				!errors.Is(err, model.ErrTopUpStatusInvalid) {
				logger.LogError(c.Request.Context(), common.LogText("Waffo failed to mark order as failed trade_no=%s error=%q", result.MerchantOrderID, err.Error()))
			}
		}
		sendWaffoWebhookResponse(c, wh, true, "")
		return
	}

	merchantOrderId := result.MerchantOrderID

	LockOrder(merchantOrderId)
	defer UnlockOrder(merchantOrderId)

	if err := model.RechargeWaffo(merchantOrderId, c.ClientIP()); err != nil {
		logger.LogError(c.Request.Context(), common.LogText("Waffo top-up processing failed trade_no=%s client_ip=%s error=%q", merchantOrderId, c.ClientIP(), err.Error()))
		sendWaffoWebhookResponse(c, wh, false, err.Error())
		return
	}

	logger.LogInfo(c.Request.Context(), common.LogText("Waffo top-up succeeded trade_no=%s client_ip=%s", merchantOrderId, c.ClientIP()))
	sendWaffoWebhookResponse(c, wh, true, "")
}

// sendWaffoWebhookResponse 发送签名响应
func sendWaffoWebhookResponse(c *gin.Context, wh *core.WebhookHandler, success bool, msg string) {
	var body, sig string
	if success {
		body, sig = wh.BuildSuccessResponse()
	} else {
		body, sig = wh.BuildFailedResponse(msg)
	}
	c.Header("X-SIGNATURE", sig)
	c.Data(http.StatusOK, "application/json", []byte(body))
}
