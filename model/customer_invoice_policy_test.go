package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomerInvoicePolicyRequiresExplicitEnableAndMerchant(t *testing.T) {
	for _, tc := range []struct {
		name, flag, scope, wantScope string
		wantEnabled                  bool
	}{
		{"empty", "", "", "", false},
		{"explicit flag only", "true", "", "", false},
		{"merchant alone", "", "merchant-fixture", "merchant-fixture", false},
		{"disabled can finish pending", "false", "merchant-fixture", "merchant-fixture", false},
		{"valid", "true", " merchant-fixture ", "merchant-fixture", true},
		{"invalid", "true", "m\nother", "", false},
		{"oversized", "true", strings.Repeat("m", 129), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUSTOMER_INVOICE_ENABLED", tc.flag)
			t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", tc.scope)
			require.Equal(t, CustomerInvoicePolicy{Enabled: tc.wantEnabled, MerchantScope: tc.wantScope}, GetCustomerInvoicePolicy())
		})
	}
}
