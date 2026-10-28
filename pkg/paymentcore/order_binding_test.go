package paymentcore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validBindingFixture() *PaymentOrderBinding {
	return &PaymentOrderBinding{
		TradeNo:           "WAFFO_PANCAKE-1-contract",
		Provider:          "waffo_pancake",
		OrderKind:         OrderBindingKindTopUp,
		Environment:       "prod",
		StoreID:           "STORE_SITE",
		MerchantID:        "",
		PaymentRequestID:  "",
		ProviderSessionID: "SESS_1",
		ProviderOrderID:   "ORD_1",
		ExpectedAmount:    "100",
		ExpectedCurrency:  "USD",
	}
}

func validFactsFixture() PaymentCallbackFacts {
	return PaymentCallbackFacts{
		Provider:        "waffo_pancake",
		OrderKind:       OrderBindingKindTopUp,
		Environment:     "prod",
		StoreID:         "STORE_SITE",
		ProviderOrderID: "ORD_1",
		ActualAmount:    "100.00",
		ActualCurrency:  "USD",
	}
}

func TestValidatePaymentOrderBindingRejectsEveryPinnedFactMismatch(t *testing.T) {
	testCases := []struct {
		name      string
		mutate    func(facts *PaymentCallbackFacts)
		wantError string
	}{
		{
			name:      "provider mismatch",
			mutate:    func(facts *PaymentCallbackFacts) { facts.Provider = "waffo" },
			wantError: "provider/kind mismatch",
		},
		{
			name:      "order kind mismatch",
			mutate:    func(facts *PaymentCallbackFacts) { facts.OrderKind = OrderBindingKindSubscription },
			wantError: "provider/kind mismatch",
		},
		{
			name:      "environment mismatch",
			mutate:    func(facts *PaymentCallbackFacts) { facts.Environment = "test" },
			wantError: "environment mismatch",
		},
		{
			name:      "store mismatch",
			mutate:    func(facts *PaymentCallbackFacts) { facts.StoreID = "STORE_OTHER" },
			wantError: "store mismatch",
		},
		{
			name:      "provider order id mismatch",
			mutate:    func(facts *PaymentCallbackFacts) { facts.ProviderOrderID = "ORD_2" },
			wantError: "provider order id mismatch",
		},
		{
			name:      "missing provider order id",
			mutate:    func(facts *PaymentCallbackFacts) { facts.ProviderOrderID = "" },
			wantError: "provider order id missing",
		},
		{
			name:      "amount mismatch",
			mutate:    func(facts *PaymentCallbackFacts) { facts.ActualAmount = "0.01" },
			wantError: "amount mismatch",
		},
		{
			name:      "invalid amount",
			mutate:    func(facts *PaymentCallbackFacts) { facts.ActualAmount = "not-a-number" },
			wantError: "invalid payment amount",
		},
		{
			name:      "currency mismatch",
			mutate:    func(facts *PaymentCallbackFacts) { facts.ActualCurrency = "EUR" },
			wantError: "currency mismatch",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			facts := validFactsFixture()
			tc.mutate(&facts)
			err := ValidatePaymentOrderBinding(validBindingFixture(), facts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantError)
		})
	}
}

func TestValidatePaymentOrderBindingAcceptsPinnedFactsCaseInsensitively(t *testing.T) {
	facts := validFactsFixture()
	facts.Environment = "PROD"
	facts.ActualCurrency = "usd"
	facts.ActualAmount = "100.0"
	require.NoError(t, ValidatePaymentOrderBinding(validBindingFixture(), facts))

	require.Error(t, ValidatePaymentOrderBinding(nil, facts), "missing binding must fail closed")
}

func TestValidatePaymentOrderBindingRequiresWaffoIdentityFields(t *testing.T) {
	binding := validBindingFixture()
	binding.Provider = "waffo"
	binding.Environment = "production"
	binding.StoreID = ""
	binding.MerchantID = "MER_SITE"
	binding.PaymentRequestID = binding.TradeNo
	binding.ProviderOrderID = ""

	base := PaymentCallbackFacts{
		Provider:         "waffo",
		OrderKind:        OrderBindingKindTopUp,
		Environment:      "production",
		MerchantID:       "MER_SITE",
		PaymentRequestID: binding.TradeNo,
		ProviderOrderID:  "ACQ_1",
		ActualAmount:     "100.00",
		ActualCurrency:   "USD",
	}
	require.NoError(t, ValidatePaymentOrderBinding(binding, base))

	wrongMerchant := base
	wrongMerchant.MerchantID = "MER_OTHER"
	require.ErrorContains(t, ValidatePaymentOrderBinding(binding, wrongMerchant), "merchant mismatch")

	wrongRequest := base
	wrongRequest.PaymentRequestID = "REQ_OTHER"
	require.ErrorContains(t, ValidatePaymentOrderBinding(binding, wrongRequest), "payment request id mismatch")
}

func TestCanonicalPaymentAmountRejectsNonPositiveAndMalformedValues(t *testing.T) {
	for _, raw := range []string{"", "0", "0.00", "-1", "abc"} {
		_, err := CanonicalPaymentAmount(raw)
		assert.Error(t, err, "amount %q must be rejected", raw)
	}
	canonical, err := CanonicalPaymentAmount(" 100.50 ")
	require.NoError(t, err)
	assert.Equal(t, "100.5", canonical)
}

func TestNewPaymentOrderBindingRejectsIncompleteIdentity(t *testing.T) {
	_, err := NewPaymentOrderBinding("", "waffo", OrderBindingKindTopUp, "production", "", "", "", "1.00", "USD")
	require.ErrorContains(t, err, "incomplete")

	_, err = NewWaffoPancakeBinding("trade-1", OrderBindingKindTopUp, "1.00")
	require.Error(t, err, "empty pancake store id must fail closed")
}
