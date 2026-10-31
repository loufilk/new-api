package service

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 邀请奖励：好友前三次成功在线余额充值各返有效比例，第四次起不再返佣
// ---------------------------------------------------------------------------

func setReferralRewardConfig(t *testing.T, enabled bool, rate float64) {
	t.Helper()
	previousEnabled, previousRate := common.AffiliateRewardEnabled, common.AffiliateRewardRate
	common.AffiliateRewardEnabled = enabled
	common.AffiliateRewardRate = rate
	t.Cleanup(func() {
		common.AffiliateRewardEnabled = previousEnabled
		common.AffiliateRewardRate = previousRate
	})
}

func seedReferralUser(t *testing.T, id, inviterId, status int) {
	t.Helper()
	user := &model.User{
		Id:        id,
		Username:  fmt.Sprintf("referral-user-%d", id),
		AffCode:   fmt.Sprintf("refcode-%d", id),
		InviterId: inviterId,
		Status:    status,
	}
	require.NoError(t, model.DB.Create(user).Error)
}

func seedPendingOnlineTopUp(t *testing.T, userId int, amount int64, tradeNo string) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId:          userId,
		Amount:          amount,
		Money:           float64(amount),
		TradeNo:         tradeNo,
		PaymentProvider: model.PaymentProviderEpay,
		CreateTime:      common.GetTimestamp(),
		Status:          common.TopUpStatusPending,
	}).Error)
}

func settleOnlineTopUp(t *testing.T, tradeNo string, amount int64) {
	t.Helper()
	alreadyDone, err := model.RechargeEpay(tradeNo, "alipay", fmt.Sprintf("%d.00", amount), "127.0.0.1")
	require.NoError(t, err)
	require.False(t, alreadyDone)
	drainReferralOutbox(t)
}

func drainReferralOutbox(t *testing.T) {
	t.Helper()
	_, err := model.ProcessAgentCommissionOutbox(context.Background(), 200)
	require.NoError(t, err)
}

func countReferralCommissions(t *testing.T, inviterId int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.ReferralCommission{}).Where("user_id = ?", inviterId).Count(&count).Error)
	return count
}

func seedOnlineTopUpOrder(t *testing.T, userId int, amount int64, tradeNo string) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId:          userId,
		Amount:          amount,
		Money:           float64(amount),
		TradeNo:         tradeNo,
		PaymentProvider: model.PaymentProviderEpay,
		CreateTime:      common.GetTimestamp(),
		CompleteTime:    common.GetTimestamp(),
		Status:          common.TopUpStatusSuccess,
	}).Error)
}

func TestReferralCommissionFirstThreeOnlineTopUpsAwardPendingReward(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 601, 0, common.UserStatusEnabled)
	seedReferralUser(t, 602, 601, common.UserStatusEnabled)

	tradeNos := []string{"REF-ONLINE-1", "REF-ONLINE-2", "REF-ONLINE-3", "REF-ONLINE-4"}
	for _, tradeNo := range tradeNos {
		seedPendingOnlineTopUp(t, 602, 2, tradeNo)
		settleOnlineTopUp(t, tradeNo, 2)
	}

	var commissions []model.ReferralCommission
	require.NoError(t, model.DB.Where("user_id = ? AND source_user_id = ?", 601, 602).Order("id asc").Find(&commissions).Error)
	require.Len(t, commissions, 3, "只有前三次成功充值产生返佣")
	for i, commission := range commissions {
		assert.Equal(t, 1_000_000, commission.PaidQuota)
		assert.Equal(t, 50_000, commission.RewardQuota, "第 %d 次充值返佣应为 5%%", i+1)
		assert.Equal(t, 0.05, commission.Rate)
		assert.Equal(t, model.ReferralReferenceTopUp, commission.ReferenceType)
		assert.Equal(t, tradeNos[i], commission.ReferenceId)
	}

	var fourth int64
	require.NoError(t, model.DB.Model(&model.ReferralCommission{}).
		Where("user_id = ? AND reference_id = ?", 601, tradeNos[3]).Count(&fourth).Error)
	assert.Zero(t, fourth, "第四次充值不得生成返佣记录")

	inviter := getUserBalances(t, 601)
	assert.Equal(t, 150_000, inviter.AffQuota, "奖励进入待使用收益")
	assert.Equal(t, 150_000, inviter.AffHistoryQuota, "累计收益同步记录")
	assert.Zero(t, inviter.QuotaReward, "未经划转不得进入可消费余额")
	assert.Zero(t, inviter.Quota)
	assertQuotaInvariant(t, inviter)
}

func TestReferralCommissionReplayedCallbackDoesNotDoubleAward(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 603, 0, common.UserStatusEnabled)
	seedReferralUser(t, 604, 603, common.UserStatusEnabled)

	seedPendingOnlineTopUp(t, 604, 2, "REF-REPLAY-1")
	settleOnlineTopUp(t, "REF-REPLAY-1", 2)

	alreadyDone, err := model.RechargeEpay("REF-REPLAY-1", "alipay", "2.00", "127.0.0.1")
	require.NoError(t, err)
	require.True(t, alreadyDone)

	assert.EqualValues(t, 1, countReferralCommissions(t, 603))
	inviter := getUserBalances(t, 603)
	assert.Equal(t, 50_000, inviter.AffQuota)
}

func TestReferralCommissionCountsHistoricalSuccessfulOnlineOrders(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 605, 0, common.UserStatusEnabled)
	seedReferralUser(t, 606, 605, common.UserStatusEnabled)

	// 历史成功在线订单（无新返佣记录、无账本），计次必须覆盖。
	seedOnlineTopUpOrder(t, 606, 2, "REF-HISTORY-1")
	seedOnlineTopUpOrder(t, 606, 2, "REF-HISTORY-2")

	seedPendingOnlineTopUp(t, 606, 2, "REF-HISTORY-3")
	settleOnlineTopUp(t, "REF-HISTORY-3", 2)
	seedPendingOnlineTopUp(t, 606, 2, "REF-HISTORY-4")
	settleOnlineTopUp(t, "REF-HISTORY-4", 2)

	var commissions []model.ReferralCommission
	require.NoError(t, model.DB.Where("user_id = ?", 605).Find(&commissions).Error)
	require.Len(t, commissions, 1, "历史两单加本次为第三次，只有本次产生返佣")
	assert.Equal(t, "REF-HISTORY-3", commissions[0].ReferenceId)
	assert.Equal(t, 50_000, getUserBalances(t, 605).AffQuota)
}

func TestReferralCommissionIgnoresSubscriptionLinkedTopUpsInCount(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 607, 0, common.UserStatusEnabled)
	seedReferralUser(t, 608, 607, common.UserStatusEnabled)

	// subscription_order 关联的 top_ups 即使金额非 0 也不得计次。
	seedOnlineTopUpOrder(t, 608, 2, "REF-SUB-LINKED")
	require.NoError(t, model.DB.Create(&model.SubscriptionOrder{
		UserId: 608, PlanId: 1, Money: 2, TradeNo: "REF-SUB-LINKED",
		PaymentProvider: model.PaymentProviderStripe, Status: common.TopUpStatusSuccess,
		CreateTime: common.GetTimestamp(), CompleteTime: common.GetTimestamp(),
	}).Error)
	// 订阅 upsert 形态：Amount = 0。
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId: 608, Amount: 0, Money: 2, TradeNo: "REF-SUB-ZERO",
		PaymentMethod: "stripe", Status: common.TopUpStatusSuccess,
		CreateTime: common.GetTimestamp(), CompleteTime: common.GetTimestamp(),
	}).Error)
	// 余额标记务必排除。
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId: 608, Amount: 2, Money: 2, TradeNo: "REF-BALANCE-MARKED",
		PaymentMethod: model.PaymentMethodBalance, Status: common.TopUpStatusSuccess,
		CreateTime: common.GetTimestamp(), CompleteTime: common.GetTimestamp(),
	}).Error)

	for i := 1; i <= 3; i++ {
		tradeNo := fmt.Sprintf("REF-REAL-%d", i)
		seedPendingOnlineTopUp(t, 608, 2, tradeNo)
		settleOnlineTopUp(t, tradeNo, 2)
	}

	assert.EqualValues(t, 3, countReferralCommissions(t, 607), "订阅与余额标记订单不得占用返佣次数")
	assert.Equal(t, 150_000, getUserBalances(t, 607).AffQuota)
}

// 历史成功订单必须有真实正实付与在线支付凭证才计入前三次机会：负 Amount、
// 正 Amount 但 Money<=0、余额账户、无任何 provider/method 的伪订单都不得占用
// 名额，旧数据同样如此。
func TestReferralCommissionIgnoresInvalidPaidEvidenceInCount(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 631, 0, common.UserStatusEnabled)
	seedReferralUser(t, 632, 631, common.UserStatusEnabled)

	invalidTradeNos := []string{
		"REF-EVIDENCE-NEG-AMOUNT",
		"REF-EVIDENCE-ZERO-MONEY",
		"REF-EVIDENCE-NEG-MONEY",
		"REF-EVIDENCE-BALANCE",
		"REF-EVIDENCE-NO-CREDENTIAL",
	}
	orders := []model.TopUp{
		{UserId: 632, Amount: -2, Money: 2, TradeNo: invalidTradeNos[0], PaymentProvider: model.PaymentProviderEpay, PaymentMethod: "alipay"},
		{UserId: 632, Amount: 2, Money: 0, TradeNo: invalidTradeNos[1], PaymentProvider: model.PaymentProviderStripe, PaymentMethod: model.PaymentMethodStripe},
		{UserId: 632, Amount: 2, Money: -2, TradeNo: invalidTradeNos[2], PaymentProvider: model.PaymentProviderEpay, PaymentMethod: "alipay"},
		{UserId: 632, Amount: 2, Money: 2, TradeNo: invalidTradeNos[3], PaymentProvider: model.PaymentProviderBalance, PaymentMethod: model.PaymentMethodBalance},
		{UserId: 632, Amount: 2, Money: 2, TradeNo: invalidTradeNos[4]},
	}
	for i := range orders {
		orders[i].Status = common.TopUpStatusSuccess
		orders[i].CreateTime = common.GetTimestamp()
		orders[i].CompleteTime = common.GetTimestamp()
		require.NoError(t, model.DB.Create(&orders[i]).Error)
	}

	for i := 1; i <= 3; i++ {
		tradeNo := fmt.Sprintf("REF-EVIDENCE-REAL-%d", i)
		seedPendingOnlineTopUp(t, 632, 2, tradeNo)
		settleOnlineTopUp(t, tradeNo, 2)
	}

	assert.EqualValues(t, 3, countReferralCommissions(t, 631), "伪订单不得占用前三次机会")
	assert.Equal(t, 150_000, getUserBalances(t, 631).AffQuota)

	var invalidAwards int64
	require.NoError(t, model.DB.Model(&model.ReferralCommission{}).
		Where("reference_id IN ?", invalidTradeNos).Count(&invalidAwards).Error)
	assert.Zero(t, invalidAwards, "伪订单不得自身产生返佣")
}

func TestReferralCommissionIgnoresRedemptionAdminAndSubscriptionPaths(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 609, 0, common.UserStatusEnabled)
	seedReferralUser(t, 610, 609, common.UserStatusEnabled)
	seedReferralUser(t, 611, 609, common.UserStatusEnabled)
	seedReferralUser(t, 612, 609, common.UserStatusEnabled)

	// 兑换码：不计次、不返佣（也不占用前三次名额）。
	require.NoError(t, model.DB.Create(&model.Redemption{
		Key: "REF-REDEEM-CODE", Status: common.RedemptionCodeStatusEnabled, Quota: 1_000_000,
		Name: "referral-test", CreatedTime: common.GetTimestamp(),
	}).Error)
	_, err := model.Redeem("REF-REDEEM-CODE", 610)
	require.NoError(t, err)
	assert.Zero(t, countReferralCommissions(t, 609), "兑换码不得产生返佣")

	// 管理员直接加额：不计次、不返佣。
	_, err = model.AdjustUserQuota(611, common.RoleRootUser, "add", 1_000_000, model.LedgerEntry{
		ReferenceType: "user", ReferenceId: "611",
	})
	require.NoError(t, err)
	assert.Zero(t, countReferralCommissions(t, 609), "管理员加额不得产生返佣")

	// 外部订阅：计费成功但不计入在线余额返佣。
	plan := &model.SubscriptionPlan{
		Title: "referral-plan", PriceAmount: 9.99, Currency: "USD",
		DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1, Enabled: true, TotalAmount: 1000,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	require.NoError(t, model.DB.Create(&model.SubscriptionOrder{
		UserId: 612, PlanId: plan.Id, Money: plan.PriceAmount, TradeNo: "REF-SUB-ORDER",
		PaymentProvider: model.PaymentProviderEpay, Status: common.TopUpStatusPending,
		CreateTime: common.GetTimestamp(),
	}).Error)
	require.NoError(t, model.CompleteSubscriptionOrder("REF-SUB-ORDER", "{}", model.PaymentProviderEpay, ""))
	assert.Zero(t, countReferralCommissions(t, 609), "外部订阅不得产生返佣")

	// 余额购买订阅：只扣 paid，不产生 top_ups，也不返佣。
	paidUser := getUserBalances(t, 612)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 612).Updates(map[string]interface{}{
		"quota_paid": paidUser.QuotaPaid + 10_000_000,
		"quota":      paidUser.Quota + 10_000_000,
	}).Error)
	allowBalancePay := true
	balancePlan := &model.SubscriptionPlan{
		Title: "referral-balance-plan", PriceAmount: 1, Currency: "USD",
		DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1, Enabled: true, TotalAmount: 1000,
		AllowBalancePay: &allowBalancePay,
	}
	require.NoError(t, model.DB.Create(balancePlan).Error)
	require.NoError(t, model.PurchaseSubscriptionWithBalance(612, balancePlan.Id))
	assert.Zero(t, countReferralCommissions(t, 609), "余额购买订阅不得产生返佣")
}

func TestReferralCommissionTruncatesFractionalReward(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	previousQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 21
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })

	seedReferralUser(t, 613, 0, common.UserStatusEnabled)
	seedReferralUser(t, 614, 613, common.UserStatusEnabled)

	seedPendingOnlineTopUp(t, 614, 1, "REF-ROUND-1")
	settleOnlineTopUp(t, "REF-ROUND-1", 1)

	var commission model.ReferralCommission
	require.NoError(t, model.DB.Where("user_id = ?", 613).First(&commission).Error)
	assert.Equal(t, 21, commission.PaidQuota)
	assert.Equal(t, 1, commission.RewardQuota, "21×5%%=1.05 必须向下取整为 1")
	assert.Equal(t, 1, getUserBalances(t, 613).AffQuota)
}

func TestReferralCommissionDisabledOrZeroRateProducesNothing(t *testing.T) {
	truncate(t)
	seedReferralUser(t, 615, 0, common.UserStatusEnabled)
	seedReferralUser(t, 616, 615, common.UserStatusEnabled)

	setReferralRewardConfig(t, false, 0.05)
	seedPendingOnlineTopUp(t, 616, 2, "REF-DISABLED-1")
	settleOnlineTopUp(t, "REF-DISABLED-1", 2)
	assert.Zero(t, countReferralCommissions(t, 615))

	setReferralRewardConfig(t, true, 0)
	seedPendingOnlineTopUp(t, 616, 2, "REF-ZERO-RATE-1")
	settleOnlineTopUp(t, "REF-ZERO-RATE-1", 2)
	assert.Zero(t, countReferralCommissions(t, 615))
	assert.Zero(t, getUserBalances(t, 615).AffQuota)
}

// Invalid reward policy is a durable worker exception, never a reason to
// discard a separately verified customer payment. Its original snapshot remains
// unchanged and cannot silently adopt a later commercial rate.
func TestReferralCommissionInvalidRateKeepsPrincipalAndRetryableEvent(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	for i, rate := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1.5, -0.05} {
		common.AffiliateRewardRate = rate
		inviterID := 660 + i*2
		buyerID := inviterID + 1
		seedReferralUser(t, inviterID, 0, common.UserStatusEnabled)
		seedReferralUser(t, buyerID, inviterID, common.UserStatusEnabled)
		trade := fmt.Sprintf("REF-INVALID-RATE-%d", i)
		seedPendingOnlineTopUp(t, buyerID, 2, trade)
		done, err := model.RechargeEpay(trade, "alipay", "2.00", "127.0.0.1")
		require.NoError(t, err)
		assert.False(t, done)
		_, err = model.ProcessAgentCommissionOutbox(context.Background(), 200)
		require.Error(t, err)
		assertReferralPrincipalPreserved(t, buyerID, inviterID, trade)
	}
}

func assertReferralPrincipalPreserved(t *testing.T, buyerID, inviterID int, trade string) {
	t.Helper()
	var order model.TopUp
	require.NoError(t, model.DB.Where("trade_no = ?", trade).First(&order).Error)
	assert.Equal(t, common.TopUpStatusSuccess, order.Status)
	assert.Equal(t, 1_000_000, getUserBalances(t, buyerID).QuotaPaid)
	var ledgerRows int64
	require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Where("reference_id = ?", trade).Count(&ledgerRows).Error)
	assert.EqualValues(t, 1, ledgerRows)
	assert.Zero(t, countReferralCommissions(t, inviterID))
	var event model.AgentCommissionOutbox
	require.NoError(t, model.DB.Where("reference_id = ?", trade).First(&event).Error)
	assert.Equal(t, "failed", event.Status)
	assert.Positive(t, event.Attempts)
	var inbox int64
	require.NoError(t, model.DB.Model(&model.AgentCommissionInbox{}).Where("event_id = ?", event.ID).Count(&inbox).Error)
	assert.Zero(t, inbox, "failed reward has no success receipt")
}

func TestReferralCommissionSkipsInvalidInviter(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 617, 0, common.UserStatusDisabled)
	seedReferralUser(t, 618, 617, common.UserStatusEnabled) // 邀请人已禁用
	seedReferralUser(t, 619, 999, common.UserStatusEnabled) // 邀请人不存在
	seedReferralUser(t, 620, 620, common.UserStatusEnabled) // 自邀请（脏数据）

	for _, userId := range []int{618, 619, 620} {
		tradeNo := fmt.Sprintf("REF-INVALID-%d", userId)
		seedPendingOnlineTopUp(t, userId, 2, tradeNo)
		settleOnlineTopUp(t, tradeNo, 2)
	}
	assert.Zero(t, countReferralCommissions(t, 617))
	assert.EqualValues(t, 0, countReferralCommissions(t, 619))
	assert.EqualValues(t, 0, countReferralCommissions(t, 620))
}

func TestReferralCommissionInvalidBalancesDoNotRollBackPrincipal(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	for i, balances := range [][2]int{{common.MaxWalletQuota, 0}, {0, common.MaxWalletQuota}, {-1, 0}} {
		inviterID := 620 + i*2
		buyerID := inviterID + 1
		seedReferralUser(t, inviterID, 0, common.UserStatusEnabled)
		seedReferralUser(t, buyerID, inviterID, common.UserStatusEnabled)
		require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", inviterID).Updates(map[string]any{"aff_quota": balances[0], "aff_history": balances[1]}).Error)
		trade := fmt.Sprintf("REF-BAD-BALANCE-%d", i)
		seedPendingOnlineTopUp(t, buyerID, 2, trade)
		_, err := model.RechargeEpay(trade, "alipay", "2.00", "127.0.0.1")
		require.NoError(t, err)
		_, err = model.ProcessAgentCommissionOutbox(context.Background(), 200)
		require.Error(t, err)
		assertReferralPrincipalPreserved(t, buyerID, inviterID, trade)
		user := getUserBalances(t, inviterID)
		assert.Equal(t, balances[0], user.AffQuota)
		assert.Equal(t, balances[1], user.AffHistoryQuota)
	}
}

func TestReferralCommissionConcurrentInviteesAtAffBoundary(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 641, 0, common.UserStatusEnabled)
	seedReferralUser(t, 642, 641, common.UserStatusEnabled)
	seedReferralUser(t, 643, 641, common.UserStatusEnabled)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 641).Update("aff_quota", common.MaxWalletQuota-50_000).Error)
	errs := make(chan error, 2)
	for i := range 2 {
		trade := fmt.Sprintf("REF-BOUNDARY-%d", i)
		seedPendingOnlineTopUp(t, 642+i, 2, trade)
		go func() { _, err := model.RechargeEpay(trade, "alipay", "2.00", "127.0.0.1"); errs <- err }()
	}
	for range 2 {
		require.NoError(t, <-errs)
	}
	_, err := model.ProcessAgentCommissionOutbox(context.Background(), 200)
	require.Error(t, err, "one award remains retryable at the upper bound")
	assert.Equal(t, 1_000_000, getUserBalances(t, 642).QuotaPaid)
	assert.Equal(t, 1_000_000, getUserBalances(t, 643).QuotaPaid)
	assert.EqualValues(t, 1, countReferralCommissions(t, 641))
	inviter := getUserBalances(t, 641)
	assert.Equal(t, common.MaxWalletQuota, inviter.AffQuota)
	assert.Equal(t, 50_000, inviter.AffHistoryQuota)
	var pending int64
	require.NoError(t, model.DB.Model(&model.AgentCommissionOutbox{}).Where("status = ?", "failed").Count(&pending).Error)
	assert.EqualValues(t, 1, pending)
}

// 管理员人工完成订单使用 admin_topup：不得产生返佣，也不得占用用户的前三次
// 在线充值计次。
func TestReferralCommissionManualCompleteTopUpDoesNotAwardOrCount(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 645, 0, common.UserStatusEnabled)
	seedReferralUser(t, 646, 645, common.UserStatusEnabled)

	seedPendingOnlineTopUp(t, 646, 2, "REF-MANUAL-ADMIN")
	require.NoError(t, model.ManualCompleteTopUp("REF-MANUAL-ADMIN", "127.0.0.1"))
	assert.Zero(t, countReferralCommissions(t, 645), "管理员补单不得返佣")
	assert.Zero(t, getUserBalances(t, 645).AffQuota)
	assert.Equal(t, 1_000_000, getUserBalances(t, 646).QuotaPaid, "补单充值本身成功")

	// 补单不占次：后续三次真实在线充值仍应各返一次。
	for i := 1; i <= 3; i++ {
		tradeNo := fmt.Sprintf("REF-AFTER-MANUAL-%d", i)
		seedPendingOnlineTopUp(t, 646, 2, tradeNo)
		settleOnlineTopUp(t, tradeNo, 2)
	}
	assert.EqualValues(t, 3, countReferralCommissions(t, 645), "管理员补单不得挤占在线充值计次")
	assert.Equal(t, 150_000, getUserBalances(t, 645).AffQuota)
}

// 账本幂等重放（例如订单状态被修复后重放历史充值）不得补发返佣。
func TestReferralCommissionLedgerReplayDoesNotBackfillReward(t *testing.T) {
	truncate(t)
	seedReferralUser(t, 647, 0, common.UserStatusEnabled)
	seedReferralUser(t, 648, 647, common.UserStatusEnabled)

	setReferralRewardConfig(t, false, 0.05)
	seedPendingOnlineTopUp(t, 648, 2, "REF-LEDGER-REPLAY")
	settleOnlineTopUp(t, "REF-LEDGER-REPLAY", 2)
	require.Equal(t, 1_000_000, getUserBalances(t, 648).QuotaPaid)

	// 模拟订单状态修复后重放：账本已存在，CreditQuotaTx 会命中幂等重放。
	require.NoError(t, model.DB.Model(&model.TopUp{}).Where("trade_no = ?", "REF-LEDGER-REPLAY").
		Update("status", common.TopUpStatusPending).Error)
	setReferralRewardConfig(t, true, 0.05)
	alreadyDone, err := model.RechargeEpay("REF-LEDGER-REPLAY", "alipay", "2.00", "127.0.0.1")
	require.NoError(t, err)
	require.False(t, alreadyDone)

	assert.Zero(t, countReferralCommissions(t, 647), "账本重放不得补发返佣")
	assert.Equal(t, 1_000_000, getUserBalances(t, 648).QuotaPaid, "重放不得重复入账")
	assert.Zero(t, getUserBalances(t, 647).AffQuota)
}

// 有效微小金额向下取整为 0：属于正常业务结果，充值成功但无奖励。
func TestReferralCommissionZeroRewardAfterTruncationStillTopsUp(t *testing.T) {
	truncate(t)
	seedReferralUser(t, 649, 0, common.UserStatusEnabled)
	seedReferralUser(t, 650, 649, common.UserStatusEnabled)

	setReferralRewardConfig(t, true, 1e-9)
	seedPendingOnlineTopUp(t, 650, 1, "REF-TINY-REWARD")
	settleOnlineTopUp(t, "REF-TINY-REWARD", 1)

	assert.Zero(t, countReferralCommissions(t, 649), "向下取整为 0 时不生成凭证")
	assert.Equal(t, 500_000, getUserBalances(t, 650).QuotaPaid)
	assert.Zero(t, getUserBalances(t, 649).AffQuota)
}

func TestReferralCommissionConcurrentTopUpsCapAtThree(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 623, 0, common.UserStatusEnabled)
	seedReferralUser(t, 624, 623, common.UserStatusEnabled)

	const events = 5
	for i := 0; i < events; i++ {
		seedPendingOnlineTopUp(t, 624, 2, fmt.Sprintf("REF-CONCURRENT-%d", i))
	}

	var wg sync.WaitGroup
	errs := make([]error, events)
	wg.Add(events)
	for i := 0; i < events; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = model.RechargeEpay(fmt.Sprintf("REF-CONCURRENT-%d", idx), "alipay", "2.00", "127.0.0.1")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "event %d", i)
	}

	drainReferralOutbox(t)
	assert.EqualValues(t, 3, countReferralCommissions(t, 623), "并发下第四次及以后不得返佣")
	assert.Equal(t, 150_000, getUserBalances(t, 623).AffQuota)
}

func TestReferralCommissionTransferMakesRewardSpendableOnNonBonusModel(t *testing.T) {
	truncate(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralUser(t, 625, 0, common.UserStatusEnabled)
	seedReferralUser(t, 626, 625, common.UserStatusEnabled)

	// 三笔 20 美元充值：每笔 10,000,000 quota，返佣 500,000。
	for i := 1; i <= 3; i++ {
		tradeNo := fmt.Sprintf("REF-TRANSFER-%d", i)
		seedPendingOnlineTopUp(t, 626, 20, tradeNo)
		settleOnlineTopUp(t, tradeNo, 20)
	}
	inviter := getUserBalances(t, 625)
	require.Equal(t, 1_500_000, inviter.AffQuota)

	// 最低划转额度即 common.QuotaPerUnit。
	result, err := model.TransferAffQuota(625, 1_000_000, "referral-transfer-e2e")
	require.NoError(t, err)
	require.False(t, result.Replayed)

	inviter = getUserBalances(t, 625)
	assert.Equal(t, 500_000, inviter.AffQuota)
	assert.Equal(t, 1_000_000, inviter.QuotaReward)
	assert.Equal(t, 1_500_000, inviter.AffHistoryQuota)

	// 非 bonus_eligible 模型：reward 可用，paid 保持不动。
	seedBonusEligibleModel(t, "referral-non-bonus-model", boolPtr(false))
	funding := &WalletFunding{
		userId:     625,
		requestId:  "referral-reward-spend",
		debitOrder: WalletDebitOrder(walletInfoFor(t, 625, "referral-non-bonus-model")),
	}
	require.NoError(t, funding.PreConsume(400_000))
	spent := getUserBalances(t, 625)
	assert.Equal(t, 600_000, spent.QuotaReward, "reward 账户被扣减")
	assert.Zero(t, spent.QuotaPaid, "非赠金模型不得触碰 paid 之外的账户")
	assertQuotaInvariant(t, spent)

	// 失败退款必须精确退回 reward 来源。
	require.NoError(t, funding.Refund())
	refunded := getUserBalances(t, 625)
	assert.Equal(t, 1_000_000, refunded.QuotaReward, "退款回到 reward，不得洗成 paid")
	assertQuotaInvariant(t, refunded)
}
