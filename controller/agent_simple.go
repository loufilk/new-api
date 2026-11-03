package controller

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func writeAgentSimpleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAgentSimplePermission), errors.Is(err, model.ErrAgentProfilePermission):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage(err.Error()))
	case errors.Is(err, model.ErrAgentSimpleUnavailable):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage(err.Error()))
	case errors.Is(err, model.ErrAgentSimpleInsufficient), errors.Is(err, model.ErrAgentSimpleConflict), errors.Is(err, model.ErrAgentQuotaConflict), errors.Is(err, model.ErrAgentPaymentReceiptConflict), errors.Is(err, model.ErrAgentSimpleState), errors.Is(err, model.ErrAgentSimpleInvariant):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage(err.Error()))
	case errors.Is(err, model.ErrAgentSimpleInvalid):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Agent record not found"))
	default:
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Agent operation failed; check its status before retrying"))
	}
}

func GetAgentSimpleBalance(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	agentID, _ := strconv.Atoi(c.Query("agent_user_id"))
	account, err := model.GetAgentSimpleAccount(identity, agentID)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, account)
}

func RequestAgentSimpleRecharge(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Payment compliance terms are not confirmed"))
		return
	}
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	var request model.AgentSimpleRechargeRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil || !operation_setting.ContainsPayMethod(request.PaymentMethod) || !isAgentSimpleEpayMethod(request.PaymentMethod) {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	quote, err := model.QuoteAgentSimpleRecharge(request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	client := GetEpayClient()
	if client == nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Payment information has not been configured by administrator"))
		return
	}
	tradeNo, err := buildEpayTradeNo(identity.UserID, time.Now().UnixMilli(), strings.ToUpper(common.GetRandomString(epayTradeNoRandomLength)))
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	callbackAddress := service.GetCallbackAddress()
	returnURL, err := url.Parse(paymentReturnPath("/agent-platform"))
	if err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	notifyURL, err := url.Parse(callbackAddress + "/api/user/epay/notify")
	if err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	name := "代理余额充值"
	if request.Kind == "repayment" {
		name = "代理还款"
	}
	uri, params, err := client.Purchase(&epay.PurchaseArgs{Type: request.PaymentMethod, ServiceTradeNo: tradeNo, Name: name, Money: decimal.NewFromInt(quote.PayFen).Div(decimal.NewFromInt(100)).StringFixed(2), Device: epay.PC, NotifyUrl: notifyURL, ReturnUrl: returnURL})
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	result, err := model.CreateAgentSimpleRecharge(identity, request, tradeNo, uri, params)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func CreateAgentSimpleCode(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimpleCodeRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	code, err := model.CreateAgentSimpleCode(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, code)
}

func CancelAgentSimpleCode(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	codeID, err := strconv.Atoi(c.Param("id"))
	if err != nil || codeID <= 0 {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimpleCodeCancelRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	request.CodeID = codeID
	code, err := model.CancelAgentSimpleCode(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, code)
}

func GetAgentSimpleCodes(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	page := redemptionPage(c)
	agentID, _ := strconv.Atoi(c.Query("agent_user_id"))
	mode := c.Query("mode")
	rows, total, err := model.GetAgentSimpleRecords(identity, agentID, page.GetStartIdx(), page.GetPageSize(), mode)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func GetAgentSimplePaymentFees(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	rows, err := model.ListAgentSimplePaymentFees(identity)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func UpdateAgentSimplePaymentFee(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimplePaymentFeeRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	result, err := model.UpdateAgentSimplePaymentFee(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func QuoteAgentSimplePayment(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimplePaymentRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	if _, err := model.GetAgentSimpleAccount(identity, identity.UserID); err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	quote, err := model.QuoteAgentSimplePayment(request.PaymentMethod, request.Quota, request.SalePriceFen)
	if errors.Is(err, model.ErrAgentSimpleInvalid) && quote != nil && quote.MinimumSalePriceFen > 0 {
		common.ApiSuccess(c, quote)
		return
	}
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, quote)
}

func GetAgentSimplePaymentMethods(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	methods, err := model.GetAgentSimpleAvailablePaymentMethods(identity, c.Query("purpose"))
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, methods)
}

func CreateAgentSimplePayment(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Payment compliance terms are not confirmed"))
		return
	}
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimplePaymentRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil || !operation_setting.ContainsPayMethod(request.PaymentMethod) || !isAgentSimpleEpayMethod(request.PaymentMethod) {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	client := GetEpayClient()
	if client == nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Payment information has not been configured by administrator"))
		return
	}
	tradeNo, err := buildEpayTradeNo(identity.UserID, time.Now().UnixMilli(), strings.ToUpper(common.GetRandomString(epayTradeNoRandomLength)))
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	publicID := common.GetUUID()
	token := common.GetRandomString(64)
	amount := decimal.NewFromInt(request.SalePriceFen).Div(decimal.NewFromInt(100)).StringFixed(2)
	callbackAddress := service.GetCallbackAddress()
	returnURL, err := url.Parse(strings.TrimRight(paymentReturnPath(""), "/") + "/agent-pay/" + publicID + "#token=" + token)
	if err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	notifyURL, err := url.Parse(callbackAddress + "/api/user/epay/notify")
	if err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	paymentURL, params, err := client.Purchase(&epay.PurchaseArgs{Type: request.PaymentMethod, ServiceTradeNo: tradeNo, Name: "万川兑换码", Money: amount, Device: epay.PC, NotifyUrl: notifyURL, ReturnUrl: returnURL})
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	encodedParams, err := common.Marshal(params)
	if err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	order, err := model.CreateAgentSimplePaymentOrder(identity, request, publicID, token, tradeNo, paymentURL, string(encodedParams), paymentReturnPath(""))
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	var storedParams map[string]string
	if common.UnmarshalJsonStr(order.PaymentParams, &storedParams) != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvariant)
		return
	}
	common.ApiSuccess(c, gin.H{"order": order, "payment_url": order.PaymentURL, "payment_params": storedParams})
}

func GetAgentSimplePayments(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	page := redemptionPage(c)
	agentID, _ := strconv.Atoi(c.Query("agent_user_id"))
	rows, total, err := model.ListAgentSimplePaymentOrders(identity, agentID, page.GetStartIdx(), page.GetPageSize(), paymentReturnPath(""))
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func UpdateAgentSimpleStatus(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimpleStatusRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	result, err := model.SetAgentSimpleStatus(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func TransferAgentSimplePaymentProfit(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Payment compliance terms are not confirmed"))
		return
	}
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimplePaymentTransferCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	result, err := model.TransferAgentSimplePaymentProfit(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func RefundAgentSimplePayment(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimplePaymentRefundCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	if err := model.RefundAgentSimplePayment(identity, request); err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func WithdrawAgentSimplePayment(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimplePaymentWithdrawalRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	result, err := model.WithdrawAgentSimplePaymentProfit(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func GetAgentSimplePublicPayment(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var request struct {
		Token string `json:"token"`
	}
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	result, err := model.GetAgentSimplePublicPayment(c.Param("public_id"), request.Token)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func isAgentSimpleEpayMethod(method string) bool {
	switch method {
	case model.PaymentMethodStripe, model.PaymentMethodCreem, model.PaymentMethodWaffo, model.PaymentMethodWaffoPancake, model.PaymentMethodBalance:
		return false
	default:
		return true
	}
}

func AdjustAgentSimpleBalance(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimpleAdjustmentRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	account, err := model.AdjustAgentSimpleBalance(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, account)
}

func SetAgentSimpleCreditLimit(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimpleCreditLimitRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	account, err := model.SetAgentSimpleCreditLimit(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, account)
}

func RepayAgentSimpleCredit(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimpleRepaymentRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	account, err := model.RepayAgentSimpleCredit(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, account)
}

func ListAgentSimpleAgents(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	page := redemptionPage(c)
	rows, total, err := model.ListAgentSimpleAgents(identity, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func ListAgentSimpleHistory(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	page := redemptionPage(c)
	targetID, _ := strconv.Atoi(c.Query("agent_user_id"))
	rows, total, err := model.QueryAgentSimpleHistory(identity, c.Param("kind"), targetID, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func RevealAgentSimpleCode(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	codeID, err := strconv.Atoi(c.Param("id"))
	if err != nil || codeID <= 0 {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var request struct {
		Reason string `json:"reason"`
	}
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	key, err := model.RevealAgentSimpleCode(identity, codeID, request.Reason)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"key": key})
}

func RecordAgentSimpleWithdrawal(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimpleWithdrawalRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	result, err := model.RecordAgentSimpleWithdrawal(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func TransferReferralToAgentBalance(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Payment compliance terms are not confirmed"))
		return
	}
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var request model.AgentSimpleTransferRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	result, err := model.TransferReferralToAgentBalance(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func SettleAgentSimpleExit(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request model.AgentSimpleExitRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	result, err := model.SettleAgentSimpleExit(identity, request)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func GetAgentSimpleUnreadReminders(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	rows, err := model.ListUnreadAgentSimpleReminders(identity)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func AcknowledgeAgentSimpleReminders(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentSimpleError(c, model.ErrAgentSimplePermission)
		return
	}
	var request struct {
		IDs []int64 `json:"ids"`
	}
	if common.DecodeJsonStrict(c.Request.Body, &request) != nil {
		writeAgentSimpleError(c, model.ErrAgentSimpleInvalid)
		return
	}
	if err := model.AcknowledgeAgentSimpleReminders(identity, request.IDs); err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
