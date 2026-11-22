package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func simpleAccountingFixture(t *testing.T) (AuthSessionIdentity, AuthSessionIdentity) {
	t.Helper()
	_, agent := setupAgentRedemptionTest(t)
	root := User{Username: "a07-accounting-root", AffCode: "a07-accounting-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(&root).Error)
	rootIdentity := agentSimpleIdentity(t, root.Id, "a07-accounting-root")
	agentIdentity := agentSimpleIdentity(t, agent.UserID, "a07-accounting-agent")
	require.NoError(t, DB.Create(&AgentSimpleAccount{AgentUserID: agent.UserID, Status: AgentSimpleStatusActive, Version: 1}).Error)
	_, err := UpdateAgentSimplePaymentFee(rootIdentity, AgentSimplePaymentFeeRequest{RequestID: "a07-accounting-fee", PaymentMethod: "alipay", FeeBPS: 0, Enabled: true, Reason: "Synthetic fee fixture"})
	require.NoError(t, err)
	return rootIdentity, agentIdentity
}

func simpleAccountingPayment(t *testing.T, identity AuthSessionIdentity, reference string, price int64) *AgentSimplePaymentOrderView {
	t.Helper()
	order, err := CreateAgentSimplePaymentOrder(identity, AgentSimplePaymentRequest{RequestID: reference, Name: reference, Quota: 500_000, SalePriceFen: price, PaymentMethod: "alipay"}, reference, reference+"-synthetic-payment-link-token-000000000000", reference, "https://pay.example.test", `{}`, "https://example.test")
	require.NoError(t, err)
	amount := fmt.Sprintf("%d.%02d", price/100, price%100)
	_, err = SettleAgentSimplePaymentByTradeNo(reference, "alipay", amount, simpleVerifiedEpayFacts(t, reference, amount))
	require.NoError(t, err)
	return order
}

func TestAgentSimpleRetiredCommissionEventsCannotGrantCashOrReferral(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	for _, route := range []string{"cash", "legacy"} {
		snapshot := agentCommissionPaymentSnapshot{UserID: agentTestRecipientID, InviterID: agent.UserID, Eligible: true, PaidQuota: 500_000, PaidAt: common.GetTimestamp(), Qualification: "agent"}
		payload, err := common.Marshal(snapshot)
		require.NoError(t, err)
		event := AgentCommissionOutbox{EventKey: "retired-" + route, Kind: "payment", ReferenceID: "retired-" + route, SourceUserID: agentTestRecipientID, Route: route, Payload: string(payload), Status: "pending"}
		require.NoError(t, DB.Create(&event).Error)
		for range 2 {
			require.ErrorIs(t, processAgentCommissionEvent(context.Background(), event.ID), ErrAgentCommissionInvariant)
		}
		var inboxCount int64
		require.NoError(t, DB.Model(&AgentCommissionInbox{}).Where("event_id = ?", event.ID).Count(&inboxCount).Error)
		assert.Zero(t, inboxCount)
		var preserved AgentCommissionOutbox
		require.NoError(t, DB.First(&preserved, event.ID).Error)
		assert.Equal(t, "pending", preserved.Status)
		assert.Equal(t, event.Payload, preserved.Payload)
	}
	var count int64
	require.NoError(t, DB.Model(&AgentCommissionAward{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&ReferralCommission{}).Count(&count).Error)
	assert.Zero(t, count)
	assert.Zero(t, getUserBalances(t, agent.UserID).AffQuota)
}

func TestAgentSimplePaymentCreationPersistsCompleteGatewayAndReplaysOriginal(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	request := AgentSimplePaymentRequest{RequestID: "a07-atomic-payment", Name: "Atomic payment", Quota: 500_000, SalePriceFen: 120, PaymentMethod: "alipay"}
	token := "synthetic-atomic-payment-token-000000000000000000000"
	_, err := CreateAgentSimplePaymentOrder(agent, request, "a07-atomic-public", token, "a07-atomic-trade", "", "", "https://example.test")
	require.ErrorIs(t, err, ErrAgentSimpleInvalid, "never publish an unpayable pending link")
	first, err := CreateAgentSimplePaymentOrder(agent, request, "a07-atomic-public", token, "a07-atomic-trade", "https://pay.example.test/submit", `{"out_trade_no":"a07-atomic-trade"}`, "https://example.test")
	require.NoError(t, err)
	replay, err := CreateAgentSimplePaymentOrder(agent, request, "a07-retry-public", token+"1", "a07-retry-trade", "https://other.example.test/submit", `{"out_trade_no":"a07-retry-trade"}`, "https://example.test")
	require.NoError(t, err)
	assert.Equal(t, first.ID, replay.ID)
	assert.Equal(t, first.CustomerURL, replay.CustomerURL)
	assert.Equal(t, first.TradeNo, replay.TradeNo)
	assert.Equal(t, first.PaymentURL, replay.PaymentURL)
	assert.Equal(t, first.PaymentParams, replay.PaymentParams)
	public, err := GetAgentSimplePublicPayment(first.PublicID, token)
	require.NoError(t, err)
	assert.Equal(t, first.PaymentURL, public.PaymentURL)
	assert.Equal(t, first.PaymentParams, public.PaymentParams)
	assert.Empty(t, public.RedemptionKey)
	var count int64
	require.NoError(t, DB.Model(&AgentSimplePaymentOrder{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestAgentSimpleRefundedPaymentAcknowledgesOriginalSuccessWithoutRecredit(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	order := simpleAccountingPayment(t, agent, "a07-refunded-replay", 120)
	err := RefundAgentSimplePayment(root, AgentSimplePaymentRefundCommand{RequestID: "a07-refunded-replay-refund", OrderID: order.ID, Reason: "Synthetic customer refund", Evidence: "Synthetic refund receipt"})
	require.NoError(t, err)
	for range 2 {
		done, err := SettleAgentSimplePaymentByTradeNo(order.TradeNo, "alipay", "1.20", simpleVerifiedEpayFacts(t, order.TradeNo, "1.20"))
		require.NoError(t, err)
		assert.True(t, done)
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
			done, err = SettleAgentSimplePaymentCallbackTx(tx, order.TradeNo, "alipay", "1.20", common.GetTimestamp(), simpleVerifiedEpayFacts(t, order.TradeNo, "1.20"))
			assert.True(t, done)
			return err
		}))
	}
	_, err = SettleAgentSimplePaymentByTradeNo(order.TradeNo, "alipay", "1.19", simpleVerifiedEpayFacts(t, order.TradeNo, "1.19"))
	require.ErrorIs(t, err, ErrPaymentAmountMismatch)
	var stored AgentSimplePaymentOrder
	require.NoError(t, DB.First(&stored, order.ID).Error)
	assert.Equal(t, AgentSimplePaymentRefunded, stored.Status)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.PaymentAvailableQuota)
	assert.Zero(t, account.PaymentPendingQuota)
}

func TestAgentSimpleRedeemedPaymentProfitStillMaturesExactlyOnce(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	order := simpleAccountingPayment(t, agent, "a07-redeemed-profit", 120)
	var stored AgentSimplePaymentOrder
	require.NoError(t, DB.First(&stored, order.ID).Error)
	var code Redemption
	require.NoError(t, DB.First(&code, stored.RedemptionID).Error)
	_, err := Redeem(code.Key, agentTestRecipientID)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&stored).Update("matures_at", common.GetTimestamp()-1).Error)
	for range 2 {
		_, err = MatureAgentSimplePaymentProfits(context.Background(), 100)
		require.NoError(t, err)
	}
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.PaymentPendingQuota)
	assert.EqualValues(t, 150_000, account.PaymentAvailableQuota)
}

func TestAgentSimpleRefundOfWithdrawnProfitPreservesOtherOrdersHold(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	first := simpleAccountingPayment(t, agent, "a07-refundable-withdrawn", 3090)
	require.NoError(t, DB.Model(&AgentSimplePaymentOrder{}).Where("id = ?", first.ID).Update("matures_at", common.GetTimestamp()-1).Error)
	_, err := MatureAgentSimplePaymentProfits(context.Background(), 100)
	require.NoError(t, err)
	_, err = WithdrawAgentSimplePaymentProfit(root, AgentSimplePaymentWithdrawalRequest{RequestID: "a07-profit-withdrawal", AgentUserID: agent.UserID, Quota: 15_000_000, Reason: "Synthetic withdrawal", Evidence: "Synthetic paid receipt"})
	require.NoError(t, err)
	second := simpleAccountingPayment(t, agent, "a07-unrelated-frozen", 120)
	refund := AgentSimplePaymentRefundCommand{RequestID: "a07-profit-refund", OrderID: first.ID, Reason: "Unredeemed customer refund", Evidence: "Synthetic refund receipt"}
	require.NoError(t, RefundAgentSimplePayment(root, refund))
	require.NoError(t, RefundAgentSimplePayment(root, refund), "a replay must not reverse profit twice")
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.EqualValues(t, 150_000, account.PaymentPendingQuota)
	assert.EqualValues(t, 15_000_000, account.PaymentDebtQuota)
	for range 2 {
		_, err = MatureAgentSimplePaymentProfits(context.Background(), 100)
		require.NoError(t, err)
	}
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.EqualValues(t, 15_000_000, account.PaymentDebtQuota, "frozen profits cannot repeatedly erase refund debt")
	require.NoError(t, DB.Model(&AgentSimplePaymentOrder{}).Where("id = ?", second.ID).Update("matures_at", common.GetTimestamp()-1).Error)
	_, err = MatureAgentSimplePaymentProfits(context.Background(), 100)
	require.NoError(t, err)
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.PaymentPendingQuota)
	assert.Zero(t, account.PaymentAvailableQuota)
	assert.EqualValues(t, 14_850_000, account.PaymentDebtQuota)
}

func TestAgentSimpleExpiredCodeCannotReleaseAnotherCodesCredit(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	_, err := SetAgentSimpleCreditLimit(root, AgentSimpleCreditLimitRequest{RequestID: "a07-expiry-credit", AgentUserID: agent.UserID, LimitQuota: 500, Reason: "Synthetic credit"})
	require.NoError(t, err)
	first, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-expiry-first-code", Name: "Expires", Quota: 100, Mode: AgentSimpleCodeCredit})
	require.NoError(t, err)
	second, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-expiry-second-code", Name: "Remains valid", Quota: 100, Mode: AgentSimpleCodeCredit})
	require.NoError(t, err)
	require.NoError(t, DB.Model(first).Update("expired_time", common.GetTimestamp()-1).Error)
	expired, err := ExpireAgentSimpleCodes(t.Context(), 100)
	require.NoError(t, err)
	require.Equal(t, 1, expired)
	expired, err = ExpireAgentSimpleCodes(t.Context(), 100)
	require.NoError(t, err)
	assert.Zero(t, expired)
	require.NoError(t, DB.First(first, first.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusDisabled, first.Status)
	assert.False(t, first.BudgetReserved)
	_, err = CancelAgentSimpleCode(agent, AgentSimpleCodeCancelRequest{RequestID: "a07-expired-cancel", CodeID: first.Id, Version: int64(first.BudgetVersion), Reason: "Already expired"})
	require.ErrorIs(t, err, ErrAgentSimpleState)
	_, err = Redeem(second.Key, agentTestRecipientID)
	require.NoError(t, err, "another valid code must retain its reserved credit")
}

func TestAgentSimpleLateVerifiedPaymentIsDeliveredAfterLinkExpiry(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	order, err := CreateAgentSimplePaymentOrder(agent, AgentSimplePaymentRequest{RequestID: "a07-late-payment", Name: "Late callback", Quota: 500_000, SalePriceFen: 120, PaymentMethod: "alipay"}, "a07-late-public", "synthetic-late-payment-token-00000000000000000000000000000", "a07-late-trade", "https://pay.example.test", `{}`, "https://example.test")
	require.NoError(t, err)
	require.NoError(t, DB.Model(&AgentSimplePaymentOrder{}).Where("id = ?", order.ID).Update("expires_at", common.GetTimestamp()-1).Error)
	_, err = ExpireAgentSimplePaymentOrders(context.Background(), 100)
	require.NoError(t, err)
	_, err = SettleAgentSimplePaymentByTradeNo("a07-late-trade", "alipay", "1.20", simpleVerifiedEpayFacts(t, "a07-late-trade", "1.20"))
	require.NoError(t, err, "a verified customer payment must remain recoverable after local link expiry")
	var stored AgentSimplePaymentOrder
	require.NoError(t, DB.First(&stored, order.ID).Error)
	assert.Equal(t, AgentSimplePaymentPaid, stored.Status)
	assert.Positive(t, stored.RedemptionID)
}

func TestAgentSimpleCodeAutomaticallyCombinesOwnBalanceAndCredit(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"cash_quota": 50, "credit_limit_quota": 90}).Error)
	code, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-mixed-sources", Name: "Automatic sources", Quota: 100, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.CashQuota)
	assert.EqualValues(t, 45, account.CreditReservedQuota)
	assert.EqualValues(t, 45, code.AgentSimpleCostQuota)
	_, err = Redeem(code.Key, agentTestRecipientID)
	require.NoError(t, err)
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.CreditReservedQuota)
	assert.EqualValues(t, 45, account.CreditOutstandingQuota)
	_, err = RepayAgentSimpleCredit(root, AgentSimpleRepaymentRequest{RequestID: "a07-mixed-repay", AgentUserID: agent.UserID, Quota: 45, Reason: "Synthetic paid repayment"})
	require.NoError(t, err)
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.CreditOutstandingQuota)
	assert.EqualValues(t, 90, simpleAvailableCredit(&account))
}

func TestAgentSimpleMixedCodeReturnRepaysExistingDebtBeforeRestoringBalance(t *testing.T) {
	for _, scenario := range []struct {
		name                               string
		cash, spentFace, wantCash, wantDue int64
	}{
		{"partial_debt", 50, 100, 0, 45},
		{"debt_fully_paid", 50, 20, 30, 0},
		{"credit_release_is_not_cash", 0, 100, 0, 90},
	} {
		for _, expire := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/expire=%t", scenario.name, expire), func(t *testing.T) {
				_, agent := simpleAccountingFixture(t)
				const unit int64 = 500_000
				require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"cash_quota": scenario.cash * unit, "credit_limit_quota": 300 * unit}).Error)
				returned, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-return-reserved", Name: "Returned code", Quota: 100 * unit, Mode: AgentSimpleCodeDirect})
				require.NoError(t, err)
				spent, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-return-spent", Name: "Redeemed credit code", Quota: scenario.spentFace * unit, Mode: AgentSimpleCodeDirect})
				require.NoError(t, err)
				_, err = Redeem(spent.Key, agentTestRecipientID)
				require.NoError(t, err)
				_, err = runAgentSimpleWeeklySettlementAt(context.Background(), 100, time.Now().In(agentSimpleLocation).AddDate(0, 0, 8))
				require.NoError(t, err)
				request := AgentSimpleCodeCancelRequest{RequestID: "a07-return-cancel", CodeID: returned.Id, Version: int64(returned.BudgetVersion), Reason: "Synthetic unissued code return"}
				if expire {
					require.NoError(t, DB.Model(returned).Update("expired_time", common.GetTimestamp()-1).Error)
				}
				for range 2 {
					if expire {
						_, err = ExpireAgentSimpleCodes(t.Context(), 100)
					} else {
						_, err = CancelAgentSimpleCode(agent, request)
					}
					require.NoError(t, err)
				}
				var account AgentSimpleAccount
				require.NoError(t, DB.First(&account, agent.UserID).Error)
				assert.Equal(t, scenario.wantCash*unit, account.CashQuota)
				assert.Equal(t, scenario.wantDue*unit, account.CreditOutstandingQuota)
				assert.Zero(t, account.CreditReservedQuota)
				var debt AgentSimpleCreditDebt
				require.NoError(t, DB.Where("code_id = ?", spent.Id).First(&debt).Error)
				assert.Equal(t, scenario.wantDue*unit, debt.Quota-debt.PaidQuota)
				var bill AgentSimpleBill
				require.NoError(t, DB.First(&bill, debt.BillID).Error)
				assert.Equal(t, scenario.wantDue*unit, bill.Quota-bill.PaidQuota)
			})
		}
	}
}

func TestAgentSimpleRechargeRepaysDiscountedDebtBeforeAddingCashQuota(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Updates(map[string]any{"credit_limit_quota": 90_000_000, "credit_outstanding_quota": 45_000_000}).Error)
	require.NoError(t, DB.Create(&AgentSimpleCreditDebt{AgentUserID: agent.UserID, CodeID: 999, Quota: 45_000_000}).Error)
	require.NoError(t, DB.Create(&TopUp{AgentSimpleTopUp: true, UserId: agent.UserID, Amount: 200, Money: 180, TradeNo: "A07-REPAY-FIRST", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending}).Error)
	for range 2 {
		_, err := RechargeEpay("A07-REPAY-FIRST", "alipay", "180.00", "", simpleVerifiedEpayFacts(t, "A07-REPAY-FIRST", "180.00"))
		require.NoError(t, err)
	}
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agent.UserID).Error)
	assert.Zero(t, account.CreditOutstandingQuota)
	assert.EqualValues(t, 50_000_000, account.CashQuota, "180 paid first repays 90 debt; the remaining 90 funds 100 face value")
}
