package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUserBenefitHTTPPaymentIdentityRecoveryPostgres(t *testing.T) {
	db := setupNewUserBenefitControllerPostgres(t)
	buyer, queryCount := "", 0
	queryReferences := map[string]string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryCount++
		assert.Equal(t, "/api.php", r.URL.Path)
		assert.Equal(t, "order", r.URL.Query().Get("act"))
		assert.Equal(t, "new-user-test-merchant", r.URL.Query().Get("pid"))
		assert.Equal(t, "new-user-synthetic-key", r.URL.Query().Get("key"))
		orderID := r.URL.Query().Get("out_trade_no")
		data, err := common.Marshal(map[string]any{"code": 1, "status": 1, "pid": "new-user-test-merchant", "out_trade_no": orderID,
			"trade_no": queryReferences[orderID], "type": "alipay", "money": "0.10", "buyer": buyer})
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write(data)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	client := service.GetHttpClient()
	oldTransport := client.Transport
	client.Transport = server.Client().Transport
	t.Cleanup(func() { client.Transport = oldTransport })
	operation_setting.PayAddress = server.URL
	next := newUserBenefitControllerConfig()
	next.PaymentIdentityCheckEnabled = true
	config, err := model.UpdateNewUserBenefitConfig(t.Context(), 0, 7, next)
	require.NoError(t, err)
	first := createNewUserBenefitControllerUser(t, db, "review-first", true)
	second := createNewUserBenefitControllerUser(t, db, "review-second", true)
	state := newUserBenefitStateForTest(t, first.Id)
	assert.True(t, state.PaymentIdentityCheckEnabled)
	assert.Equal(t, []newUserBenefitPayMethod{{Type: "alipay", Name: "支付宝"}}, state.PayMethods)
	body := fmt.Sprintf(`{"payment_method":"alipay","config_version":%d}`, config.Version)
	checkout := newUserBenefitCheckoutResponseForTest(t, newUserBenefitTestRequest(RequestNewUserBenefitPay, first.Id, body, uuid.NewString()))
	queryReferences[checkout.OrderID] = "payment-first"
	response := newUserBenefitSignedCallbackForTest(t, checkout.OrderID, "new-user-test-merchant", "alipay", "0.10", "payment-first")
	require.Equal(t, "success", response.Body.String())
	assert.Equal(t, 1, queryCount)
	state = newUserBenefitStateForTest(t, first.Id)
	assert.Equal(t, model.NewUserBenefitStatusVerificationPending, state.Status)
	assert.Equal(t, "identity_unavailable", state.RewardReason)
	assert.False(t, state.Eligible)
	var user model.User
	require.NoError(t, db.First(&user, first.Id).Error)
	assert.Equal(t, 5000, user.QuotaPaid)
	assert.Zero(t, user.QuotaBonus)
	history, _, err := model.GetUserTopUps(first.Id, &common.PageInfo{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "0.10", history[0].NewUserBenefitCreditedAmountCNY)
	assert.Equal(t, "1.00", history[0].NewUserBenefitCreditAmountCNY)
	assert.Equal(t, "pending", history[0].NewUserBenefitRewardStatus)
	// A caller-supplied user/order/buyer cannot make this route review another account.
	crossAccount := newUserBenefitTestRequest(RecheckNewUserBenefitPayment, second.Id, fmt.Sprintf(`{"user_id":%d,"order_id":%q,"buyer":"2088000000000001"}`, first.Id, checkout.OrderID), "")
	assert.Equal(t, http.StatusForbidden, crossAccount.Code)
	assert.Equal(t, 1, queryCount)
	// Credentials must never be sent to an old gateway after an administrator switch.
	operation_setting.PayAddress = "https://another-gateway.example.test"
	response = newUserBenefitTestRequest(RecheckNewUserBenefitPayment, first.Id, "{}", "")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 1, queryCount)
	assert.Contains(t, response.Body.String(), "query_unavailable")
	operation_setting.PayAddress = server.URL
	buyer = "2088000000000001"
	response = newUserBenefitTestRequest(RecheckNewUserBenefitPayment, first.Id, "{}", "")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"status":"claimed"`)
	assert.NotContains(t, response.Body.String(), buyer)
	assert.NotContains(t, response.Body.String(), "new-user-synthetic-key")
	assert.Equal(t, 2, queryCount)
	for range 2 {
		response = newUserBenefitSignedCallbackForTest(t, checkout.OrderID, "new-user-test-merchant", "alipay", "0.10", "payment-first")
		assert.Equal(t, "success", response.Body.String())
	}
	assert.Equal(t, 2, queryCount, "a terminal reward never queries or grants again")
	require.NoError(t, db.First(&user, first.Id).Error)
	assert.Equal(t, 50000, user.Quota)
	secondCheckout := newUserBenefitCheckoutResponseForTest(t, newUserBenefitTestRequest(RequestNewUserBenefitPay, second.Id, body, uuid.NewString()))
	queryReferences[secondCheckout.OrderID] = "payment-second"
	response = newUserBenefitSignedCallbackForTest(t, secondCheckout.OrderID, "new-user-test-merchant", "alipay", "0.10", "payment-second")
	require.Equal(t, "success", response.Body.String())
	state = newUserBenefitStateForTest(t, second.Id)
	assert.Equal(t, model.NewUserBenefitStatusRewardDenied, state.Status)
	assert.Equal(t, "payment_identity_used", state.BlockedReason)
	assert.Equal(t, "denied", state.RewardStatus)
	user = model.User{}
	require.NoError(t, db.First(&user, second.Id).Error)
	assert.Equal(t, 5000, user.QuotaPaid)
	assert.Zero(t, user.QuotaBonus)
	var count int64
	require.NoError(t, db.Model(&model.PaymentReceipt{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	require.NoError(t, db.Model(&model.NewUserBenefitPaymentIdentity{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestNewUserBenefitSettingsIndependentControlsAndOldClientsPostgres(t *testing.T) {
	setupNewUserBenefitControllerPostgres(t)
	common.TurnstileCheckEnabled = false
	response := requestNewUserBenefitSettings(t, http.MethodPut, newUserBenefitSettingsJSON(t, map[string]any{
		"require_verified_email": false, "require_turnstile": false, "network_limit_enabled": false, "payment_identity_check_enabled": true,
	}))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	config, err := model.GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.False(t, config.RequireVerifiedEmail)
	assert.False(t, config.RequireTurnstile)
	assert.False(t, config.NetworkLimitEnabled)
	assert.True(t, config.PaymentIdentityCheckEnabled)
	legacyBody := `{"enabled":true,"pay_amount_cny":"0.20","credit_amount_cny":"2.00","version":1,"max_total_orders":1000,"max_orders_per_24h":100,"max_orders_per_network_24h":3,"total_bonus_budget_cny":"900.00"}`
	response = requestNewUserBenefitSettings(t, http.MethodPut, legacyBody)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	config, err = model.GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.False(t, config.RequireTurnstile)
	assert.True(t, config.PaymentIdentityCheckEnabled, "an old settings client cannot silently disable a saved payer check")
	for _, badGateway := range []string{"http://pay.example.test", "https://pay.example.test?key=value", "https://pay.example.test#fragment"} {
		operation_setting.PayAddress = badGateway
		response = requestNewUserBenefitSettings(t, http.MethodPut, newUserBenefitSettingsJSON(t, map[string]any{"version": 2, "require_turnstile": false, "payment_identity_check_enabled": true}))
		assert.Equal(t, http.StatusConflict, response.Code)
		assert.Contains(t, response.Body.String(), "new_user_benefit_payment_identity_unavailable")
	}
}
