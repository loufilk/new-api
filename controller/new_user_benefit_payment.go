package controller

import (
	"context"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Only the server's authenticated order query can produce reward evidence.
// Callback/client buyer parameters are never read, even when callback-signed.
func reviewNewUserBenefitPayment(ctx context.Context, tradeNo string, userID int) error {
	order, topUp, err := model.GetNewUserBenefitPaymentReview(ctx, tradeNo, userID)
	if err != nil || order == nil {
		return err
	}
	common.OptionMapRWMutex.RLock()
	client := GetEpayClient()
	common.OptionMapRWMutex.RUnlock()
	if client == nil || strings.TrimSpace(client.Config.PartnerID) != order.MerchantScope || client.BaseUrl.String() != order.PaymentGateway {
		return model.FinalizeNewUserBenefitReward(ctx, tradeNo, "", "query_unavailable", model.VerifiedPaymentFacts{})
	}
	buyer, reason := service.QueryNewUserBenefitZpayOrder(ctx, order.PaymentGateway, client.Config.PartnerID, client.Config.Key, service.ZpayBenefitOrderExpectation{
		OutTradeNo: order.TradeNo, TradeNo: topUp.PaidProviderReference, PaymentMethod: order.PaymentMethod, PaidCents: order.PayAmountCents,
	})
	evidence := model.VerifiedPaymentFacts{Provider: model.PaymentProviderEpay, MerchantScope: order.MerchantScope,
		Environment: "live", Currency: "CNY", CurrencyExponent: 2, PaidMinor: order.PayAmountCents, ExternalReference: topUp.PaidProviderReference}
	return model.FinalizeNewUserBenefitReward(ctx, tradeNo, buyer, reason, evidence)
}

func RecheckNewUserBenefitPayment(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		writeNewUserBenefitError(c, model.ErrNewUserBenefitIneligible)
		return
	}
	state, err := model.GetNewUserBenefitState(c.Request.Context(), userID)
	if err != nil {
		writeNewUserBenefitError(c, err)
		return
	}
	if state.Order == nil {
		writeNewUserBenefitError(c, model.ErrNewUserBenefitIneligible)
		return
	}
	if state.Status == model.NewUserBenefitStatusVerificationPending {
		if err := reviewNewUserBenefitPayment(c.Request.Context(), state.Order.TradeNo, userID); err != nil {
			writeNewUserBenefitError(c, err)
			return
		}
	}
	GetNewUserBenefit(c)
}
