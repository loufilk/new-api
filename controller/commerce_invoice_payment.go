package controller

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type invoicePaymentMethod struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

func invoiceFeePaymentError(c *gin.Context, err error) {
	status := 0
	switch {
	case errors.Is(err, model.ErrInvoiceWalletUnavailable), errors.Is(err, model.ErrInvoiceWalletInsufficient):
		status = http.StatusBadRequest
	case errors.Is(err, model.ErrInvoicePaymentActive), errors.Is(err, model.ErrInvoicePaymentChanged):
		status = http.StatusConflict
	}
	if status == 0 {
		commerceError(c, err)
		return
	}
	common.ApiErrorStatus(c, status, common.NewMessage(err.Error()), gin.H{"code": "invoice_payment_failed"})
}

func invoiceEpayMethodEnabled(method string) bool {
	if !isEpayTopUpEnabled() || slices.Contains([]string{"", "wallet", model.PaymentMethodBalance, model.PaymentMethodStripe, model.PaymentMethodCreem, model.PaymentMethodWaffo, model.PaymentMethodWaffoPancake}, method) {
		return false
	}
	return operation_setting.ContainsPayMethod(method)
}

// Before creation, query values provide a display-only quote. After creation,
// every amount is loaded from the caller's application. The pay endpoint never
// accepts a client-supplied fee amount.
func GetInvoiceFeePaymentOptions(c *gin.Context) {
	if !commerceActor(c, false, authz.Permission{}) {
		return
	}
	currency, exponent, fee := strings.ToUpper(c.DefaultQuery("currency", "CNY")), 2, int64(0)
	var payment *model.InvoiceFeePayment
	if c.Param("id") != "" {
		id, ok := commerceID(c)
		if !ok {
			return
		}
		invoice, err := model.GetInvoiceApplication(c.Request.Context(), c.GetInt("id"), id, false)
		if err != nil {
			commerceError(c, err)
			return
		}
		currency, exponent, fee, payment = invoice.Currency, invoice.CurrencyExponent, invoice.FeeMinor, invoice.FeePayment
	} else {
		var err error
		exponent, err = strconv.Atoi(c.DefaultQuery("currency_exponent", "2"))
		if err != nil {
			commerceError(c, model.ErrCommerceInvalid)
			return
		}
		fee, err = strconv.ParseInt(c.DefaultQuery("fee_minor", "0"), 10, 64)
		if err != nil {
			commerceError(c, model.ErrCommerceInvalid)
			return
		}
	}
	quota, _, _, quoteErr := model.InvoiceWalletFeeQuote(currency, exponent, fee)
	if quoteErr != nil && !errors.Is(quoteErr, model.ErrInvoiceWalletUnavailable) {
		invoiceFeePaymentError(c, quoteErr)
		return
	}
	if payment != nil && payment.Provider == "wallet" {
		quota, quoteErr = payment.WalletQuota, nil
	}
	paid, _, _, err := model.GetUserAccountBalances(c.GetInt("id"))
	if err != nil {
		commerceError(c, err)
		return
	}
	methods := make([]invoicePaymentMethod, 0)
	if quoteErr == nil {
		methods = append(methods, invoicePaymentMethod{Type: "wallet", Name: "Paid wallet balance", Provider: "wallet"})
	}
	if currency == "CNY" && exponent == 2 {
		seen := make(map[string]bool)
		for _, method := range operation_setting.PayMethods {
			kind := method["type"]
			if seen[kind] || !invoiceEpayMethodEnabled(kind) {
				continue
			}
			seen[kind] = true
			name := method["name"]
			if name == "" {
				name = kind
			}
			methods = append(methods, invoicePaymentMethod{Type: kind, Name: name, Provider: model.PaymentProviderEpay})
		}
	}
	if payment != nil {
		methods = slices.DeleteFunc(methods, func(method invoicePaymentMethod) bool { return method.Type != payment.Method })
	}
	common.ApiSuccess(c, gin.H{"methods": methods, "wallet_quota": strconv.Itoa(quota), "wallet_balance_quota": strconv.Itoa(paid),
		"currency": currency, "currency_exponent": exponent, "fee_minor": strconv.FormatInt(fee, 10), "payment": payment})
}

func PayInvoiceFee(c *gin.Context) {
	if !commerceActor(c, false, authz.Permission{}) {
		return
	}
	id, ok := commerceID(c)
	if !ok {
		return
	}
	var input model.InvoiceFeePayRequest
	if !commerceBody(c, &input) {
		return
	}
	var client *epay.Client
	if input.Method != "wallet" {
		if !invoiceEpayMethodEnabled(input.Method) {
			commerceError(c, model.ErrCommerceInvalid)
			return
		}
		client = GetEpayClient()
		if client == nil {
			common.ApiErrorT(c, "Payment information has not been configured by administrator")
			return
		}
	}
	invoice, payment, err := model.StartInvoiceFeePayment(c.Request.Context(), c.GetInt("id"), id, c.GetHeader("Idempotency-Key"), input, operation_setting.EpayId)
	if err != nil {
		invoiceFeePaymentError(c, err)
		return
	}
	result := gin.H{"invoice": invoice, "payment": payment}
	if payment.Provider == model.PaymentProviderEpay && payment.Status == "pending" {
		if payment.MerchantScope != operation_setting.EpayId {
			invoiceFeePaymentError(c, model.ErrInvoicePaymentChanged)
			return
		}
		returnURL, returnErr := url.Parse(paymentReturnPath("/invoice"))
		notifyURL, notifyErr := url.Parse(strings.TrimRight(service.GetCallbackAddress(), "/") + "/api/invoices/payments/epay/notify")
		if returnErr != nil || notifyErr != nil {
			commerceError(c, model.ErrCommerceInvalid)
			return
		}
		uri, params, err := client.Purchase(&epay.PurchaseArgs{Type: payment.Method, ServiceTradeNo: payment.TradeNo,
			Name: "Invoice service fee", Money: decimal.NewFromInt(payment.AmountMinor).Shift(-int32(payment.CurrencyExponent)).StringFixed(int32(payment.CurrencyExponent)),
			Device: epay.PC, NotifyUrl: notifyURL, ReturnUrl: returnURL})
		if err != nil {
			common.ApiErrorT(c, "Failed to start payment")
			return
		}
		result["url"], result["params"] = uri, params
	}
	common.ApiSuccess(c, result)
}

func InvoiceEpayNotify(c *gin.Context) {
	// Continue to settle already-created payable orders when new payment methods
	// or applications are disabled. Valid credentials and a bound order are needed.
	if !isEpayWebhookConfigured() {
		c.String(http.StatusOK, "fail")
		return
	}
	var values url.Values
	if c.Request.Method == http.MethodPost {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
		if c.Request.ParseForm() != nil {
			c.String(http.StatusOK, "fail")
			return
		}
		values = c.Request.PostForm
	} else {
		values = c.Request.URL.Query()
	}
	params := make(map[string]string, len(values))
	for key, value := range values {
		if len(value) != 1 {
			c.String(http.StatusOK, "fail")
			return
		}
		params[key] = value[0]
	}
	client := GetEpayClient()
	if client == nil {
		c.String(http.StatusOK, "fail")
		return
	}
	verified, err := client.Verify(params)
	if err != nil || !verified.VerifyStatus {
		c.String(http.StatusOK, "fail")
		return
	}
	if verified.TradeStatus != epay.StatusTradeSuccess {
		c.String(http.StatusOK, "success")
		return
	}
	facts, ok := model.NewVerifiedPaymentFacts(model.PaymentProviderEpay, params["trade_no"], params["pid"], "live", "CNY", verified.Money, false)
	if !ok || model.SettleInvoiceEpayFee(c.Request.Context(), verified.ServiceTradeNo, verified.Type, facts) != nil {
		c.String(http.StatusOK, "fail")
		return
	}
	c.String(http.StatusOK, "success")
}
