package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentSimpleRechargeRejectsKnownCapacityButPreservesPaidConcurrentOverflow(t *testing.T) {
	for _, kind := range []string{"recharge", "repayment"} {
		t.Run(kind, func(t *testing.T) {
			root, agent := simpleAccountingFixture(t)
			request := AgentSimpleRechargeRequest{RequestID: "a07-deferred-payment", Amount: "0.01", Kind: kind, PaymentMethod: "alipay"}
			if kind == "repayment" {
				require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"credit_limit_quota": 5000, "credit_outstanding_quota": 4500}).Error)
				require.NoError(t, DB.Create(&AgentSimpleCreditDebt{AgentUserID: agent.UserID, CodeID: 87150, Quota: 4500}).Error)
			}
			require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("root_quota", common.MaxWalletQuota).Error)
			_, err := CreateAgentSimpleRecharge(agent, request, "a07-capacity-reject", "https://pay.example.test", map[string]string{})
			require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
			require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("root_quota", 0).Error)
			order, err := CreateAgentSimpleRecharge(agent, request, "a07-capacity-paid", "https://pay.example.test", map[string]string{})
			require.NoError(t, err)
			require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("root_quota", common.MaxWalletQuota).Error)
			for range 2 {
				_, err = RechargeEpay(order.TradeNo, "alipay", "0.01", "", simpleVerifiedEpayFacts(t, order.TradeNo, "0.01"))
				require.NoError(t, err, "verified cash must remain acknowledged even when the balance filled after order creation")
			}
			var stored TopUp
			require.NoError(t, DB.First(&stored, order.TopUpID).Error)
			assert.Equal(t, common.TopUpStatusSuccess, stored.Status)
			assert.Positive(t, stored.PaymentReceiptID)
			deferred := int64(5000)
			if kind == "repayment" {
				deferred = 555
			}
			assert.Equal(t, deferred, stored.AgentSimpleDeferredQuota)
			var account AgentSimpleAccount
			require.NoError(t, DB.First(&account, agent.UserID).Error)
			assert.Zero(t, account.CreditOutstandingQuota)
			assert.Zero(t, account.CashQuota)
			_, err = DrainAgentSimpleDeferredCash(context.Background(), 100)
			require.NoError(t, err)
			_, err = AdjustAgentSimpleBalance(root, AgentSimpleAdjustmentRequest{RequestID: "a07-open-capacity", AgentUserID: agent.UserID, Delta: -100, Reason: "Synthetic capacity release"})
			require.NoError(t, err)
			_, err = DrainAgentSimpleDeferredCash(context.Background(), 100)
			require.NoError(t, err)
			require.NoError(t, DB.First(&stored, order.TopUpID).Error)
			assert.Equal(t, deferred-100, stored.AgentSimpleDeferredQuota)
			require.NoError(t, DB.First(&account, agent.UserID).Error)
			assert.EqualValues(t, 100, account.CashQuota)
			_, err = AdjustAgentSimpleBalance(root, AgentSimpleAdjustmentRequest{RequestID: "a07-open-rest-capacity", AgentUserID: agent.UserID, Delta: -deferred, Reason: "Synthetic capacity release"})
			require.NoError(t, err)
			for range 2 {
				_, err = DrainAgentSimpleDeferredCash(context.Background(), 100)
				require.NoError(t, err)
			}
			_, err = RechargeEpay(order.TradeNo, "alipay", "0.01", "", simpleVerifiedEpayFacts(t, order.TradeNo, "0.01"))
			require.NoError(t, err)
			require.NoError(t, DB.First(&stored, order.TopUpID).Error)
			assert.Zero(t, stored.AgentSimpleDeferredQuota)
			require.NoError(t, DB.First(&account, agent.UserID).Error)
			assert.Equal(t, deferred, account.CashQuota)
			assert.Zero(t, getUserBalances(t, agent.UserID).QuotaPaid)
			var commissions int64
			require.NoError(t, DB.Model(&ReferralCommission{}).Count(&commissions).Error)
			assert.Zero(t, commissions)
		})
	}
}

func TestAgentSimpleRechargeCannotBeCreditedByLegacyManualCompletion(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	order, err := CreateAgentSimpleRecharge(agent, AgentSimpleRechargeRequest{RequestID: "a07-manual-reject", Amount: "100.01", Kind: "recharge", PaymentMethod: "alipay"}, "a07-manual-reject", "https://pay.example.test", map[string]string{})
	require.NoError(t, err)
	require.ErrorIs(t, ManualCompleteTopUp(order.TradeNo, ""), ErrAgentPaymentReceiptConflict)
	var stored TopUp
	require.NoError(t, DB.First(&stored, order.TopUpID).Error)
	assert.Equal(t, common.TopUpStatusPending, stored.Status)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.CashQuota)
}
