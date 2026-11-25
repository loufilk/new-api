package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func testUSDTSign(fields map[string]any, secret string) string {
	keys := make([]string, 0, len(fields))
	for k, v := range fields {
		if k != "signature" && v != "" && v != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	mac := hmac.New(sha256.New, []byte(secret))
	for i, k := range keys {
		if i > 0 {
			fmt.Fprint(mac, "&")
		}
		fmt.Fprintf(mac, "%s=%v", k, fields[k])
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func TestUSDTWalletRoutesSettleExactlyOnce(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	schemas := []any{&model.USDTTopUpOrder{}, &model.USDTTopUpConfig{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}}
	schemas = append(schemas, model.BonusSourceModels()...)
	require.NoError(t, db.AutoMigrate(schemas...))
	require.NoError(t, db.Create(&model.User{Id: 407, Username: "safe-wallet-test", AffCode: "usdt-test-aff", Status: common.UserStatusEnabled, Role: common.RoleCommonUser}).Error)
	q := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	defer func() { common.QuotaPerUnit = q }()
	const secret = "fake-fixture-testing-only"
	const receiver = "0x1111111111111111111111111111111111111111"
	const tradeID = "mock-gmpay-trade"
	var created atomic.Int32
	var paid atomic.Int32
	paid.Store(1)
	expires := time.Now().Unix() + 300
	var gateway *httptest.Server
	gateway = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/payments/gmpay/v1/order/create-transaction":
			created.Add(1)
			var f map[string]any
			if json.NewDecoder(r.Body).Decode(&f) != nil || f["signature"] != testUSDTSign(f, secret) || f["notify_url"] != "https://merchant.example/api/user/usdt/notify" || f["network"] != "binance" || f["token"] != "usdt" || f["callback_version"] != float64(2) {
				http.Error(w, "bad fixture request", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "data": map[string]any{
				"trade_id": tradeID, "order_id": f["order_id"], "amount": 100.25, "actual_amount": 14.23, "receive_address": receiver,
				"currency": "CNY", "token": "USDT", "status": 1, "expiration_time": expires, "payment_url": gateway.URL + "/pay/checkout-counter/" + tradeID,
			}})
		case "/pay/checkout-counter-resp/" + tradeID:
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "data": map[string]any{
				"trade_id": tradeID, "amount": 100.25, "actual_amount": 14.23, "receive_address": receiver, "network": "binance",
				"currency": "CNY", "token": "USDT", "payment_type": "gmpay", "expiration_time": expires * 1000,
			}})
		case "/pay/check-status/" + tradeID:
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "data": map[string]any{"trade_id": tradeID, "status": paid.Load()}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = gateway.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	for k, v := range map[string]string{"USDT_GMPAY_ORIGIN": gateway.URL, "USDT_GMPAY_PID": "1000", "USDT_GMPAY_SECRET": secret, "USDT_CALLBACK_ORIGIN": "https://merchant.example"} {
		t.Setenv(k, v)
	}
	p := operation_setting.GetPaymentSetting()
	before, beforeVersion := p.ComplianceConfirmed, p.ComplianceTermsVersion
	p.ComplianceConfirmed, p.ComplianceTermsVersion = true, operation_setting.CurrentComplianceTermsVersion
	defer func() { p.ComplianceConfirmed, p.ComplianceTermsVersion = before, beforeVersion }()
	gin.SetMode(gin.TestMode)
	mux := gin.New()
	mux.Use(func(c *gin.Context) {
		id := 407
		if c.GetHeader("X-Test-Other") == "1" {
			id = 408
		}
		c.Set("id", id)
	})
	mux.GET("/api/user/usdt/options", GetUSDTOptions)
	mux.POST("/api/user/usdt/orders", USDTCreateOrder)
	mux.GET("/api/user/usdt/orders/:id", GetUSDTOrder)
	mux.GET("/api/user/usdt/orders", ListUSDTOrders)
	mux.POST("/api/user/usdt/notify", USDTNotify)
	mux.PUT("/api/option/usdt", PutUSDTConfig)
	makeReq := func(method, path string, payload any, other bool) *httptest.ResponseRecorder {
		var r io.Reader
		if payload != nil {
			b, err := json.Marshal(payload)
			require.NoError(t, err)
			r = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, r)
		req.Header.Set("Content-Type", "application/json")
		if other {
			req.Header.Set("X-Test-Other", "1")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.NotContains(t, w.Body.String(), secret)
		return w
	}
	envelope := func(w *httptest.ResponseRecorder) map[string]any {
		require.Equal(t, 200, w.Code, w.Body.String())
		var body struct {
			Success bool
			Data    map[string]any
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.True(t, body.Success, w.Body.String())
		return body.Data
	}
	opts := envelope(makeReq("GET", "/api/user/usdt/options", nil, false))
	require.Equal(t, false, opts["enabled"])
	require.NotContains(t, opts, "checkout_origin")
	config := usdtConfig{Enabled: true, MinAmount: "10.00", MaxAmount: "1000.00", Networks: []usdtNetworkConfig{
		{ID: "bsc", Enabled: true, BonusBPS: 1000}, {ID: "polygon"}, {ID: "tron"},
	}}
	require.Equal(t, true, envelope(makeReq("PUT", "/api/option/usdt", config, false))["enabled"])
	f := map[string]any{"network": "bsc", "amount": "100.25", "idempotency_key": "sample-wallet-order-key"}
	order := envelope(makeReq("POST", "/api/user/usdt/orders", f, false))
	require.Equal(t, "pending", order["status"])
	require.Equal(t, "100.25", order["wallet_credit"])
	require.Equal(t, "10.02", order["bonus_credit"])
	require.Equal(t, "14.23", order["usdt_amount"])
	require.Equal(t, receiver, order["address"])
	require.NotContains(t, order, "checkout_url")
	require.NotContains(t, order, "gateway_trade_id")
	id := order["id"].(string)
	require.Equal(t, id, envelope(makeReq("POST", "/api/user/usdt/orders", f, false))["id"])
	require.Equal(t, int32(1), created.Load())
	require.Equal(t, 404, makeReq("GET", "/api/user/usdt/orders/"+id, nil, true).Code)
	require.Equal(t, 200, makeReq("GET", "/api/user/usdt/orders", nil, false).Code)
	require.Equal(t, 400, makeReq("POST", "/api/user/usdt/orders", map[string]any{"network": "tron", "amount": "100.00", "idempotency_key": "another-sample-wallet-key"}, false).Code)
	f["amount"] = "101"
	require.Equal(t, 409, makeReq("POST", "/api/user/usdt/orders", f, false).Code)
	config.Enabled = false
	require.Equal(t, false, envelope(makeReq("PUT", "/api/option/usdt", config, false))["enabled"])
	f["amount"] = "100.25"
	require.Equal(t, id, envelope(makeReq("POST", "/api/user/usdt/orders", f, false))["id"])
	callback := map[string]any{"pid": "1000", "trade_id": tradeID, "order_id": id, "amount": 100.25, "actual_amount": 14.23, "receive_address": receiver, "token": "USDT",
		"status": 2, "block_transaction_id": "0x" + strings.Repeat("AB", 32), "signature": strings.Repeat("0", 64),
	}
	require.Equal(t, 400, makeReq("POST", "/api/user/usdt/notify", callback, false).Code)
	callback["signature"] = testUSDTSign(callback, secret)
	require.Equal(t, 400, makeReq("POST", "/api/user/usdt/notify", callback, false).Code, "signed but network-unbound legacy callback")
	callback["network"] = "binance"
	callback["signature"] = testUSDTSign(callback, secret)
	require.Equal(t, 503, makeReq("POST", "/api/user/usdt/notify", callback, false).Code)
	paid.Store(2)
	require.Equal(t, 200, makeReq("POST", "/api/user/usdt/notify", callback, false).Code)
	require.Equal(t, 200, makeReq("POST", "/api/user/usdt/notify", callback, false).Code)
	require.Equal(t, "paid", envelope(makeReq("GET", "/api/user/usdt/orders/"+id, nil, false))["status"])
	var user model.User
	require.NoError(t, db.First(&user, 407).Error)
	require.Equal(t, 50125000, user.QuotaPaid)
	require.Equal(t, 5010000, user.QuotaBonus)
	require.Equal(t, user.QuotaPaid+user.QuotaBonus, user.Quota)
	var n int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Where("user_id = ?", 407).Count(&n).Error)
	require.EqualValues(t, 1, n)
}
