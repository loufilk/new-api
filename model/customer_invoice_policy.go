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
	// CenterVisible controls navigation; intake also requires a configured merchant.
	CenterVisible      bool
	Enabled            bool
	MerchantScope      string
	EpayFeeAvailable   bool
	WalletFeeAvailable bool
}

func GetCustomerInvoicePolicy() CustomerInvoicePolicy {
	merchant := strings.TrimSpace(os.Getenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE"))
	if len(merchant) > 128 || strings.IndexFunc(merchant, unicode.IsControl) >= 0 {
		merchant = ""
	}
	flag := strings.TrimSpace(os.Getenv("CUSTOMER_INVOICE_ENABLED"))
	visible := flag == ""
	if flag != "" {
		visible, _ = strconv.ParseBool(flag)
	}
	return CustomerInvoicePolicy{CenterVisible: visible, Enabled: visible && merchant != "", MerchantScope: merchant}
}
