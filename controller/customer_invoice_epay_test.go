package controller

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All configured URLs are synthetic. The SDK signs the form locally and no
// test follows or submits it, nor does any test contact a payment provider.
func TestCustomerInvoiceEpayHTTPPostgres(t *testing.T) {
	db := setupNewUserBenefitControllerPostgres(t)
	require.NoError(t, db.AutoMigrate(model.CustomerInvoiceModels()...))
	require.NoError(t, db.AutoMigrate(&model.AgentCommissionRefundFact{}, &model.ReferralCommission{}))
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "true")
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", operation_setting.EpayId)
	u := createNewUserBenefitControllerUser(t, db, "invoice-fee", false)
	a := model.CustomerInvoiceApplication{UserID: u.Id, IdempotencyKey: "test-1", MerchantScope: operation_setting.EpayId,
		Status: "awaiting_payment", FeeState: "unpaid", InvoiceType: "ordinary", TitleType: "company",
		AmountMinor: 1234, FeeRateBPS: 1000, FeeMinor: 123, OrderCount: 1}
	require.NoError(t, db.Create(&a).Error)
	now := common.GetTimestamp()
	original, err := model.ClaimPaymentReceiptTx(db, &model.PaymentReceipt{Provider: model.PaymentProviderEpay, ProviderReference: "initial-payment", LocalOrderKey: "original-recharge",
		MerchantScope: a.MerchantScope, Environment: "live", Purpose: "topup", OwnerUserID: u.Id, Currency: "CNY", CurrencyExponent: 2, PaidMinor: 1234, PaidAt: now - 120, VerifiedAt: now - 60})
	require.NoError(t, err)
	top := model.TopUp{TradeNo: original.LocalOrderKey, UserId: u.Id, PaymentProvider: original.Provider, PaymentReceiptID: original.ID, PaidProviderReference: original.ProviderReference,
		PaidMerchantScope: original.MerchantScope, PaidEnvironment: "live", PaidCurrency: "CNY", PaidCurrencyExponent: 2, PaidAmountMinor: 1234, Status: common.TopUpStatusSuccess, CompleteTime: now - 60}
	require.NoError(t, db.Create(&top).Error)
	require.NoError(t, db.Create(&model.CustomerInvoiceItem{ApplicationID: a.ID, TopUpID: top.Id, ActiveTopUpID: &top.Id, ReceiptID: original.ID, Reference: top.TradeNo, PaidAt: original.PaidAt, AmountMinor: 1234}).Error)
	operation_setting.PayMethods = append(operation_setting.PayMethods,
		map[string]string{"type": "paid_wallet", "name": "wallet collision"}, map[string]string{"type": "creem", "name": "unsupported"},
		map[string]string{"type": "alipay", "name": "duplicate"})
	require.Equal(t, []map[string]string{{"type": "alipay", "name": "支付宝"}, {"type": "wxpay", "name": "微信"}}, CustomerInvoiceEpayMethods())
	start := func(user int, payload string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", user)
		c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(a.ID, 10)}}
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
		c.Request.Header.Set("Content-Type", "application/json")
		BeginCustomerInvoiceEpay(c)
		return w
	}
	const req = `{"payment_method":"wxpay"}`
	assert.Equal(t, http.StatusBadRequest, start(u.Id, `{"payment_method":"stripe"}`).Code)
	assert.Equal(t, http.StatusBadRequest, start(u.Id, `{"payment_method":"wxpay","money":"0.01"}`).Code)
	assert.Equal(t, http.StatusNotFound, start(u.Id+1, req).Code)
	w := start(u.Id, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Success bool
		Data    struct {
			URL    string
			Params map[string]string
		}
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success, w.Body.String())
	p := response.Data.Params
	assert.Equal(t, "https://pay.example.test/submit.php", response.Data.URL)
	assert.Equal(t, "1.23", p["money"])
	assert.Equal(t, "wxpay", p["type"])
	assert.Equal(t, "https://console.example.test"+customerInvoiceEpayNotifyPath, p["notify_url"])
	assert.Equal(t, "https://console.example.test/api/invoice/fee/epay/return", p["return_url"])
	require.Regexp(t, `^CIF[0-9a-f]{29}$`, p["out_trade_no"])
	verified, err := GetEpayClient().Verify(p)
	require.NoError(t, err)
	assert.True(t, verified.VerifyStatus)
	replay := start(u.Id, req)
	assert.Contains(t, replay.Body.String(), p["out_trade_no"])

	notify := func(raw, method string) string {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		if method == http.MethodPost {
			c.Request = httptest.NewRequest(method, customerInvoiceEpayNotifyPath, strings.NewReader(raw))
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			c.Request = httptest.NewRequest(method, customerInvoiceEpayNotifyPath+"?"+raw, nil)
		}
		CustomerInvoiceEpayNotify(c)
		return w.Body.String()
	}
	signed := func(input map[string]string) url.Values {
		s, err := GetEpayClient().SignParams(input)
		require.NoError(t, err)
		v := url.Values{}
		for k, value := range s {
			v.Set(k, value)
		}
		return v
	}
	data := map[string]string{"trade_no": "synthetic-transaction", "out_trade_no": p["out_trade_no"], "type": "wxpay", "money": "1.23", "trade_status": epay.StatusTradeSuccess}
	wrong := maps.Clone(data)
	wrong["money"] = "1.24"
	assert.Equal(t, "fail", notify(signed(wrong).Encode(), http.MethodPost), "a valid signature is not an exact price match")
	v := signed(data)
	wrongSig := url.Values(maps.Clone(v))
	wrongSig.Set("sign", "incorrect")
	assert.Equal(t, "fail", notify(wrongSig.Encode(), http.MethodPost))
	assert.Equal(t, "fail", notify(v.Encode()+"&money=1.23", http.MethodGet))
	for _, extra := range []string{"&unexpected%3Dname=1", "&x=a%26money%3D9", "&x=%00", "&x=%FF", "&MONEY=1.23"} {
		assert.Equal(t, "fail", notify(v.Encode()+extra, http.MethodPost))
	}
	originalGateway := operation_setting.PayAddress
	operation_setting.PayAddress = "https://second-gateway.example.test"
	assert.Equal(t, "fail", notify(v.Encode(), http.MethodGet), "the same PID on a new gateway is not the original merchant binding")
	operation_setting.PayAddress = originalGateway
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "false")
	operation_setting.PayMethods = nil
	assert.Empty(t, CustomerInvoiceEpayMethods())
	assert.Equal(t, "success", notify(v.Encode(), http.MethodPost), "disabling new checkouts cannot forfeit previously paid cash")
	assert.Equal(t, "success", notify(v.Encode(), http.MethodGet), "provider retries must be idempotent")
	var paid model.CustomerInvoiceApplication
	require.NoError(t, db.First(&paid, a.ID).Error)
	assert.Equal(t, "pending", paid.Status)
	assert.Equal(t, "paid", paid.FeeState)
	assert.EqualValues(t, 123, paid.FeePaidMinor)
	var r model.PaymentReceipt
	require.NoError(t, db.First(&r, paid.FeeReceiptID).Error)
	assert.Equal(t, "invoice_fee", r.Purpose)
	assert.Equal(t, u.Id, r.OwnerUserID)
	assert.EqualValues(t, 123, r.PaidMinor)
	var count int64
	require.NoError(t, db.Model(&model.TopUp{}).Count(&count).Error)
	assert.EqualValues(t, 1, count, "original recharge is the only top-up")
	require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&count).Error)
	assert.Zero(t, count, "external fee must not change the wallet")
	require.NoError(t, db.Model(&model.PaymentReceipt{}).Where("purpose = ?", "invoice_fee").Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestCustomerInvoiceEpayReturnIsNotPaymentProof(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?trade_status=TRADE_SUCCESS&sign=synthetic", nil)
	CustomerInvoiceEpayReturn(c)
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/invoice", w.Header().Get("Location"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
}
