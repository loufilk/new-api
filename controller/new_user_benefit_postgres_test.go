package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupNewUserBenefitControllerPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("requires an isolated PostgreSQL TEST_POSTGRES_DSN")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	var version string
	require.NoError(t, db.Raw("SHOW server_version").Scan(&version).Error)
	t.Logf("new user benefit PostgreSQL server_version=%s", version)
	models := []any{
		&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{},
		&model.NewUserBenefitConfig{}, &model.NewUserBenefitConfigAudit{}, &model.NewUserBenefitOrder{},
		&model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.PaymentReceipt{},
		&model.AgentSimplePaymentOrder{}, &model.Log{},
	}
	models = append(models, model.BonusSourceModels()...)
	require.NoError(t, db.AutoMigrate(models...))
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory := common.RedisEnabled, common.MemoryCacheEnabled
	oldRate, oldPrice, oldMinimum, oldUnit := operation_setting.USDExchangeRate, operation_setting.Price, operation_setting.MinTopUp, common.QuotaPerUnit
	oldDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.SetDatabaseTypes(oldMain, oldLogType)
		common.RedisEnabled, common.MemoryCacheEnabled = oldRedis, oldMemory
		operation_setting.USDExchangeRate, operation_setting.Price, operation_setting.MinTopUp, common.QuotaPerUnit = oldRate, oldPrice, oldMinimum, oldUnit
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay
	})
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	// Deliberately differ from the recharge retail price and normal minimum.
	operation_setting.USDExchangeRate, operation_setting.Price, operation_setting.MinTopUp, common.QuotaPerUnit = 10, 77, 99, 500000
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	configureNewUserBenefitEpayTest(t)
	return db
}

func newUserBenefitStateForTest(t *testing.T, userID int) newUserBenefitResponse {
	t.Helper()
	response := newUserBenefitTestRequest(GetNewUserBenefit, userID, "", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var result struct {
		Success bool                   `json:"success"`
		Data    newUserBenefitResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	for _, private := range []string{"merchant_scope", "new-user-test-merchant", "new-user-synthetic-key", "payment_receipt_id", "usd_exchange_rate"} {
		assert.NotContains(t, response.Body.String(), private)
	}
	return result.Data
}

type newUserBenefitCheckoutForTest struct {
	OrderID         string `json:"order_id"`
	PayAmountCNY    string `json:"pay_amount_cny"`
	CreditAmountCNY string `json:"credit_amount_cny"`
	Status          string `json:"status"`
	Checkout        struct {
		Kind   string            `json:"kind"`
		URL    string            `json:"url"`
		Params map[string]string `json:"params"`
	} `json:"checkout"`
}

func newUserBenefitCheckoutResponseForTest(t *testing.T, response *httptest.ResponseRecorder) newUserBenefitCheckoutForTest {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var result struct {
		Success bool                          `json:"success"`
		Data    newUserBenefitCheckoutForTest `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	return result.Data
}

func newUserBenefitSignedCallbackForTest(t *testing.T, orderID, merchant, paymentMethod, money, reference string) *httptest.ResponseRecorder {
	t.Helper()
	params, err := epay.GenerateParamsWithError(map[string]string{
		"pid": merchant, "out_trade_no": orderID, "trade_no": reference,
		"trade_status": epay.StatusTradeSuccess, "type": paymentMethod, "money": money,
		"name": "New user benefit test", "sign_type": "MD5",
	}, operation_setting.EpayKey)
	require.NoError(t, err)
	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/epay/notify", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	EpayNotify(c)
	return response
}

func TestNewUserBenefitHTTPLifecyclePostgres(t *testing.T) {
	db := setupNewUserBenefitControllerPostgres(t)
	user := model.User{Username: "benefit-controller-new-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	state := newUserBenefitStateForTest(t, user.Id)
	assert.False(t, state.Enabled)
	assert.False(t, state.Eligible)
	assert.Equal(t, "ineligible", state.Status)
	assert.Equal(t, "0.10", state.PayAmountCNY)
	assert.Equal(t, "1.00", state.CreditAmountCNY)
	assert.Empty(t, state.PayMethods)
	config, err := model.UpdateNewUserBenefitConfig(context.Background(), 0, 1, model.NewUserBenefitConfig{Enabled: true, PayAmountCents: 10, CreditAmountCents: 100})
	require.NoError(t, err)
	state = newUserBenefitStateForTest(t, user.Id)
	assert.True(t, state.Enabled)
	assert.True(t, state.Eligible)
	assert.Equal(t, "available", state.Status)
	assert.Equal(t, 5000, state.PaidQuota)
	assert.Equal(t, 45000, state.BonusQuota)
	assert.Len(t, state.PayMethods, 2)
	assert.Equal(t, config.Version, state.ConfigVersion)
	requestID := uuid.NewString()
	body := fmt.Sprintf(`{"payment_method":"alipay","config_version":%d}`, config.Version)
	stale := newUserBenefitTestRequest(RequestNewUserBenefitPay, user.Id, `{"payment_method":"alipay","config_version":0}`, requestID)
	assert.Equal(t, http.StatusConflict, stale.Code)
	assert.Contains(t, stale.Body.String(), "new_user_benefit_config_changed")
	var count int64
	require.NoError(t, db.Model(&model.NewUserBenefitOrder{}).Count(&count).Error)
	assert.Zero(t, count)
	checkout := newUserBenefitCheckoutResponseForTest(t, newUserBenefitTestRequest(RequestNewUserBenefitPay, user.Id, body, requestID))
	assert.Equal(t, "pending", checkout.Status)
	assert.Equal(t, "0.10", checkout.PayAmountCNY)
	assert.Equal(t, "1.00", checkout.CreditAmountCNY)
	assert.Equal(t, "form", checkout.Checkout.Kind)
	assert.Equal(t, "https://pay.example.test/submit.php", checkout.Checkout.URL)
	assert.Equal(t, "0.10", checkout.Checkout.Params["money"])
	assert.Equal(t, "new-user-test-merchant", checkout.Checkout.Params["pid"])
	assert.Equal(t, checkout.OrderID, checkout.Checkout.Params["out_trade_no"])
	assert.Equal(t, "https://console.example.test/api/user/epay/notify", checkout.Checkout.Params["notify_url"])
	assert.Equal(t, "https://console.example.test/wallet?show_history=true", checkout.Checkout.Params["return_url"])
	assert.NotContains(t, checkout.Checkout.Params, "key")
	verified, err := GetEpayClient().Verify(checkout.Checkout.Params)
	require.NoError(t, err)
	assert.True(t, verified.VerifyStatus)
	for _, key := range []string{requestID, uuid.NewString()} {
		replayed := newUserBenefitCheckoutResponseForTest(t, newUserBenefitTestRequest(RequestNewUserBenefitPay, user.Id, body, key))
		assert.Equal(t, checkout, replayed, "all retries must use the one account order and snapshot")
	}
	require.NoError(t, db.Model(&model.NewUserBenefitOrder{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&model.TopUp{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	state = newUserBenefitStateForTest(t, user.Id)
	require.NotNil(t, state.PendingOrder)
	assert.Equal(t, "pending", state.Status)
	assert.Equal(t, checkout.OrderID, state.PendingOrder.OrderID)
	assert.Equal(t, "alipay", state.PendingOrder.PaymentMethod)
	assert.Equal(t, []newUserBenefitPayMethod{{Type: "alipay", Name: "支付宝"}}, state.PayMethods)
	_, err = model.UpdateNewUserBenefitConfig(context.Background(), config.Version, 1, model.NewUserBenefitConfig{Enabled: false, PayAmountCents: 20, CreditAmountCents: 200})
	require.NoError(t, err)
	operation_setting.USDExchangeRate, common.QuotaPerUnit = 20, 1000000
	state = newUserBenefitStateForTest(t, user.Id)
	assert.False(t, state.Enabled)
	assert.Equal(t, "pending", state.Status)
	assert.Equal(t, "0.10", state.PayAmountCNY)
	assert.Equal(t, "1.00", state.CreditAmountCNY)
	assert.Equal(t, config.Version, state.ConfigVersion)
	assert.Equal(t, 5000, state.PaidQuota)
	assert.Equal(t, 45000, state.BonusQuota)
	resumed := newUserBenefitCheckoutResponseForTest(t, newUserBenefitTestRequest(RequestNewUserBenefitPay, user.Id, body, uuid.NewString()))
	assert.Equal(t, checkout, resumed)
	// Merchant changes cannot silently reissue an old order to a different account.
	operation_setting.EpayId = "another-merchant"
	assert.Empty(t, newUserBenefitStateForTest(t, user.Id).PayMethods)
	assert.Equal(t, http.StatusConflict, newUserBenefitTestRequest(RequestNewUserBenefitPay, user.Id, body, uuid.NewString()).Code)
	operation_setting.EpayId = "new-user-test-merchant"
	for _, bad := range []struct{ merchant, method, money, reference string }{
		{"other-signed-merchant", "alipay", "0.10", "wrong-merchant"},
		{"", "alipay", "0.10", "missing-merchant"},
		{"new-user-test-merchant", "wxpay", "0.10", "wrong-method"},
		{"new-user-test-merchant", "alipay", "0.20", "wrong-amount"},
	} {
		response := newUserBenefitSignedCallbackForTest(t, checkout.OrderID, bad.merchant, bad.method, bad.money, bad.reference)
		assert.Equal(t, "fail", response.Body.String())
	}
	assert.ErrorIs(t, model.ManualCompleteTopUp(checkout.OrderID, "127.0.0.1"), model.ErrNewUserBenefitPaymentRequired)
	var before model.User
	require.NoError(t, db.First(&before, user.Id).Error)
	assert.Zero(t, before.Quota)
	require.NoError(t, db.Model(&model.PaymentReceipt{}).Count(&count).Error)
	assert.Zero(t, count)
	for range 2 {
		response := newUserBenefitSignedCallbackForTest(t, checkout.OrderID, "new-user-test-merchant", "alipay", "0.10", "verified-benefit-payment")
		assert.Equal(t, "success", response.Body.String())
	}
	var credited model.User
	require.NoError(t, db.First(&credited, user.Id).Error)
	assert.Equal(t, 50000, credited.Quota)
	assert.Equal(t, 5000, credited.QuotaPaid)
	assert.Equal(t, 45000, credited.QuotaBonus)
	assert.Zero(t, credited.QuotaReward)
	require.NoError(t, db.Model(&model.PaymentReceipt{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	state = newUserBenefitStateForTest(t, user.Id)
	assert.Equal(t, "claimed", state.Status)
	assert.False(t, state.Eligible)
	assert.Nil(t, state.PendingOrder)
	assert.Empty(t, state.PayMethods)
	rejected := newUserBenefitTestRequest(RequestNewUserBenefitPay, user.Id, body, uuid.NewString())
	assert.Equal(t, http.StatusForbidden, rejected.Code)
	assert.Contains(t, rejected.Body.String(), "new_user_benefit_ineligible")
}

func TestNewUserBenefitHTTPRejectsPriorTopUpAndCrossAccountAccessPostgres(t *testing.T) {
	db := setupNewUserBenefitControllerPostgres(t)
	config, err := model.UpdateNewUserBenefitConfig(context.Background(), 0, 1, model.NewUserBenefitConfig{Enabled: true, PayAmountCents: 10, CreditAmountCents: 100})
	require.NoError(t, err)
	prior := model.User{Username: "benefit-existing-payer", AffCode: "benefit-existing-payer-aff", Status: common.UserStatusEnabled}
	fresh := model.User{Username: "benefit-new-payer", AffCode: "benefit-new-payer-aff", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&prior).Error)
	require.NoError(t, db.Create(&fresh).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: prior.Id, Amount: 1, Money: 7.3, TradeNo: "OLDTOPUP", PaymentProvider: model.PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusSuccess, CreateTime: 1, CompleteTime: 2}).Error)
	state := newUserBenefitStateForTest(t, prior.Id)
	assert.False(t, state.Eligible)
	assert.Equal(t, "ineligible", state.Status)
	assert.Empty(t, state.PayMethods)
	body := fmt.Sprintf(`{"payment_method":"alipay","config_version":%d}`, config.Version)
	denied := newUserBenefitTestRequest(RequestNewUserBenefitPay, prior.Id, body, uuid.NewString())
	assert.Equal(t, http.StatusForbidden, denied.Code)
	first := newUserBenefitCheckoutResponseForTest(t, newUserBenefitTestRequest(RequestNewUserBenefitPay, fresh.Id, body, uuid.NewString()))
	assert.NotEmpty(t, first.OrderID)
	state = newUserBenefitStateForTest(t, prior.Id)
	assert.Nil(t, state.PendingOrder)
	assert.NotEqual(t, "pending", state.Status)
	var count int64
	require.NoError(t, db.Model(&model.NewUserBenefitOrder{}).Where("user_id = ?", prior.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&model.NewUserBenefitOrder{}).Where("user_id = ?", fresh.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
