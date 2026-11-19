package controller

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/paymentcore"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

const customerInvoiceEpayNotifyPath = "/api/invoice/fee/epay/notify"

var (
	invoiceEpayField  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	invoiceEpayMethod = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)
	invoiceEpayAmount = regexp.MustCompile(`^[0-9]{1,14}(\.[0-9]{1,2})?$`)
)

type invoiceEpayCheckoutConfig struct {
	client               *epay.Client
	methods              []map[string]string
	returnURL, notifyURL *url.URL
}

func invoiceEpayURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 512 || !utf8.ValidString(raw) || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || strings.ContainsAny(raw, "&#\\") || strings.IndexFunc(u.Path, unicode.IsControl) >= 0 {
		return nil
	}
	return u
}

func configuredInvoiceEpayCheckout() *invoiceEpayCheckoutConfig {
	policy := model.GetCustomerInvoicePolicy()
	if policy.MerchantScope == "" {
		return nil
	}
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if !isEpayTopUpEnabled() || operation_setting.EpayId != policy.MerchantScope {
		return nil
	}
	client := GetEpayClient()
	if client == nil || invoiceEpayURL(client.BaseUrl.String()) == nil || strings.ContainsAny(client.Config.PartnerID, "&=") {
		return nil
	}
	returnURL := invoiceEpayURL(paymentReturnPath("/api/invoice/fee/epay/return"))
	notifyURL := invoiceEpayURL(strings.TrimRight(paymentcore.CallbackAddress(), "/") + customerInvoiceEpayNotifyPath)
	if returnURL == nil || notifyURL == nil {
		return nil
	}
	config := &invoiceEpayCheckoutConfig{client: client, returnURL: returnURL, notifyURL: notifyURL}
	// The configured list can contain dedicated (non-Epay) providers. The
	// existing CNY checkout method filter removes those names and duplicates.
	for _, m := range newUserBenefitPayMethods(operation_setting.PayMethods) {
		if invoiceEpayMethod.MatchString(m.Type) && !strings.EqualFold(m.Type, "wallet") && !strings.EqualFold(m.Type, "paid_wallet") {
			config.methods = append(config.methods, map[string]string{"type": m.Type, "name": m.Name})
		}
	}
	if len(config.methods) == 0 {
		return nil
	}
	return config
}

// CustomerInvoiceEpayMethods is the public list of usable Epay sub-methods,
// not the complete wallet top-up or payment-provider catalog.
func CustomerInvoiceEpayMethods() []map[string]string {
	if cfg := configuredInvoiceEpayCheckout(); cfg != nil {
		return cfg.methods
	}
	return []map[string]string{}
}

func BeginCustomerInvoiceEpay(c *gin.Context) {
	id, ok := customerInvoiceID(c)
	if !ok {
		return
	}
	var req struct {
		Method string `json:"payment_method"`
	}
	if !readCustomerInvoiceJSON(c, &req) {
		return
	}
	if !invoiceEpayMethod.MatchString(req.Method) {
		customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		return
	}
	cfg := configuredInvoiceEpayCheckout()
	if cfg == nil {
		customerInvoiceError(c, model.ErrCustomerInvoiceDisabled)
		return
	}
	allowed := false
	for _, m := range cfg.methods {
		if m["type"] == req.Method {
			allowed = true
			break
		}
	}
	if !allowed {
		customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		return
	}
	order, err := model.BeginCustomerInvoiceEpay(c.Request.Context(), c.GetInt("id"), id, cfg.client.Config.PartnerID, req.Method, cfg.client.BaseUrl.String())
	if err != nil {
		customerInvoiceError(c, err)
		return
	}
	// Purchase signs a form locally, it never executes a payment. Both the
	// fee amount and the payable order identity come from the committed row.
	money := fmt.Sprintf("%d.%02d", order.FeeMinor/100, order.FeeMinor%100)
	uri, params, err := cfg.client.Purchase(&epay.PurchaseArgs{Type: req.Method, ServiceTradeNo: order.TradeNo,
		Name: "Invoice service fee", Money: money, Device: epay.PC, NotifyUrl: cfg.notifyURL, ReturnUrl: cfg.returnURL})
	if err != nil {
		customerInvoiceError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"url": uri, "params": params})
}

// The browser return only navigates to the dashboard. The only public entry
// point that accepts fee payment evidence is this signed server notification.
func CustomerInvoiceEpayNotify(c *gin.Context) {
	const maxForm = 16 << 10
	c.Header("Cache-Control", "no-store")
	var form url.Values
	var err error
	switch c.Request.Method {
	case http.MethodGet:
		if len(c.Request.URL.RawQuery) <= maxForm {
			form, err = url.ParseQuery(c.Request.URL.RawQuery)
		}
	case http.MethodPost:
		mt, _, e := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if e == nil && mt == "application/x-www-form-urlencoded" && c.Request.URL.RawQuery == "" {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxForm)
			err = c.Request.ParseForm()
			form = c.Request.PostForm
		}
	}
	if err != nil || len(form) == 0 || len(form) > 64 {
		c.String(http.StatusOK, "fail")
		return
	}
	params := make(map[string]string, len(form))
	for key, values := range form {
		// The gateway signature format joins unescaped key=value pairs with
		// '&'. Reject decoded delimiters and malformed/ambiguous keys before
		// invoking the SDK; never accept duplicate values, even if identical.
		if len(values) != 1 || !invoiceEpayField.MatchString(key) || len(values[0]) > 4096 ||
			!utf8.ValidString(values[0]) || strings.Contains(values[0], "&") || strings.IndexFunc(values[0], unicode.IsControl) >= 0 {
			c.String(http.StatusOK, "fail")
			return
		}
		params[key] = values[0]
	}
	// Disabling new checkout or a sub-method must not strand a payment
	// already made. Only a fully configured matching merchant can verify it.
	common.OptionMapRWMutex.RLock()
	client := GetEpayClient()
	common.OptionMapRWMutex.RUnlock()
	if client == nil || invoiceEpayURL(client.BaseUrl.String()) == nil || params["pid"] != client.Config.PartnerID ||
		params["trade_status"] != epay.StatusTradeSuccess || !invoiceEpayMethod.MatchString(params["type"]) ||
		!invoiceEpayAmount.MatchString(params["money"]) || len(params["out_trade_no"]) != 32 || !strings.HasPrefix(params["out_trade_no"], "CIF") ||
		params["trade_no"] == "" || strings.TrimSpace(params["trade_no"]) != params["trade_no"] || len(params["trade_no"]) > 255 {
		c.String(http.StatusOK, "fail")
		return
	}
	v, err := client.Verify(params)
	if err != nil || !v.VerifyStatus {
		c.String(http.StatusOK, "fail")
		return
	}
	facts, ok := model.NewVerifiedPaymentFacts(model.PaymentProviderEpay, params["trade_no"], params["pid"], "live", "CNY", params["money"], false)
	if !ok || model.SettleCustomerInvoiceEpay(c.Request.Context(), params["out_trade_no"], params["type"], facts, client.BaseUrl.String()) != nil {
		c.String(http.StatusOK, "fail")
		return
	}
	c.String(http.StatusOK, "success")
}

// The return URL is a browser convenience, never payment proof. Strip all
// provider parameters before entering the dashboard, including referrer data.
func CustomerInvoiceEpayReturn(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Redirect(http.StatusSeeOther, "/invoice")
}
