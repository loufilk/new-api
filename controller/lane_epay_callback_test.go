package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLaneEpayWebhookUsesVerifiedDurableOutcomeOnly(t *testing.T) {
	setupEpayCallbackTest(t) // nil model.DB catches an ordinary-fallback mistake
	operation_setting.PayAddress = "http://127.0.0.1:1/"
	operation_setting.EpayKey = "synthetic-lane-signer"
	operation_setting.GetPaymentSetting().EpaySignType = operation_setting.EpaySignTypeMD5
	client := GetEpayClient()
	require.NotNil(t, client)
	cases := []struct {
		name, status, method, want string
		out                        model.LaneEpayOutcome
		err                        error
		mutate                     func(url.Values)
		wantCall                   bool
		ordinary                   bool
	}{
		{"durable waiting", epay.StatusTradeSuccess, "POST", "success", model.LaneEpayOutcome{State: model.LaneEpayCapturedWaiting, ReceiptID: 11}, nil, nil, true, false},
		{"durable replay credited", epay.StatusTradeSuccess, "GET", "success", model.LaneEpayOutcome{State: model.LaneEpayCredited, ReceiptID: 11, Replayed: true}, nil, nil, true, false},
		{"transaction failure", epay.StatusTradeSuccess, "POST", "fail", model.LaneEpayOutcome{State: model.LaneEpayCapturedWaiting, ReceiptID: 11}, errors.New("synthetic DB failure"), nil, true, false},
		{"commit uncertain", epay.StatusTradeSuccess, "GET", "fail", model.LaneEpayOutcome{State: model.LaneEpayCredited, ReceiptID: 11}, model.ErrLaneStoreCommitUncertain, nil, true, false},
		{"missing receipt ID", epay.StatusTradeSuccess, "POST", "fail", model.LaneEpayOutcome{State: model.LaneEpayCapturedWaiting}, nil, nil, true, false},
		{"invalid state", epay.StatusTradeSuccess, "GET", "fail", model.LaneEpayOutcome{State: "pending", ReceiptID: 11}, nil, nil, true, false},
		{"nonterminal scoped", "WAIT_BUYER_PAY", "GET", "fail", model.LaneEpayOutcome{}, nil, nil, false, false},
		{"nonterminal ordinary unchanged", "WAIT_BUYER_PAY", "POST", "success", model.LaneEpayOutcome{}, nil, nil, false, true},
		{"bad signature", epay.StatusTradeSuccess, "POST", "fail", model.LaneEpayOutcome{}, nil, func(f url.Values) { f.Set("money", "0.01") }, false, false},
		{"duplicate reference", epay.StatusTradeSuccess, "GET", "fail", model.LaneEpayOutcome{}, nil, func(f url.Values) { f.Add("trade_no", "other") }, false, false},
		{"duplicate amount in body", epay.StatusTradeSuccess, "POST", "fail", model.LaneEpayOutcome{}, nil, func(f url.Values) { f.Add("money", "2.00") }, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signed, err := client.SignParams(map[string]string{"out_trade_no": "SYNTHETIC_BOUND_ORDER",
				"type": "alipay", "trade_no": "SYNTHETIC_CAPTURE", "trade_status": tc.status, "money": "1.00"})
			require.NoError(t, err)
			values := url.Values{}
			for key, value := range signed {
				values.Set(key, value)
			}
			if tc.mutate != nil {
				tc.mutate(values)
			}
			calls := 0
			router := gin.New()
			handler := func(c *gin.Context) {
				epayNotify(c, func(ctx context.Context, capture model.LaneEpayVerifiedNotification) (model.LaneEpayOutcome, error) {
					calls++
					require.NotNil(t, ctx)
					require.Equal(t, "SYNTHETIC_BOUND_ORDER", capture.TradeNo)
					require.Equal(t, "alipay", capture.Method)
					require.Equal(t, "SYNTHETIC_CAPTURE", capture.Facts.ExternalReference)
					require.Equal(t, "epay_id", capture.Facts.MerchantScope)
					require.EqualValues(t, 100, capture.Facts.PaidMinor)
					return tc.out, tc.err
				})
			}
			if tc.ordinary {
				handler = EpayNotify
			}
			router.Any("/notify", handler)
			req := httptest.NewRequest(tc.method, "/notify?"+values.Encode(), nil)
			if tc.method == "POST" {
				req = httptest.NewRequest("POST", "/notify", strings.NewReader(values.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, tc.want, w.Body.String())
			if tc.wantCall {
				require.Equal(t, 1, calls)
			} else {
				require.Zero(t, calls)
			}
		})
	}
}
