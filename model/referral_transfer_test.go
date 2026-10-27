package model

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedAffQuotaUser(t *testing.T, id, affQuota, affHistory int) {
	t.Helper()
	user := User{
		Id:              id,
		Username:        fmt.Sprintf("aff-transfer-user-%d", id),
		AffCode:         fmt.Sprintf("aft%d", id),
		Status:          common.UserStatusEnabled,
		AffQuota:        affQuota,
		AffHistoryQuota: affHistory,
	}
	require.NoError(t, DB.Create(&user).Error)
}

func countReferralTransferLedgerRows(t *testing.T, userId int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&BalanceLedger{}).
		Where("user_id = ? AND reason = ?", userId, LedgerReasonReferralTransfer).
		Count(&count).Error)
	return count
}

func TestTransferAffQuotaMovesPendingRewardToRewardAccount(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 301, 1_000_000, 1_000_000)

	result, err := TransferAffQuota(301, 500_000, "transfer-model-1")
	require.NoError(t, err)
	require.False(t, result.Replayed)

	user := getUserBalances(t, 301)
	assert.Equal(t, 500_000, user.AffQuota, "划转后待使用收益减少")
	assert.Equal(t, 500_000, user.QuotaReward, "划转金额进入 reward 账户")
	assert.Equal(t, 1_000_000, user.AffHistoryQuota, "累计收益不因划转减少")
	assert.Equal(t, 500_000, user.Quota, "quota 镜像 = paid + bonus + reward")
	assertQuotaInvariant(t, user)

	var row BalanceLedger
	require.NoError(t, DB.Where("user_id = ? AND reason = ?", 301, LedgerReasonReferralTransfer).First(&row).Error)
	assert.Equal(t, string(AccountReward), row.Account)
	assert.Equal(t, 500_000, row.Delta)
	assert.Equal(t, "aff_transfer", row.ReferenceType)
	assert.Equal(t, "transfer-model-1", row.ReferenceId)
}

func TestTransferAffQuotaReplayBySameKeyDoesNotDebitAgain(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 302, 900_000, 900_000)

	first, err := TransferAffQuota(302, 500_000, "transfer-model-2")
	require.NoError(t, err)
	require.False(t, first.Replayed)

	second, err := TransferAffQuota(302, 500_000, "transfer-model-2")
	require.NoError(t, err)
	require.True(t, second.Replayed, "同键同金额必须判定为重放")
	assert.Equal(t, first.TotalAfter, second.TotalAfter)

	user := getUserBalances(t, 302)
	assert.Equal(t, 400_000, user.AffQuota)
	assert.Equal(t, 500_000, user.QuotaReward)
	assert.EqualValues(t, 1, countReferralTransferLedgerRows(t, 302))
}

func TestTransferAffQuotaSameKeyDifferentAmountConflicts(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 303, 900_000, 900_000)

	_, err := TransferAffQuota(303, 500_000, "transfer-model-3")
	require.NoError(t, err)

	_, err = TransferAffQuota(303, 600_000, "transfer-model-3")
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)

	user := getUserBalances(t, 303)
	assert.Equal(t, 400_000, user.AffQuota, "冲突请求不得再次扣减")
	assert.Equal(t, 500_000, user.QuotaReward)
	assert.EqualValues(t, 1, countReferralTransferLedgerRows(t, 303))
}

func TestTransferAffQuotaInsufficientLeavesNoLedgerEffect(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 304, 500_000, 500_000)

	_, err := TransferAffQuota(304, 600_000, "transfer-model-4")
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)

	user := getUserBalances(t, 304)
	assert.Equal(t, 500_000, user.AffQuota)
	assert.Zero(t, user.QuotaReward)
	assert.Zero(t, user.Quota)
	assert.Zero(t, countReferralTransferLedgerRows(t, 304))
}

func TestTransferAffQuotaRejectsInvalidAmounts(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 305, common.MaxWalletQuota, common.MaxWalletQuota)

	for _, amount := range []int{0, -1, int(common.QuotaPerUnit) - 1, common.MaxWalletQuota + 1} {
		_, err := TransferAffQuota(305, amount, fmt.Sprintf("invalid-%d", amount))
		require.ErrorIs(t, err, ErrReferralTransferInvalid, "amount=%d", amount)
	}
	assert.Zero(t, countReferralTransferLedgerRows(t, 305))
}

// 模型层同样限制幂等键长度与字符集，防止绕过控制器写入异常键。
func TestTransferAffQuotaRejectsInvalidIdempotencyKeys(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 307, 1_000_000, 1_000_000)

	tooLong := strings.Repeat("a", ReferralTransferIdempotencyKeyMaxLength+1)
	for name, key := range map[string]string{
		"empty":        "",
		"blank":        "   ",
		"too_long":     tooLong,
		"space":        "transfer key",
		"slash":        "transfer/key",
		"quote":        "transfer'key",
		"newline":      "transfer\nkey",
		"non_ascii":    "划转键",
		"control_char": "transfer\x00key",
	} {
		_, err := TransferAffQuota(307, 500_000, key)
		require.ErrorIs(t, err, ErrReferralTransferInvalid, "key case %s", name)
	}
	assert.Zero(t, countReferralTransferLedgerRows(t, 307), "非法键不得产生任何账本")

	result, err := TransferAffQuota(307, 500_000, "Valid.Key_1:scope-2")
	require.NoError(t, err)
	require.False(t, result.Replayed)
	assert.EqualValues(t, 1, countReferralTransferLedgerRows(t, 307))
}

// 超大页码必须明确失败而不是让 (page-1)*pageSize 回绕成负偏移。
func TestListReferralCommissionsRejectsOverflowPage(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 308, 0, 0)

	_, _, err := ListReferralCommissions(308, math.MaxInt, 200)
	require.ErrorIs(t, err, ErrReferralTransferInvalid)

	rows, total, err := ListReferralCommissions(308, 1, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, rows)
}

func TestTransferAffQuotaConcurrentSameKeyCreditsOnce(t *testing.T) {
	setupLedgerTestState(t)
	seedAffQuotaUser(t, 306, 1_000_000, 1_000_000)

	const workers = 6
	var wg sync.WaitGroup
	errs := make([]error, workers)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = TransferAffQuota(306, 500_000, "transfer-model-concurrent")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "worker %d", i)
	}

	user := getUserBalances(t, 306)
	assert.Equal(t, 500_000, user.AffQuota, "并发同键只允许扣减一次")
	assert.Equal(t, 500_000, user.QuotaReward, "并发同键只允许入账一次")
	assert.EqualValues(t, 1, countReferralTransferLedgerRows(t, 306))
}
