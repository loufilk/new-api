package model

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedModel(t *testing.T, id int, name string, bonusEligible *bool) {
	t.Helper()
	require.NoError(t, DB.Create(&Model{
		Id:            id,
		ModelName:     name,
		Status:        1,
		SyncOfficial:  1,
		BonusEligible: bonusEligible,
	}).Error)
}

// ---------------------------------------------------------------------------
// IsModelBonusEligible fail-closed 语义
// ---------------------------------------------------------------------------

func TestIsModelBonusEligibleFailClosed(t *testing.T) {
	setupLedgerTestState(t)

	seedModel(t, 1, "eligible-model", boolPtr(true))
	seedModel(t, 2, "ineligible-model", boolPtr(false))
	seedModel(t, 3, "unset-model", nil)

	// 显式 true 才可用；false/null/缺失均不可用
	assert.True(t, IsModelBonusEligible("eligible-model"))
	assert.False(t, IsModelBonusEligible("ineligible-model"))
	assert.False(t, IsModelBonusEligible("unset-model"))
	assert.False(t, IsModelBonusEligible("missing-model"))
	assert.False(t, IsModelBonusEligible(""))

	// 变更后立即失效缓存：false -> true 可用，true -> false 不可用
	require.NoError(t, DB.Model(&Model{}).Where("id = ?", 1).Update("bonus_eligible", false).Error)
	require.NoError(t, DB.Model(&Model{}).Where("id = ?", 2).Update("bonus_eligible", true).Error)
	InvalidateModelBonusEligibleCache()
	assert.False(t, IsModelBonusEligible("eligible-model"))
	assert.True(t, IsModelBonusEligible("ineligible-model"))

	// 清除回未配置（NULL）同样不可用
	require.NoError(t, DB.Model(&Model{}).Where("id = ?", 2).Update("bonus_eligible", nil).Error)
	InvalidateModelBonusEligibleCache()
	assert.False(t, IsModelBonusEligible("ineligible-model"))
}

// ---------------------------------------------------------------------------
// CreditQuotaShares 多账户入账
// ---------------------------------------------------------------------------

func TestCreditQuotaShares(t *testing.T) {
	setupLedgerTestState(t)

	createLedgerTestUser(t, 201, 5000)

	result, err := CreditQuotaShares(LedgerEntry{
		UserId:         201,
		Reason:         LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    "credit-shares-1",
		RequestId:      "credit-shares-1",
		IdempotencyKey: "billing:settle_refund:credit-shares-1",
	}, []LedgerShare{
		{Account: AccountBonus, Amount: 70},
		{Account: AccountPaid, Amount: 30},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 100, result.TotalAfter-result.TotalBefore)

	user := getUserBalances(t, 201)
	assert.Equal(t, 5030, user.QuotaPaid)
	assert.Equal(t, 70, user.QuotaBonus)
	assertQuotaInvariant(t, user)

	// 同键同份额重放：不重复入账
	replayed, err := CreditQuotaShares(LedgerEntry{
		UserId:         201,
		Reason:         LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    "credit-shares-1",
		RequestId:      "credit-shares-1",
		IdempotencyKey: "billing:settle_refund:credit-shares-1",
	}, []LedgerShare{
		{Account: AccountBonus, Amount: 70},
		{Account: AccountPaid, Amount: 30},
	})
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	user = getUserBalances(t, 201)
	assert.Equal(t, 5030, user.QuotaPaid)
	assert.Equal(t, 70, user.QuotaBonus)

	// 同键不同份额：参数冲突，拒绝
	_, err = CreditQuotaShares(LedgerEntry{
		UserId:         201,
		Reason:         LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    "credit-shares-1",
		RequestId:      "credit-shares-1",
		IdempotencyKey: "billing:settle_refund:credit-shares-1",
	}, []LedgerShare{
		{Account: AccountBonus, Amount: 100},
	})
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)

	// 非法份额被拒绝
	_, err = CreditQuotaShares(LedgerEntry{
		UserId:         201,
		Reason:         LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    "credit-shares-bad",
		IdempotencyKey: "billing:settle_refund:credit-shares-bad",
	}, nil)
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	_, err = CreditQuotaShares(LedgerEntry{
		UserId:         201,
		Reason:         LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    "credit-shares-bad2",
		IdempotencyKey: "billing:settle_refund:credit-shares-bad2",
	}, []LedgerShare{
		{Account: AccountBonus, Amount: 70},
		{Account: AccountBonus, Amount: 30},
	})
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	_, err = CreditQuotaShares(LedgerEntry{
		UserId:         201,
		Reason:         LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    "credit-shares-bad3",
		IdempotencyKey: "billing:settle_refund:credit-shares-bad3",
	}, []LedgerShare{
		{Account: AccountBonus, Amount: 0},
	})
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
}

// ---------------------------------------------------------------------------
// 批量调整 bonus_eligible + 独立审计
// ---------------------------------------------------------------------------

func TestBatchUpdateModelBonusEligibleWithAudit(t *testing.T) {
	setupLedgerTestState(t)

	seedModel(t, 1, "batch-model-a", boolPtr(false))
	seedModel(t, 2, "batch-model-b", nil)
	seedModel(t, 3, "batch-model-c", boolPtr(false))

	// 批量开启
	changed, err := BatchUpdateModelBonusEligible([]int{1, 2, 3}, boolPtr(true), 42, "admin", "batch-key-1")
	require.NoError(t, err)
	assert.Equal(t, 3, changed)

	var audits []ModelBonusEligibleAudit
	require.NoError(t, DB.Order("model_id asc").Find(&audits).Error)
	require.Len(t, audits, 3)
	assert.Equal(t, 42, audits[0].OperatorId)
	assert.Equal(t, "admin", audits[0].OperatorName)
	assert.Equal(t, 1, audits[0].ModelId)
	assert.Equal(t, "batch-model-a", audits[0].ModelName)
	assert.False(t, *audits[0].Before)
	assert.True(t, *audits[0].After)
	assert.Equal(t, "batch-key-1:1", audits[0].IdempotencyKey)
	assert.NotZero(t, audits[0].CreatedAt)

	// 重复请求（同键同目标）：幂等，不重复变更也不重复写审计
	changed, err = BatchUpdateModelBonusEligible([]int{1, 2, 3}, boolPtr(true), 42, "admin", "batch-key-1")
	require.NoError(t, err)
	assert.Equal(t, 0, changed)
	var auditCount int64
	require.NoError(t, DB.Model(&ModelBonusEligibleAudit{}).Count(&auditCount).Error)
	assert.Equal(t, int64(3), auditCount)

	// 同键不同目标值：冲突拒绝
	_, err = BatchUpdateModelBonusEligible([]int{1}, boolPtr(false), 42, "admin", "batch-key-1")
	require.ErrorIs(t, err, ErrModelAuditConflict)

	// 新键批量关闭 + 清除（NULL）
	changed, err = BatchUpdateModelBonusEligible([]int{1, 2}, boolPtr(false), 42, "admin", "batch-key-2")
	require.NoError(t, err)
	assert.Equal(t, 2, changed)
	// 当前值被其他请求改为 false 后，旧幂等键原始参数仍是 true；携带
	// false 重放必须冲突，不能因当前状态相同而绕过幂等参数检查。
	_, err = BatchUpdateModelBonusEligible([]int{1}, boolPtr(false), 42, "admin", "batch-key-1")
	require.ErrorIs(t, err, ErrModelAuditConflict)
	changed, err = BatchUpdateModelBonusEligible([]int{3}, nil, 42, "admin", "batch-key-3")
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	var model3 Model
	require.NoError(t, DB.Where("id = ?", 3).First(&model3).Error)
	assert.Nil(t, model3.BonusEligible)
	assert.False(t, IsModelBonusEligible("batch-model-c"))
	assert.False(t, IsModelBonusEligible("batch-model-a"))

	// 审计查询：按模型/操作者/关键字过滤
	byModel, total, err := SearchModelBonusEligibleAudits(1, 0, "", 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, byModel, 2)
	byOperator, total, err := SearchModelBonusEligibleAudits(0, 42, "", 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(6), total)
	require.Len(t, byOperator, 6)
	byKeyword, total, err := SearchModelBonusEligibleAudits(0, 0, "batch-model-b", 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, byKeyword, 2)

	// 并发重复提交同键：不超量（唯一约束 + 重放路径）
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = BatchUpdateModelBonusEligible([]int{1}, boolPtr(true), 42, "admin", "batch-key-concurrent")
		}()
	}
	wg.Wait()
	var finalCount int64
	require.NoError(t, DB.Model(&ModelBonusEligibleAudit{}).Where("idempotency_key LIKE ?", "batch-key-concurrent%").Count(&finalCount).Error)
	assert.Equal(t, int64(1), finalCount)
}
