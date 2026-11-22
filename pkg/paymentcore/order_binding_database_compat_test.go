package paymentcore

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestPaymentOrderBindingDatabaseCompatibilityReal exercises the host payment
// binding schema against a real database engine. It is opt-in: configure
// TEST_DATABASE_DIALECT (sqlite|mysql|postgres) and, for network engines,
// TEST_DATABASE_DSN. An explicitly configured engine that cannot connect or
// migrate fails the test instead of skipping, because a skipped database
// matrix must never look like evidence.
func TestPaymentOrderBindingDatabaseCompatibilityReal(t *testing.T) {
	dialect := strings.ToLower(strings.TrimSpace(os.Getenv("TEST_DATABASE_DIALECT")))
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if os.Getenv("TEST_DATABASE_COMPAT") != "1" && dialect == "" && dsn == "" {
		t.Skip("set TEST_DATABASE_DIALECT=postgres (+ TEST_DATABASE_DSN) to run the real payment binding database compatibility test")
	}

	var dialector gorm.Dialector
	switch dialect {
	case "sqlite":
		dialector = sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_")))
	case "mysql":
		require.NotEmpty(t, dsn, "TEST_DATABASE_DSN is required for mysql")
		dialector = mysql.Open(dsn)
	case "postgres":
		require.NotEmpty(t, dsn, "TEST_DATABASE_DSN is required for postgres")
		dialector = postgres.Open(dsn)
	case "":
		t.Fatalf("TEST_DATABASE_DIALECT is required when a compatibility run is requested (got TEST_DATABASE_DSN=%t)", dsn != "")
	default:
		t.Fatalf("unsupported TEST_DATABASE_DIALECT %q", dialect)
	}

	db, err := gorm.Open(dialector, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// Fresh migration and repeat migration must both succeed.
	require.NoError(t, db.Migrator().DropTable(&PaymentOrderBinding{}))
	require.NoError(t, db.AutoMigrate(&PaymentOrderBinding{}))
	require.NoError(t, db.AutoMigrate(&PaymentOrderBinding{}))
	require.True(t, db.Migrator().HasTable(&PaymentOrderBinding{}))

	// Recreate the pre-hardening schema and prove the candidate upgrades it
	// without losing the row that anchors an in-flight payment.
	require.NoError(t, db.Migrator().DropTable(&PaymentOrderBinding{}))
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
		"matrix-legacy",
		model.PaymentProviderWaffoPancake,
		OrderBindingKindTopUp,
		"prod",
		"20.00",
		"USD",
	).Error)
	require.NoError(t, db.AutoMigrate(&PaymentOrderBinding{}))
	require.NoError(t, db.AutoMigrate(&PaymentOrderBinding{}))

	for _, column := range []string{"store_id", "merchant_id", "payment_request_id", "provider_session_id", "provider_order_id", "created_at", "updated_at"} {
		assert.True(t, db.Migrator().HasColumn(&PaymentOrderBinding{}, column), "missing upgraded column %s", column)
	}
	var upgraded PaymentOrderBinding
	require.NoError(t, db.Where("trade_no = ?", "matrix-legacy").First(&upgraded).Error)
	assert.Equal(t, "20.00", upgraded.ExpectedAmount)
	assert.Equal(t, "USD", upgraded.ExpectedCurrency)
	assert.Empty(t, upgraded.ProviderOrderID)

	// trade_no remains globally unique after the upgrade.
	unique := PaymentOrderBinding{
		TradeNo:          "matrix-unique",
		Provider:         model.PaymentProviderWaffoPancake,
		OrderKind:        OrderBindingKindTopUp,
		Environment:      "prod",
		ExpectedAmount:   "1.00",
		ExpectedCurrency: "USD",
	}
	require.NoError(t, db.Create(&unique).Error)
	duplicate := unique
	duplicate.Provider = model.PaymentProviderWaffo
	require.Error(t, db.Create(&duplicate).Error)

	// Order + binding inserts are one transaction on every supported database.
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.SubscriptionOrder{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })

	topUpBinding := PaymentOrderBinding{
		TradeNo:          "matrix-rollback-topup",
		Provider:         model.PaymentProviderWaffoPancake,
		OrderKind:        OrderBindingKindTopUp,
		Environment:      "prod",
		ExpectedAmount:   "10.00",
		ExpectedCurrency: "USD",
	}
	require.NoError(t, db.Create(&topUpBinding).Error)
	topUp := &model.TopUp{
		UserId:          1,
		Amount:          10,
		Money:           10,
		TradeNo:         topUpBinding.TradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusPending,
	}
	require.Error(t, InsertTopUpWithBinding(topUp, topUpBinding))
	var topUpCount int64
	require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", topUpBinding.TradeNo).Count(&topUpCount).Error)
	assert.Zero(t, topUpCount)

	subscriptionBinding := PaymentOrderBinding{
		TradeNo:          "matrix-rollback-subscription",
		Provider:         model.PaymentProviderWaffoPancake,
		OrderKind:        OrderBindingKindSubscription,
		Environment:      "prod",
		ExpectedAmount:   "29.99",
		ExpectedCurrency: "USD",
	}
	require.NoError(t, db.Create(&subscriptionBinding).Error)
	order := &model.SubscriptionOrder{
		UserId:          1,
		PlanId:          7,
		Money:           29.99,
		TradeNo:         subscriptionBinding.TradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusPending,
	}
	require.Error(t, InsertSubscriptionOrderWithBinding(order, subscriptionBinding))
	var orderCount int64
	require.NoError(t, db.Model(&model.SubscriptionOrder{}).Where("trade_no = ?", subscriptionBinding.TradeNo).Count(&orderCount).Error)
	assert.Zero(t, orderCount)

	if dialect == "sqlite" {
		return
	}

	// Two instances racing to bind different upstream order identities must
	// produce exactly one winner and one permanent identity conflict.
	racing := PaymentOrderBinding{
		TradeNo:          "matrix-race",
		Provider:         model.PaymentProviderWaffoPancake,
		OrderKind:        OrderBindingKindTopUp,
		Environment:      "prod",
		ExpectedAmount:   "1.00",
		ExpectedCurrency: "USD",
	}
	require.NoError(t, db.Create(&racing).Error)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, providerOrderID := range []string{"matrix-provider-A", "matrix-provider-B"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			errs <- BindProviderOrderID("matrix-race", id)
		}(providerOrderID)
	}
	wg.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for bindErr := range errs {
		if bindErr == nil {
			successes++
			continue
		}
		if errors.Is(bindErr, ErrProviderOrderIDMismatch) {
			conflicts++
			continue
		}
		t.Fatalf("unexpected provider-order bind error: %v", bindErr)
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
}
