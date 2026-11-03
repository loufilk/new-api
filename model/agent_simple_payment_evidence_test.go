package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentSimpleVerifiedPaymentCannotFundTwoOrdersOrPurposes(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	for _, reference := range []string{"a07-evidence-first", "a07-evidence-second"} {
		_, err := CreateAgentSimplePaymentOrder(agent, AgentSimplePaymentRequest{RequestID: reference, Name: reference, Quota: 500_000, SalePriceFen: 120, PaymentMethod: "alipay"}, reference, reference+"-synthetic-payment-token-000000000000", reference, "https://pay.example.test", `{}`, "https://example.test")
		require.NoError(t, err)
	}
	facts := simpleVerifiedEpayFacts(t, "one-verified-transaction", "1.20")
	_, err := SettleAgentSimplePaymentByTradeNo("a07-evidence-first", "alipay", "1.20")
	require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict, "missing provider evidence must not deliver a code")
	done, err := SettleAgentSimplePaymentByTradeNo("a07-evidence-first", "alipay", "1.20", facts)
	require.NoError(t, err)
	assert.False(t, done)
	done, err = SettleAgentSimplePaymentByTradeNo("a07-evidence-first", "alipay", "1.20", facts)
	require.NoError(t, err)
	assert.True(t, done)
	_, err = SettleAgentSimplePaymentByTradeNo("a07-evidence-second", "alipay", "1.20", facts)
	require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict)
	_, err = SettleAgentSimplePaymentByTradeNo("a07-evidence-first", "alipay", "1.20", simpleVerifiedEpayFacts(t, "different-transaction", "1.20"))
	require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict, "a replay must still match the original provider transaction")
	recharge, err := CreateAgentSimpleRecharge(agent, AgentSimpleRechargeRequest{RequestID: "a07-evidence-recharge", Amount: "1.33", Kind: "recharge", PaymentMethod: "alipay"}, "a07-evidence-recharge", "https://pay.example.test", map[string]string{})
	require.NoError(t, err)
	_, err = RechargeEpay(recharge.TradeNo, "alipay", "1.20", "", facts)
	require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict, "customer payment evidence cannot fund an agent recharge")
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.CashQuota)
	assert.EqualValues(t, 150_000, account.PaymentPendingQuota)
	var count int64
	require.NoError(t, DB.Model(&AgentPaymentReceipt{}).Count(&count).Error)
	assert.EqualValues(t, 1, count, "failed receipt claims must roll back")
	var second AgentSimplePaymentOrder
	require.NoError(t, DB.Where("trade_no = ?", "a07-evidence-second").First(&second).Error)
	assert.Equal(t, AgentSimplePaymentPending, second.Status)
	assert.Zero(t, second.RedemptionID)
	var topup TopUp
	require.NoError(t, DB.First(&topup, recharge.TopUpID).Error)
	assert.Equal(t, common.TopUpStatusPending, topup.Status)
}
