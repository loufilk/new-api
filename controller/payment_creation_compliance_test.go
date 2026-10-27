package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaymentCreationEndpointsRequireCompliance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	paymentSetting := operation_setting.GetPaymentSetting()
	originalConfirmed := paymentSetting.ComplianceConfirmed
	originalTermsVersion := paymentSetting.ComplianceTermsVersion
	t.Cleanup(func() {
		paymentSetting.ComplianceConfirmed = originalConfirmed
		paymentSetting.ComplianceTermsVersion = originalTermsVersion
	})
	paymentSetting.ComplianceConfirmed = false
	paymentSetting.ComplianceTermsVersion = ""

	testCases := []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{name: "epay", handler: RequestEpay},
		{name: "stripe", handler: RequestStripePay},
		{name: "creem", handler: RequestCreemPay},
		{name: "waffo", handler: RequestWaffoPay},
		{name: "waffo pancake", handler: RequestWaffoPancakePay},
		{name: "waffo pancake subscription", handler: SubscriptionRequestWaffoPancakePay},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/pay", strings.NewReader(`{"amount":100}`))
			ctx.Request.Header.Set("Content-Type", "application/json")

			testCase.handler(ctx)

			require.Equal(t, http.StatusOK, response.Code)
			var payload struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			assert.False(t, payload.Success)
			assert.NotEmpty(t, payload.Message)
		})
	}
}
