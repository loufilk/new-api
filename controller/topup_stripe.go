package controller

import (
	"context"
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
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
	"github.com/thanhpk/randstr"
)

var stripeAdaptor = &StripeAdaptor{}

// StripePayRequest represents a payment request for Stripe checkout.
type StripePayRequest struct {
	// Amount is the quantity of units to purchase.
	Amount int64 `json:"amount"`
	// PaymentMethod specifies the payment method (e.g., "stripe").
	PaymentMethod string `json:"payment_method"`
	// SuccessURL is the optional custom URL to redirect after successful payment.
	// If empty, defaults to the server's console log page.
	SuccessURL string `json:"success_url,omitempty"`
	// CancelURL is the optional custom URL to redirect when payment is canceled.
	// If empty, defaults to the server's console topup page.
	CancelURL string `json:"cancel_url,omitempty"`
}

type StripeAdaptor struct {
}

func (*StripeAdaptor) RequestAmount(c *gin.Context, req *StripePayRequest) {
	if req.Amount < getStripeMinTopup() {
		paymentError(c, common.NewMessage("Top-up amount cannot be less than {{min}}", map[string]any{"min": getStripeMinTopup()}))
		return
	}
	if stripeTopupUnits(req.Amount).GreaterThan(decimal.NewFromInt(10000)) {
		paymentError(c, common.NewMessage("Top-up amount cannot exceed {{max}}", map[string]any{"max": 10000}))
		return
	}
	id := c.GetInt("id")
	guard, guardOK := beginControllerGroupConfigRead(c)
	if !guardOK {
		return
	}
	defer guard.Close()
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		paymentError(c, common.NewMessage("Failed to get the user group"))
		return
	}
	terms := stripeTerms(req.Amount, group)
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	if rejectInvalidCreditedQuota(c, id, terms.creditedQuota) {
		return
	}
	quote, err := quoteStripeCheckout(c.Request.Context(), terms)
	if err != nil {
		paymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": quote.money()})
}

func (*StripeAdaptor) RequestPay(c *gin.Context, req *StripePayRequest) {
	if req.PaymentMethod != model.PaymentMethodStripe {
		paymentError(c, common.NewMessage("Unsupported payment channel"))
		return
	}
	if req.Amount < getStripeMinTopup() {
		common.ApiErrorStatus(c, http.StatusOK, common.NewMessage("Top-up amount cannot be less than {{min}}", map[string]any{"min": getStripeMinTopup()}), gin.H{"data": 10})
		return
	}
	if stripeTopupUnits(req.Amount).GreaterThan(decimal.NewFromInt(10000)) {
		common.ApiErrorStatus(c, http.StatusOK, common.NewMessage("Top-up amount cannot exceed {{max}}", map[string]any{"max": 10000}), gin.H{"data": 10})
		return
	}

	if req.SuccessURL != "" && common.ValidateRedirectURL(req.SuccessURL) != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Payment success redirect URL is not in the trusted domain list"), gin.H{"data": ""})
		return
	}

	if req.CancelURL != "" && common.ValidateRedirectURL(req.CancelURL) != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Payment cancel redirect URL is not in the trusted domain list"), gin.H{"data": ""})
		return
	}

	id := c.GetInt("id")
	guard, guardOK := beginControllerGroupConfigRead(c)
	if !guardOK {
		return
	}
	defer guard.Close()
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		paymentError(c, common.NewMessage("User does not exist"))
		return
	}
	terms := stripeTerms(req.Amount, user.Group)
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	creditedQuota, err := validateCreditedQuota(terms.creditedQuota)
	if err == nil {
		err = model.ValidateTopUpQuotaCapacity(id, creditedQuota)
	}
	if err != nil {
		paymentError(c, err)
		return
	}
	quote, err := quoteStripeCheckout(c.Request.Context(), terms)
	if err != nil {
		paymentError(c, err)
		return
	}

	reference := fmt.Sprintf("new-api-ref-%d-%d-%s", user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceId := "ref_" + common.Sha1([]byte(reference))

	// The order exists before any external checkout session is public.
	topUp := &model.TopUp{
		UserId:                       id,
		Amount:                       req.Amount,
		Money:                        terms.creditUnits.InexactFloat64(), // legacy credit projection
		TradeNo:                      referenceId,
		PaymentMethod:                model.PaymentMethodStripe,
		PaymentProvider:              model.PaymentProviderStripe,
		CreateTime:                   time.Now().Unix(),
		Status:                       common.TopUpStatusPending,
		QuotedWalletQuota:            creditedQuota,
		QuotedPaymentMinor:           quote.minor,
		QuotedPaymentCurrency:        quote.currency,
		QuotedPaymentEnvironment:     quote.environment,
		QuotedPaymentScope:           "primary",
		QuotedStripePromotionAllowed: quote.promotion,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), common.LogText("Stripe failed to create top-up order user_id=%d trade_no=%s amount=%d error=%q", id, referenceId, req.Amount, err.Error()))
		paymentError(c, common.NewMessage("Failed to create order"))
		return
	}
	payLink, err := genStripeLink(c.Request.Context(), referenceId, user.StripeCustomer, user.Email, quote, req.SuccessURL, req.CancelURL)
	if err != nil {
		logger.LogError(c.Request.Context(), common.LogText("Stripe failed to create Checkout Session user_id=%d trade_no=%s amount=%d error=%q", id, referenceId, req.Amount, err.Error()))
		paymentError(c, common.NewMessage("Failed to start payment"))
		return
	}
	logger.LogInfo(c.Request.Context(), common.LogText("Stripe top-up order created user_id=%d trade_no=%s amount=%d money=%s", id, referenceId, req.Amount, quote.money()))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"pay_link": payLink,
		},
	})
}

func RequestStripeAmount(c *gin.Context) {
	var req StripePayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		paymentError(c, common.NewMessage("Invalid parameters"))
		return
	}
	stripeAdaptor.RequestAmount(c, &req)
}

func RequestStripePay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	if !isStripeTopUpEnabled() {
		paymentError(c, common.NewMessage("Payment information has not been configured by administrator"))
		return
	}

	var req StripePayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		paymentError(c, common.NewMessage("Invalid parameters"))
		return
	}
	stripeAdaptor.RequestPay(c, &req)
}

func StripeWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	if !isStripeWebhookEnabled() {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.URL.Path, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}

	signature := c.GetHeader("Stripe-Signature")
	logger.LogInfo(ctx, fmt.Sprintf("Stripe webhook 收到请求 path=%q client_ip=%s signature_present=%t payload=%s", c.Request.URL.Path, c.ClientIP(), signature != "", paymentPayloadSummary(payload)))
	event, err := webhook.ConstructEventWithOptions(payload, signature, setting.StripeWebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})

	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 验签失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	callerIp := c.ClientIP()
	logger.LogInfo(ctx, fmt.Sprintf("Stripe webhook 验签成功 event_type=%s client_ip=%s path=%q", string(event.Type), callerIp, c.Request.URL.Path))
	var processingErr error
	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted:
		processingErr = sessionCompleted(ctx, event, callerIp)
	case stripe.EventTypeCheckoutSessionExpired:
		sessionExpired(ctx, event)
	case stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded:
		processingErr = sessionAsyncPaymentSucceeded(ctx, event, callerIp)
	case stripe.EventTypeCheckoutSessionAsyncPaymentFailed:
		sessionAsyncPaymentFailed(ctx, event, callerIp)
	default:
		logger.LogInfo(ctx, common.LogText("Stripe webhook ignored event event_type=%s client_ip=%s", string(event.Type), callerIp))
	}

	if processingErr != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}

func sessionCompleted(ctx context.Context, event stripe.Event, callerIp string) error {
	customerId := event.GetObjectValue("customer")
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if "complete" != status {
		logger.LogWarn(ctx, common.LogText("Stripe checkout.completed has unexpected status, ignored trade_no=%s status=%s client_ip=%s", referenceId, status, callerIp))
		return nil
	}

	paymentStatus := event.GetObjectValue("payment_status")
	if paymentStatus != "paid" {
		logger.LogInfo(ctx, common.LogText("Stripe Checkout payment not completed, waiting for async result trade_no=%s payment_status=%s client_ip=%s", referenceId, paymentStatus, callerIp))
		return nil
	}

	return fulfillOrder(ctx, event, referenceId, customerId, callerIp)
}

// sessionAsyncPaymentSucceeded handles delayed payment methods (bank transfer, SEPA, etc.)
// that confirm payment after the checkout session completes.
func sessionAsyncPaymentSucceeded(ctx context.Context, event stripe.Event, callerIp string) error {
	customerId := event.GetObjectValue("customer")
	referenceId := event.GetObjectValue("client_reference_id")
	logger.LogInfo(ctx, common.LogText("Stripe async payment succeeded trade_no=%s client_ip=%s", referenceId, callerIp))

	return fulfillOrder(ctx, event, referenceId, customerId, callerIp)
}

// sessionAsyncPaymentFailed marks orders as failed when delayed payment methods
// ultimately fail (e.g. bank transfer not received, SEPA rejected).
func sessionAsyncPaymentFailed(ctx context.Context, event stripe.Event, callerIp string) {
	referenceId := event.GetObjectValue("client_reference_id")
	logger.LogWarn(ctx, common.LogText("Stripe async payment failed trade_no=%s client_ip=%s", referenceId, callerIp))

	if len(referenceId) == 0 {
		logger.LogWarn(ctx, common.LogText("Stripe async payment failed event missing order number client_ip=%s", callerIp))
		return
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)

	topUp := model.GetTopUpByTradeNo(referenceId)
	if topUp == nil {
		logger.LogWarn(ctx, common.LogText("Stripe async payment failed but local order not found trade_no=%s client_ip=%s", referenceId, callerIp))
		return
	}

	if topUp.PaymentProvider != model.PaymentProviderStripe {
		logger.LogWarn(ctx, common.LogText("Stripe async payment failed but order payment gateway mismatch trade_no=%s payment_provider=%s client_ip=%s", referenceId, topUp.PaymentProvider, callerIp))
		return
	}

	if topUp.Status != common.TopUpStatusPending {
		logger.LogInfo(ctx, common.LogText("Stripe async payment failed but order not pending, ignored trade_no=%s status=%s client_ip=%s", referenceId, topUp.Status, callerIp))
		return
	}

	if err := model.UpdatePendingTopUpStatus(referenceId, model.PaymentProviderStripe, common.TopUpStatusFailed); err != nil {
		if errors.Is(err, model.ErrTopUpStatusInvalid) {
			logger.LogInfo(ctx, common.LogText("Stripe async payment failed but order is no longer pending, ignored trade_no=%s client_ip=%s", referenceId, callerIp))
			return
		}
		logger.LogError(ctx, common.LogText("Stripe failed to mark top-up order as failed trade_no=%s client_ip=%s error=%q", referenceId, callerIp, err.Error()))
		return
	}
	logger.LogInfo(ctx, common.LogText("Stripe top-up order marked as failed trade_no=%s client_ip=%s", referenceId, callerIp))
}

// fulfillOrder is the shared logic for crediting quota after payment is confirmed.
func fulfillOrder(ctx context.Context, event stripe.Event, referenceId string, customerId string, callerIp string) error {
	if len(referenceId) == 0 {
		logger.LogWarn(ctx, common.LogText("Stripe order completion missing order number client_ip=%s", callerIp))
		return nil
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	payload := map[string]any{
		"customer":     customerId,
		"amount_total": event.GetObjectValue("amount_total"),
		"currency":     strings.ToUpper(event.GetObjectValue("currency")),
		"event_type":   string(event.Type),
	}
	if err := model.CompleteSubscriptionOrder(referenceId, common.GetJsonString(payload), model.PaymentProviderStripe, ""); err == nil {
		logger.LogInfo(ctx, common.LogText("Stripe subscription order processed trade_no=%s event_type=%s client_ip=%s", referenceId, string(event.Type), callerIp))
		return nil
	} else if err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		logger.LogError(ctx, common.LogText("Stripe subscription order processing failed trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, err.Error()))
		return err
	}

	environment := "test"
	if event.Livemode {
		environment = "live"
	}
	merchantScope := event.Account
	if merchantScope == "" {
		merchantScope = "primary"
	}
	var paymentFacts []model.VerifiedPaymentFacts
	if fact, ok := model.NewVerifiedPaymentFacts(model.PaymentProviderStripe, event.GetObjectValue("payment_intent"), merchantScope, environment, event.GetObjectValue("currency"), event.GetObjectValue("amount_total"), true); ok {
		var proof stripe.CheckoutSession
		if event.Data != nil && common.Unmarshal(event.Data.Raw, &proof) == nil && proof.TotalDetails != nil {
			fact.CheckoutSubtotalMinor = proof.AmountSubtotal
			fact.CheckoutDiscountMinor = proof.TotalDetails.AmountDiscount
		}
		paymentFacts = append(paymentFacts, fact)
	}
	err := model.Recharge(referenceId, customerId, callerIp, paymentFacts...)
	if err != nil {
		logger.LogError(ctx, common.LogText("Stripe top-up processing failed trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, err.Error()))
		return err
	}

	total, _ := strconv.ParseFloat(event.GetObjectValue("amount_total"), 64)
	currency := strings.ToUpper(event.GetObjectValue("currency"))
	logger.LogInfo(ctx, common.LogText("Stripe top-up succeeded trade_no=%s amount_total=%.2f currency=%s event_type=%s client_ip=%s", referenceId, total/100, currency, string(event.Type), callerIp))
	return nil
}

func sessionExpired(ctx context.Context, event stripe.Event) {
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if "expired" != status {
		logger.LogWarn(ctx, common.LogText("Stripe checkout.expired has unexpected status, ignored trade_no=%s status=%s", referenceId, status))
		return
	}

	if len(referenceId) == 0 {
		logger.LogWarn(ctx, common.LogText("Stripe checkout.expired missing order number"))
		return
	}

	// Subscription order expiration
	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	if err := model.ExpireSubscriptionOrder(referenceId, model.PaymentProviderStripe); err == nil {
		logger.LogInfo(ctx, common.LogText("Stripe subscription order expired trade_no=%s", referenceId))
		return
	} else if err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		logger.LogError(ctx, common.LogText("Stripe subscription order expiry processing failed trade_no=%s error=%q", referenceId, err.Error()))
		return
	}

	err := model.UpdatePendingTopUpStatus(referenceId, model.PaymentProviderStripe, common.TopUpStatusExpired)
	if errors.Is(err, model.ErrTopUpNotFound) {
		logger.LogWarn(ctx, common.LogText("Stripe top-up order not found, cannot mark as expired trade_no=%s", referenceId))
		return
	}
	if err != nil {
		logger.LogError(ctx, common.LogText("Stripe top-up order expiry processing failed trade_no=%s error=%q", referenceId, err.Error()))
		return
	}

	logger.LogInfo(ctx, common.LogText("Stripe top-up order expired trade_no=%s", referenceId))
}

// genStripeLink generates a Stripe Checkout session URL for payment.
// It creates a new checkout session with the specified parameters and returns the payment URL.
//
// Parameters:
//   - referenceId: unique reference identifier for the transaction
//   - customerId: existing Stripe customer ID (empty string if new customer)
//   - email: customer email address for new customer creation
//   - amount: quantity of units to purchase
//   - successURL: custom URL to redirect after successful payment (empty for default)
//   - cancelURL: custom URL to redirect when payment is canceled (empty for default)
//
// Returns the checkout session URL or an error if the session creation fails.
func genStripeLink(ctx context.Context, referenceId string, customerId string, email string, quote stripeCheckoutQuote, successURL string, cancelURL string) (string, error) {
	// Use custom URLs if provided, otherwise use defaults
	if successURL == "" {
		successURL = paymentReturnPath("/usage-logs")
	}
	if cancelURL == "" {
		cancelURL = paymentReturnPath("/wallet")
	}

	params := &stripe.CheckoutSessionCreateParams{
		ClientReferenceID: stripe.String(referenceId),
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(cancelURL),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
					Product: stripe.String(quote.product), Currency: stripe.String(strings.ToLower(quote.currency)), UnitAmount: stripe.Int64(quote.minor),
				},
				Quantity: stripe.Int64(1),
			},
		},
		Mode:                stripe.String(string(stripe.CheckoutSessionModePayment)),
		AllowPromotionCodes: stripe.Bool(quote.promotion),
		AdaptivePricing:     &stripe.CheckoutSessionCreateAdaptivePricingParams{Enabled: stripe.Bool(false)},
	}

	if "" == customerId {
		if "" != email {
			params.CustomerEmail = stripe.String(email)
		}

		params.CustomerCreation = stripe.String(string(stripe.CheckoutSessionCustomerCreationAlways))
	} else {
		params.Customer = stripe.String(customerId)
	}

	return createStripeCheckoutWithClient(ctx, quote.client, params)
}

// Changing the Go SDK must not silently change the merchant-facing Stripe API
// version. Top-ups and subscriptions both used this version with stripe-go v81.
const stripeCheckoutAPIVersion = "2025-02-24.acacia"

func createStripeCheckout(ctx context.Context, params *stripe.CheckoutSessionCreateParams) (string, error) {
	return createStripeCheckoutWithClient(ctx, stripe.NewClient(setting.StripeApiSecret), params)
}

func createStripeCheckoutWithClient(ctx context.Context, client *stripe.Client, params *stripe.CheckoutSessionCreateParams) (string, error) {
	params.Headers = http.Header{"Stripe-Version": {stripeCheckoutAPIVersion}}
	params.SetIdempotencyKey(*params.ClientReferenceID)
	result, err := client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return "", err
	}

	return result.URL, nil
}

func stripeTopupUnits(amount int64) decimal.Decimal {
	units := decimal.NewFromInt(amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		unitQuota := decimal.NewFromFloat(common.QuotaPerUnit)
		if !unitQuota.IsPositive() {
			return decimal.Zero
		}
		units = units.Div(unitQuota)
	}
	return units
}

type stripeTopupTerms struct {
	creditUnits, creditedQuota, cashMajor decimal.Decimal
}

func stripeTerms(amount int64, group string) stripeTopupTerms {
	units := stripeTopupUnits(amount)
	groupRatio := common.GetTopupGroupRatio(group)
	if groupRatio == 0 {
		groupRatio = 1
	}
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]; ok && ds > 0 {
		discount = ds
	}
	withGroup := units.Mul(decimal.NewFromFloat(groupRatio))
	return stripeTopupTerms{
		creditUnits:   withGroup,
		creditedQuota: withGroup.Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
		cashMajor:     withGroup.Mul(decimal.NewFromFloat(setting.StripeUnitPrice)).Mul(decimal.NewFromFloat(discount)),
	}
}

type stripeCheckoutQuote struct {
	client                         *stripe.Client
	product, currency, environment string
	exponent                       int
	minor                          int64
	promotion                      bool
}

func (q stripeCheckoutQuote) money() string {
	return decimal.NewFromInt(q.minor).Shift(-int32(q.exponent)).StringFixed(int32(q.exponent))
}

func quoteStripeCheckout(ctx context.Context, terms stripeTopupTerms) (stripeCheckoutQuote, error) {
	secret, priceID, promo := setting.StripeApiSecret, setting.StripePriceId, setting.StripePromotionCodesEnabled
	if (!strings.HasPrefix(secret, "sk_") && !strings.HasPrefix(secret, "rk_")) || priceID == "" || !terms.cashMajor.IsPositive() {
		return stripeCheckoutQuote{}, common.NewMessage("Payment information has not been configured by administrator")
	}
	client := stripe.NewClient(secret)
	params := &stripe.PriceRetrieveParams{}
	params.Headers = http.Header{"Stripe-Version": {stripeCheckoutAPIVersion}}
	// Preserve the configured merchant product and settlement currency, but
	// do not let its fixed unit price override the displayed discount/ratio.
	price, err := client.V1Prices.Retrieve(ctx, priceID, params)
	if err != nil || price == nil || !price.Active || price.Product == nil || price.Product.ID == "" {
		return stripeCheckoutQuote{}, common.NewMessage("Payment information has not been configured by administrator")
	}
	currency := strings.ToUpper(string(price.Currency))
	exponent, supported := model.PaymentCurrencyExponent(currency)
	if !supported {
		return stripeCheckoutQuote{}, common.NewMessage("Payment information has not been configured by administrator")
	}
	amount := terms.cashMajor.Shift(int32(exponent)).Round(0)
	if !amount.IsPositive() || amount.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return stripeCheckoutQuote{}, common.NewMessage("Invalid top-up amount")
	}
	environment := "test"
	if price.Livemode {
		environment = "live"
	}
	return stripeCheckoutQuote{client: client, product: price.Product.ID, currency: currency, environment: environment, exponent: exponent, minor: amount.IntPart(), promotion: promo}, nil
}

func getStripeMinTopup() int64 {
	minTopup := setting.StripeMinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		minTopup = minTopup * int(common.QuotaPerUnit)
	}
	return int64(minTopup)
}
