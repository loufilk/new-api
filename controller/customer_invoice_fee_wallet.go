package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Financial consent must contain one unambiguous positive value for each
// field. Go's ordinary object decoder otherwise accepts repeated fields.
type invoiceWalletQuoteNumber int64
type invoiceWalletQuoteString string

func (s *invoiceWalletQuoteString) UnmarshalJSON(data []byte) error {
	if *s != "" || len(data) < 3 || data[0] != '"' || data[len(data)-1] != '"' {
		return model.ErrCustomerInvoiceInvalid
	}
	var n invoiceWalletQuoteNumber
	if err := n.UnmarshalJSON(data[1 : len(data)-1]); err != nil {
		return err
	}
	*s = invoiceWalletQuoteString(data[1 : len(data)-1])
	return nil
}

func (n *invoiceWalletQuoteNumber) UnmarshalJSON(data []byte) error {
	s := string(data)
	v, err := strconv.ParseInt(s, 10, 64)
	if *n != 0 || err != nil || v <= 0 || v > common.MaxWalletQuota || strconv.FormatInt(v, 10) != s {
		return model.ErrCustomerInvoiceInvalid
	}
	*n = invoiceWalletQuoteNumber(v)
	return nil
}

func PayCustomerInvoiceWalletFee(c *gin.Context) {
	id, ok := customerInvoiceID(c)
	if !ok {
		return
	}
	var quote struct {
		FeeMinor     invoiceWalletQuoteString `json:"fee_minor"`
		WalletQuota  invoiceWalletQuoteString `json:"wallet_quota"`
		QuotaPerCent invoiceWalletQuoteNumber `json:"wallet_quota_per_cent"`
	}
	if !readCustomerInvoiceJSON(c, &quote) {
		return
	}
	if quote.FeeMinor == "" || quote.WalletQuota == "" || quote.QuotaPerCent == 0 {
		customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		return
	}
	confirm := model.CustomerInvoiceWalletConfirmation{FeeMinor: string(quote.FeeMinor), WalletQuota: string(quote.WalletQuota), WalletQuotaPerCent: int64(quote.QuotaPerCent)}
	policy := model.GetCustomerInvoicePolicy()
	policy.WalletFeeAvailable = invoiceWalletPaymentAvailable() > 0
	detail, err := model.PayCustomerInvoiceWalletFee(c.Request.Context(), c.GetInt("id"), policy, id, confirm)
	if errors.Is(err, model.ErrLedgerInsufficientBalance) {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Insufficient balance"), gin.H{"code": "INVOICE_WALLET_INSUFFICIENT"})
		return
	}
	respondCustomerInvoice(c, detail, err)
}
