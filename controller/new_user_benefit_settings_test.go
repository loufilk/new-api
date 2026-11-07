package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newUserBenefitSettingsJSON(t *testing.T, fields map[string]any) string {
	t.Helper()
	payload := map[string]any{
		"enabled": true, "pay_amount_cny": "0.10", "credit_amount_cny": "1.00", "version": 0,
		"max_total_orders": 1000, "max_orders_per_24h": 100, "max_orders_per_network_24h": 3,
		"total_bonus_budget_cny": "900.00",
	}
	for key, value := range fields {
		payload[key] = value
	}
	encoded, err := common.Marshal(payload)
	require.NoError(t, err)
	return string(encoded)
}

func requestNewUserBenefitSettings(t *testing.T, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 7)
	c.Request = httptest.NewRequest(method, "/api/option/new-user-benefit", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if method == http.MethodGet {
		GetNewUserBenefitSettings(c)
	} else {
		UpdateNewUserBenefitSettings(c)
	}
	return recorder
}

func TestNewUserBenefitSettingsRejectInvalidPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fields := range []map[string]any{
		{"enabled": nil}, {"pay_amount_cny": 0.1}, {"pay_amount_cny": "0.001"},
		{"pay_amount_cny": "2.00"}, {"version": -1}, {"version": 9_007_199_254_740_991},
		{"paid_quota": 1000000}, {"max_total_orders": nil}, {"max_total_orders": 0},
		{"max_total_orders": 1_000_001}, {"max_orders_per_24h": -1}, {"max_orders_per_24h": 0.5},
		{"max_orders_per_network_24h": 10_001}, {"max_orders_per_network_24h": "3"},
		{"total_bonus_budget_cny": "0"}, {"total_bonus_budget_cny": "1000000.01"},
		{"reserved_orders": 0}, {"reserved_bonus_cny": "0.00"}, {"reserved_orders_24h": 0}, {"turnstile_ready": true},
	} {
		body := newUserBenefitSettingsJSON(t, fields)
		recorder := requestNewUserBenefitSettings(t, http.MethodPut, body)
		assert.Equal(t, http.StatusBadRequest, recorder.Code, body)
		assert.Contains(t, recorder.Body.String(), `"code":"new_user_benefit_invalid_config"`)
	}
	for _, body := range []string{`{}`, `{"enabled":true,"pay_amount_cny":"0.10","credit_amount_cny":"1.00","version":0}`} {
		assert.Equal(t, http.StatusBadRequest, requestNewUserBenefitSettings(t, http.MethodPut, body).Code)
	}
}

func TestNewUserBenefitSettingsPostgresSaveReadAndStaleForm(t *testing.T) {
	db := setupNewUserBenefitControllerPostgres(t)
	get := requestNewUserBenefitSettings(t, http.MethodGet, "")
	require.Equal(t, http.StatusOK, get.Code)
	var response struct {
		Success bool                           `json:"success"`
		Data    newUserBenefitSettingsResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(get.Body.Bytes(), &response))
	assert.Equal(t, newUserBenefitSettingsResponse{PayAmountCNY: "0.10", CreditAmountCNY: "1.00", MaxTotalOrders: 1000, MaxOrdersPer24h: 100,
		MaxOrdersPerNetwork24h: 3, TotalBonusBudgetCNY: "900.00", ReservedBonusCNY: "0.00", TurnstileReady: true}, response.Data)

	put := requestNewUserBenefitSettings(t, http.MethodPut, newUserBenefitSettingsJSON(t, map[string]any{
		"pay_amount_cny": "0.2", "credit_amount_cny": "2", "max_total_orders": 25, "max_orders_per_24h": 10,
		"max_orders_per_network_24h": 2, "total_bonus_budget_cny": "45",
	}))
	require.Equal(t, http.StatusOK, put.Code)
	require.NoError(t, common.Unmarshal(put.Body.Bytes(), &response))
	require.True(t, response.Success)
	expected := newUserBenefitSettingsResponse{Enabled: true, PayAmountCNY: "0.20", CreditAmountCNY: "2.00", Version: 1,
		MaxTotalOrders: 25, MaxOrdersPer24h: 10, MaxOrdersPerNetwork24h: 2, TotalBonusBudgetCNY: "45.00", ReservedBonusCNY: "0.00", TurnstileReady: true}
	assert.Equal(t, expected, response.Data)
	stale := requestNewUserBenefitSettings(t, http.MethodPut, newUserBenefitSettingsJSON(t, map[string]any{"enabled": false}))
	assert.Equal(t, http.StatusConflict, stale.Code)
	assert.Contains(t, stale.Body.String(), `"code":"new_user_benefit_config_changed"`)
	get = requestNewUserBenefitSettings(t, http.MethodGet, "")
	require.NoError(t, common.Unmarshal(get.Body.Bytes(), &response))
	assert.Equal(t, expected, response.Data)

	user := model.User{Username: "benefit-usage-user", Status: common.UserStatusEnabled, Email: "benefit-usage@example.invalid"}
	model.MarkNewUserEmailVerified(&user)
	require.NoError(t, db.Create(&user).Error)
	order, err := model.ReserveNewUserBenefit(t.Context(), user.Id, 1, "alipay", "new-user-test-merchant", "SETTINGSUSAGE", model.NewUserBenefitRiskContext{ClientIP: "203.0.113.10", HumanVerified: true})
	require.NoError(t, err)
	assert.Equal(t, model.NewUserBenefitStatusPending, order.Status)
	get = requestNewUserBenefitSettings(t, http.MethodGet, "")
	require.Equal(t, http.StatusOK, get.Code)
	require.NoError(t, common.Unmarshal(get.Body.Bytes(), &response))
	expected.ReservedOrders, expected.ReservedBonusCNY, expected.ReservedOrders24h = 1, "1.80", 1
	assert.Equal(t, expected, response.Data, "pending payment promises must already consume the displayed budget")
	assert.NotContains(t, get.Body.String(), "email_identity_hash")
	assert.NotContains(t, get.Body.String(), "network_hash")
}

func TestNewUserBenefitSettingsRequireHumanVerificationConfiguration(t *testing.T) {
	configureNewUserBenefitEpayTest(t)
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previousDB })
	for _, incomplete := range []string{"disabled", "site_key", "secret_key", "hostname"} {
		t.Run(incomplete, func(t *testing.T) {
			common.TurnstileCheckEnabled, common.TurnstileSiteKey, common.TurnstileSecretKey = true, "public-site-key", "synthetic-secret"
			system_setting.ServerAddress = "https://console.example.test"
			switch incomplete {
			case "disabled":
				common.TurnstileCheckEnabled = false
			case "site_key":
				common.TurnstileSiteKey = " "
			case "secret_key":
				common.TurnstileSecretKey = " "
			case "hostname":
				system_setting.ServerAddress = ""
			}
			response := requestNewUserBenefitSettings(t, http.MethodPut, newUserBenefitSettingsJSON(t, nil))
			assert.Equal(t, http.StatusConflict, response.Code)
			assert.Contains(t, response.Body.String(), `"code":"new_user_benefit_verification_unavailable"`)
			assert.NotContains(t, response.Body.String(), "synthetic-secret")
		})
	}
}

func TestNewUserBenefitSettingsCanDisableWithoutHumanVerificationPostgres(t *testing.T) {
	setupNewUserBenefitControllerPostgres(t)
	_, err := model.UpdateNewUserBenefitConfig(t.Context(), 0, 7, newUserBenefitControllerConfig())
	require.NoError(t, err)
	common.TurnstileCheckEnabled, common.TurnstileSiteKey, common.TurnstileSecretKey = false, "", ""
	response := requestNewUserBenefitSettings(t, http.MethodPut, newUserBenefitSettingsJSON(t, map[string]any{"enabled": false, "version": 1}))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	config, err := model.GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.False(t, config.Enabled)
	assert.EqualValues(t, 2, config.Version)
	assert.Contains(t, response.Body.String(), `"turnstile_ready":false`)
}
