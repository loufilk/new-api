package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 当前返佣引用的订单必须具备真实在线支付凭证：负 Amount、Money<=0、余额账户、
// 无任何 provider/method 的伪订单都不得发奖；老版本空 provider 但留下已知线上
// method 与正实付的存量订单仍然合格。
func TestRecordReferralCommissionRequiresValidPaidEvidence(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.Exec("DELETE FROM top_ups").Error)
	require.NoError(t, DB.Exec("DELETE FROM referral_commissions").Error)

	previousEnabled, previousRate := common.AffiliateRewardEnabled, common.AffiliateRewardRate
	common.AffiliateRewardEnabled = true
	common.AffiliateRewardRate = 0.05
	t.Cleanup(func() {
		common.AffiliateRewardEnabled, common.AffiliateRewardRate = previousEnabled, previousRate
	})

	require.NoError(t, DB.Create(&User{
		Id: 701, Username: "commission-inviter-701", AffCode: "cinv701", Status: common.UserStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Id: 702, Username: "commission-invitee-702", AffCode: "cuser702",
		InviterId: 701, Status: common.UserStatusEnabled,
	}).Error)

	invalid := []struct {
		name  string
		order TopUp
	}{
		{"negative_amount", TopUp{Amount: -2, Money: 2, PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay"}},
		{"zero_money", TopUp{Amount: 2, Money: 0, PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe}},
		{"negative_money", TopUp{Amount: 2, Money: -2, PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay"}},
		{"balance_provider", TopUp{Amount: 2, Money: 2, PaymentProvider: PaymentProviderBalance, PaymentMethod: PaymentMethodBalance}},
		{"no_credential", TopUp{Amount: 2, Money: 2}},
	}
	for i, tc := range invalid {
		tradeNo := fmt.Sprintf("MODEL-EVIDENCE-INVALID-%d", i)
		order := tc.order
		order.UserId = 702
		order.TradeNo = tradeNo
		order.Status = common.TopUpStatusSuccess
		order.CreateTime = common.GetTimestamp()
		order.CompleteTime = common.GetTimestamp()
		require.NoError(t, DB.Create(&order).Error)

		err := DB.Transaction(func(tx *gorm.DB) error {
			return RecordReferralCommissionForTopUpTx(tx, 702, 1_000_000, ReferralReferenceTopUp, tradeNo, LedgerReasonTopup)
		})
		require.NoError(t, err, tc.name)

		var commissions int64
		require.NoError(t, DB.Model(&ReferralCommission{}).Where("user_id = ?", 701).Count(&commissions).Error)
		assert.Zero(t, commissions, "%s 不得生成返佣", tc.name)
		assert.Zero(t, getUserBalances(t, 701).AffQuota, "%s 邀请人收益不得变化", tc.name)
	}

	totalInvalidEvents, err := countQualifyingTopUpEventsTx(DB, 702)
	require.NoError(t, err)
	assert.Zero(t, totalInvalidEvents, "伪订单不得计入充值计次")

	legacyTradeNo := "MODEL-EVIDENCE-LEGACY-PROVIDER"
	require.NoError(t, DB.Create(&TopUp{
		UserId: 702, Amount: 2, Money: 2, TradeNo: legacyTradeNo,
		PaymentMethod: "alipay", Status: common.TopUpStatusSuccess,
		CreateTime: common.GetTimestamp(), CompleteTime: common.GetTimestamp(),
	}).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return RecordReferralCommissionForTopUpTx(tx, 702, 1_000_000, ReferralReferenceTopUp, legacyTradeNo, LedgerReasonTopup)
	}), "空 provider 但有已知线上 method 与正实付的存量订单必须合格")

	var legacyCommissions int64
	require.NoError(t, DB.Model(&ReferralCommission{}).Where("user_id = ? AND reference_id = ?", 701, legacyTradeNo).Count(&legacyCommissions).Error)
	assert.EqualValues(t, 1, legacyCommissions)
	assert.Equal(t, 50_000, getUserBalances(t, 701).AffQuota)
}
