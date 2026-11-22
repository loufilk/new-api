package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCustomerInvoiceWalletHTTPRequiresExactExplicitQuote(t *testing.T) {
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", "")
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"fee_minor":"12","wallet_quota":"36","wallet_quota_per_cent":3}`, http.StatusForbidden}, // valid wire shape reaches merchant policy
		{`{"fee_minor":12,"wallet_quota":"36","wallet_quota_per_cent":3}`, http.StatusBadRequest},
		{`{"fee_minor":"12","wallet_quota":"36","wallet_quota_per_cent":"3"}`, http.StatusBadRequest},
		{`{"fee_minor":"12","wallet_quota":"36"}`, http.StatusBadRequest},
		{`{"fee_minor":"1","fee_minor":"12","wallet_quota":"36","wallet_quota_per_cent":3}`, http.StatusBadRequest},
		{`{"fee_minor":"12","wallet_quota":"36","wallet_quota_per_cent":3,"wallet_quota_per_cent":3}`, http.StatusBadRequest},
		{`{"fee_minor":"12","wallet_quota":null,"wallet_quota_per_cent":3}`, http.StatusBadRequest},
		{`{"fee_minor":"12","wallet_quota":"036","wallet_quota_per_cent":3}`, http.StatusBadRequest},
		{`{"fee_minor":"12","wallet_quota":"36","wallet_quota_per_cent":3,"account":"bonus"}`, http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/invoice/applications/1/fee/wallet", strings.NewReader(tc.body))
		c.Request.Header.Set("Content-Type", "application/json")
		PayCustomerInvoiceWalletFee(c)
		require.Equal(t, tc.status, w.Code, tc.body)
	}
}
