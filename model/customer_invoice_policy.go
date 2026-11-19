package model

import (
	"os"
	"strconv"
	"strings"
	"unicode"
)

// CustomerInvoicePolicy scopes v1 invoices to one verified Epay merchant. The
// merchant value never belongs in public status or in customer response data.
type CustomerInvoicePolicy struct {
	Enabled       bool
	MerchantScope string
}

func GetCustomerInvoicePolicy() CustomerInvoicePolicy {
	merchant := strings.TrimSpace(os.Getenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE"))
	if len(merchant) > 128 || strings.IndexFunc(merchant, unicode.IsControl) >= 0 {
		merchant = ""
	}
	enabled, _ := strconv.ParseBool(os.Getenv("CUSTOMER_INVOICE_ENABLED"))
	return CustomerInvoicePolicy{Enabled: enabled && merchant != "", MerchantScope: merchant}
}
