package service

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(v bool) *bool { return &v }

func testGinContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

func getUserBalances(t *testing.T, id int) model.User {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Where("id = ?", id).First(&user).Error)
	return user
}

func assertQuotaInvariant(t *testing.T, user model.User) {
	t.Helper()
	assert.Equal(t, user.QuotaPaid+user.QuotaBonus+user.QuotaReward, user.Quota,
		"quota must equal paid+bonus+reward")
}

// ---------------------------------------------------------------------------
// SplitRefund 按原始份额比例拆分退款
// ---------------------------------------------------------------------------

func TestSplitRefundProportionalAndExact(t *testing.T) {
	original := []model.LedgerShare{
		{Account: model.AccountBonus, Amount: 70},
		{Account: model.AccountPaid, Amount: 30},
	}

	// 全额退款：原份额原样退回
	shares := SplitRefund(original, 100)
	require.Len(t, shares, 2)
	assert.Equal(t, 70, shares[0].Amount)
	assert.Equal(t, 30, shares[1].Amount)

	// 按比例拆分：余数归第一位账户（bonus）
	shares = SplitRefund(original, 60)
	require.Len(t, shares, 2)
	assert.Equal(t, 42, shares[0].Amount)
	assert.Equal(t, 18, shares[1].Amount)
	assert.Equal(t, 60, shares[0].Amount+shares[1].Amount)

	shares = SplitRefund(original, 61)
	require.Len(t, shares, 2)
	assert.Equal(t, 43, shares[0].Amount)
	assert.Equal(t, 18, shares[1].Amount)

	// An excessive refund is inconsistent provenance, not a full-refund alias.
	assert.Nil(t, SplitRefund(original, 1000))

	// 空/非正输入
	assert.Nil(t, SplitRefund(nil, 10))
	assert.Nil(t, SplitRefund(original, 0))
	assert.Nil(t, SplitRefund(original, -5))

	// 多次部分退款 + 全额退款叠加 = 各账户累计精确退回
	user := seedBonusUser(t, 911, 70, 30)
	funding := &WalletFunding{userId: user, requestId: "split-refund-seq-1", debitOrder: []model.LedgerAccount{model.AccountBonus, model.AccountPaid}}
	require.NoError(t, funding.PreConsume(100))
	require.NoError(t, funding.Settle(-40)) // 实际 60
	require.NoError(t, funding.Settle(-60)) // 实际 0（继续按比例退还）
	u := getUserBalances(t, user)
	assert.Equal(t, 70, u.QuotaBonus, "bonus 累计退回必须精确等于原始扣款")
	assert.Equal(t, 30, u.QuotaPaid, "paid 累计退回必须精确等于原始扣款")
	assertQuotaInvariant(t, u)
}

func seedBonusUser(t *testing.T, id int, bonus, paid int) int {
	t.Helper()
	user := &model.User{Id: id, Username: fmt.Sprintf("bonus-user-%d", id), AffCode: fmt.Sprintf("bonus-aff-%d", id), Quota: bonus + paid, QuotaBonus: bonus, QuotaPaid: paid, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
	return id
}

func seedBonusEligibleModel(t *testing.T, name string, eligible *bool) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.Model{ModelName: name, Status: 1, SyncOfficial: 1, BonusEligible: eligible}).Error)
	model.InvalidateModelBonusEligibleCache()
}

func walletInfoFor(t *testing.T, userId int, modelName string) *relaycommon.RelayInfo {
	t.Helper()
	return &relaycommon.RelayInfo{UserId: userId, RequestId: fmt.Sprintf("req-%d-%s", userId, modelName), OriginModelName: modelName, IsPlayground: true}
}

func assertUserBalances(t *testing.T, id int, bonus, paid, reward int) {
	t.Helper()
	u := getUserBalances(t, id)
	assert.Equal(t, paid, u.QuotaPaid, "paid balance")
	assert.Equal(t, bonus, u.QuotaBonus, "bonus balance")
	assert.Equal(t, reward, u.QuotaReward, "reward balance")
	assertQuotaInvariant(t, u)
}

// ---------------------------------------------------------------------------
// WalletFunding 按模型赠金资格分账户扣减/退款
// ---------------------------------------------------------------------------

func TestWalletFundingEligibleBonusFirstOrder(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "bonus-model", boolPtr(true))
	const userID = 901
	seedBonusUser(t, userID, 800, 1000)

	funding := &WalletFunding{userId: userID, requestId: "eligible-order-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "bonus-model"))}
	require.NoError(t, funding.PreConsume(1000))

	// bonus 优先：800 来自 bonus，200 来自 paid（paid 剩 800）
	assertUserBalances(t, userID, 0, 800, 0)
	// 扣减回执走 moneyport.FundingReceipt 能力（异步任务持久化的生产路径）
	assert.Equal(t, []moneyport.AccountShare{
		{Account: string(model.AccountBonus), Amount: 800},
		{Account: string(model.AccountPaid), Amount: 200},
	}, funding.Receipt())

	// 结算退款：按份额比例退回（实际 400 -> 退 600）
	require.NoError(t, funding.Settle(-600))
	// bonus 退 800*600/1000=480；paid 退 200*600/1000=120
	assertUserBalances(t, userID, 480, 920, 0)

	// 全额退款（新 request）：精确退回原始扣款账户
	funding2 := &WalletFunding{userId: userID, requestId: "eligible-refund-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "bonus-model"))}
	require.NoError(t, funding2.PreConsume(400))
	assertUserBalances(t, userID, 80, 920, 0)
	require.NoError(t, funding2.Refund())
	assertUserBalances(t, userID, 480, 920, 0)
}

func TestWalletFundingEligibleBonusOnlySuccess(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "bonus-only-model", boolPtr(true))
	const userID = 902
	seedBonusUser(t, userID, 500, 0)

	funding := &WalletFunding{userId: userID, requestId: "bonus-only-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "bonus-only-model"))}
	require.NoError(t, funding.PreConsume(500))
	assertUserBalances(t, userID, 0, 0, 0)

	require.NoError(t, funding.Settle(-500))
	assertUserBalances(t, userID, 500, 0, 0)
}

func TestWalletFundingIneligiblePaidOnly(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "paid-only-model", boolPtr(false))
	const userID = 903
	seedBonusUser(t, userID, 9999, 1000)

	funding := &WalletFunding{userId: userID, requestId: "paid-only-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "paid-only-model"))}
	require.NoError(t, funding.PreConsume(1000))
	// 赠金一分不动
	assertUserBalances(t, userID, 9999, 0, 0)
	require.NoError(t, funding.Settle(-300))
	assertUserBalances(t, userID, 9999, 300, 0)

	// 新请求全额退款：精确退回 paid（不进入 bonus）
	funding2 := &WalletFunding{userId: userID, requestId: "paid-only-refund-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "paid-only-model"))}
	require.NoError(t, funding2.PreConsume(300))
	require.NoError(t, funding2.Refund())
	assertUserBalances(t, userID, 9999, 300, 0)
}

func TestWalletFundingIneligibleBonusOnlyRejected(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "ineligible-model", boolPtr(false))
	const userID = 904
	seedBonusUser(t, userID, 5000, 0)

	funding := &WalletFunding{userId: userID, requestId: "ineligible-bonus-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "ineligible-model"))}
	err := funding.PreConsume(100)
	require.ErrorIs(t, err, ErrModelBonusNotSupported)
	// 赠金未被扣减
	assertUserBalances(t, userID, 5000, 0, 0)
}

func TestWalletFundingIneligiblePaidInsufficientRejected(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "ineligible-model-2", nil)
	const userID = 905
	seedBonusUser(t, userID, 8000, 50)

	funding := &WalletFunding{userId: userID, requestId: "ineligible-paid-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "ineligible-model-2"))}
	err := funding.PreConsume(100)
	require.ErrorIs(t, err, ErrModelBonusNotSupported)
	assertUserBalances(t, userID, 8000, 50, 0)
}

func TestWalletFundingNullEligibilityRejected(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "null-model", nil)
	const userID = 906
	seedBonusUser(t, userID, 300, 700)

	funding := &WalletFunding{userId: userID, requestId: "null-elig-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "null-model"))}
	require.NoError(t, funding.PreConsume(700))
	// null 视为不可用：只扣 paid
	assertUserBalances(t, userID, 300, 0, 0)
}

func TestWalletFundingReserveUsesSameOrder(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "reserve-model", boolPtr(true))
	const userID = 907
	seedBonusUser(t, userID, 300, 700)

	funding := &WalletFunding{userId: userID, requestId: "reserve-order-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "reserve-model"))}
	require.NoError(t, funding.PreConsume(200))
	require.NoError(t, funding.Reserve(500))
	// 预扣 200（bonus 200）+ 追扣 300（bonus 100 + paid 200）
	assertUserBalances(t, userID, 0, 500, 0)

	// 追扣后的退款按累计份额精确退回（bonus 300 / paid 200）
	require.NoError(t, funding.Refund())
	assertUserBalances(t, userID, 300, 700, 0)
}

// ---------------------------------------------------------------------------
// 资金身份矩阵：OriginModelName != BillingModelName 时，价格与赠金资格
// 必须同源（GetBillingModelName），UpstreamModelName 不得影响资金规则。
// ---------------------------------------------------------------------------

func TestWalletFinancialIdentityMatrix(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "gemini-2.5-flash-thinking-8192", boolPtr(true))
	seedBonusEligibleModel(t, "matrix-paid-only", boolPtr(false))
	seedBonusEligibleModel(t, "matrix-alias-eligible", boolPtr(true))

	cases := []struct {
		name        string
		origin      string
		billing     string
		upstream    string
		bonusFirst  bool
		description string
	}{
		{
			name:        "reasoning suffix keeps origin as pricing alias",
			origin:      "gemini-2.5-flash-thinking-8192",
			upstream:    "gemini-2.5-flash",
			bonusFirst:  true,
			description: "带 reasoning 后缀的 origin 是计费别名：赠金资格跟随 origin（未显式设置 BillingModelName 时为回退身份）",
		},
		{
			name:        "explicit billing identity overrides origin",
			origin:      "matrix-paid-only",
			billing:     "matrix-alias-eligible",
			upstream:    "matrix-alias-eligible",
			bonusFirst:  true,
			description: "显式 BillingModelName（价格身份）决定赠金资格，origin 不再参与",
		},
		{
			name:        "billing identity disables bonus even when origin eligible",
			origin:      "matrix-alias-eligible",
			billing:     "matrix-paid-only",
			bonusFirst:  false,
			description: "计费身份不可用赠金时，不得借用 origin 的赠金资格",
		},
		{
			name:        "upstream routing identity never affects financial rules",
			origin:      "matrix-alias-eligible",
			upstream:    "matrix-paid-only",
			bonusFirst:  true,
			description: "UpstreamModelName 只是路由身份：上游模型不可用赠金不影响本请求的资金规则",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			relayInfo := &relaycommon.RelayInfo{
				UserId:           917,
				OriginModelName:  tc.origin,
				BillingModelName: tc.billing,
			}
			if tc.upstream != "" {
				relayInfo.ChannelMeta = &relaycommon.ChannelMeta{UpstreamModelName: tc.upstream}
			}

			order := WalletDebitOrder(relayInfo)
			if tc.bonusFirst {
				assert.Equal(t, []model.LedgerAccount{model.AccountBonus, model.AccountReward, model.AccountPaid}, order, tc.description)
			} else {
				assert.Equal(t, []model.LedgerAccount{model.AccountReward, model.AccountPaid}, order, tc.description)
			}
			assert.Equal(t, tc.bonusFirst, WalletBonusEligible(relayInfo),
				"WalletBonusEligible 必须与 WalletDebitOrder 同源")
		})
	}
}

func TestWalletDebitOrderAndBonusEligibleHandleNilRelayInfo(t *testing.T) {
	assert.Equal(t, []model.LedgerAccount{model.AccountReward, model.AccountPaid}, WalletDebitOrder(nil))
	assert.False(t, WalletBonusEligible(nil))
}

// ---------------------------------------------------------------------------
// NewBillingSession 预扣前校验（清晰错误 + 订阅回退不受影响）
// ---------------------------------------------------------------------------

func TestNewBillingSessionIneligibleBonusOnlyClearError(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "session-ineligible", boolPtr(false))
	const userID = 908
	seedBonusUser(t, userID, 10_000, 0)

	relayInfo := &relaycommon.RelayInfo{UserId: userID, RequestId: "session-inelig-1", OriginModelName: "session-ineligible"}
	_, apiErr := NewBillingSession(testGinContext(t), relayInfo, 100)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	assert.Contains(t, apiErr.Err.Error(), "该模型不支持赠金支付，付费余额不足")
	assertUserBalances(t, userID, 10_000, 0, 0)
}

func TestNewBillingSessionEligibleBonusPlusPaid(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "session-eligible", boolPtr(true))
	const userID = 909
	seedBonusUser(t, userID, 600, 400)

	relayInfo := &relaycommon.RelayInfo{UserId: userID, RequestId: "session-elig-1", OriginModelName: "session-eligible", IsPlayground: true}
	session, apiErr := NewBillingSession(testGinContext(t), relayInfo, 800)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assertUserBalances(t, userID, 0, 200, 0)

	shares := session.WalletDebitShares()
	require.Len(t, shares, 2)
	assert.Equal(t, model.AccountBonus, shares[0].Account)
	assert.Equal(t, 600, shares[0].Amount)
	assert.Equal(t, model.AccountPaid, shares[1].Account)
	assert.Equal(t, 200, shares[1].Amount)
}

// ---------------------------------------------------------------------------
// PostConsumeQuota 回退路径按账户扣减/退款
// ---------------------------------------------------------------------------

func TestPostConsumeQuotaAccountAwareDebitAndRefund(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "fallback-model", boolPtr(true))
	const userID = 910
	seedBonusUser(t, userID, 700, 300)

	relayInfo := &relaycommon.RelayInfo{UserId: userID, RequestId: "fallback-1", OriginModelName: "fallback-model", IsPlayground: true}

	// 预扣（WSS 前扣）：bonus 优先
	require.NoError(t, PostConsumeQuota(relayInfo, 500, 0, false))
	assertUserBalances(t, userID, 200, 300, 0)
	require.Len(t, relayInfo.WalletDebitShares, 1)

	// 结算退款（实际 300 -> 退 200）：按份额比例退回（全部来自 bonus）
	require.NoError(t, PostConsumeQuota(relayInfo, -200, 0, false))
	assertUserBalances(t, userID, 400, 300, 0)

	// 结算补扣（实际 700 -> 补 200）：同样 bonus 优先
	require.NoError(t, PostConsumeQuota(relayInfo, 200, 0, false))
	assertUserBalances(t, userID, 200, 300, 0)
}

func TestPostConsumeQuotaIneligibleClearError(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "fallback-ineligible", nil)
	const userID = 912
	seedBonusUser(t, userID, 5000, 10)

	relayInfo := &relaycommon.RelayInfo{UserId: userID, RequestId: "fallback-inelig-1", OriginModelName: "fallback-ineligible", IsPlayground: true}
	err := PostConsumeQuota(relayInfo, 100, 0, false)
	require.ErrorIs(t, err, ErrModelBonusNotSupported)
	assertUserBalances(t, userID, 5000, 10, 0)
}

// ---------------------------------------------------------------------------
// 异步任务分账：taskAdjustFunding / RefundTaskQuota 精确退款
// 生产流程中任务提交时已通过 PreConsumeBilling 完成预扣，这里直接验证
// 退款/结算按任务记录的原始份额落入对应账户。
// ---------------------------------------------------------------------------

func TestTaskRefundSplitsToOriginalAccounts(t *testing.T) {
	truncate(t)
	const userID = 913
	seedBonusUser(t, userID, 0, 0)

	task := makeTask(userID, 0, 1000, 0, BillingSourceWallet, 0)
	task.PrivateData.WalletDebit = []model.LedgerShare{
		{Account: model.AccountBonus, Amount: 700},
		{Account: model.AccountPaid, Amount: 300},
	}
	require.NoError(t, model.DB.Create(task).Error)

	require.True(t, RefundTaskQuota(t.Context(), task, "test failure"))
	// 全额退款精确退回原账户：bonus 700 / paid 300，不得混入其他账户
	assertUserBalances(t, userID, 700, 300, 0)
	assert.Equal(t, 0, task.Quota)
}

func TestTaskAdjustFundingSettleAndRefundPrecise(t *testing.T) {
	truncate(t)
	const userID = 914
	seedBonusUser(t, userID, 0, 0)

	task := makeTask(userID, 0, 1000, 0, BillingSourceWallet, 0)
	task.PrivateData.WalletDebit = []model.LedgerShare{
		{Account: model.AccountBonus, Amount: 700},
		{Account: model.AccountPaid, Amount: 300},
	}

	// 差额结算退款（退 600）：bonus 退 420 / paid 退 180
	_, err := taskAdjustFunding(task, -600, nil)
	require.NoError(t, err)
	assertUserBalances(t, userID, 420, 180, 0)

	// 差额结算补扣（补 200）：按任务记录顺序 bonus 优先
	_, err = taskAdjustFunding(task, 200, nil)
	require.NoError(t, err)
	assertUserBalances(t, userID, 220, 180, 0)
}

func TestTaskRefundIdempotent(t *testing.T) {
	truncate(t)
	const userID = 915
	seedBonusUser(t, userID, 0, 0)

	task := makeTask(userID, 0, 1000, 0, BillingSourceWallet, 0)
	task.PrivateData.WalletDebit = []model.LedgerShare{
		{Account: model.AccountBonus, Amount: 700},
		{Account: model.AccountPaid, Amount: 300},
	}
	require.NoError(t, model.DB.Create(task).Error)

	require.True(t, RefundTaskQuota(t.Context(), task, "failure 1"))
	// task.Quota 已清零：第二次调用不再退款
	require.True(t, RefundTaskQuota(t.Context(), task, "failure 2"))
	assertUserBalances(t, userID, 700, 300, 0)
}

// ---------------------------------------------------------------------------
// 并发不超扣：eligible 模型并发预扣
// ---------------------------------------------------------------------------

func TestWalletFundingConcurrentNoOvercharge(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "concurrent-model", boolPtr(true))
	const userID = 916
	seedBonusUser(t, userID, 100_000, 100_000)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			funding := &WalletFunding{
				userId:     userID,
				requestId:  fmt.Sprintf("concurrent-%d-%d", n, userID),
				debitOrder: []model.LedgerAccount{model.AccountBonus, model.AccountPaid},
			}
			if err := funding.PreConsume(1_000); err != nil {
				t.Errorf("pre-consume failed: %v", err)
				return
			}
			_ = funding.Refund()
		}(i)
	}
	wg.Wait()
	// 8 次预扣 1000 并全额退款：账户恢复初始余额，总余额不超扣
	assertUserBalances(t, userID, 100_000, 100_000, 0)
	var ledgerCount int64
	require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Count(&ledgerCount).Error)
	assert.GreaterOrEqual(t, ledgerCount, int64(8*2), "每个并发请求应有独立的预扣/退款账本记录")
}

// ---------------------------------------------------------------------------
// 邀请奖励（reward）参与计费：eligible 模型 bonus -> reward -> paid；
// 非 eligible 模型 reward -> paid（reward 不受赠金资格限制）。
// ---------------------------------------------------------------------------

func seedRewardUser(t *testing.T, id int, bonus, reward, paid int) {
	t.Helper()
	user := &model.User{
		Id: id, Username: fmt.Sprintf("reward-user-%d", id), AffCode: fmt.Sprintf("reward-aff-%d", id),
		Quota: bonus + reward + paid, QuotaBonus: bonus, QuotaReward: reward, QuotaPaid: paid,
		Status: common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(user).Error)
}

func TestWalletFundingEligibleOrderSpendsBonusThenRewardThenPaid(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "reward-eligible-model", boolPtr(true))
	const userID = 918
	seedRewardUser(t, userID, 300, 500, 1000)

	funding := &WalletFunding{userId: userID, requestId: "reward-eligible-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "reward-eligible-model"))}
	require.NoError(t, funding.PreConsume(1000))

	assertUserBalances(t, userID, 0, 800, 0)
	assert.Equal(t, []moneyport.AccountShare{
		{Account: string(model.AccountBonus), Amount: 300},
		{Account: string(model.AccountReward), Amount: 500},
		{Account: string(model.AccountPaid), Amount: 200},
	}, funding.Receipt())

	require.NoError(t, funding.Settle(-400)) // 实际 600：退 bonus 120 / reward 200 / paid 80
	assertUserBalances(t, userID, 120, 880, 200)

	// 新请求全额退款：精确退回各原始账户，reward 不得洗成 paid。
	funding2 := &WalletFunding{userId: userID, requestId: "reward-eligible-refund", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "reward-eligible-model"))}
	require.NoError(t, funding2.PreConsume(280))
	assertUserBalances(t, userID, 0, 880, 40)
	require.NoError(t, funding2.Refund())
	assertUserBalances(t, userID, 120, 880, 200)
}

func TestWalletFundingIneligibleModelSpendsRewardBeforePaid(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "reward-ineligible-model", boolPtr(false))
	const userID = 919
	seedRewardUser(t, userID, 9999, 400, 100)

	funding := &WalletFunding{userId: userID, requestId: "reward-ineligible-1", debitOrder: WalletDebitOrder(walletInfoFor(t, userID, "reward-ineligible-model"))}
	require.NoError(t, funding.PreConsume(450))

	assertUserBalances(t, userID, 9999, 50, 0)
	assert.Equal(t, []moneyport.AccountShare{
		{Account: string(model.AccountReward), Amount: 400},
		{Account: string(model.AccountPaid), Amount: 50},
	}, funding.Receipt())

	require.NoError(t, funding.Refund())
	assertUserBalances(t, userID, 9999, 100, 400)
}

func TestNewBillingSessionIneligibleModelAcceptsRewardOnly(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "session-reward-only", boolPtr(false))
	const userID = 920
	seedRewardUser(t, userID, 0, 1000, 0)

	relayInfo := &relaycommon.RelayInfo{UserId: userID, RequestId: "session-reward-1", OriginModelName: "session-reward-only", IsPlayground: true}
	session, apiErr := NewBillingSession(testGinContext(t), relayInfo, 800)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assertUserBalances(t, userID, 0, 0, 200)
}
