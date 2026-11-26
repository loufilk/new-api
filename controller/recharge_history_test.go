package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRechargeHistoryFiltersOnlyOwnersOrders(t *testing.T) {
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.USDTTopUpOrder{}))
	const owner, other = 17, 18
	// Every recent row except one uses exactly the same timestamp, including
	// both source tables. The last page must not repeat or omit orders.
	now := time.Now().Unix()
	old := now - 31*24*60*60
	for _, row := range []model.TopUp{
		{UserId: owner, TradeNo: "FIAT_1", PaymentMethod: "alipay", PaymentProvider: "epay", CreateTime: now},
		{UserId: owner, TradeNo: "equal-id", PaymentMethod: "alipay", PaymentProvider: "epay", CreateTime: now},
		{UserId: owner, TradeNo: "FIAT_2", PaymentMethod: "wxpay", PaymentProvider: "epay", CreateTime: now},
		{UserId: owner, TradeNo: "FIAT_OLD", PaymentMethod: "stripe", CreateTime: old},
		{UserId: other, TradeNo: "FIAT_OTHER", PaymentMethod: "stripe", CreateTime: now},
	} {
		require.NoError(t, db.Create(&row).Error)
	}
	orders := make([]model.USDTTopUpOrder, 0, 56)
	for i := range 53 {
		id := fmt.Sprintf("USDT_%03d", i)
		orders = append(orders, model.USDTTopUpOrder{ID: id, UserID: owner, RequestKey: id, CreatedAt: now, Status: model.USDTPending, ExpiresAt: now - 1})
	}
	orders = append(orders,
		model.USDTTopUpOrder{ID: "equal-id", UserID: owner, RequestKey: "same-visible-id", CreatedAt: now, Status: model.USDTPaid, Network: "tron", AmountMinor: 10025, BonusMinor: 100, USDTAmount: "14.234501", GatewayMerchant: "private-merchant", Address: "private-address"},
		model.USDTTopUpOrder{ID: "USDT_OLDER_THAN_FIAT_WINDOW", UserID: owner, RequestKey: "old", CreatedAt: old, Status: model.USDTExpired},
		model.USDTTopUpOrder{ID: "USDT_OTHER", UserID: other, RequestKey: "other-account", CreatedAt: now, Status: model.USDTFailed},
	)
	require.NoError(t, db.CreateInBatches(orders, 20).Error)

	mux := gin.New()
	mux.Use(middleware.DisableCache(), func(c *gin.Context) {
		userID, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
		c.Set("id", userID)
	})
	mux.GET("/api/user/topup/history", GetRechargeHistory)
	fetch := func(query string, id int) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/user/topup/history?"+query, nil)
		request.Header.Set("X-Test-User", strconv.Itoa(id))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}
	read := func(query string, id int) model.RechargeHistoryPage {
		t.Helper()
		response := fetch(query, id)
		require.Equal(t, 200, response.Code)
		require.Contains(t, response.Header().Get("Cache-Control"), "no-store")
		require.NotContains(t, response.Body.String(), "private-merchant")
		require.NotContains(t, response.Body.String(), "private-address")
		require.NotContains(t, response.Body.String(), "payment_receipt_id")
		require.NotContains(t, response.Body.String(), "user_id")
		var body struct {
			Success bool                      `json:"success"`
			Data    model.RechargeHistoryPage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.True(t, body.Success, response.Body.String())
		return body.Data
	}

	require.Equal(t, http.StatusUnauthorized, fetch("", 0).Code)
	require.Equal(t, http.StatusBadRequest, fetch("order_no=%00", owner).Code)
	require.EqualValues(t, 2, read("", other).Total)

	seen := map[string]bool{}
	for page := 1; page <= 6; page++ {
		data := read(fmt.Sprintf("p=%d&page_size=10&user_id=%d", page, other), owner)
		require.EqualValues(t, 58, data.Total)
		require.ElementsMatch(t, []string{"fiat:alipay", "fiat:wxpay", "usdt"}, data.Methods)
		for _, row := range data.Items {
			source := "usdt:"
			if row.Fiat != nil {
				require.Nil(t, row.USDT)
				source = "fiat:"
			} else {
				require.NotNil(t, row.USDT)
			}
			require.False(t, seen[source+row.OrderNo])
			seen[source+row.OrderNo] = true
		}
	}
	require.Len(t, seen, 58)
	require.Contains(t, seen, "usdt:USDT_OLDER_THAN_FIAT_WINDOW")
	require.NotContains(t, seen, "fiat:FIAT_OLD")

	for _, orderNo := range []string{"USDT_OTHER", "FIAT_OTHER", "FIAT_OLD", "FIAT%25"} {
		require.Empty(t, read("order_no="+orderNo, owner).Items)
	}
	require.EqualValues(t, 2, read("method=fiat%3Aalipay", owner).Total)
	require.Zero(t, read("method=fiat%3Aepay", owner).Total)
	require.Zero(t, read("method=usdt&order_no=FIAT_1", owner).Total)
	require.EqualValues(t, 55, read("method=usdt", owner).Total)
	require.Equal(t, "USDT_OLDER_THAN_FIAT_WINDOW", read("method=usdt&order_no=USDT_OLDER_THAN_FIAT_WINDOW", owner).Items[0].OrderNo)
	paid := read("order_no=equal-id&method=usdt", owner)
	require.Len(t, paid.Items, 1)
	require.Equal(t, &model.RechargeHistoryUSDT{Network: "tron", Status: "paid", QuoteUSDT: "14.234501", WalletCreditCNY: "101.25"}, paid.Items[0].USDT)
	expired := read("order_no=USDT_002", owner)
	require.Len(t, expired.Items, 1)
	require.Equal(t, model.USDTExpired, expired.Items[0].USDT.Status)
	require.Empty(t, expired.Items[0].USDT.WalletCreditCNY)
	var stored model.USDTTopUpOrder
	require.NoError(t, db.First(&stored, "id = ?", "USDT_002").Error)
	require.Equal(t, model.USDTPending, stored.Status)
}
