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
	"github.com/QuantumNous/new-api/setting/system_setting"
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
	RequireVerifiedEmail        bool                        `json:"require_verified_email"`
	NetworkLimitEnabled         bool                        `json:"network_limit_enabled"`
	PaymentIdentityCheckEnabled bool                        `json:"payment_identity_check_enabled"`
	RewardStatus                string                      `json:"reward_status,omitempty"`
	RewardReason                string                      `json:"reward_reason,omitempty"`
	Enabled                     bool                        `json:"enabled"`
	Eligible                    bool                        `json:"eligible"`
	Status                      string                      `json:"status"`
	PayAmountCNY                string                      `json:"pay_amount_cny"`
	CreditAmountCNY             string                      `json:"credit_amount_cny"`
	PaidQuota                   int                         `json:"paid_quota"`
	BonusQuota                  int                         `json:"bonus_quota"`
	ConfigVersion               int64                       `json:"config_version"`
	PayMethods                  []newUserBenefitPayMethod   `json:"pay_methods"`
	PendingOrder                *newUserBenefitPendingOrder `json:"pending_order,omitempty"`
	BlockedReason               string                      `json:"blocked_reason,omitempty"`
	TurnstileRequired           bool                        `json:"turnstile_required"`
	TurnstileSiteKey            string                      `json:"turnstile_site_key,omitempty"`
}

type newUserBenefitVerificationConfig struct {
	Enabled bool
	Ready   bool
	SiteKey string
}

func snapshotNewUserBenefitVerification() newUserBenefitVerificationConfig {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	_, hostnameErr := system_setting.PublicSiteHostname()
	config := newUserBenefitVerificationConfig{Enabled: common.TurnstileCheckEnabled, SiteKey: strings.TrimSpace(common.TurnstileSiteKey)}
	config.Ready = config.Enabled && config.SiteKey != "" && strings.TrimSpace(common.TurnstileSecretKey) != "" && hostnameErr == nil
	return config
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

func newUserBenefitIdentityGatewayReady(payment *newUserBenefitPaymentSnapshot) bool {
	return payment != nil && payment.Client.BaseUrl.Scheme == "https" && payment.Client.BaseUrl.Hostname() != "" &&
		payment.Client.BaseUrl.RawQuery == "" && !payment.Client.BaseUrl.ForceQuery && !strings.Contains(payment.Client.BaseUrl.String(), "#") &&
		newUserBenefitMethodAvailable("alipay", payment.Methods)
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
	case errors.Is(err, model.ErrNewUserBenefitEmailRequired):
		status, code, message = http.StatusForbidden, "new_user_benefit_email_required", "Verify your email address before claiming the new user benefit"
	case errors.Is(err, model.ErrNewUserBenefitIdentityUsed):
		status, code, message = http.StatusForbidden, "new_user_benefit_identity_used", "This account is not eligible for the new user benefit"
	case errors.Is(err, model.ErrNewUserBenefitLimitReached):
		status, code, message = http.StatusTooManyRequests, "new_user_benefit_limit_reached", "The new user benefit limit has been reached. Please try again later"
	case errors.Is(err, model.ErrNewUserBenefitVerificationRequired):
		status, code, message = http.StatusForbidden, "new_user_benefit_verification_required", "Complete the security verification before claiming the new user benefit"
	case errors.Is(err, model.ErrNewUserBenefitInvalidNetwork):
		status, code, message = http.StatusForbidden, "new_user_benefit_invalid_network", "The new user benefit is unavailable from this network"
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
		RequireVerifiedEmail: state.Config.RequireVerifiedEmail, NetworkLimitEnabled: state.Config.NetworkLimitEnabled,
		PaymentIdentityCheckEnabled: state.Config.PaymentIdentityCheckEnabled,
		Eligible:                    state.Eligible, Status: state.Status,
		PayAmountCNY:    model.FormatNewUserBenefitCNY(state.Config.PayAmountCents),
		CreditAmountCNY: model.FormatNewUserBenefitCNY(state.Config.CreditAmountCents),
		PaidQuota:       state.PaidQuota, BonusQuota: state.BonusQuota,
		ConfigVersion: state.Config.Version, PayMethods: []newUserBenefitPayMethod{},
		BlockedReason: state.BlockedReason,
	}
	verification := snapshotNewUserBenefitVerification()
	response.TurnstileRequired = state.Config.RequireTurnstile && state.Status == model.NewUserBenefitStatusAvailable
	if response.TurnstileRequired {
		response.TurnstileSiteKey = verification.SiteKey
	}
	payment := snapshotNewUserBenefitPayment()
	if payment != nil {
		response.Enabled = state.Config.Enabled
		if state.Status == "available" && (!state.Config.PaymentIdentityCheckEnabled || newUserBenefitIdentityGatewayReady(payment)) {
			for _, method := range payment.Methods {
				if !state.Config.PaymentIdentityCheckEnabled || method.Type == "alipay" {
					response.PayMethods = append(response.PayMethods, method)
				}
			}
		}
	}
	if state.Order != nil {
		order := state.Order
		response.RequireVerifiedEmail, response.NetworkLimitEnabled = order.RequireVerifiedEmail, order.NetworkLimitEnabled
		response.PaymentIdentityCheckEnabled = order.PaymentIdentityCheckEnabled
		response.RewardStatus, response.RewardReason = order.RewardStatus, order.RewardReason
		response.TurnstileRequired = order.Status == model.NewUserBenefitStatusPending && order.RequireTurnstile && verification.Enabled
		if response.TurnstileRequired {
			response.TurnstileSiteKey = verification.SiteKey
		}
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
			if payment != nil && strings.TrimSpace(payment.Client.Config.PartnerID) == order.MerchantScope && (!order.PaymentIdentityCheckEnabled || order.PaymentGateway == payment.Client.BaseUrl.String()) {
				for _, method := range payment.Methods {
					if method.Type == order.PaymentMethod {
						response.PayMethods = append(response.PayMethods, method)
					}
				}
			}
		}
	}
	if response.Eligible && len(response.PayMethods) == 0 {
		response.Eligible, response.BlockedReason = false, "unavailable"
	}
	if !verification.Ready && response.TurnstileRequired {
		response.Eligible, response.BlockedReason = false, "verification_unavailable"
	}
	if !response.Eligible {
		response.PayMethods = []newUserBenefitPayMethod{}
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
	verification := snapshotNewUserBenefitVerification()
	order, err := model.ReserveNewUserBenefit(c.Request.Context(), userID, *request.ConfigVersion, request.PaymentMethod, strings.TrimSpace(payment.Client.Config.PartnerID), tradeNo, model.NewUserBenefitRiskContext{
		PaymentGateway: payment.Client.BaseUrl.String(), ClientIP: c.ClientIP(), HumanVerified: verification.Ready && c.GetString("turnstile_verified_action") == "new_user_benefit",
	})
	if err != nil {
		if errors.Is(err, model.ErrNewUserBenefitVerificationRequired) && !verification.Ready {
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("New user benefit security verification is currently unavailable"), gin.H{"code": "new_user_benefit_verification_unavailable"})
			return
		}
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

// NewUserBenefitVerification applies the benefit's policy independently of the
// site's general challenge. Existing orders use their captured policy.
func NewUserBenefitVerification(verify gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		state, err := model.GetNewUserBenefitState(c.Request.Context(), c.GetInt("id"))
		if err != nil {
			writeNewUserBenefitError(c, err)
			c.Abort()
			return
		}
		required := state.Config.RequireTurnstile
		if state.Order != nil {
			required = state.Order.Status == model.NewUserBenefitStatusPending && state.Order.RequireTurnstile
		}
		if required {
			verify(c)
			return
		}
		c.Next()
	}
}
