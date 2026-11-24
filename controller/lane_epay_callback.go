package controller

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type laneEpayCallbackScope func(context.Context, model.LaneEpayVerifiedNotification) (model.LaneEpayOutcome, error)

func singleValuedLaneEpayForm(req *http.Request) bool {
	var form url.Values
	switch req.Method {
	case http.MethodPost:
		if req.URL.RawQuery != "" {
			return false
		}
		form = req.PostForm
	case http.MethodGet:
		var err error
		form, err = url.ParseQuery(req.URL.RawQuery)
		if err != nil {
			return false
		}
	default:
		return false
	}
	for _, values := range form {
		if len(values) != 1 {
			return false
		}
	}
	return true
}

// Only receives already signature/merchant-verified input. "success" for
// the waiting state would acknowledge durable receipt, not wallet credit.
// No decision here is authority to register a live provider callback.
func writeBoundLaneEpayAcknowledgement(c *gin.Context, scope laneEpayCallbackScope, verified *epay.VerifyRes, params map[string]string) {
	if verified == nil || !verified.VerifyStatus || verified.TradeStatus != epay.StatusTradeSuccess {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	facts, ok := model.NewVerifiedPaymentFacts(model.PaymentProviderEpay, params["trade_no"], params["pid"], "live", "CNY", verified.Money, false)
	if !ok {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	outcome, err := scope(c.Request.Context(), model.LaneEpayVerifiedNotification{
		TradeNo: verified.ServiceTradeNo, Method: verified.Type, Money: verified.Money, Facts: facts,
	})
	// Do not acknowledge uncertainty, SQL failure, missing receipt or an
	// accidental result from an ordinary or unbound order.
	if err != nil || outcome.ReceiptID <= 0 ||
		(outcome.State != model.LaneEpayCapturedWaiting && outcome.State != model.LaneEpayCredited) {
		logger.LogWarn(c.Request.Context(), "Bound Epay capture not acknowledged")
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	if outcome.State == model.LaneEpayCapturedWaiting {
		logger.LogInfo(c.Request.Context(), "Verified Epay receipt durable; wallet credit waiting")
	}
	_, _ = c.Writer.Write([]byte("success"))
}
