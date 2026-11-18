package controller

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func invoiceFeeControllerFixture(t *testing.T) (interceptionControllerFixture, *model.InvoiceApplication) {
	t.Helper()
	setupEpayCallbackTest(t)
	operation_setting.EpayKey = "isolated-invoice-test-key"
	operation_setting.GetPaymentSetting().EpaySignType = operation_setting.EpaySignTypeMD5
	operation_setting.PayMethods = []map[string]string{{"name": "WeChat Pay", "type": "wxpay"}, {"name": "Alipay", "type": "alipay"}, {"type": "stripe"}}
	oldPrice, oldUnit := operation_setting.Price, common.QuotaPerUnit
	operation_setting.Price, common.QuotaPerUnit = 1, 1000
	t.Cleanup(func() { operation_setting.Price, common.QuotaPerUnit = oldPrice, oldUnit })
	f := setupInterceptionControllerTest(t)
	var version int
	require.NoError(t, f.db.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, 180006, version)
	require.NoError(t, i18n.Init())
	schemas := append(model.CommerceModels(), model.BonusSourceModels()...)
	schemas = append(schemas, &model.TopUp{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.PaymentReceipt{})
	require.NoError(t, f.db.AutoMigrate(schemas...))
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.user.Id).Updates(map[string]any{"quota": 20000, "quota_paid": 20000}).Error)
	config := model.DefaultCommerceConfig()
	config.InvoiceEnabled, config.GeneralFeeBPS = true, 500
	_, err := model.SaveCommerceConfig(t.Context(), f.root.Id, config)
	require.NoError(t, err)
	order := model.TopUp{UserId: f.user.Id, TradeNo: "invoice-fee-controller-order", Status: common.TopUpStatusSuccess, PaidCurrency: "CNY", PaidCurrencyExponent: 2, PaidAmountMinor: 10000, CompleteTime: common.GetTimestamp()}
	require.NoError(t, f.db.Create(&order).Error)
	invoice, err := model.CreateInvoiceApplication(t.Context(), f.user.Id, "invoice-fee-controller-create", model.InvoiceCreate{OrderIDs: []int{order.Id}, Kind: "general", TitleType: "individual", Title: "Example buyer", Email: "invoice@example.com"})
	require.NoError(t, err)
	require.EqualValues(t, 500, invoice.FeeMinor)
	return f, invoice
}

func invoiceFeeOptionsRequest(actorID int, invoiceID int64, query string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/invoices/payment-options"+query, nil)
	c.Set("id", actorID)
	if invoiceID > 0 {
		c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(invoiceID, 10)}}
	}
	GetInvoiceFeePaymentOptions(c)
	return response
}

func TestCommercePostgresInvoiceFeeAPIQuotesWalletAndRefund(t *testing.T) {
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	f, invoice := invoiceFeeControllerFixture(t)
	response := invoiceFeeOptionsRequest(f.user.Id, 0, "?currency=CNY&currency_exponent=2&fee_minor=500")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var quote struct {
		Data struct {
			Methods     []invoicePaymentMethod `json:"methods"`
			WalletQuota string                 `json:"wallet_quota"`
			PaidBalance string                 `json:"wallet_balance_quota"`
			FeeMinor    string                 `json:"fee_minor"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &quote))
	assert.Equal(t, "5000", quote.Data.WalletQuota)
	assert.Equal(t, "20000", quote.Data.PaidBalance)
	assert.Equal(t, "500", quote.Data.FeeMinor)
	require.Len(t, quote.Data.Methods, 3)
	assert.Equal(t, "wallet", quote.Data.Methods[0].Type)
	assert.Equal(t, "wxpay", quote.Data.Methods[1].Type)
	assert.Equal(t, "alipay", quote.Data.Methods[2].Type)
	assert.Equal(t, http.StatusNotFound, invoiceFeeOptionsRequest(f.admin.Id, invoice.ID, "").Code)
	assert.Equal(t, http.StatusBadRequest, invoiceFeeOptionsRequest(f.user.Id, 0, "?fee_minor=-1").Code)
	// An application quote always uses its immutable fee, ignoring query overrides.
	response = invoiceFeeOptionsRequest(f.user.Id, invoice.ID, "?fee_minor=1")
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &quote))
	assert.Equal(t, "500", quote.Data.FeeMinor)
	assert.Equal(t, http.StatusConflict, commerceControllerRequest(f.user.Id, int(invoice.ID), `{"method":"wallet","expected_revision":1,"expected_wallet_quota":"4999"}`, "wallet-quote-stale", PayInvoiceFee).Code)
	assert.Equal(t, http.StatusBadRequest, commerceControllerRequest(f.user.Id, int(invoice.ID), `{"method":"wallet","expected_revision":1,"expected_wallet_quota":"5000"}`, "", PayInvoiceFee).Code)
	body := fmt.Sprintf(`{"method":"wallet","expected_revision":1,"expected_wallet_quota":"5000","fee_minor":"1","user_id":%d}`, f.root.Id)
	response = commerceControllerRequest(f.user.Id, int(invoice.ID), body, "wallet-pay-contract", PayInvoiceFee)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var paid struct {
		Data struct {
			Invoice model.InvoiceApplication `json:"invoice"`
			Payment model.InvoiceFeePayment  `json:"payment"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &paid))
	assert.Equal(t, "submitted", paid.Data.Invoice.Status)
	assert.Equal(t, "paid", paid.Data.Payment.Status)
	assert.EqualValues(t, 500, paid.Data.Payment.AmountMinor)
	assert.Equal(t, 5000, paid.Data.Payment.WalletQuota)
	assert.NotContains(t, response.Body.String(), "request_key")
	assert.NotContains(t, response.Body.String(), "merchant_scope")
	response = commerceControllerRequest(f.user.Id, int(invoice.ID), body, "wallet-pay-contract", PayInvoiceFee)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, http.StatusConflict, commerceControllerRequest(f.root.Id, int(invoice.ID), `{"expected_revision":2,"action":"confirm_fee","receipt_reference":"manual-overwrite"}`, "", ReviewInvoiceApplication).Code)
	response = commerceControllerRequest(f.user.Id, int(invoice.ID), `{"expected_revision":2,"action":"cancel"}`, "", CancelInvoiceApplication)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"fee_refund_status":"refunded"`)
	assert.Equal(t, http.StatusConflict, commerceControllerRequest(f.root.Id, int(invoice.ID), `{"expected_revision":3,"action":"record_fee_refund","receipt_reference":"duplicate-manual-refund"}`, "", ReviewInvoiceApplication).Code)
	balance, _, _, err := model.GetUserAccountBalances(f.user.Id)
	require.NoError(t, err)
	assert.Equal(t, 20000, balance)
	var count int64
	require.NoError(t, f.db.Model(&model.BalanceLedger{}).Where("reference_type = ?", "invoice_fee").Count(&count).Error)
	assert.EqualValues(t, 2, count)
}

func invoiceEpayCallbackRequest(method string, values url.Values) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, "/api/invoices/payments/epay/notify?"+values.Encode(), nil)
	if method == http.MethodPost {
		c.Request = httptest.NewRequest(method, "/api/invoices/payments/epay/notify", strings.NewReader(values.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	InvoiceEpayNotify(c)
	return response
}

func invoiceCallbackValues(fields map[string]string) url.Values {
	values := make(url.Values)
	for key, value := range fields {
		values.Set(key, value)
	}
	return values
}

func TestCommercePostgresInvoiceFeeAPIEpayFormAndVerifiedCallback(t *testing.T) {
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	f, invoice := invoiceFeeControllerFixture(t)
	body := `{"method":"wxpay","expected_revision":1}`
	response := commerceControllerRequest(f.user.Id, int(invoice.ID), body, "online-pay-contract", PayInvoiceFee)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var created struct {
		Data struct {
			Invoice model.InvoiceApplication `json:"invoice"`
			Payment model.InvoiceFeePayment  `json:"payment"`
			URL     string                   `json:"url"`
			Params  map[string]string        `json:"params"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &created))
	assert.Equal(t, "awaiting_fee", created.Data.Invoice.Status)
	assert.Equal(t, "pending", created.Data.Payment.Status)
	assert.Equal(t, "https://pay.example.com/submit.php", created.Data.URL)
	assert.Equal(t, created.Data.Payment.TradeNo, created.Data.Params["out_trade_no"])
	assert.Equal(t, "5.00", created.Data.Params["money"])
	assert.Equal(t, "https://dashboard.example.com/api/invoices/payments/epay/notify", created.Data.Params["notify_url"])
	assert.Equal(t, "https://dashboard.example.com/invoice", created.Data.Params["return_url"])
	verified, err := GetEpayClient().Verify(created.Data.Params)
	require.NoError(t, err)
	assert.True(t, verified.VerifyStatus)
	response = commerceControllerRequest(f.user.Id, int(invoice.ID), body, "online-pay-contract", PayInvoiceFee)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), created.Data.Payment.TradeNo)
	assert.Equal(t, http.StatusConflict, commerceControllerRequest(f.user.Id, int(invoice.ID), `{"method":"wallet","expected_revision":1,"expected_wallet_quota":"5000"}`, "online-change-method", PayInvoiceFee).Code)
	assert.Equal(t, http.StatusConflict, commerceControllerRequest(f.root.Id, int(invoice.ID), `{"expected_revision":1,"action":"confirm_fee","receipt_reference":"manual-reference"}`, "", ReviewInvoiceApplication).Code)
	fields := map[string]string{"type": "wxpay", "money": "5.00", "name": "Invoice service fee", "out_trade_no": created.Data.Payment.TradeNo, "trade_no": strings.Repeat("p", 255), "trade_status": epay.StatusTradeSuccess}
	client := GetEpayClient()
	signed, err := client.SignParams(fields)
	require.NoError(t, err)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, field := range []string{"money", "type", "out_trade_no"} {
			wrong := maps.Clone(fields)
			wrong[field] = "invalid"
			if field == "money" {
				wrong[field] = "6.00"
			}
			mismatch, err := client.SignParams(wrong)
			require.NoError(t, err)
			assert.Equal(t, "fail", invoiceEpayCallbackRequest(method, invoiceCallbackValues(mismatch)).Body.String())
		}
		tampered := maps.Clone(signed)
		tampered["money"] = "500.00"
		assert.Equal(t, "fail", invoiceEpayCallbackRequest(method, invoiceCallbackValues(tampered)).Body.String())
		duplicate := invoiceCallbackValues(signed)
		duplicate.Add("money", "5.00")
		assert.Equal(t, "fail", invoiceEpayCallbackRequest(method, duplicate).Body.String())
	}
	var count int64
	require.NoError(t, f.db.Model(&model.PaymentReceipt{}).Count(&count).Error)
	assert.Zero(t, count)
	// Disabling new payments does not abandon already-created cash liabilities.
	operation_setting.PayMethods = nil
	config, err := model.GetCommerceConfig(t.Context())
	require.NoError(t, err)
	config.InvoiceEnabled = false
	_, err = model.SaveCommerceConfig(t.Context(), f.root.Id, *config)
	require.NoError(t, err)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodGet} {
		response = invoiceEpayCallbackRequest(method, invoiceCallbackValues(signed))
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "success", response.Body.String())
	}
	settled, err := model.GetInvoiceApplication(t.Context(), f.user.Id, invoice.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "submitted", settled.Status)
	assert.Equal(t, "epay:"+created.Data.Payment.TradeNo, settled.FeeReceiptReference)
	require.NotNil(t, settled.FeePayment)
	assert.Equal(t, "paid", settled.FeePayment.Status)
	require.NoError(t, f.db.Model(&model.PaymentReceipt{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	balance, _, _, err := model.GetUserAccountBalances(f.user.Id)
	require.NoError(t, err)
	assert.Equal(t, 20000, balance, "invoice cash receipts must never credit wallet quota")
}
