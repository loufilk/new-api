package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaymentCallbackLogsRedactUntrustedData(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	savedStripeAPI, savedStripePrice, savedStripeSecret := setting.StripeApiSecret, setting.StripePriceId, setting.StripeWebhookSecret
	savedCreemAPI, savedCreemProducts, savedCreemSecret := setting.CreemApiKey, setting.CreemProducts, setting.CreemWebhookSecret
	savedAddress, savedID, savedKey := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey
	savedMode := gin.Mode()
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripePriceId, setting.StripeWebhookSecret = savedStripeAPI, savedStripePrice, savedStripeSecret
		setting.CreemApiKey, setting.CreemProducts, setting.CreemWebhookSecret = savedCreemAPI, savedCreemProducts, savedCreemSecret
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = savedAddress, savedID, savedKey
		gin.SetMode(savedMode)
	})
	gin.SetMode(gin.TestMode)
	setting.StripeApiSecret, setting.StripePriceId, setting.StripeWebhookSecret = "test-api", "test-price", "test-secret"
	setting.CreemApiKey, setting.CreemProducts, setting.CreemWebhookSecret = "test-api", "[{\"productId\":\"test\"}]", "test-secret"
	operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "https://payment.invalid", "1", "test-secret"
	const payload = "{\"email\":\"private-customer@example.invalid\",\"value\":\"private-body-marker\"}"
	for _, tc := range []struct {
		path, method, signatureHeader, body string
		handler                             gin.HandlerFunc
		status                              int
	}{
		{"/api/stripe/webhook", "POST", "Stripe-Signature", payload, StripeWebhook, http.StatusBadRequest},
		{"/api/creem/webhook", "POST", "creem-signature", payload, CreemWebhook, http.StatusUnauthorized},
		{"/api/user/epay/notify", "GET", "", "", EpayNotify, http.StatusOK},
		{"/api/user/epay/notify", "POST", "", "sign=private-signature-marker&name=private-body-marker&money=1.00", EpayNotify, http.StatusOK},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			var output bytes.Buffer
			common.LogWriterMu.Lock()
			oldOut, oldErr := gin.DefaultWriter, gin.DefaultErrorWriter
			gin.DefaultWriter, gin.DefaultErrorWriter = &output, &output
			common.LogWriterMu.Unlock()
			t.Cleanup(func() {
				common.LogWriterMu.Lock()
				gin.DefaultWriter, gin.DefaultErrorWriter = oldOut, oldErr
				common.LogWriterMu.Unlock()
			})
			engine := gin.New()
			middleware.SetUpLogger(engine)
			engine.Handle(tc.method, tc.path, tc.handler)
			request := httptest.NewRequest(tc.method, tc.path+"?sign=private-query-marker", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			if strings.Contains(tc.path, "epay") {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if tc.signatureHeader != "" {
				request.Header.Set(tc.signatureHeader, "private-signature-marker")
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code)
			assert.Contains(t, output.String(), tc.path)
			assert.Contains(t, output.String(), "signature_present=true")
			for _, sensitive := range []string{"private-customer", "private-body-marker", "private-signature-marker", "private-query-marker"} {
				assert.NotContains(t, output.String(), sensitive)
			}
		})
	}
}
