package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newUserBenefitTestRequest(handler gin.HandlerFunc, userID int, body, requestID string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	method, path := http.MethodGet, "/api/user/new-user-benefit"
	if body != "" {
		method, path = http.MethodPost, path+"/pay"
	}
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		c.Request.Header.Set("Idempotency-Key", requestID)
	}
	c.Set("id", userID)
	handler(c)
	return response
}

func configureNewUserBenefitEpayTest(t *testing.T) {
	t.Helper()
	confirmPaymentComplianceForTest(t)
	oldAddress, oldID, oldKey, oldMethods := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods
	oldServer, oldCallback := system_setting.ServerAddress, operation_setting.CustomCallbackAddress
	t.Cleanup(func() {
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods = oldAddress, oldID, oldKey, oldMethods
		system_setting.ServerAddress, operation_setting.CustomCallbackAddress = oldServer, oldCallback
	})
	operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "https://pay.example.test", "new-user-test-merchant", "new-user-synthetic-key"
	operation_setting.PayMethods = []map[string]string{{"type": "alipay", "name": "支付宝"}, {"type": "wxpay", "name": "微信"}, {"type": "stripe", "name": "Stripe"}}
	system_setting.ServerAddress, operation_setting.CustomCallbackAddress = "https://console.example.test", ""
}

func TestNewUserBenefitRejectsInvalidIntentBeforeDatabaseAccess(t *testing.T) {
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previousDB })
	validID := uuid.NewString()
	for _, test := range []struct {
		name, body, id string
	}{
		{"missing key", `{"payment_method":"alipay","config_version":1}`, ""},
		{"invalid key", `{"payment_method":"alipay","config_version":1}`, "request-1"},
		{"nil UUID", `{"payment_method":"alipay","config_version":1}`, uuid.Nil.String()},
		{"noncanonical UUID", `{"payment_method":"alipay","config_version":1}`, "urn:uuid:" + validID},
		{"missing version", `{"payment_method":"alipay"}`, validID},
		{"null version", `{"payment_method":"alipay","config_version":null}`, validID},
		{"negative version", `{"payment_method":"alipay","config_version":-1}`, validID},
		{"fractional version", `{"payment_method":"alipay","config_version":0.1}`, validID},
		{"missing method", `{"config_version":1}`, validID},
		{"client amount", `{"payment_method":"alipay","config_version":1,"amount":0.1}`, validID},
		{"client credit", `{"payment_method":"alipay","config_version":1,"credit_amount_cny":"9.00"}`, validID},
		{"client quota", `{"payment_method":"alipay","config_version":1,"paid_quota":500000}`, validID},
		{"client identity", `{"payment_method":"alipay","config_version":1,"user_id":7}`, validID},
		{"trailing object", `{"payment_method":"alipay","config_version":1}{}`, validID},
		{"oversize", `{"payment_method":"` + strings.Repeat("a", 5000) + `","config_version":1}`, validID},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := newUserBenefitTestRequest(RequestNewUserBenefitPay, 42, test.body, test.id)
			assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"success":false`)
		})
	}
	for _, handler := range []gin.HandlerFunc{GetNewUserBenefit, RequestNewUserBenefitPay} {
		response := newUserBenefitTestRequest(handler, 0, `{"payment_method":"alipay","config_version":1}`, validID)
		assert.Equal(t, http.StatusUnauthorized, response.Code)
	}
}

func TestNewUserBenefitFiltersNonEpayPaymentMethods(t *testing.T) {
	oldMethods := operation_setting.PayMethods
	t.Cleanup(func() { operation_setting.PayMethods = oldMethods })
	operation_setting.PayMethods = []map[string]string{
		{"type": "alipay", "name": "支付宝", "icon": "SiAlipay", "secret": "must-not-return"},
		{"type": "alipay", "name": "duplicate"},
		{"type": "wxpay"}, {"type": ""}, {"type": " alipay "}, {"type": "invalid&type"},
		{"type": "stripe"}, {"type": "STRIPE"}, {"type": "creem"}, {"type": "waffo"}, {"type": "waffo_pancake"}, {"type": "balance"},
	}
	assert.Equal(t, []newUserBenefitPayMethod{{Type: "alipay", Name: "支付宝", Icon: "SiAlipay"}, {Type: "wxpay", Name: "wxpay"}}, newUserBenefitPayMethods(operation_setting.PayMethods))
}

func TestNewUserBenefitPaymentSnapshotKeepsOneConfiguration(t *testing.T) {
	configureNewUserBenefitEpayTest(t)
	snapshot := snapshotNewUserBenefitPayment()
	require.NotNil(t, snapshot)
	common.OptionMapRWMutex.Lock()
	operation_setting.PayAddress = "https://new-gateway.example.test"
	operation_setting.EpayId, operation_setting.EpayKey = "new-merchant", "new-synthetic-key"
	operation_setting.PayMethods[0]["type"] = "custom1"
	operation_setting.PayMethods[0]["name"] = "New method"
	system_setting.ServerAddress, operation_setting.CustomCallbackAddress = "https://new-console.example.test", "https://new-callback.example.test"
	common.OptionMapRWMutex.Unlock()
	assert.Equal(t, []newUserBenefitPayMethod{{Type: "alipay", Name: "支付宝"}, {Type: "wxpay", Name: "微信"}}, snapshot.Methods)
	uri, params, err := snapshot.Client.Purchase(&epay.PurchaseArgs{Type: "alipay", ServiceTradeNo: "SNAPSHOT1", Name: "Benefit", Money: "0.10", Device: epay.PC, NotifyUrl: snapshot.NotifyURL, ReturnUrl: snapshot.ReturnURL})
	require.NoError(t, err)
	assert.Equal(t, "https://pay.example.test/submit.php", uri)
	assert.Equal(t, "new-user-test-merchant", params["pid"])
	assert.Equal(t, "https://console.example.test/api/user/epay/notify", params["notify_url"])
	assert.Equal(t, "https://console.example.test/wallet?show_history=true", params["return_url"])
	verified, err := snapshot.Client.Verify(params)
	require.NoError(t, err)
	assert.True(t, verified.VerifyStatus)
	current := snapshotNewUserBenefitPayment()
	require.NotNil(t, current)
	verified, err = current.Client.Verify(params)
	require.NoError(t, err)
	assert.False(t, verified.VerifyStatus, "an already captured checkout must keep the original key")
	assert.Equal(t, "new-merchant", current.Client.Config.PartnerID)
	assert.Equal(t, "custom1", current.Methods[0].Type)
}

func TestNewUserBenefitPaymentGatePrecedesReservation(t *testing.T) {
	configureNewUserBenefitEpayTest(t)
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previousDB })
	request := func(method string) *httptest.ResponseRecorder {
		return newUserBenefitTestRequest(RequestNewUserBenefitPay, 42, `{"payment_method":"`+method+`","config_version":0}`, uuid.NewString())
	}
	for _, method := range []string{"stripe", "creem", "waffo", "waffo_pancake", "balance", "unknown", " alipay "} {
		response := request(method)
		assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), "new_user_benefit_payment_method_unavailable")
	}
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false
	assert.Equal(t, http.StatusServiceUnavailable, request("alipay").Code)
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.EpayKey = ""
	assert.Equal(t, http.StatusServiceUnavailable, request("alipay").Code)
	operation_setting.EpayKey = "new-user-synthetic-key"
	operation_setting.PayAddress = "javascript:alert(1)"
	assert.Equal(t, http.StatusServiceUnavailable, request("alipay").Code)
	operation_setting.PayAddress = "https://pay.example.test"
	system_setting.ServerAddress = "/wallet"
	assert.Equal(t, http.StatusServiceUnavailable, request("alipay").Code)
}
