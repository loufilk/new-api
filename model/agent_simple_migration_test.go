package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentSimpleMigrationTransfersOldReferralOnceAndPreservesNewRewards(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 9801, 500_000, 700_000)
	seedAffQuotaUser(t, 9802, 300_000, 400_000)
	for range 2 {
		require.NoError(t, MigrateAgentSimpleA07WithDB(DB))
	}
	var report AgentSimpleMigration
	require.NoError(t, DB.First(&report, 1).Error)
	assert.EqualValues(t, 800_000, report.Transferred)
	assert.Equal(t, report.Transferred, report.Credited)
	assert.Equal(t, 2, report.UserCount)
	assert.Equal(t, 500_000, getUserBalances(t, 9801).QuotaPaid)
	assert.Zero(t, getUserBalances(t, 9801).AffQuota)
	assert.Equal(t, 700_000, getUserBalances(t, 9801).AffHistoryQuota)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 9801).Update("aff_quota", 123).Error)
	require.NoError(t, MigrateAgentSimpleA07WithDB(DB))
	assert.Equal(t, 123, getUserBalances(t, 9801).AffQuota)
	assert.Equal(t, 500_000, getUserBalances(t, 9801).QuotaPaid)
}

func TestAgentSimpleMigrationRejectsExistingAgentFundsWithoutPartialTransfers(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 9811, 500_000, 500_000)
	require.NoError(t, DB.Create(&AgentRedemptionAccount{AgentUserID: 9811, AllocatedQuota: 100, AvailableQuota: 100}).Error)
	require.Error(t, MigrateAgentSimpleA07WithDB(DB))
	assert.Equal(t, 500_000, getUserBalances(t, 9811).AffQuota)
	assert.Zero(t, getUserBalances(t, 9811).QuotaPaid)
	var count int64
	require.NoError(t, DB.Model(&AgentSimpleMigration{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&AgentRedemptionAccount{}).Where("agent_user_id = ?", 9811).Updates(map[string]any{"available_quota": 0, "allocated_quota": 0}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 9811).Updates(map[string]any{"quota": common.MaxWalletQuota, "quota_paid": common.MaxWalletQuota}).Error)
	require.Error(t, MigrateAgentSimpleA07WithDB(DB), "wallet ceiling failure must roll back the migration")
	assert.Equal(t, 500_000, getUserBalances(t, 9811).AffQuota)
}

func TestAgentSimpleMigrationRequiresReviewOfPendingRetiredEntitlements(t *testing.T) {
	for _, route := range []string{"legacy", "cash"} {
		t.Run(route, func(t *testing.T) {
			setupLedgerTestState(t)
			seedAffQuotaUser(t, 9821, 500_000, 500_000)
			event := AgentCommissionOutbox{EventKey: "migration-pending-" + route, Kind: "payment", ReferenceID: "migration-pending-" + route, SourceUserID: 9821, Route: route, Payload: "{}", Status: "pending"}
			require.NoError(t, DB.Create(&event).Error)
			require.Error(t, MigrateAgentSimpleA07WithDB(DB))
			assert.EqualValues(t, 500_000, getUserBalances(t, 9821).AffQuota)
			assert.Zero(t, getUserBalances(t, 9821).QuotaPaid)
			var count int64
			require.NoError(t, DB.Model(&AgentSimpleMigration{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestAgentSimpleMigrationPreservesPendingOrdinaryReferralObligations(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 9831, 400_000, 400_000)
	pending := ReferralRewardOutbox{ReferenceID: "ordinary-before-a07", SourceUserID: 9832, Payload: "{}", Status: "pending"}
	require.NoError(t, DB.Create(&pending).Error)
	require.Error(t, MigrateAgentSimpleA07WithDB(DB))
	assert.Equal(t, 400_000, getUserBalances(t, 9831).AffQuota)
	assert.Zero(t, getUserBalances(t, 9831).QuotaPaid)
	var unchanged ReferralRewardOutbox
	require.NoError(t, DB.First(&unchanged, pending.ID).Error)
	assert.Equal(t, pending.Payload, unchanged.Payload)
	assert.Equal(t, "pending", unchanged.Status)
}

func TestAgentSimpleCutoverRecognizesCompletedMigrationWithoutRetiringLegacyDebt(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, MigrateAgentSimpleA07WithDB(DB))
	require.NoError(t, DB.Create(&AgentSimpleAccount{AgentUserID: 9833, Status: AgentSimpleStatusActive, CashQuota: 100, Version: 1}).Error)
	_, err := AgentSimpleCutoverPreflight(DB)
	require.NoError(t, err)
	require.NoError(t, DB.Create(&AgentCommissionAccount{UserID: 9833, Currency: "CNY", CurrencyExponent: 2, DebtMinor: 1}).Error)
	_, err = AgentSimpleCutoverPreflight(DB)
	require.Error(t, err)
	var account AgentCommissionAccount
	require.NoError(t, DB.Where("user_id = ?", 9833).First(&account).Error)
	assert.EqualValues(t, 1, account.DebtMinor)
}

func TestAgentSimpleCutoverRepeatedStartupPreservesLiveCodes(t *testing.T) {
	_, agentScope := setupAgentRedemptionTest(t)
	// Complete cutover before the first live A07 account, fee or operation.
	require.NoError(t, MigrateAgentSimpleA07WithDB(DB))
	rootUser := User{Username: "a07-restart-root", AffCode: "a07-restart-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(&rootUser).Error)
	root := agentSimpleIdentity(t, rootUser.Id, "a07-restart-root")
	agent := agentSimpleIdentity(t, agentScope.UserID, "a07-restart-agent")
	require.NoError(t, DB.Create(&AgentSimpleAccount{AgentUserID: agent.UserID, Status: AgentSimpleStatusActive, Version: 1}).Error)
	_, err := UpdateAgentSimplePaymentFee(root, AgentSimplePaymentFeeRequest{RequestID: "a07-restart-fee", PaymentMethod: "alipay", FeeBPS: 0, Enabled: true, Reason: "Synthetic restart fee"})
	require.NoError(t, err)
	_, err = AdjustAgentSimpleBalance(root, AgentSimpleAdjustmentRequest{RequestID: "restart-code-funds", AgentUserID: agent.UserID, Delta: 1000, Reason: "Synthetic restart fixture"})
	require.NoError(t, err)
	direct, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "restart-direct-code", Name: "Restart direct", Quota: 100, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	payment := simpleAccountingPayment(t, agent, "restart-payment-code", 120)
	for range 2 {
		_, err = AgentSimpleCutoverPreflight(DB)
		require.NoError(t, err)
		require.NoError(t, MigrateAgentSimpleA07WithDB(DB))
	}
	var preserved Redemption
	require.NoError(t, DB.First(&preserved, direct.Id).Error)
	assert.Equal(t, direct.Key, preserved.Key)
	assert.True(t, preserved.BudgetReserved)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, preserved.Status)
	var order AgentSimplePaymentOrder
	require.NoError(t, DB.First(&order, payment.ID).Error)
	assert.Equal(t, AgentSimplePaymentPaid, order.Status)
	assert.Positive(t, order.RedemptionID)
}

func TestAgentSimpleMigrationPreservesSettledBudgetHistory(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 9841, 250_000, 250_000)
	history := AgentRedemptionAccount{AgentUserID: 9841, AllocatedQuota: 100, SpentQuota: 100}
	require.NoError(t, DB.Create(&history).Error)
	for range 2 {
		require.NoError(t, MigrateAgentSimpleA07WithDB(DB))
	}
	var after AgentRedemptionAccount
	require.NoError(t, DB.First(&after, 9841).Error)
	assert.Equal(t, history, after)
	assert.EqualValues(t, 250_000, getUserBalances(t, 9841).QuotaPaid)
	assert.Zero(t, getUserBalances(t, 9841).AffQuota)
}

func TestAgentSimpleMigrationRejectsIncompleteExistingReceipt(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 9842, 250_000, 250_000)
	require.NoError(t, DB.Create(&AgentSimpleMigration{ID: 1}).Error)
	require.ErrorIs(t, MigrateAgentSimpleA07WithDB(DB), ErrAgentSimpleInvariant)
	assert.EqualValues(t, 250_000, getUserBalances(t, 9842).AffQuota)
	assert.Zero(t, getUserBalances(t, 9842).QuotaPaid)
}
