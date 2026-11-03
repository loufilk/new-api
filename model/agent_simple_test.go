package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agentSimpleIdentity(t *testing.T, userID int, sid string) AuthSessionIdentity {
	t.Helper()
	var user User
	require.NoError(t, DB.First(&user, userID).Error)
	session := newTestUserSession(sid, userID, time.Now().Unix())
	session.UserAuthVersion = user.AuthVersion
	require.NoError(t, CreateUserSession(session))
	return AuthSessionIdentity{UserID: userID, SessionID: sid, UserAuthVersion: user.AuthVersion, SessionVersion: session.Version}
}

func TestAgentSimpleDirectAndCreditCodeAccounting(t *testing.T) {
	_, agentScope := setupAgentRedemptionTest(t)
	require.NoError(t, DB.AutoMigrate(AgentSimpleModels()...))
	root := User{Id: 78006, Username: "simple-root", AffCode: "simple-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(&root).Error)
	rootIdentity := agentSimpleIdentity(t, root.Id, "simple-root-session")
	agentIdentity := agentSimpleIdentity(t, agentScope.UserID, "simple-agent-session")
	_, activationErr := SetAgentSimpleStatus(rootIdentity, AgentSimpleStatusRequest{RequestID: "simple-root-activate", AgentUserID: agentScope.UserID, Status: AgentSimpleStatusActive, Reason: "Explicit synthetic agent activation"})
	require.NoError(t, activationErr)

	_, err := AdjustAgentSimpleBalance(rootIdentity, AgentSimpleAdjustmentRequest{RequestID: "simple-root-credit", AgentUserID: agentScope.UserID, Delta: 1000, Reason: "Initial internal code capacity"})
	require.NoError(t, err)
	replayed, err := AdjustAgentSimpleBalance(rootIdentity, AgentSimpleAdjustmentRequest{RequestID: "simple-root-credit", AgentUserID: agentScope.UserID, Delta: 1000, Reason: "Initial internal code capacity"})
	require.NoError(t, err)
	assert.EqualValues(t, 1000, replayed.AvailableQuota)

	direct, err := CreateAgentSimpleCode(agentIdentity, AgentSimpleCodeRequest{RequestID: "simple-direct-code", Name: "Direct code", Quota: 100, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	assert.Equal(t, AgentSimpleCodeDirect, direct.AgentSimpleMode)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agentScope.UserID).Error)
	assert.EqualValues(t, 900, simpleAvailableQuota(&account))

	credited, err := Redeem(direct.Key, agentTestRecipientID)
	require.NoError(t, err)
	assert.Equal(t, 100, credited)
	assert.Equal(t, 100, getUserBalances(t, agentTestRecipientID).QuotaPaid)
	require.NoError(t, DB.First(&account, agentScope.UserID).Error)
	assert.EqualValues(t, 900, simpleAvailableQuota(&account))
	assert.Zero(t, account.CreditOutstandingQuota)

	_, err = AdjustAgentSimpleBalance(rootIdentity, AgentSimpleAdjustmentRequest{RequestID: "simple-remove-own-balance", AgentUserID: agentScope.UserID, Delta: -900, Reason: "Complete synthetic own-balance scenario"})
	require.NoError(t, err)
	_, err = SetAgentSimpleCreditLimit(rootIdentity, AgentSimpleCreditLimitRequest{RequestID: "simple-credit-limit", AgentUserID: agentScope.UserID, LimitQuota: 200, Reason: "Approved weekly limit"})
	require.NoError(t, err)
	creditCode, err := CreateAgentSimpleCode(agentIdentity, AgentSimpleCodeRequest{RequestID: "simple-credit-code", Name: "Credit code", Quota: 100, Mode: AgentSimpleCodeCredit})
	require.NoError(t, err)
	assert.EqualValues(t, int64(90), creditCode.AgentSimpleCostQuota)
	require.NoError(t, DB.First(&account, agentScope.UserID).Error)
	assert.EqualValues(t, 90, account.CreditReservedQuota)
	assert.EqualValues(t, 110, simpleAvailableCredit(&account))

	credited, err = Redeem(creditCode.Key, agentTestRecipientID)
	require.NoError(t, err)
	assert.Equal(t, 100, credited)
	require.NoError(t, DB.First(&account, agentScope.UserID).Error)
	assert.Zero(t, account.CreditReservedQuota)
	assert.EqualValues(t, 90, account.CreditOutstandingQuota)
	assert.EqualValues(t, 110, simpleAvailableCredit(&account))

	_, err = CreateAgentSimpleCode(agentIdentity, AgentSimpleCodeRequest{RequestID: "simple-unredeemed-code", Name: "Unredeemed code", Quota: 50, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	agentRows, total, err := GetAgentSimpleRecords(agentIdentity, agentScope.UserID, 0, 10, "")
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	require.Len(t, agentRows, 3)
	assert.NotEmpty(t, agentRows[0].Key, "an agent needs the full unredeemed code for customer delivery")
	assert.Empty(t, agentRows[1].Key, "used codes no longer expose their credential")

	adminRows, total, err := GetAgentSimpleRecords(rootIdentity, agentScope.UserID, 0, 10, AgentSimpleCodeDirect)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	require.Len(t, adminRows, 3)
	assert.Empty(t, adminRows[0].Key)
	assert.NotEmpty(t, adminRows[0].MaskedKey)
	assert.EqualValues(t, int64(90), adminRows[1].CostQuota)
}

func TestAgentSimpleCancellingCreditCodeReleasesDiscountedReserve(t *testing.T) {
	_, agentScope := setupAgentRedemptionTest(t)
	require.NoError(t, DB.AutoMigrate(AgentSimpleModels()...))
	root := User{Id: 78007, Username: "simple-cancel-root", AffCode: "simple-cancel-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(&root).Error)
	rootIdentity := agentSimpleIdentity(t, root.Id, "simple-cancel-root-session")
	agentIdentity := agentSimpleIdentity(t, agentScope.UserID, "simple-cancel-agent-session")
	_, activationErr := SetAgentSimpleStatus(rootIdentity, AgentSimpleStatusRequest{RequestID: "simple-cancel-activate", AgentUserID: agentScope.UserID, Status: AgentSimpleStatusActive, Reason: "Explicit synthetic agent activation"})
	require.NoError(t, activationErr)
	_, err := SetAgentSimpleCreditLimit(rootIdentity, AgentSimpleCreditLimitRequest{RequestID: "simple-cancel-limit", AgentUserID: agentScope.UserID, LimitQuota: 500, Reason: "Approved weekly limit"})
	require.NoError(t, err)
	code, err := CreateAgentSimpleCode(agentIdentity, AgentSimpleCodeRequest{RequestID: "simple-cancel-code", Name: "Cancel credit code", Quota: 100, Mode: AgentSimpleCodeCredit})
	require.NoError(t, err)
	account, err := GetAgentSimpleAccount(agentIdentity, agentScope.UserID)
	require.NoError(t, err)
	assert.EqualValues(t, 100, account.ReservedQuota)

	cancelRequest := AgentSimpleCodeCancelRequest{RequestID: "simple-cancel-request", CodeID: code.Id, Version: int64(code.BudgetVersion), Reason: "Customer did not receive the code"}
	_, err = CancelAgentSimpleCode(agentIdentity, cancelRequest)
	require.NoError(t, err)
	_, err = CancelAgentSimpleCode(agentIdentity, cancelRequest)
	require.NoError(t, err, "retrying after a lost response must return the recorded cancellation")
	account, err = GetAgentSimpleAccount(agentIdentity, agentScope.UserID)
	require.NoError(t, err)
	assert.Zero(t, account.ReservedQuota)
	assert.EqualValues(t, 500, account.AvailableCreditQuota)
	assert.Zero(t, account.AmountDueQuota)
}

func TestAgentSimpleCodeSpendsNonRefundableSourcesBeforeCash(t *testing.T) {
	_, agentScope := setupAgentRedemptionTest(t)
	require.NoError(t, DB.AutoMigrate(AgentSimpleModels()...))
	identity := agentSimpleIdentity(t, agentScope.UserID, "simple-source-order-session")
	require.NoError(t, DB.Create(&AgentSimpleAccount{
		AgentUserID: agentScope.UserID, Status: AgentSimpleStatusActive,
		CashQuota: 100, RewardQuota: 100, RootQuota: 100, Version: 1,
	}).Error)

	code, err := CreateAgentSimpleCode(identity, AgentSimpleCodeRequest{
		RequestID: "simple-source-order-code", Name: "Source priority", Quota: 150, Mode: AgentSimpleCodeDirect,
	})
	require.NoError(t, err)

	var allocations []AgentSimpleCodeAllocation
	require.NoError(t, DB.Where("redemption_id = ?", code.Id).Order("id").Find(&allocations).Error)
	require.Len(t, allocations, 2)
	assert.Equal(t, AgentSimpleSourceReward, allocations[0].Source)
	assert.EqualValues(t, 100, allocations[0].Quota)
	assert.Equal(t, AgentSimpleSourceRoot, allocations[1].Source)
	assert.EqualValues(t, 50, allocations[1].Quota)
	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agentScope.UserID).Error)
	assert.EqualValues(t, 100, account.CashQuota, "cash-funded quota remains eligible for a later cash refund")
}

func TestAgentSimpleCodeRejectsAlreadyExpiredIssueRequest(t *testing.T) {
	_, agentScope := setupAgentRedemptionTest(t)
	require.NoError(t, DB.AutoMigrate(AgentSimpleModels()...))
	identity := agentSimpleIdentity(t, agentScope.UserID, "simple-expired-code-session")
	require.NoError(t, DB.Create(&AgentSimpleAccount{
		AgentUserID: agentScope.UserID, Status: AgentSimpleStatusActive,
		CashQuota: 100, Version: 1,
	}).Error)

	_, err := CreateAgentSimpleCode(identity, AgentSimpleCodeRequest{
		RequestID: "simple-expired-code", Name: "Expired", Quota: 50,
		Mode: AgentSimpleCodeDirect, ExpiresAt: common.GetTimestamp() - 1,
	})
	require.ErrorIs(t, err, ErrAgentSimpleInvalid)
	var count int64
	require.NoError(t, DB.Model(&Redemption{}).Where("agent_simple_mode <> '' AND budget_agent_id = ?", agentScope.UserID).Count(&count).Error)
	assert.Zero(t, count, "a rejected expiry must not create a funded code")
}

func TestAgentSimpleRechargeDoesNotCreditOrdinaryWalletOrReferralOutbox(t *testing.T) {
	_, agentScope := setupAgentRedemptionTest(t)
	require.NoError(t, DB.AutoMigrate(AgentSimpleModels()...))
	require.NoError(t, DB.Create(&AgentSimpleAccount{
		AgentUserID: agentScope.UserID, Status: AgentSimpleStatusActive, Version: 1,
	}).Error)
	require.NoError(t, DB.Create(&TopUp{
		AgentSimpleTopUp: true,
		UserId:           agentScope.UserID,
		Amount:           10,
		Money:            9,
		TradeNo:          "SIMPLE-RECHARGE-1",
		PaymentMethod:    "alipay",
		PaymentProvider:  PaymentProviderEpay,
		CreateTime:       common.GetTimestamp(),
		Status:           common.TopUpStatusPending,
	}).Error)

	alreadyDone, err := RechargeEpay("SIMPLE-RECHARGE-1", "alipay", "9.00", "127.0.0.1", simpleVerifiedEpayFacts(t, "SIMPLE-RECHARGE-1", "9.00"))
	require.NoError(t, err)
	assert.False(t, alreadyDone)

	var account AgentSimpleAccount
	require.NoError(t, DB.First(&account, agentScope.UserID).Error)
	assert.EqualValues(t, 10*int64(common.QuotaPerUnit), account.CashQuota)
	user := getUserBalances(t, agentScope.UserID)
	assert.Zero(t, user.Quota)
	assert.Zero(t, user.QuotaPaid)
	var ledgerCount int64
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ?", agentScope.UserID).Count(&ledgerCount).Error)
	assert.Zero(t, ledgerCount)
	var outboxCount int64
	require.NoError(t, DB.Model(&AgentCommissionOutbox{}).Where("source_user_id = ?", agentScope.UserID).Count(&outboxCount).Error)
	assert.Zero(t, outboxCount)
}

func TestAgentSimplePaymentSettlementDeliversSameRedemptionCode(t *testing.T) {
	_, agentScope := setupAgentRedemptionTest(t)
	require.NoError(t, DB.AutoMigrate(AgentSimpleModels()...))
	root := User{Id: 78008, Username: "simple-payment-root", AffCode: "simple-payment-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(&root).Error)
	rootIdentity := agentSimpleIdentity(t, root.Id, "simple-payment-root-session")
	agentIdentity := agentSimpleIdentity(t, agentScope.UserID, "simple-payment-agent-session")
	require.NoError(t, DB.Create(&AgentSimpleAccount{AgentUserID: agentScope.UserID, Status: AgentSimpleStatusActive, Version: 1}).Error)

	_, err := UpdateAgentSimplePaymentFee(rootIdentity, AgentSimplePaymentFeeRequest{RequestID: "simple-payment-fee", PaymentMethod: "alipay", FeeBPS: 0, Enabled: true, Reason: "Test channel fee"})
	require.NoError(t, err)
	quote, err := QuoteAgentSimplePayment("alipay", int64(common.QuotaPerUnit), 120)
	require.NoError(t, err)
	assert.EqualValues(t, 90, quote.CostPriceFen)
	assert.EqualValues(t, int64(common.QuotaPerUnit*0.3), quote.ProfitQuota)

	token := "payment-token-for-test-000000000000000000000000000000000000"
	request := AgentSimplePaymentRequest{RequestID: "simple-payment-order", Name: "Paid code", Quota: int64(common.QuotaPerUnit), SalePriceFen: 120, PaymentMethod: "alipay"}
	order, err := CreateAgentSimplePaymentOrder(agentIdentity, request, "PAY-PUBLIC-1", token, "PAY-TRADE-1", "https://pay.example/submit", `{"pid":"fixture"}`, "https://example.test")
	require.NoError(t, err)
	assert.Equal(t, AgentSimplePaymentPending, order.Status)
	assert.Contains(t, order.CustomerURL, "/agent-pay/PAY-PUBLIC-1#token=")

	done, err := SettleAgentSimplePaymentByTradeNo("PAY-TRADE-1", "alipay", "1.20", simpleVerifiedEpayFacts(t, "PAY-TRADE-1", "1.20"))
	require.NoError(t, err)
	assert.False(t, done)
	done, err = SettleAgentSimplePaymentByTradeNo("PAY-TRADE-1", "alipay", "1.20", simpleVerifiedEpayFacts(t, "PAY-TRADE-1", "1.20"))
	require.NoError(t, err)
	assert.True(t, done)

	public, err := GetAgentSimplePublicPayment("PAY-PUBLIC-1", token)
	require.NoError(t, err)
	assert.Equal(t, AgentSimplePaymentPaid, public.Status)
	assert.NotEmpty(t, public.RedemptionKey)
	account, err := GetAgentSimpleAccount(agentIdentity, agentScope.UserID)
	require.NoError(t, err)
	assert.EqualValues(t, quote.ProfitQuota, account.PaymentPendingQuota)
	assert.Zero(t, account.PaymentAvailableQuota)

	credited, err := Redeem(public.RedemptionKey, agentTestRecipientID)
	require.NoError(t, err)
	assert.EqualValues(t, common.QuotaPerUnit, credited)
	rows, total, err := GetAgentSimpleRecords(agentIdentity, agentScope.UserID, 0, 10, AgentSimpleCodePayment)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, AgentSimpleCodePayment, rows[0].Mode)
	assert.Empty(t, rows[0].Key, "redeemed payment code credentials are hidden")
}

func TestAgentSimplePaymentCallbackRejectsWrongAmountWithoutDelivering(t *testing.T) {
	_, agentScope := setupAgentRedemptionTest(t)
	require.NoError(t, DB.AutoMigrate(AgentSimpleModels()...))
	root := User{Id: 78009, Username: "simple-payment-root-amount", AffCode: "simple-payment-root-amount", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(&root).Error)
	rootIdentity := agentSimpleIdentity(t, root.Id, "simple-payment-root-amount-session")
	agentIdentity := agentSimpleIdentity(t, agentScope.UserID, "simple-payment-agent-amount-session")
	require.NoError(t, DB.Create(&AgentSimpleAccount{AgentUserID: agentScope.UserID, Status: AgentSimpleStatusActive, Version: 1}).Error)
	_, err := UpdateAgentSimplePaymentFee(rootIdentity, AgentSimplePaymentFeeRequest{RequestID: "simple-payment-fee-amount", PaymentMethod: "alipay", FeeBPS: 0, Enabled: true, Reason: "Test channel fee"})
	require.NoError(t, err)
	request := AgentSimplePaymentRequest{RequestID: "simple-payment-order-amount", Name: "Amount guarded", Quota: int64(common.QuotaPerUnit), SalePriceFen: 120, PaymentMethod: "alipay"}
	_, err = CreateAgentSimplePaymentOrder(agentIdentity, request, "PAY-PUBLIC-2", "payment-token-for-test-111111111111111111111111111111111111", "PAY-TRADE-2", "https://pay.example/submit", `{"pid":"fixture"}`, "https://example.test")
	require.NoError(t, err)
	_, err = SettleAgentSimplePaymentByTradeNo("PAY-TRADE-2", "alipay", "1.19", simpleVerifiedEpayFacts(t, "PAY-TRADE-2", "1.19"))
	require.ErrorIs(t, err, ErrPaymentAmountMismatch)
	var order AgentSimplePaymentOrder
	require.NoError(t, DB.Where("trade_no = ?", "PAY-TRADE-2").First(&order).Error)
	assert.Equal(t, AgentSimplePaymentPending, order.Status)
	assert.Zero(t, order.RedemptionID)
}
