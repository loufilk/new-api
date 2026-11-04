package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentSimpleTransfersShareWalletComplianceGate(t *testing.T) {
	previous := *operation_setting.GetPaymentSetting()
	previousDB := model.DB
	t.Cleanup(func() { *operation_setting.GetPaymentSetting() = previous; model.DB = previousDB })
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false
	model.DB = nil
	for _, handler := range []gin.HandlerFunc{TransferReferralToAgentBalance, TransferAgentSimplePaymentProfit, TransferWalletToAgentSimple} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/synthetic", strings.NewReader(`{"quota":500000,"request_id":"synthetic-transfer"}`))
		handler(ctx)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Contains(t, response.Body.String(), "Payment compliance terms are not confirmed")
	}
}

func TestAgentSimpleWalletTransferHTTPRejectsInvalidIntentBeforeDatabaseAccess(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previousDB })
	for _, body := range []string{
		`{"request_id":"wallet-http-invalid","quota":0,"expected_user_id":1}`,
		`{"request_id":"wallet-http-invalid","quota":-1,"expected_user_id":1}`,
		`{"request_id":"wallet-http-invalid","quota":1.5,"expected_user_id":1}`,
		`{"request_id":"wallet-http-invalid","quota":"100","expected_user_id":1}`,
		`{"request_id":"wallet-http-invalid","quota":9223372036854775807,"expected_user_id":1}`,
		`{"request_id":"short","quota":100,"expected_user_id":1}`,
		`{"request_id":"wallet-http-invalid","quota":100,"agent_user_id":2,"expected_user_id":1}`,
		`{"request_id":"wallet-http-invalid","quota":100,"source":"bonus","expected_user_id":1}`,
		`{"request_id":"wallet-http-invalid","quota":100}`,
	} {
		t.Run(body, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/agent-platform/simple/wallet-transfer", strings.NewReader(body))
			ctx.Set("id", 1)
			ctx.Set("session_id", "wallet-http-session")
			ctx.Set("auth_version", int64(1))
			ctx.Set("session_version", int64(1))
			TransferWalletToAgentSimple(ctx)
			assert.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
}

func TestAgentSimpleWalletTransferHTTPRequiresMatchingSessionAccount(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previousDB })
	for _, signedIn := range []bool{false, true} {
		t.Run(strconv.FormatBool(signedIn), func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/agent-platform/simple/wallet-transfer", strings.NewReader(`{"request_id":"wallet-http-account-switch","quota":100,"expected_user_id":1}`))
			if signedIn {
				ctx.Set("id", 2)
				ctx.Set("session_id", "wallet-new-account-session")
				ctx.Set("auth_version", int64(1))
				ctx.Set("session_version", int64(1))
			}
			TransferWalletToAgentSimple(ctx)
			assert.Equal(t, http.StatusForbidden, response.Code)
		})
	}
}

func TestAgentSimpleHTTPRechargeReplayAndSignedPaymentDelivery(t *testing.T) {
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldAddress, oldID, oldKey, oldMethods := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods
	oldServer, oldRedis := system_setting.ServerAddress, common.RedisEnabled
	confirmPaymentComplianceForTest(t)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.SetDatabaseTypes(oldMain, oldLogType)
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods = oldAddress, oldID, oldKey, oldMethods
		system_setting.ServerAddress, common.RedisEnabled = oldServer, oldRedis
	})
	model.DB, model.LOG_DB, common.RedisEnabled = db, db, false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "https://payment.example.test", "synthetic-merchant", "synthetic-test-key"
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	system_setting.ServerAddress = "https://preview.example.test"
	require.NoError(t, db.AutoMigrate(append([]any{&model.User{}, &model.UserSession{}, &model.TopUp{}, &model.Redemption{}, &model.AgentRedemptionOperation{}, &model.Log{}, &model.Option{}}, model.AgentPlatformModels()...)...))
	user := model.User{Username: "a07-http-agent", AffCode: "a07-http-agent", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.UserSession{SID: "synthetic-a07-http-session", UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: strings.Repeat("a", 64), LoginMethod: "password", ExpiresAt: common.GetTimestamp() + 3600}).Error)
	require.NoError(t, db.Create(&model.AgentProfile{UserID: user.Id, Status: model.AgentProfileActive, Version: 1, Reason: "Synthetic HTTP fixture"}).Error)
	require.NoError(t, db.Create(&model.AgentSimpleAccount{AgentUserID: user.Id, Status: model.AgentSimpleStatusActive, Version: 1}).Error)
	require.NoError(t, db.Create(&model.AgentSimplePaymentFee{PaymentMethod: "alipay", Enabled: true, Version: 1}).Error)
	perform := func(body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/synthetic", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Set("id", user.Id)
		ctx.Set("session_id", "synthetic-a07-http-session")
		ctx.Set("auth_version", int64(1))
		ctx.Set("session_version", int64(1))
		handler(ctx)
		return response
	}
	for range 2 {
		response := perform(`{"request_id":"a07-http-recharge","amount":"100.01","kind":"recharge","payment_method":"alipay"}`, RequestAgentSimpleRecharge)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"success":true`)
		assert.Contains(t, response.Body.String(), `"money":"90.01"`)
	}
	var count int64
	require.NoError(t, db.Model(&model.TopUp{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	for range 2 {
		response := perform(`{"request_id":"a07-http-payment","name":"Synthetic code","quota":500000,"sale_price_fen":120,"payment_method":"alipay"}`, CreateAgentSimplePayment)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"success":true`)
	}
	var order model.AgentSimplePaymentOrder
	require.NoError(t, db.First(&order).Error)
	assert.NotEmpty(t, order.PaymentParams)
	assert.NotEmpty(t, order.PaymentURL)
	assert.Zero(t, order.RedemptionID)
	require.NoError(t, db.Model(&model.AgentSimplePaymentOrder{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	params, err := epay.GenerateParamsWithError(map[string]string{"pid": "synthetic-merchant", "out_trade_no": order.TradeNo, "trade_no": "synthetic-http-provider-transaction", "trade_status": epay.StatusTradeSuccess, "type": "alipay", "money": "1.20", "name": "Synthetic code", "sign_type": "MD5"}, "synthetic-test-key")
	require.NoError(t, err)
	for range 2 {
		values := url.Values{}
		for key, value := range params {
			values.Set(key, value)
		}
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/epay/notify", strings.NewReader(values.Encode()))
		ctx.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		EpayNotify(ctx)
		assert.Equal(t, "success", response.Body.String())
	}
	require.NoError(t, db.First(&order, order.ID).Error)
	assert.Equal(t, model.AgentSimplePaymentPaid, order.Status)
	assert.Positive(t, order.RedemptionID)
	assert.Positive(t, order.PaymentReceiptID)
	require.NoError(t, db.Model(&model.Redemption{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
