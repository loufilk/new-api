package model

import (
	"context"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReferralA07OnlineTopupsAreUncappedAndSnapshotAgentStatus(t *testing.T) {
	setupLedgerTestState(t)
	inviter := User{Username: "a07-inviter", AffCode: "a07-inviter", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&inviter).Error)
	buyer := User{Username: "a07-buyer", AffCode: "a07-buyer", Status: common.UserStatusEnabled, InviterId: inviter.Id}
	require.NoError(t, DB.Create(&buyer).Error)
	for i, state := range []string{"ordinary", "active", "paused", "cancelled", "active"} {
		if i == 1 {
			require.NoError(t, DB.Create(&AgentProfile{UserID: inviter.Id, Status: AgentProfileActive, Version: 1}).Error)
			require.NoError(t, DB.Create(&AgentSimpleAccount{AgentUserID: inviter.Id, Status: AgentSimpleStatusActive, Version: 1}).Error)
		}
		if i > 1 {
			require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", inviter.Id).Update("status", state).Error)
		}
		trade := fmt.Sprintf("A07-SNAPSHOT-%d", i)
		require.NoError(t, DB.Create(&TopUp{UserId: buyer.Id, TradeNo: trade, Amount: 100, Money: 100, PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending}).Error)
		_, err := RechargeEpay(trade, "alipay", "100.00", "")
		require.NoError(t, err)
	}
	// Processing after a status change must preserve the status at payment time.
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", inviter.Id).Update("status", AgentSimpleStatusPaused).Error)
	_, err := ProcessAgentCommissionOutbox(context.Background(), 100)
	require.NoError(t, err)
	_, err = ProcessAgentCommissionOutbox(context.Background(), 100)
	require.NoError(t, err)
	var rewards []ReferralCommission
	require.NoError(t, DB.Order("id").Find(&rewards).Error)
	require.Len(t, rewards, 5)
	for i, rate := range []float64{0.03, 0.05, 0.03, 0.03, 0.05} {
		var order TopUp
		require.NoError(t, DB.Where("trade_no = ?", rewards[i].ReferenceId).First(&order).Error)
		assert.Equal(t, rate, rewards[i].Rate)
		assert.Equal(t, ReferralCommissionStatusFrozen, rewards[i].Status)
		assert.Equal(t, order.CompleteTime+ReferralCommissionFreezeSeconds, rewards[i].MaturesAt)
	}
	available, err := GetAvailableReferralQuota(inviter.Id)
	require.NoError(t, err)
	assert.Zero(t, available)
	var cashAwards int64
	require.NoError(t, DB.Model(&AgentCommissionAward{}).Count(&cashAwards).Error)
	assert.Zero(t, cashAwards, "A07 must not award the old cash commission as well")
}

func TestReferralA07FrozenRewardsCannotTransferAndReleasedRewardsCreditPaid(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 811, 1_000_000, 1_000_000)
	reward := ReferralCommission{UserId: 811, SourceUserId: 812, ReferenceType: ReferralReferenceTopUp, ReferenceId: "A07-HOLD", PaidQuota: 20_000_000, RewardQuota: 1_000_000, Rate: ReferralAgentRate, Status: ReferralCommissionStatusFrozen, MaturesAt: common.GetTimestamp() + 3600}
	require.NoError(t, DB.Create(&reward).Error)
	_, err := TransferAffQuota(811, 500_000, "a07-transfer-frozen")
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
	assert.Zero(t, countReferralTransferLedgerRows(t, 811))
	require.NoError(t, DB.Model(&reward).Update("matures_at", common.GetTimestamp()-1).Error)
	_, err = TransferAffQuota(811, 500_000, "a07-transfer-released")
	require.NoError(t, err)
	user := getUserBalances(t, 811)
	assert.Equal(t, 500_000, user.QuotaPaid)
	assert.Zero(t, user.QuotaReward)
	assert.Equal(t, 500_000, user.AffQuota)
	assertQuotaInvariant(t, user)
}

func TestReferralA07RefundCancelsHoldAndClawsBackSpentReward(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 821, 1_000_000, 1_000_000)
	reward := ReferralCommission{UserId: 821, SourceUserId: 822, ReferenceType: ReferralReferenceTopUp, ReferenceId: "A07-REFUND", PaidQuota: 20_000_000, RewardQuota: 1_000_000, Rate: ReferralAgentRate, Status: ReferralCommissionStatusFrozen, MaturesAt: common.GetTimestamp() + 3600}
	require.NoError(t, DB.Create(&reward).Error)
	fact := AgentCommissionRefundFact{TopUpReferenceID: reward.ReferenceId, CumulativeRefundedMinor: 5000}
	err := DB.Transaction(func(tx *gorm.DB) error { _, err := reverseReferralCommissionTx(tx, nil, fact, 10_000); return err })
	require.NoError(t, err)
	available, err := GetAvailableReferralQuota(821)
	require.NoError(t, err)
	assert.Zero(t, available, "a partial refund does not unfreeze the remaining reward")
	require.NoError(t, DB.Model(&reward).Update("matures_at", common.GetTimestamp()-1).Error)
	_, err = TransferAffQuota(821, 500_000, "a07-spent-reward")
	require.NoError(t, err)
	fact.CumulativeRefundedMinor = 10_000
	for range 2 {
		err = DB.Transaction(func(tx *gorm.DB) error { _, err := reverseReferralCommissionTx(tx, nil, fact, 10_000); return err })
		require.NoError(t, err)
	}
	assert.Equal(t, -500_000, getUserBalances(t, 821).AffQuota)
	_, err = TransferAffQuota(821, 500_000, "a07-refund-debt")
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
	require.NoError(t, DB.First(&reward, reward.Id).Error)
	assert.Equal(t, ReferralCommissionStatusCancelled, reward.Status)
	assert.Equal(t, 1_000_000, reward.ReversedQuota)
}
