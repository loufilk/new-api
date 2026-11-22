package model

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomerInvoicePolicyDefaultsVisibleButRequiresMerchantForIntake(t *testing.T) {
	for _, tc := range []struct {
		name, flag, scope, wantScope string
		wantVisible, wantEnabled     bool
	}{
		{"unset", "", "", "", true, false},
		{"explicit flag only", "true", "", "", true, false},
		{"merchant enables intake by default", "", "merchant-fixture", "merchant-fixture", true, true},
		{"explicit opt out", "false", "merchant-fixture", "merchant-fixture", false, false},
		{"invalid flag fails closed", "invalid", "merchant-fixture", "merchant-fixture", false, false},
		{"valid", "true", " merchant-fixture ", "merchant-fixture", true, true},
		{"invalid merchant", "true", "m\nother", "", true, false},
		{"oversized merchant", "true", strings.Repeat("m", 129), "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUSTOMER_INVOICE_ENABLED", tc.flag)
			if tc.name == "unset" {
				require.NoError(t, os.Unsetenv("CUSTOMER_INVOICE_ENABLED"))
			}
			t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", tc.scope)
			require.Equal(t, CustomerInvoicePolicy{
				CenterVisible: tc.wantVisible, Enabled: tc.wantEnabled, MerchantScope: tc.wantScope,
			}, GetCustomerInvoicePolicy())
		})
	}
}
