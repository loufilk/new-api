package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentSimpleQuoteExplainsMinimumAndRejectsStaleFeeAtCreation(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	_, err := UpdateAgentSimplePaymentFee(root, AgentSimplePaymentFeeRequest{RequestID: "a07-quote-fee", PaymentMethod: "alipay", FeeBPS: 100, Enabled: true, Reason: "Synthetic channel fee"})
	require.NoError(t, err)
	quote, err := QuoteAgentSimplePayment("alipay", 500_000, 90)
	require.ErrorIs(t, err, ErrAgentSimpleInvalid)
	require.NotNil(t, quote)
	assert.EqualValues(t, 91, quote.MinimumSalePriceFen)
	assert.EqualValues(t, 90, quote.CostPriceFen)
	assert.EqualValues(t, 1, quote.FeeFen)
	assert.False(t, quote.PriceValid)
	quote, err = QuoteAgentSimplePayment("alipay", 500_000, 120)
	require.NoError(t, err)
	assert.True(t, quote.PriceValid)
	assert.EqualValues(t, 2, quote.FeeFen)
	assert.EqualValues(t, 140_000, quote.ProfitQuota)
	_, err = UpdateAgentSimplePaymentFee(root, AgentSimplePaymentFeeRequest{RequestID: "a07-quote-fee-change", PaymentMethod: "alipay", FeeBPS: 200, Enabled: true, Reason: "Synthetic changed fee"})
	require.NoError(t, err)
	request := AgentSimplePaymentRequest{RequestID: "a07-quote-create", Name: "Quoted code", Quota: 500_000, SalePriceFen: 120, PaymentMethod: "alipay", FeeVersion: quote.FeeVersion}
	_, err = CreateAgentSimplePaymentOrder(agent, request, "a07-quote-public", "synthetic-quoted-payment-token-00000000000000000000000", "a07-quote-trade", "https://pay.example.test", `{}`, "https://example.test")
	require.ErrorIs(t, err, ErrAgentSimpleConflict, "changed costs require an updated preview")
	var count int64
	require.NoError(t, DB.Model(&AgentSimplePaymentOrder{}).Count(&count).Error)
	assert.Zero(t, count)
	quote, err = QuoteAgentSimplePayment("alipay", 500_000, 120)
	require.NoError(t, err)
	request.FeeVersion = quote.FeeVersion
	order, err := CreateAgentSimplePaymentOrder(agent, request, "a07-quote-public", "synthetic-quoted-payment-token-00000000000000000000000", "a07-quote-trade", "https://pay.example.test", `{}`, "https://example.test")
	require.NoError(t, err)
	assert.Equal(t, quote.ProfitQuota, order.ProfitQuota)
	assert.Equal(t, quote.FeeFen, order.FeeFen)
	var admin User
	require.NoError(t, DB.First(&admin, root.UserID).Error)
	admin.Role = common.RoleAdminUser
	require.NoError(t, DB.Model(&admin).Update("role", common.RoleAdminUser).Error)
	_, _, err = ListAgentSimplePaymentOrders(root, agent.UserID, 0, 20, "https://example.test")
	require.ErrorIs(t, err, ErrAgentSimplePermission)
}

func TestAgentSimplePaymentRecordsExposeMaturityAndOnlyOwnerDeliveryCode(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	order := simpleAccountingPayment(t, agent, "a07-record-maturity", 120)
	for _, identity := range []AuthSessionIdentity{root, agent} {
		rows, total, err := ListAgentSimplePaymentOrders(identity, agent.UserID, 0, 20, "https://example.test")
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Len(t, rows, 1)
		assert.Positive(t, rows[0].MaturesAt)
		assert.False(t, rows[0].ProfitMatured)
		if identity.UserID == root.UserID {
			assert.Empty(t, rows[0].RedemptionKey)
		} else {
			assert.NotEmpty(t, rows[0].RedemptionKey)
		}
	}
	var code Redemption
	require.NoError(t, DB.Where("agent_simple_payment_id = ?", order.ID).First(&code).Error)
	_, err := Redeem(code.Key, agentTestRecipientID)
	require.NoError(t, err)
	rows, _, err := ListAgentSimplePaymentOrders(agent, agent.UserID, 0, 20, "https://example.test")
	require.NoError(t, err)
	assert.Empty(t, rows[0].RedemptionKey)
}

func TestAgentSimpleCreditLimitHistoryRecordsAmountAndResult(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	_, err := SetAgentSimpleCreditLimit(root, AgentSimpleCreditLimitRequest{RequestID: "a07-credit-history", AgentUserID: agent.UserID, LimitQuota: 500_000, Reason: "Synthetic limit adjustment"})
	require.NoError(t, err)
	var ledger AgentSimpleLedger
	require.NoError(t, DB.Where("request_id = ?", "a07-credit-history").First(&ledger).Error)
	assert.EqualValues(t, 500_000, ledger.DeltaCreditLimit)
	assert.EqualValues(t, 500_000, ledger.CreditLimitAfter)
	assert.Equal(t, root.UserID, ledger.ActorUserID)
}

func TestAgentSimpleRefundEvidenceRemainsQueryableOnlyByRoot(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	order := simpleAccountingPayment(t, agent, "a07-refund-evidence", 120)
	require.NoError(t, RefundAgentSimplePayment(root, AgentSimplePaymentRefundCommand{RequestID: "a07-refund-evidence-result", OrderID: order.ID, Reason: "Customer returned unused code", Evidence: "Synthetic completed refund receipt"}))
	for _, identity := range []AuthSessionIdentity{root, agent} {
		rows, _, err := ListAgentSimplePaymentOrders(identity, agent.UserID, 0, 20, "https://example.test")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, AgentSimplePaymentRefunded, rows[0].Status)
		assert.Positive(t, rows[0].RefundedAt)
		if identity.UserID == root.UserID {
			assert.Equal(t, root.UserID, rows[0].RefundedBy)
			assert.Equal(t, "Synthetic completed refund receipt", rows[0].RefundEvidence)
		} else {
			assert.Empty(t, rows[0].RefundEvidence)
			assert.Empty(t, rows[0].RedemptionKey)
		}
	}
}
