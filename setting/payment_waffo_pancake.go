package setting

import (
	"fmt"
	"strings"
)

const (
	WaffoPancakeEnvironmentTest = "test"
	WaffoPancakeEnvironmentProd = "prod"
)

// Waffo Pancake hosted checkout configuration. Gateway is enabled once
// MerchantID + PrivateKey + ProductID are populated (no separate Enabled
// flag, matching Stripe / Creem). StoreID + ProductID are operator-bound
// via SaveWaffoPancakeConfig.
var (
	WaffoPancakeMerchantID  string
	WaffoPancakePrivateKey  string
	WaffoPancakeReturnURL   string
	WaffoPancakeUnitPrice   float64 = 1.0
	WaffoPancakeMinTopUp    int     = 1
	WaffoPancakeStoreID     string
	WaffoPancakeProductID   string
	WaffoPancakeEnvironment string = WaffoPancakeEnvironmentProd
)

func NormalizeWaffoPancakeEnvironment(raw string) (string, error) {
	environment := strings.ToLower(strings.TrimSpace(raw))
	if environment == "" {
		environment = WaffoPancakeEnvironmentProd
	}
	switch environment {
	case WaffoPancakeEnvironmentTest, WaffoPancakeEnvironmentProd:
		return environment, nil
	default:
		return "", fmt.Errorf("invalid Waffo Pancake environment %q: expected test or prod", raw)
	}
}
