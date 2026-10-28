package controller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/paymentcore"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/waffo-com/waffo-go/core"
	"gorm.io/gorm"
)

func withPaymentBindingDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.SubscriptionOrder{}, &model.Option{}, &paymentcore.PaymentOrderBinding{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func withPancakeBindingConfig(t *testing.T, storeID string) {
	t.Helper()
	previousStoreID := setting.WaffoPancakeStoreID
	previousEnvironment := setting.WaffoPancakeEnvironment
	setting.WaffoPancakeStoreID = storeID
	setting.WaffoPancakeEnvironment = setting.WaffoPancakeEnvironmentProd
	t.Cleanup(func() {
		setting.WaffoPancakeStoreID = previousStoreID
		setting.WaffoPancakeEnvironment = previousEnvironment
	})
}

func TestValidateWaffoPaymentCallbackRejectsAmountCurrencyIdentityMismatch(t *testing.T) {
	withPaymentBindingDB(t)
	previousCurrency := setting.WaffoCurrency
	previousMerchant := setting.WaffoMerchantId
	previousSandbox := setting.WaffoSandbox
	setting.WaffoCurrency = "USD"
	setting.WaffoMerchantId = "MER_SITE"
	setting.WaffoSandbox = false
	t.Cleanup(func() {
		setting.WaffoCurrency = previousCurrency
		setting.WaffoMerchantId = previousMerchant
		setting.WaffoSandbox = previousSandbox
	})

	tradeNo := "WAFFO-3-secure"
	topUp := &model.TopUp{
		UserId: 3, Amount: 100, Money: 100, TradeNo: tradeNo,
		PaymentMethod: model.PaymentMethodWaffo, PaymentProvider: model.PaymentProviderWaffo,
		Status: common.TopUpStatusPending,
	}
	binding, err := paymentcore.NewWaffoBinding(tradeNo, "100.00", "USD")
	require.NoError(t, err)
	require.NoError(t, paymentcore.InsertTopUpWithBinding(topUp, binding))

	base := &core.PaymentNotificationResult{
		PaymentRequestID: tradeNo,
		MerchantOrderID:  tradeNo,
		AcquiringOrderID: "ACQ_SITE_1",
		OrderAmount:      "100.00",
		OrderCurrency:    "USD",
		MerchantInfo:     map[string]interface{}{"merchantId": "MER_SITE"},
	}

	wrongAmount := *base
	wrongAmount.OrderAmount = "0.01"
	err = validateWaffoPaymentCallback(&wrongAmount)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount mismatch")

	wrongCurrency := *base
	wrongCurrency.OrderCurrency = "EUR"
	err = validateWaffoPaymentCallback(&wrongCurrency)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "currency mismatch")

	wrongRequest := *base
	wrongRequest.PaymentRequestID = "REQ_OTHER"
	err = validateWaffoPaymentCallback(&wrongRequest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payment request id mismatch")

	wrongMerchant := *base
	wrongMerchant.MerchantInfo = map[string]interface{}{"merchantId": "MER_OTHER"}
	err = validateWaffoPaymentCallback(&wrongMerchant)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "merchant mismatch")

	require.NoError(t, validateWaffoPaymentCallback(base))
	stored, err := paymentcore.GetPaymentOrderBinding(tradeNo)
	require.NoError(t, err)
	assert.Equal(t, "ACQ_SITE_1", stored.ProviderOrderID)

	legacyTradeNo := "WAFFO-3-legacy-unbound"
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId: 3, Amount: 100, Money: 100, TradeNo: legacyTradeNo,
		PaymentMethod: model.PaymentMethodWaffo, PaymentProvider: model.PaymentProviderWaffo,
		Status: common.TopUpStatusPending,
	}).Error)
	legacyCallback := *base
	legacyCallback.PaymentRequestID = legacyTradeNo
	legacyCallback.MerchantOrderID = legacyTradeNo
	legacyCallback.AcquiringOrderID = "ACQ_LEGACY"
	err = validateWaffoPaymentCallback(&legacyCallback)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binding not found")

	conflictingOrder := *base
	conflictingOrder.AcquiringOrderID = "ACQ_SITE_2"
	err = validateWaffoPaymentCallback(&conflictingOrder)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider order id mismatch")
}
