package controller

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/paymentcore"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func signedPancakeRetryRequest(t *testing.T, body []byte, privateKey *rsa.PrivateKey) *http.Request {
	t.Helper()
	timestamp := fmt.Sprint(time.Now().UnixMilli())
	digest := sha256.Sum256([]byte(timestamp + "." + string(body)))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/waffo-pancake/webhook/prod", strings.NewReader(string(body)))
	req.Header.Set("X-Waffo-Signature", "t="+timestamp+",v1="+base64.StdEncoding.EncodeToString(signature))
	return req
}

func runPancakeRetryWebhook(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req
	c.Params = gin.Params{{Key: "env", Value: "prod"}}
	WaffoPancakeWebhook(c)
	return recorder
}

func TestPancakeWebhookRetriesTransientBindingWriteFailureAndCompletesOnce(t *testing.T) {
	db := withPaymentBindingDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Log{}))
	withPancakeBindingConfig(t, "STORE_SITE")
	confirmPaymentComplianceForTest(t)

	originalMerchantID := setting.WaffoPancakeMerchantID
	originalPrivateKey := setting.WaffoPancakePrivateKey
	originalProductID := setting.WaffoPancakeProductID
	originalRedisEnabled := common.RedisEnabled
	originalLogDB := model.LOG_DB
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = originalMerchantID
		setting.WaffoPancakePrivateKey = originalPrivateKey
		setting.WaffoPancakeProductID = originalProductID
		common.RedisEnabled = originalRedisEnabled
		model.LOG_DB = originalLogDB
	})
	setting.WaffoPancakeMerchantID = "merchant"
	setting.WaffoPancakePrivateKey = "private"
	setting.WaffoPancakeProductID = "product"
	common.RedisEnabled = false
	model.LOG_DB = db

	user := &model.User{
		Id:       41,
		Username: "pancake-retry",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)

	tradeNo := "WAFFO_PANCAKE-retry-regression"
	binding, err := paymentcore.NewWaffoPancakeBinding(tradeNo, paymentcore.OrderBindingKindTopUp, "100.00")
	require.NoError(t, err)
	order := &model.TopUp{
		UserId:          user.Id,
		Amount:          100,
		Money:           100,
		TradeNo:         tradeNo,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, paymentcore.InsertTopUpWithBinding(order, binding))

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	t.Setenv("WAFFO_WEBHOOK_PROD_PUBLIC_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})))

	body, err := common.Marshal(map[string]any{
		"id":        "retry-regression",
		"eventType": "order.completed",
		"mode":      "prod",
		"storeId":   "STORE_SITE",
		"data": map[string]any{
			"orderId":                       "ORD_RETRY_1",
			"orderMerchantExternalId":       tradeNo,
			"merchantProvidedBuyerIdentity": service.WaffoPancakeBuyerIdentityFromUserID(user.Id),
			"amount":                        "100.00",
			"currency":                      "USD",
		},
	})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE TRIGGER fail_binding_write BEFORE UPDATE ON chuanapi_payment_order_bindings BEGIN SELECT RAISE(ABORT, 'temporary binding write failure'); END`).Error)
	first := runPancakeRetryWebhook(t, signedPancakeRetryRequest(t, body, privateKey))
	assert.GreaterOrEqual(t, first.Code, http.StatusInternalServerError)
	require.NoError(t, db.First(order, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, order.Status)

	require.NoError(t, db.Exec(`DROP TRIGGER fail_binding_write`).Error)
	second := runPancakeRetryWebhook(t, signedPancakeRetryRequest(t, body, privateKey))
	assert.Equal(t, http.StatusOK, second.Code)
	require.NoError(t, db.First(order, order.Id).Error)
	assert.Equal(t, common.TopUpStatusSuccess, order.Status)

	var afterSecond model.User
	require.NoError(t, db.First(&afterSecond, user.Id).Error)
	expectedQuota := int(100 * common.QuotaPerUnit)
	assert.Equal(t, expectedQuota, afterSecond.Quota)
	assert.Equal(t, expectedQuota, afterSecond.QuotaPaid)

	third := runPancakeRetryWebhook(t, signedPancakeRetryRequest(t, body, privateKey))
	assert.Equal(t, http.StatusOK, third.Code)
	var afterThird model.User
	require.NoError(t, db.First(&afterThird, user.Id).Error)
	assert.Equal(t, afterSecond.Quota, afterThird.Quota)
	assert.Equal(t, afterSecond.QuotaPaid, afterThird.QuotaPaid)

	var ledgerRows int64
	require.NoError(t, db.Model(&model.BalanceLedger{}).
		Where("reference_type = ? AND reference_id = ?", "topup", tradeNo).
		Count(&ledgerRows).Error)
	assert.EqualValues(t, 1, ledgerRows, "successful callback replay must not credit twice")
}

func TestPancakeSubscriptionWebhookRetriesTransientBindingWriteFailure(t *testing.T) {
	db := withPaymentBindingDB(t)
	withPancakeBindingConfig(t, "STORE_SUB")
	confirmPaymentComplianceForTest(t)

	originalMerchantID := setting.WaffoPancakeMerchantID
	originalPrivateKey := setting.WaffoPancakePrivateKey
	originalProductID := setting.WaffoPancakeProductID
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = originalMerchantID
		setting.WaffoPancakePrivateKey = originalPrivateKey
		setting.WaffoPancakeProductID = originalProductID
	})
	setting.WaffoPancakeMerchantID = "merchant"
	setting.WaffoPancakePrivateKey = "private"
	setting.WaffoPancakeProductID = "product"

	tradeNo := "WAFFO_PANCAKE_SUB-42-retry-regression"
	binding, err := paymentcore.NewWaffoPancakeBinding(tradeNo, paymentcore.OrderBindingKindSubscription, "29.99")
	require.NoError(t, err)
	order := &model.SubscriptionOrder{
		UserId:          42,
		PlanId:          7,
		Money:           29.99,
		TradeNo:         tradeNo,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, paymentcore.InsertSubscriptionOrderWithBinding(order, binding))

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	t.Setenv("WAFFO_WEBHOOK_PROD_PUBLIC_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})))

	body, err := common.Marshal(map[string]any{
		"id":        "subscription-retry-regression",
		"eventType": "order.completed",
		"mode":      "prod",
		"storeId":   "STORE_SUB",
		"data": map[string]any{
			"orderId":                       "ORD_SUB_RETRY_1",
			"orderMerchantExternalId":       tradeNo,
			"merchantProvidedBuyerIdentity": service.WaffoPancakeBuyerIdentityFromUserID(order.UserId),
			"amount":                        "29.99",
			"currency":                      "USD",
		},
	})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE TRIGGER fail_subscription_binding_write BEFORE UPDATE ON chuanapi_payment_order_bindings BEGIN SELECT RAISE(ABORT, 'temporary subscription binding write failure'); END`).Error)
	recorder := runPancakeRetryWebhook(t, signedPancakeRetryRequest(t, body, privateKey))
	assert.GreaterOrEqual(t, recorder.Code, http.StatusInternalServerError)
	require.NoError(t, db.First(order, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
}

func TestPancakeResolversTreatDatabaseUnavailableAsRetryable(t *testing.T) {
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previousDB })

	topupEvent := &service.WaffoPancakeWebhookEvent{
		Data: service.WaffoPancakeWebhookData{OrderMerchantExternalID: "WAFFO_PANCAKE-db-down"},
	}
	_, err := service.ResolveWaffoPancakeTradeNo(topupEvent)
	require.Error(t, err)
	assert.True(t, service.RetryableWaffoPancakeWebhookError(err), "top-up database outage must request provider retry")

	subscriptionEvent := &service.WaffoPancakeWebhookEvent{
		Data: service.WaffoPancakeWebhookData{OrderMerchantExternalID: "WAFFO_PANCAKE_SUB-db-down"},
	}
	_, err = service.ResolveWaffoPancakeSubscriptionTradeNo(subscriptionEvent)
	require.Error(t, err)
	assert.True(t, service.RetryableWaffoPancakeWebhookError(err), "subscription database outage must request provider retry")
}

func TestProviderOrderIdentityConflictIsPermanent(t *testing.T) {
	db := withPaymentBindingDB(t)
	withPancakeBindingConfig(t, "STORE_RACE")

	tradeNo := "provider-order-race"
	binding := paymentcore.PaymentOrderBinding{
		TradeNo:          tradeNo,
		Provider:         model.PaymentProviderWaffoPancake,
		OrderKind:        paymentcore.OrderBindingKindTopUp,
		Environment:      "prod",
		StoreID:          "STORE_RACE",
		ProviderOrderID:  "ORD_WINNER",
		ExpectedAmount:   "100.00",
		ExpectedCurrency: "USD",
	}
	require.NoError(t, db.Create(&binding).Error)

	err := paymentcore.BindProviderOrderID(binding.TradeNo, "ORD_LOSER")
	require.Error(t, err)
	assert.ErrorIs(t, err, paymentcore.ErrProviderOrderIDMismatch)
	assert.False(t, service.RetryableWaffoPancakeWebhookError(err), "identity conflict is not a transient storage error")
}
