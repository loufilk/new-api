package controller

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v87"
)

// Assert the wire contract, not the SDK's default version or its parameter structs.
// The injected transport never opens a network connection.
func TestStripeCheckoutMerchantContract(t *testing.T) {
	oldAPI, oldPrice, oldPromo := setting.StripeApiSecret, setting.StripePriceId, setting.StripePromotionCodesEnabled
	oldBackend := stripe.GetBackend(stripe.APIBackend)
	payment := operation_setting.GetPaymentSetting()
	oldDiscounts, oldUnit := payment.AmountDiscount, setting.StripeUnitPrice
	oldDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	payment.AmountDiscount = map[int]float64{7: 0.8}
	setting.StripeUnitPrice = 1.0
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripePriceId, setting.StripePromotionCodesEnabled = oldAPI, oldPrice, oldPromo
		stripe.SetBackend(stripe.APIBackend, oldBackend)
		payment.AmountDiscount, setting.StripeUnitPrice = oldDiscounts, oldUnit
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay
	})
	setting.StripeApiSecret, setting.StripePriceId, setting.StripePromotionCodesEnabled = "sk_test_local", "price_topup", true

	for _, tc := range []struct{ mode, customer, price, quantity, createCustomer string }{
		{"payment", "cus_mock", "", "1", ""},
		{"payment", "", "", "1", "always"},
		{"subscription", "cus_mock", "price_plan", "1", ""},
		{"subscription", "", "price_plan", "1", ""},
	} {
		t.Run(tc.mode+"/"+tc.customer, func(t *testing.T) {
			const ref = "local-checkout-order"
			tr := stalePancakeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					assert.Equal(t, "/v1/prices/price_topup", r.URL.Path)
					assert.Equal(t, []string{"2025-02-24.acacia"}, r.Header.Values("Stripe-Version"))
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"price_topup","object":"price","currency":"usd","active":true,"product":"prod_topup","unit_amount":100}`))}, nil
				}
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/v1/checkout/sessions", r.URL.Path)
				assert.Equal(t, []string{"2025-02-24.acacia"}, r.Header.Values("Stripe-Version"))
				assert.Equal(t, ref, r.Header.Get("Idempotency-Key"))
				require.NoError(t, r.ParseForm())
				form := r.PostForm
				assert.Equal(t, ref, form.Get("client_reference_id"))
				assert.Equal(t, tc.mode, form.Get("mode"))
				assert.Equal(t, tc.price, form.Get("line_items[0][price]"))
				assert.Equal(t, tc.quantity, form.Get("line_items[0][quantity]"))
				assert.Equal(t, tc.customer, form.Get("customer"))
				assert.Equal(t, tc.createCustomer, form.Get("customer_creation"))
				if tc.customer == "" {
					assert.Equal(t, "buyer@example.invalid", form.Get("customer_email"))
				} else {
					assert.False(t, form.Has("customer_email"))
				}
				if tc.mode == "payment" {
					assert.Equal(t, "https://example.invalid/success", form.Get("success_url"))
					assert.Equal(t, "https://example.invalid/cancel", form.Get("cancel_url"))
					assert.Equal(t, "true", form.Get("allow_promotion_codes"))
					assert.Equal(t, "false", form.Get("adaptive_pricing[enabled]"))
					assert.Equal(t, "prod_topup", form.Get("line_items[0][price_data][product]"))
					assert.Equal(t, "usd", form.Get("line_items[0][price_data][currency]"))
					assert.Equal(t, "560", form.Get("line_items[0][price_data][unit_amount]"))
				} else {
					assert.Equal(t, paymentReturnPath("/wallet"), form.Get("success_url"))
					assert.Equal(t, paymentReturnPath("/wallet"), form.Get("cancel_url"))
					assert.False(t, form.Has("allow_promotion_codes"))
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"cs_mock","object":"checkout.session","url":"https://checkout.invalid/local"}`))}, nil
			})
			stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{HTTPClient: &http.Client{Transport: tr}, MaxNetworkRetries: stripe.Int64(0)}))
			var link string
			var err error
			if tc.mode == "payment" {
				terms := stripeTerms(7, "")
				assert.True(t, terms.cashMajor.Equal(decimal.RequireFromString("5.6")))
				quote, quoteErr := quoteStripeCheckout(t.Context(), terms)
				require.NoError(t, quoteErr)
				assert.Equal(t, "5.60", quote.money())
				link, err = genStripeLink(t.Context(), ref, tc.customer, "buyer@example.invalid", quote, "https://example.invalid/success", "https://example.invalid/cancel")
			} else {
				link, err = genStripeSubscriptionLink(t.Context(), ref, tc.customer, "buyer@example.invalid", "price_plan")
			}
			require.NoError(t, err)
			assert.Equal(t, "https://checkout.invalid/local", link)
		})
	}
}
