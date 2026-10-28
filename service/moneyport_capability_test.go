package service

// moneyport_capability_test.go 锁定 BillingSession 按 moneyport 能力（而非具体
// 资金来源类型）分派后的语义等价性：补充预扣的账本序列、订阅增量换算、
// 扣减回执契约，以及能力缺失时的 fail-closed 行为。

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ledgerRow 是账本条目中与计费语义相关的字段投影。
type ledgerRow struct {
	Account        string
	Delta          int
	Reason         string
	IdempotencyKey string
}

func ledgerRowsFor(t *testing.T, userID int) []ledgerRow {
	t.Helper()
	var entries []model.BalanceLedger
	require.NoError(t, model.DB.Where("user_id = ?", userID).Order("id asc").Find(&entries).Error)
	rows := make([]ledgerRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, ledgerRow{
			Account:        entry.Account,
			Delta:          entry.Delta,
			Reason:         entry.Reason,
			IdempotencyKey: entry.IdempotencyKey,
		})
	}
	return rows
}

// recordedWalletTestSession exercises the same atomic source/token/held entry
// as production. A source-only debit is not authority to extend a request hold.
func recordedWalletTestSession(t *testing.T, info *relaycommon.RelayInfo, amount int) *BillingSession {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	if info.RequestId == "" {
		info.RequestId = "recorded-wallet-" + model.BillingTokenFingerprint(t.Name())[:32]
	}
	if info.OriginModelName == "" {
		info.OriginModelName = "recorded-wallet-model"
	}
	seedBonusEligibleModel(t, info.OriginModelName, boolPtr(false))
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}
	info.ForcePreConsume = true
	info.UserSetting.BillingPreference = "wallet_only"
	c := testGinContext(t)
	c.Set("token_quota", 1_000_000)
	s, apiErr := NewBillingSession(c, info, amount)
	require.Nil(t, apiErr)
	info.Billing = s
	assertRecordedWalletHold(t, s, amount)
	return s
}

func assertRecordedWalletHold(t *testing.T, s *BillingSession, amount int) {
	t.Helper()
	var held model.BillingFinalization
	require.NoError(t, model.DB.Where("request_id = ?", s.relayInfo.RequestId).First(&held).Error)
	require.Equal(t, model.BillingHeld, held.State)
	assert.Equal(t, s.relayInfo.UserId, held.UserID)
	assert.Equal(t, BillingSourceWallet, held.Source)
	assert.Equal(t, amount, held.PreConsumed)
	assert.Equal(t, amount, held.SourceHeld)
	assert.Equal(t, amount, held.Paid)
	assert.Zero(t, held.Bonus)
	assert.Zero(t, held.Reward)
	wantToken := amount
	if s.relayInfo.IsPlayground {
		wantToken = 0
	}
	assert.Equal(t, wantToken, held.TokenHeld)
}

// fundingWithoutCapabilities 只实现 FundingSource，不具备任何 moneyport 能力，
// 用于验证能力缺失时 BillingSession 必须 fail-closed 而非静默降级。
type fundingWithoutCapabilities struct{}

func (fundingWithoutCapabilities) Source() string       { return "stub" }
func (fundingWithoutCapabilities) PreConsume(int) error { return nil }
func (fundingWithoutCapabilities) Settle(int) error     { return nil }
func (fundingWithoutCapabilities) Refund() error        { return nil }

type walletFundingWithoutTransactionalSettlement struct {
	settleCalls int
}

func (*walletFundingWithoutTransactionalSettlement) Source() string       { return BillingSourceWallet }
func (*walletFundingWithoutTransactionalSettlement) PreConsume(int) error { return nil }
func (f *walletFundingWithoutTransactionalSettlement) Settle(int) error {
	f.settleCalls++
	return nil
}
func (*walletFundingWithoutTransactionalSettlement) Refund() error { return nil }

// 钱包来源必须显式具备事务结算能力；不能退回 funding.Settle -> token 两步提交。
func TestWalletSettleFailsClosedWithoutTransactionalSettlementCapability(t *testing.T) {
	funding := &walletFundingWithoutTransactionalSettlement{}
	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{IsPlayground: true},
		funding:          funding,
		preConsumedQuota: 100,
		tokenConsumed:    100,
	}

	err := session.Settle(200)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support transactional settlement")
	assert.Zero(t, funding.settleCalls, "wallet source must not fall back to non-transactional Settle")
	assert.False(t, session.fundingSettled)
	assert.False(t, session.settled)
}

// ---------------------------------------------------------------------------
// 钱包：逐级补充预扣的账本序列必须与类型断言时期完全一致
// ---------------------------------------------------------------------------

func TestWalletReserveLedgerSequenceUnchanged(t *testing.T) {
	truncate(t)

	const userID = 8810
	const requestID = "moneyport-wallet-reserve-1"
	seedBonusUser(t, userID, 0, 20_000)

	session := recordedWalletTestSession(t, &relaycommon.RelayInfo{
		UserId: userID, RequestId: requestID, IsPlayground: true,
	}, 2_000)

	// 逐级抬升目标：每级只补差额，幂等键由目标预扣值派生。
	require.NoError(t, session.Reserve(5_000))
	require.NoError(t, session.Reserve(8_000))
	// 重复同一目标与下降目标都必须是 no-op，不得新增账本条目。
	require.NoError(t, session.Reserve(8_000))
	require.NoError(t, session.Reserve(3_000))

	assert.Equal(t, []ledgerRow{
		{Account: "paid", Delta: -2_000, Reason: model.LedgerReasonBillingPreConsume, IdempotencyKey: "billing:preconsume:" + requestID},
		{Account: "paid", Delta: -3_000, Reason: model.LedgerReasonBillingReserve, IdempotencyKey: "billing:reserve:" + requestID + ":5000"},
		{Account: "paid", Delta: -3_000, Reason: model.LedgerReasonBillingReserve, IdempotencyKey: "billing:reserve:" + requestID + ":8000"},
	}, ledgerRowsFor(t, userID))

	assert.Equal(t, 12_000, getUserQuota(t, userID))
	assert.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 8_000}}, session.WalletDebitShares())
	assert.Equal(t, 8_000, session.preConsumedQuota)
	// The durable full hold replaces a wallet's unused subscription-extra field.
	assertRecordedWalletHold(t, session, 8_000)
}

// ---------------------------------------------------------------------------
// 钱包回执：顺序、值、防御性复制，以及 service 边界的 DTO 还原
// ---------------------------------------------------------------------------

func TestWalletReceiptOrderValuesAndDefensiveCopy(t *testing.T) {
	truncate(t)

	const userID = 8811
	seedBonusUser(t, userID, 1_200, 5_000)

	funding := &WalletFunding{
		userId:     userID,
		requestId:  "moneyport-wallet-receipt-1",
		debitOrder: []model.LedgerAccount{model.AccountBonus, model.AccountPaid},
	}
	// 预扣 2000 跨两账户：bonus 1200 用尽后由 paid 补 800。
	require.NoError(t, funding.PreConsume(2_000))

	want := []moneyport.AccountShare{
		{Account: "bonus", Amount: 1_200},
		{Account: "paid", Amount: 800},
	}
	receipt := funding.Receipt()
	assert.Equal(t, want, receipt, "回执必须按 debitOrder 顺序输出各账户份额")

	// 防御性复制：调用方改写返回值不得污染资金来源的内部份额记录。
	receipt[0].Amount = 999_999
	receipt[1].Account = "tampered"
	assert.Equal(t, want, funding.Receipt(), "回执必须是副本")

	// service 边界把叶子 DTO 还原为账本份额，外部契约保持 model.LedgerShare。
	session := &BillingSession{relayInfo: &relaycommon.RelayInfo{IsPlayground: true}, funding: funding}
	wantShares := []model.LedgerShare{
		{Account: model.AccountBonus, Amount: 1_200},
		{Account: model.AccountPaid, Amount: 800},
	}
	assert.Equal(t, wantShares, session.WalletDebitShares())
	assert.Equal(t, wantShares, WalletSessionDebitShares(&relaycommon.RelayInfo{Billing: session}),
		"RelayInfo 路径必须与会话直接取值一致")
}

func TestReceiptNilForFundingWithoutReceiptCapability(t *testing.T) {
	// 订阅不按账户拆分扣减，没有回执能力：必须返回 nil 而不是空切片，
	// 保证 TaskPrivateData.WalletDebit 在订阅计费下保持不落字段。
	subSession := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{IsPlayground: true},
		funding:   &SubscriptionFunding{subscriptionId: 1, preConsumed: 100},
	}
	assert.Nil(t, subSession.Receipt())
	assert.Nil(t, subSession.WalletDebitShares())
	assert.Nil(t, WalletSessionDebitShares(&relaycommon.RelayInfo{Billing: subSession}))

	// 无回执能力的资金来源同样返回 nil。
	assert.Nil(t, (&BillingSession{funding: fundingWithoutCapabilities{}}).WalletDebitShares())

	// 无会话 / 非 BillingSession 计费实现：保持既有 nil 契约。
	assert.Nil(t, WalletSessionDebitShares(nil))
	assert.Nil(t, WalletSessionDebitShares(&relaycommon.RelayInfo{}))

	// 钱包未发生任何扣减时也必须是 nil，不能是长度为 0 的切片。
	assert.Nil(t, (&BillingSession{funding: &WalletFunding{}}).WalletDebitShares())
}

// ---------------------------------------------------------------------------
// 订阅：Reserve 按目标值换算增量，且不与 RelayInfo 同步值双计
// ---------------------------------------------------------------------------

func TestSubscriptionReserveUsesTargetDeltaWithoutDoubleCount(t *testing.T) {
	truncate(t)

	const userID, subID = 8812, 5501
	seedBonusUser(t, userID, 0, 1_000)
	// Start before pre-consume; the real factory atomically creates both receipts.
	seedSubscription(t, subID, userID, 100_000, 0)
	plan := &model.SubscriptionPlan{Title: "moneyport reserve", QuotaResetPeriod: model.SubscriptionResetNever}
	require.NoError(t, model.DB.Create(plan).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", subID).Updates(map[string]any{"plan_id": plan.Id, "quota_period": 1}).Error)
	relayInfo := &relaycommon.RelayInfo{IsPlayground: true, RequestId: "moneyport-sub-reserve-1", UserId: userID, ForcePreConsume: true}
	relayInfo.UserSetting.BillingPreference = "subscription_only"
	session, apiErr := NewBillingSession(testGinContext(t), relayInfo, 2_000)
	require.Nil(t, apiErr)
	funding, ok := session.funding.(*SubscriptionFunding)
	require.True(t, ok)
	require.EqualValues(t, 2_000, getSubscriptionUsed(t, subID))
	var held model.BillingFinalization
	require.NoError(t, model.DB.Where("request_id = ?", relayInfo.RequestId).First(&held).Error)
	require.Equal(t, model.BillingHeld, held.State)
	require.Equal(t, 2_000, held.SourceHeld)
	require.Zero(t, held.TokenHeld)

	// 只补 target 与预扣基线（amount + reserveDelta）的差额。
	require.NoError(t, session.Reserve(5_000))
	assert.EqualValues(t, 5_000, getSubscriptionUsed(t, subID))

	// 重复同一 target 与下降 target 都是 no-op，不得再动订阅用量。
	require.NoError(t, session.Reserve(5_000))
	require.NoError(t, session.Reserve(1_000))
	assert.EqualValues(t, 5_000, getSubscriptionUsed(t, subID))

	require.NoError(t, session.Reserve(8_000))
	assert.EqualValues(t, 8_000, getSubscriptionUsed(t, subID))

	// 资金来源自身也必须守住 no-op：绕过会话直接调用不得重复补扣。
	require.NoError(t, funding.Reserve(8_000))
	require.NoError(t, funding.Reserve(4_000))
	assert.EqualValues(t, 8_000, getSubscriptionUsed(t, subID))

	// RelayInfo 同步值只由 BillingSession.extraReserved 累加，
	// 订阅侧的 reserveDelta 不得再叠加一次（否则预扣值会翻倍）。
	assert.Equal(t, 6_000, session.extraReserved)
	assert.Equal(t, 6_000, funding.reserveDelta)
	assert.EqualValues(t, 8_000, relayInfo.SubscriptionPreConsumed)
	assert.EqualValues(t, 8_000, relayInfo.SubscriptionAmountUsedAfterPreConsume)
	assert.Equal(t, 8_000, relayInfo.FinalPreConsumedQuota)
	assert.Equal(t, subID, relayInfo.SubscriptionId)
	// A replay returns the same original identity and durable absolute reserve.
	require.NoError(t, funding.PreConsume(2_000))
	assert.EqualValues(t, 2_000, funding.preConsumed)
	assert.Equal(t, 6_000, funding.reserveDelta)
	assert.Equal(t, 8_000, int(funding.amount)+funding.reserveDelta)
	var receipt model.SubscriptionPreConsumeRecord
	require.NoError(t, model.DB.Where("request_id = ?", funding.requestId).First(&receipt).Error)
	assert.EqualValues(t, 1, receipt.QuotaPeriod)
	assert.EqualValues(t, 8000, receipt.ReservedAmount)
	assert.EqualValues(t, 2000, receipt.PreConsumed)
	assert.EqualValues(t, 8_000, getSubscriptionUsed(t, subID))
}

// ---------------------------------------------------------------------------
// Reserve 生命周期：资金失败 -> 令牌回滚 -> 同目标重试真实扣款
// ---------------------------------------------------------------------------

// TestReserveFundingFailureRollsBackTokenAndRetryDebitsFresh verifies atomic
// source/token reserve and same-target retries:
//  1. 资金 reserve 失败时源/令牌均不变，会话状态不前进；
//  2. 失败的尝试不落账本（无幂等键占位）；
//  3. 充值后同目标重试必须发生真实扣款，不得被旧幂等键重放吞掉；
//  4. 全程钱包余额 / 令牌额度 / 账本一致，无免费用量。
func TestReserveFundingFailureRollsBackTokenAndRetryDebitsFresh(t *testing.T) {
	truncate(t)

	const userID, tokenID = 8814, 7702
	const tokenKey = "moneyport-reserve-retry-token"
	const initialPaid = 4_000
	seedBonusUser(t, userID, 0, initialPaid)
	seedToken(t, tokenID, userID, tokenKey, 10_000)

	const requestID = "moneyport-reserve-retry-1"
	// Production initial preconsume debits both wallet and token by 2_000.
	session := recordedWalletTestSession(t, &relaycommon.RelayInfo{
		UserId: userID, TokenId: tokenID, TokenKey: tokenKey, RequestId: requestID,
	}, 2_000)

	// 1. 同目标补充预扣 3_000：钱包仅剩 2_000，资金失败。
	err := session.Reserve(5_000)
	require.Error(t, err, "余额不足必须失败，不得产生负余额欠费")
	assert.ErrorIs(t, err, ErrModelBonusNotSupported,
		"paid-only 扣减顺序下余额不足映射为赠金不可用哨兵错误")

	// 2. Failed reserve changes neither side of the original atomic hold.
	assert.Equal(t, 8_000, getTokenRemainQuota(t, tokenID), "原始预扣之外不得再扣令牌")
	assert.Equal(t, initialPaid-2_000, getUserBalances(t, userID).QuotaPaid, "资金失败不得扣减钱包")
	assert.Equal(t, 2_000, session.preConsumedQuota)
	assert.Zero(t, session.extraReserved)
	assertRecordedWalletHold(t, session, 2_000)
	assert.Equal(t, []ledgerRow{
		{Account: "paid", Delta: -2_000, Reason: model.LedgerReasonBillingPreConsume, IdempotencyKey: "billing:preconsume:" + requestID},
	}, ledgerRowsFor(t, userID), "失败的 reserve 尝试不得留下账本条目")

	// 3. 充值 +2_000（增量写入，保持 quota = paid + bonus 恒等式），同目标重试。
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"quota":      gorm.Expr("quota + ?", 2_000),
		"quota_paid": gorm.Expr("quota_paid + ?", 2_000),
	}).Error)
	require.NoError(t, session.Reserve(5_000), "同目标重试必须真实扣款，不得被旧幂等键重放吞掉")

	assert.Equal(t, 5_000, getTokenRemainQuota(t, tokenID), "原始 2_000 + 补扣 3_000")
	assert.Equal(t, initialPaid+2_000-2_000-3_000, getUserBalances(t, userID).QuotaPaid)
	assert.Equal(t, []ledgerRow{
		{Account: "paid", Delta: -2_000, Reason: model.LedgerReasonBillingPreConsume, IdempotencyKey: "billing:preconsume:" + requestID},
		{Account: "paid", Delta: -3_000, Reason: model.LedgerReasonBillingReserve, IdempotencyKey: "billing:reserve:" + requestID + ":5000"},
	}, ledgerRowsFor(t, userID))
	assert.Equal(t, 5_000, session.preConsumedQuota)
	assertRecordedWalletHold(t, session, 5_000)
	assert.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 5_000}}, session.WalletDebitShares())

	// 4. 达到目标后重复 Reserve 是 no-op：钱包 / 令牌 / 账本完全一致。
	require.NoError(t, session.Reserve(5_000))
	assert.Equal(t, 5_000, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, initialPaid-3_000, getUserBalances(t, userID).QuotaPaid)
}

// ---------------------------------------------------------------------------
// 退款判定：官方 tokenConsumed / 具体 SubscriptionFunding 状态
// ---------------------------------------------------------------------------

func TestNeedsRefundUsesOutstandingReserveCapability(t *testing.T) {
	// Keep the legacy test name for inventory continuity. The official predicate
	// no longer consults the optional OutstandingReserve capability.
	newSession := func(funding FundingSource) *BillingSession {
		return &BillingSession{relayInfo: &relaycommon.RelayInfo{IsPlayground: true}, funding: funding}
	}

	// 订阅报告了待退预扣：tokenConsumed=0 时仍需退款。
	assert.True(t, newSession(&SubscriptionFunding{preConsumed: 1_500}).NeedsRefund())
	// 订阅无待退预扣：无需退款。
	assert.False(t, newSession(&SubscriptionFunding{}).NeedsRefund())

	// 官方钱包判定只看 tokenConsumed，不读取资金来源的可选能力。
	walletSession := newSession(&WalletFunding{consumed: 2_000})
	assert.False(t, walletSession.NeedsRefund())
	walletSession.tokenConsumed = 2_000
	assert.True(t, walletSession.NeedsRefund())

	// 其他资金来源在 tokenConsumed=0 时也不触发官方退款。
	assert.False(t, newSession(fundingWithoutCapabilities{}).NeedsRefund())

	// 已结算 / 已退款 / 资金已提交的会话不再需要退款。
	for _, mutate := range []func(*BillingSession){
		func(s *BillingSession) { s.settled = true },
		func(s *BillingSession) { s.refunded = true },
		func(s *BillingSession) { s.fundingSettled = true },
	} {
		session := newSession(&SubscriptionFunding{preConsumed: 1_500})
		mutate(session)
		assert.False(t, session.NeedsRefund())
	}
}

// ---------------------------------------------------------------------------
// 能力缺失 fail-closed：Reserve 明确报错并回滚已扣令牌额度
// ---------------------------------------------------------------------------

func TestReserveFailsClosedWithoutReservableCapability(t *testing.T) {
	truncate(t)

	const userID, tokenID = 8813, 7701
	const tokenKey = "moneyport-stub-token"
	seedBonusUser(t, userID, 0, 20_000)
	seedToken(t, tokenID, userID, tokenKey, 10_000)

	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:   userID,
			TokenId:  tokenID,
			TokenKey: tokenKey,
		},
		funding:          fundingWithoutCapabilities{},
		preConsumedQuota: 2_000,
		tokenConsumed:    2_000,
	}

	err := session.Reserve(5_000)
	require.Error(t, err, "资金来源缺少 Reserve 能力必须明确失败，不得静默放行")

	var apiErr *types.NewAPIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, types.ErrorCodeUpdateDataError, apiErr.GetErrorCode())
	assert.Contains(t, err.Error(), "unsupported funding source: stub")

	// 先令牌后资金：资金侧失败必须回滚已扣令牌额度，且会话状态不前进。
	assert.Equal(t, 10_000, getTokenRemainQuota(t, tokenID), "令牌额度必须回滚")
	assert.Equal(t, 2_000, session.preConsumedQuota)
	assert.Equal(t, 2_000, session.tokenConsumed)
	assert.Zero(t, session.extraReserved)
}
