package paymentcore

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	OrderBindingKindTopUp        = "topup"
	OrderBindingKindSubscription = "subscription"
)

// PaymentOrderBinding is the provider-owned settlement snapshot for one local
// payment order. It stays a generic host payment capability (not a provider
// module type) so top-up/subscription creation and webhook settlement can
// depend on immutable server-side facts without adding provider fields to the
// root TopUp/SubscriptionOrder tables.
type PaymentOrderBinding struct {
	TradeNo           string `gorm:"primaryKey;type:varchar(255)"`
	Provider          string `gorm:"type:varchar(64);not null;index"`
	OrderKind         string `gorm:"type:varchar(32);not null"`
	Environment       string `gorm:"type:varchar(32);not null"`
	StoreID           string `gorm:"type:varchar(128);not null;default:''"`
	MerchantID        string `gorm:"type:varchar(128);not null;default:''"`
	PaymentRequestID  string `gorm:"type:varchar(255);not null;default:''"`
	ProviderSessionID string `gorm:"type:varchar(255);not null;default:''"`
	ProviderOrderID   string `gorm:"type:varchar(255);not null;default:''"`
	ExpectedAmount    string `gorm:"type:varchar(64);not null"`
	ExpectedCurrency  string `gorm:"type:varchar(16);not null"`
	CreatedAt         int64  `gorm:"autoCreateTime"`
	UpdatedAt         int64  `gorm:"autoUpdateTime"`
}

func (PaymentOrderBinding) TableName() string { return "chuanapi_payment_order_bindings" }

var ErrProviderOrderIDMismatch = errors.New("payment provider order id mismatch")

// PaymentCallbackFacts are the immutable callback facts a webhook must match
// against the binding captured when the local order was created.
type PaymentCallbackFacts struct {
	Provider         string
	OrderKind        string
	Environment      string
	StoreID          string
	MerchantID       string
	PaymentRequestID string
	ProviderOrderID  string
	ActualAmount     string
	ActualCurrency   string
}

// ConfiguredWaffoPancakeEnvironment returns the trusted Pancake environment
// pinned at order creation; an invalid configured value fails closed.
func ConfiguredWaffoPancakeEnvironment() (string, error) {
	return setting.NormalizeWaffoPancakeEnvironment(setting.WaffoPancakeEnvironment)
}

// CurrentWaffoEnvironment pins the Waffo production/sandbox mode into the
// binding so callbacks cannot switch environments between order and webhook.
func CurrentWaffoEnvironment() string {
	if setting.WaffoSandbox {
		return "sandbox"
	}
	return "production"
}

func NewWaffoPancakeBinding(tradeNo, orderKind, amount string) (PaymentOrderBinding, error) {
	environment, err := ConfiguredWaffoPancakeEnvironment()
	if err != nil {
		return PaymentOrderBinding{}, err
	}
	storeID := strings.TrimSpace(setting.WaffoPancakeStoreID)
	if storeID == "" {
		return PaymentOrderBinding{}, errors.New("Waffo Pancake store id is not configured")
	}
	return NewPaymentOrderBinding(
		tradeNo,
		model.PaymentProviderWaffoPancake,
		orderKind,
		environment,
		storeID,
		"",
		"",
		amount,
		"USD",
	)
}

func NewWaffoBinding(tradeNo, amount, currency string) (PaymentOrderBinding, error) {
	return NewPaymentOrderBinding(
		tradeNo,
		model.PaymentProviderWaffo,
		OrderBindingKindTopUp,
		CurrentWaffoEnvironment(),
		"",
		strings.TrimSpace(setting.WaffoMerchantId),
		tradeNo,
		amount,
		currency,
	)
}

func NewPaymentOrderBinding(tradeNo, provider, orderKind, environment, storeID, merchantID, paymentRequestID, amount, currency string) (PaymentOrderBinding, error) {
	tradeNo = strings.TrimSpace(tradeNo)
	provider = strings.TrimSpace(provider)
	orderKind = strings.TrimSpace(orderKind)
	environment = strings.ToLower(strings.TrimSpace(environment))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if tradeNo == "" || provider == "" || orderKind == "" || environment == "" || currency == "" {
		return PaymentOrderBinding{}, errors.New("payment order binding is incomplete")
	}
	canonicalAmount, err := CanonicalPaymentAmount(amount)
	if err != nil {
		return PaymentOrderBinding{}, err
	}
	return PaymentOrderBinding{
		TradeNo:          tradeNo,
		Provider:         provider,
		OrderKind:        orderKind,
		Environment:      environment,
		StoreID:          strings.TrimSpace(storeID),
		MerchantID:       strings.TrimSpace(merchantID),
		PaymentRequestID: strings.TrimSpace(paymentRequestID),
		ExpectedAmount:   canonicalAmount,
		ExpectedCurrency: currency,
	}, nil
}

func CanonicalPaymentAmount(raw string) (string, error) {
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil || value.LessThanOrEqual(decimal.Zero) {
		return "", fmt.Errorf("invalid payment amount %q", raw)
	}
	return value.String(), nil
}

// InsertTopUpWithBinding persists the local order and its provider settlement
// snapshot in one transaction; a binding failure must never leave an order
// that a callback could settle without pinned facts.
func InsertTopUpWithBinding(topUp *model.TopUp, binding PaymentOrderBinding) error {
	if topUp == nil || model.DB == nil {
		return errors.New("invalid topup binding insert")
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(topUp).Error; err != nil {
			return err
		}
		return tx.Create(&binding).Error
	})
}

func InsertSubscriptionOrderWithBinding(order *model.SubscriptionOrder, binding PaymentOrderBinding) error {
	if order == nil || model.DB == nil {
		return errors.New("invalid subscription binding insert")
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if order.CreateTime == 0 {
			order.CreateTime = time.Now().Unix()
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}
		return tx.Create(&binding).Error
	})
}

func GetPaymentOrderBinding(tradeNo string) (*PaymentOrderBinding, error) {
	if model.DB == nil {
		return nil, errors.New("payment database is not initialized")
	}
	var binding PaymentOrderBinding
	if err := model.DB.Where("trade_no = ?", strings.TrimSpace(tradeNo)).First(&binding).Error; err != nil {
		return nil, err
	}
	return &binding, nil
}

// BindProviderSessionID pins the upstream checkout session exactly once; a
// different session for the same local order is rejected.
func BindProviderSessionID(tradeNo, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("provider session id is empty")
	}
	result := model.DB.Model(&PaymentOrderBinding{}).
		Where("trade_no = ? AND (provider_session_id = '' OR provider_session_id = ?)", tradeNo, sessionID).
		Update("provider_session_id", sessionID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	binding, err := GetPaymentOrderBinding(tradeNo)
	if err != nil {
		return err
	}
	if binding.ProviderSessionID != sessionID {
		return fmt.Errorf("provider session id mismatch for tradeNo=%s", tradeNo)
	}
	return nil
}

// BindProviderOrderID pins the upstream order identity exactly once; a second
// different upstream order id is a permanent conflict.
func BindProviderOrderID(tradeNo, providerOrderID string) error {
	providerOrderID = strings.TrimSpace(providerOrderID)
	if providerOrderID == "" {
		return errors.New("provider order id is empty")
	}
	result := model.DB.Model(&PaymentOrderBinding{}).
		Where("trade_no = ? AND (provider_order_id = '' OR provider_order_id = ?)", tradeNo, providerOrderID).
		Update("provider_order_id", providerOrderID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	binding, err := GetPaymentOrderBinding(tradeNo)
	if err != nil {
		return err
	}
	if binding.ProviderOrderID != providerOrderID {
		return fmt.Errorf("%w for tradeNo=%s", ErrProviderOrderIDMismatch, tradeNo)
	}
	return nil
}

// ValidatePaymentOrderBinding rejects any callback whose provider, kind,
// environment, store, merchant, payment request, amount, currency or upstream
// order identity does not match the snapshot written at order creation.
func ValidatePaymentOrderBinding(binding *PaymentOrderBinding, facts PaymentCallbackFacts) error {
	if binding == nil {
		return errors.New("missing payment order binding")
	}
	if binding.Provider != strings.TrimSpace(facts.Provider) || binding.OrderKind != strings.TrimSpace(facts.OrderKind) {
		return fmt.Errorf("payment binding provider/kind mismatch for tradeNo=%s", binding.TradeNo)
	}
	if binding.Environment != strings.ToLower(strings.TrimSpace(facts.Environment)) {
		return fmt.Errorf("payment binding environment mismatch for tradeNo=%s", binding.TradeNo)
	}
	if binding.StoreID != "" && binding.StoreID != strings.TrimSpace(facts.StoreID) {
		return fmt.Errorf("payment binding store mismatch for tradeNo=%s", binding.TradeNo)
	}
	if actualMerchantID := strings.TrimSpace(facts.MerchantID); binding.MerchantID != "" && actualMerchantID != "" && binding.MerchantID != actualMerchantID {
		return fmt.Errorf("payment binding merchant mismatch for tradeNo=%s", binding.TradeNo)
	}
	if binding.PaymentRequestID != "" && binding.PaymentRequestID != strings.TrimSpace(facts.PaymentRequestID) {
		return fmt.Errorf("payment request id mismatch for tradeNo=%s", binding.TradeNo)
	}
	actualCurrency := strings.ToUpper(strings.TrimSpace(facts.ActualCurrency))
	if actualCurrency != binding.ExpectedCurrency {
		return fmt.Errorf("payment currency mismatch for tradeNo=%s: expected=%s actual=%s", binding.TradeNo, binding.ExpectedCurrency, actualCurrency)
	}
	actualAmount, err := CanonicalPaymentAmount(facts.ActualAmount)
	if err != nil {
		return err
	}
	if actualAmount != binding.ExpectedAmount {
		return fmt.Errorf("payment amount mismatch for tradeNo=%s: expected=%s actual=%s", binding.TradeNo, binding.ExpectedAmount, actualAmount)
	}
	providerOrderID := strings.TrimSpace(facts.ProviderOrderID)
	if providerOrderID == "" {
		return fmt.Errorf("provider order id missing for tradeNo=%s", binding.TradeNo)
	}
	if binding.ProviderOrderID != "" && binding.ProviderOrderID != providerOrderID {
		return fmt.Errorf("%w for tradeNo=%s", ErrProviderOrderIDMismatch, binding.TradeNo)
	}
	return nil
}
