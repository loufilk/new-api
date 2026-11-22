package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WalletFunding 预扣/结算/退款必须通过账本幂等键保证重放安全：
// 同一 request 的同一操作重复执行不会重复入账。

func TestWalletFundingPreConsumeAndReplay(t *testing.T) {
	truncate(t)

	const userID = 801
	seedUser(t, userID, 10_000)

	funding := &WalletFunding{userId: userID, requestId: "wallet-fwd-req-1"}
	require.NoError(t, funding.PreConsume(2_000))

	quota, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 8_000, quota)
	assert.Equal(t, 2_000, funding.consumed)

	// 同 request 重放预扣：账本幂等键命中重放，不再扣款
	require.NoError(t, funding.PreConsume(2_000))
	quota, err = model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 8_000, quota)
}

func TestWalletFundingSettleReplaySafe(t *testing.T) {
	truncate(t)

	const userID = 802
	seedUser(t, userID, 10_000)

	funding := &WalletFunding{userId: userID, requestId: "wallet-fwd-req-2"}
	require.NoError(t, funding.PreConsume(2_000))

	// 结算补扣 500
	require.NoError(t, funding.Settle(500))
	quota, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 7_500, quota)

	// 重复结算同差额：幂等重放，不再扣款
	require.NoError(t, funding.Settle(500))
	quota, err = model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 7_500, quota)

	// 结算退款（负差额）：退还 1500
	require.NoError(t, funding.Settle(-1_500))
	quota, err = model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 9_000, quota)

	// 重复结算退款：幂等重放，不再退款
	require.NoError(t, funding.Settle(-1_500))
	quota, err = model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 9_000, quota)
}

func TestWalletFundingRefundReplaySafe(t *testing.T) {
	truncate(t)

	const userID = 803
	seedUser(t, userID, 10_000)

	funding := &WalletFunding{userId: userID, requestId: "wallet-fwd-req-3"}
	require.NoError(t, funding.PreConsume(3_000))

	require.NoError(t, funding.Refund())
	quota, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 10_000, quota)

	// 退款重放：账本幂等键命中重放，不重复退款
	require.NoError(t, funding.Refund())
	quota, err = model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 10_000, quota)
}

func TestWalletFundingReserveReplaySafe(t *testing.T) {
	truncate(t)

	const userID = 804
	seedUser(t, userID, 10_000)

	session := recordedWalletTestSession(t, &relaycommon.RelayInfo{
		UserId: userID, RequestId: "wallet-fwd-req-4", IsPlayground: true,
	}, 2_000)
	require.NoError(t, session.Reserve(5_000))
	quota, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 5_000, quota)

	// 重复 Reserve 同目标：targetQuota <= preConsumedQuota 直接返回
	require.NoError(t, session.Reserve(5_000))
	quota, err = model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 5_000, quota)
}

func TestWalletFundingMultipleReserveTargetsUseDistinctKeys(t *testing.T) {
	truncate(t)

	const userID = 805
	seedUser(t, userID, 20_000)
	session := recordedWalletTestSession(t, &relaycommon.RelayInfo{
		UserId: userID, RequestId: "wallet-fwd-req-5", IsPlayground: true,
	}, 2_000)
	require.NoError(t, session.Reserve(5_000))
	require.NoError(t, session.Reserve(8_000))

	quota, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 12_000, quota)
	assertRecordedWalletHold(t, session, 8_000)
}

func TestPostConsumeQuotaWalletRefundUsesPaidAccount(t *testing.T) {
	truncate(t)

	const userID = 806
	seedUser(t, userID, 6200)
	relayInfo := &relaycommon.RelayInfo{
		UserId:       userID,
		RequestId:    "post-consume-refund-1",
		IsPlayground: true,
	}
	// A refund restores a paid-account debit from this request. Matching an
	// existing balance alone is not evidence that the request paid 1200.
	require.NoError(t, PostConsumeQuota(relayInfo, 1200, 0, false))
	debited, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 5000, debited)
	require.NoError(t, PostConsumeQuota(relayInfo, -1200, 0, false))
	require.NoError(t, PostConsumeQuota(relayInfo, -1200, 0, false))

	quota, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 6200, quota)
	assert.Equal(t, []ledgerRow{
		{Account: "paid", Delta: -1200, Reason: model.LedgerReasonBillingSettle, IdempotencyKey: "billing:settle:post-consume-refund-1:1200"},
		{Account: "paid", Delta: 1200, Reason: model.LedgerReasonBillingSettle, IdempotencyKey: "billing:settle_refund:post-consume-refund-1:1200"},
	}, ledgerRowsFor(t, userID))
}
