package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type newUserBenefitPayMethod struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Icon string `json:"icon,omitempty"`
}

type newUserBenefitPendingOrder struct {
	OrderID         string `json:"order_id"`
	PayAmountCNY    string `json:"pay_amount_cny"`
	CreditAmountCNY string `json:"credit_amount_cny"`
	PaymentMethod   string `json:"payment_method"`
	ConfigVersion   int64  `json:"config_version"`
}

type newUserBenefitResponse struct {
	Enabled         bool                        `json:"enabled"`
	Eligible        bool                        `json:"eligible"`
	Status          string                      `json:"status"`
	PayAmountCNY    string                      `json:"pay_amount_cny"`
	CreditAmountCNY string                      `json:"credit_amount_cny"`
	PaidQuota       int                         `json:"paid_quota"`
	BonusQuota      int                         `json:"bonus_quota"`
	ConfigVersion   int64                       `json:"config_version"`
	PayMethods      []newUserBenefitPayMethod   `json:"pay_methods"`
	PendingOrder    *newUserBenefitPendingOrder `json:"pending_order,omitempty"`
}

// A configured payment list can also contain the dedicated Stripe, Creem or
// Waffo entries. Only actual Epay methods may use this CNY checkout.
func newUserBenefitPayMethods(configuredMethods []map[string]string) []newUserBenefitPayMethod {
	methods := make([]newUserBenefitPayMethod, 0, len(configuredMethods))
	seen := make(map[string]bool)
	for _, configured := range configuredMethods {
		method := configured["type"]
		if method == "" || method != strings.TrimSpace(method) || strings.Contains(method, "&") || len(method) > 50 || seen[method] {
			continue
		}
		switch strings.ToLower(method) {
		case model.PaymentMethodStripe, model.PaymentMethodCreem, model.PaymentMethodWaffo, model.PaymentMethodWaffoPancake, model.PaymentMethodBalance:
			continue
		}
		name := strings.TrimSpace(configured["name"])
		if name == "" {
			name = method
		}
		methods = append(methods, newUserBenefitPayMethod{Type: method, Name: name, Icon: configured["icon"]})
		seen[method] = true
	}
	return methods
}

func newUserBenefitMethodAvailable(method string, methods []newUserBenefitPayMethod) bool {
	for _, available := range methods {
		if available.Type == method {
			return true
		}
	}
	return false
}

func newUserBenefitHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || strings.Contains(raw, "&") {
		return nil, model.ErrNewUserBenefitUnavailable
	}
	return parsed, nil
}

type newUserBenefitPaymentSnapshot struct {
	Client    *epay.Client
	Methods   []newUserBenefitPayMethod
	ReturnURL *url.URL
	NotifyURL *url.URL
}

// Capture credentials, allowed methods and callback URLs under the option
// writer's lock. No database or network work is performed while holding it.
func snapshotNewUserBenefitPayment() *newUserBenefitPaymentSnapshot {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if !isEpayTopUpEnabled() {
		return nil
	}
	client := GetEpayClient()
	if client == nil || strings.Contains(client.Config.PartnerID, "&") {
		return nil
	}
	if _, err := newUserBenefitHTTPURL(client.BaseUrl.String()); err != nil {
		return nil
	}
	methods := newUserBenefitPayMethods(operation_setting.PayMethods)
	if len(methods) == 0 {
		return nil
	}
	returnURL, returnErr := newUserBenefitHTTPURL(paymentReturnPath("/wallet?show_history=true"))
	notifyURL, notifyErr := newUserBenefitHTTPURL(strings.TrimRight(service.GetCallbackAddress(), "/") + "/api/user/epay/notify")
	if returnErr != nil || notifyErr != nil {
		return nil
	}
	return &newUserBenefitPaymentSnapshot{Client: client, Methods: methods, ReturnURL: returnURL, NotifyURL: notifyURL}
}

func writeNewUserBenefitError(c *gin.Context, err error) {
	status, code, message := http.StatusServiceUnavailable, "new_user_benefit_unavailable", "New user benefit is currently unavailable"
	switch {
	case errors.Is(err, model.ErrNewUserBenefitConfigChanged):
		status, code, message = http.StatusConflict, "new_user_benefit_config_changed", "New user benefit settings have changed. Refresh and try again"
	case errors.Is(err, model.ErrNewUserBenefitIneligible), errors.Is(err, model.ErrNewUserBenefitClaimed):
		status, code, message = http.StatusForbidden, "new_user_benefit_ineligible", "This account is not eligible for the new user benefit"
	case errors.Is(err, model.ErrNewUserBenefitOrderConflict):
		status = http.StatusConflict
	case errors.Is(err, model.ErrNewUserBenefitUnavailable), errors.Is(err, model.ErrNewUserBenefitInvalidConfig):
	default:
		logger.LogError(c.Request.Context(), fmt.Sprintf("new user benefit failed user_id=%d error=%q", c.GetInt("id"), err.Error()))
	}
	common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": code})
}

func rejectNewUserBenefitPaymentMethod(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This payment method is not available for the new user benefit"), gin.H{"code": "new_user_benefit_payment_method_unavailable"})
}

// GetNewUserBenefit returns only the caller's eligibility and public payment
// terms. Merchant credentials and internal payment receipt fields stay server-side.
func GetNewUserBenefit(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("Please log in first"), gin.H{"code": "new_user_benefit_unavailable"})
		return
	}
	state, err := model.GetNewUserBenefitState(c.Request.Context(), userID)
	if err != nil {
		writeNewUserBenefitError(c, err)
		return
	}
	if state == nil || state.Config == nil {
		writeNewUserBenefitError(c, model.ErrNewUserBenefitUnavailable)
		return
	}
	response := newUserBenefitResponse{
		Eligible: state.Eligible, Status: state.Status,
		PayAmountCNY:    model.FormatNewUserBenefitCNY(state.Config.PayAmountCents),
		CreditAmountCNY: model.FormatNewUserBenefitCNY(state.Config.CreditAmountCents),
		PaidQuota:       state.PaidQuota, BonusQuota: state.BonusQuota,
		ConfigVersion: state.Config.Version, PayMethods: []newUserBenefitPayMethod{},
	}
	payment := snapshotNewUserBenefitPayment()
	if payment != nil {
		response.Enabled = state.Config.Enabled
		if state.Status == "available" {
			response.PayMethods = payment.Methods
		}
	}
	if state.Order != nil {
		order := state.Order
		response.PayAmountCNY = model.FormatNewUserBenefitCNY(order.PayAmountCents)
		response.CreditAmountCNY = model.FormatNewUserBenefitCNY(order.CreditAmountCents)
		response.PaidQuota, response.BonusQuota = order.PaidQuota, order.BonusQuota
		response.ConfigVersion = order.ConfigVersion
		if state.Status == "pending" {
			response.PendingOrder = &newUserBenefitPendingOrder{
				OrderID: order.TradeNo, PayAmountCNY: response.PayAmountCNY,
				CreditAmountCNY: response.CreditAmountCNY, PaymentMethod: order.PaymentMethod,
				ConfigVersion: order.ConfigVersion,
			}
			if payment != nil && strings.TrimSpace(payment.Client.Config.PartnerID) == order.MerchantScope {
				for _, method := range payment.Methods {
					if method.Type == order.PaymentMethod {
						response.PayMethods = append(response.PayMethods, method)
					}
				}
			}
		}
	}
	common.ApiSuccess(c, response)
}

func RequestNewUserBenefitPay(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("Please log in first"), gin.H{"code": "new_user_benefit_unavailable"})
		return
	}
	var request struct {
		PaymentMethod string `json:"payment_method"`
		ConfigVersion *int64 `json:"config_version"`
	}
	requestID := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	parsedID, idErr := uuid.Parse(requestID)
	if err := common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &request); err != nil ||
		request.ConfigVersion == nil || *request.ConfigVersion < 0 || request.PaymentMethod == "" ||
		idErr != nil || parsedID == uuid.Nil || len(requestID) != 36 || strings.ToLower(requestID) != parsedID.String() {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"), gin.H{"code": "new_user_benefit_unavailable"})
		return
	}
	payment := snapshotNewUserBenefitPayment()
	if payment == nil {
		writeNewUserBenefitError(c, model.ErrNewUserBenefitUnavailable)
		return
	}
	if !newUserBenefitMethodAvailable(request.PaymentMethod, payment.Methods) {
		rejectNewUserBenefitPaymentMethod(c)
		return
	}
	tradeNo, err := buildEpayTradeNo(userID, time.Now().UnixMilli(), strings.ToUpper(common.GetRandomString(epayTradeNoRandomLength)))
	if err != nil {
		writeNewUserBenefitError(c, err)
		return
	}
	// Account uniqueness provides durable idempotency across tabs, different
	// request IDs and instances. A pending order always keeps its original terms.
	order, err := model.ReserveNewUserBenefit(c.Request.Context(), userID, *request.ConfigVersion, request.PaymentMethod, strings.TrimSpace(payment.Client.Config.PartnerID), tradeNo)
	if err != nil {
		writeNewUserBenefitError(c, err)
		return
	}
	payAmount := model.FormatNewUserBenefitCNY(order.PayAmountCents)
	// Epay Purchase only signs a local HTML form; it performs no network call.
	// Persist the commitment before exposing a payable form to the customer.
	uri, params, err := payment.Client.Purchase(&epay.PurchaseArgs{
		Type: order.PaymentMethod, ServiceTradeNo: order.TradeNo,
		Name: "新手充值福利", Money: payAmount, Device: epay.PC,
		NotifyUrl: payment.NotifyURL, ReturnUrl: payment.ReturnURL,
	})
	if err != nil {
		writeNewUserBenefitError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"order_id": order.TradeNo, "pay_amount_cny": payAmount,
		"credit_amount_cny": model.FormatNewUserBenefitCNY(order.CreditAmountCents),
		"status":            "pending", "checkout": gin.H{"kind": "form", "url": uri, "params": params},
	})
}
