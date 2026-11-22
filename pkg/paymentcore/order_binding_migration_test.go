package paymentcore

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openPaymentMigrationSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestPaymentOrderBindingSQLiteFreshAndRepeatedMigration(t *testing.T) {
	db := openPaymentMigrationSQLite(t)

	require.NoError(t, db.AutoMigrate(&PaymentOrderBinding{}))
	require.NoError(t, db.AutoMigrate(&PaymentOrderBinding{}), "repeated startup migration must be idempotent")
	require.True(t, db.Migrator().HasTable(&PaymentOrderBinding{}))

	for _, column := range []string{
		"trade_no",
		"provider",
		"order_kind",
		"environment",
		"store_id",
		"merchant_id",
		"payment_request_id",
		"provider_session_id",
		"provider_order_id",
		"expected_amount",
		"expected_currency",
		"created_at",
		"updated_at",
	} {
		assert.True(t, db.Migrator().HasColumn(&PaymentOrderBinding{}, column), "missing column %s", column)
	}

	first := PaymentOrderBinding{
		TradeNo:          "binding-unique",
		Provider:         model.PaymentProviderWaffoPancake,
		OrderKind:        OrderBindingKindTopUp,
		Environment:      "prod",
		ExpectedAmount:   "10.00",
		ExpectedCurrency: "USD",
	}
	require.NoError(t, db.Create(&first).Error)
	duplicate := first
	duplicate.Provider = model.PaymentProviderWaffo
	require.Error(t, db.Create(&duplicate).Error, "trade_no must remain globally unique")
}

func TestPaymentOrderBindingSQLiteUpgradePreservesLegacyRows(t *testing.T) {
	db := openPaymentMigrationSQLite(t)

	require.NoError(t, db.Exec(`
CREATE TABLE chuanapi_payment_order_bindings (
  trade_no varchar(255) PRIMARY KEY,
  provider varchar(64) NOT NULL,
  order_kind varchar(32) NOT NULL,
  environment varchar(32) NOT NULL,
  expected_amount varchar(64) NOT NULL,
  expected_currency varchar(16) NOT NULL
)`).Error)
	require.NoError(t, db.Exec(`
INSERT INTO chuanapi_payment_order_bindings
  (trade_no, provider, order_kind, environment, expected_amount, expected_currency)
VALUES (?, ?, ?, ?, ?, ?)`,
		"legacy-binding",
		model.PaymentProviderWaffoPancake,
		OrderBindingKindTopUp,
		"prod",
		"20.00",
		"USD",
	).Error)

	require.NoError(t, db.AutoMigrate(&PaymentOrderBinding{}))
	require.NoError(t, db.AutoMigrate(&PaymentOrderBinding{}))

	var upgraded PaymentOrderBinding
	require.NoError(t, db.Where("trade_no = ?", "legacy-binding").First(&upgraded).Error)
	assert.Equal(t, model.PaymentProviderWaffoPancake, upgraded.Provider)
	assert.Equal(t, "20.00", upgraded.ExpectedAmount)
	assert.Equal(t, "USD", upgraded.ExpectedCurrency)
	assert.Empty(t, upgraded.StoreID)
	assert.Empty(t, upgraded.ProviderOrderID)
}

func TestTopUpAndPaymentBindingInsertRollbackTogether(t *testing.T) {
	db := openPaymentMigrationSQLite(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &PaymentOrderBinding{}))

	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })

	tradeNo := "binding-rollback"
	existing := PaymentOrderBinding{
		TradeNo:          tradeNo,
		Provider:         model.PaymentProviderWaffoPancake,
		OrderKind:        OrderBindingKindTopUp,
		Environment:      "prod",
		ExpectedAmount:   "10.00",
		ExpectedCurrency: "USD",
	}
	require.NoError(t, db.Create(&existing).Error)

	topUp := &model.TopUp{
		UserId:          1,
		Amount:          10,
		Money:           10,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusPending,
	}
	conflicting := existing
	require.Error(t, InsertTopUpWithBinding(topUp, conflicting))

	var topUpCount int64
	require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", tradeNo).Count(&topUpCount).Error)
	assert.Zero(t, topUpCount, "binding insert failure must roll back the local top-up order")
}

func TestSubscriptionAndPaymentBindingInsertRollbackTogether(t *testing.T) {
	db := openPaymentMigrationSQLite(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &PaymentOrderBinding{}))

	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })

	tradeNo := "subscription-binding-rollback"
	existing := PaymentOrderBinding{
		TradeNo:          tradeNo,
		Provider:         model.PaymentProviderWaffoPancake,
		OrderKind:        OrderBindingKindSubscription,
		Environment:      "prod",
		ExpectedAmount:   "29.99",
		ExpectedCurrency: "USD",
	}
	require.NoError(t, db.Create(&existing).Error)

	order := &model.SubscriptionOrder{
		UserId:          1,
		PlanId:          7,
		Money:           29.99,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusPending,
	}
	conflicting := existing
	require.Error(t, InsertSubscriptionOrderWithBinding(order, conflicting))

	var orderCount int64
	require.NoError(t, db.Model(&model.SubscriptionOrder{}).Where("trade_no = ?", tradeNo).Count(&orderCount).Error)
	assert.Zero(t, orderCount, "binding insert failure must roll back the local subscription order")
}
