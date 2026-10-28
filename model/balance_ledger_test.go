package model

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupLedgerTestState 清空 users / balance_ledger，关闭 Redis 与批量更新，
// 保证测试在纯数据库路径上执行。
func setupLedgerTestState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)
	require.NoError(t, DB.Exec("DELETE FROM balance_ledger").Error)
	require.NoError(t, DB.Exec("DELETE FROM balance_ledger_operations").Error)

	oldRedisEnabled := common.RedisEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
}

func createLedgerTestUser(t *testing.T, id int, quota int) {
	t.Helper()
	user := User{Id: id, Username: fmt.Sprintf("ledger-user-%d", id), AffCode: fmt.Sprintf("aff%d", id), Quota: quota, QuotaPaid: quota, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
}

func getUserBalances(t *testing.T, id int) User {
	t.Helper()
	var user User
	require.NoError(t, DB.Where("id = ?", id).First(&user).Error)
	return user
}

func assertQuotaInvariant(t *testing.T, user User) {
	t.Helper()
	assert.Equal(t, user.QuotaPaid+user.QuotaBonus+user.QuotaReward, user.Quota,
		"quota must equal paid+bonus+reward")
}

func countLedgerRows(t *testing.T, key string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("idempotency_key = ?", key).Count(&count).Error)
	return count
}

// ---------------------------------------------------------------------------
// 旧 quota 幂等回填
// ---------------------------------------------------------------------------

func TestBackfillLegacyQuotaAccounts(t *testing.T) {
	setupLedgerTestState(t)

	// 旧数据：只有 quota，三个账户均为 0
	require.NoError(t, DB.Create(&User{Id: 1, Username: "legacy-1", AffCode: "lg1", Quota: 5000, Status: common.UserStatusEnabled}).Error)
	// 新数据：已拆分（paid 非 0），不应被回填
	require.NoError(t, DB.Create(&User{Id: 2, Username: "legacy-2", AffCode: "lg2", Quota: 3000, QuotaPaid: 3000, Status: common.UserStatusEnabled}).Error)
	// quota 为 0：无需回填
	require.NoError(t, DB.Create(&User{Id: 3, Username: "legacy-3", AffCode: "lg3", Quota: 0, Status: common.UserStatusEnabled}).Error)

	affected, err := BackfillLegacyQuotaAccounts()
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	u1 := getUserBalances(t, 1)
	assert.Equal(t, 5000, u1.QuotaPaid)
	assert.Equal(t, 0, u1.QuotaBonus)
	assert.Equal(t, 0, u1.QuotaReward)
	assertQuotaInvariant(t, u1)
	assert.Equal(t, int64(1), countLedgerRows(t, "migration:legacy_quota:1"))
	var operation BalanceLedgerOperation
	require.NoError(t, DB.Where("idempotency_key = ?", "migration:legacy_quota:1").First(&operation).Error)
	assert.Equal(t, 5000, operation.TotalBefore)
	assert.Equal(t, 5000, operation.TotalAfter, "migration must not change total balance")

	// 重复执行不重复：全部用户不再满足回填条件
	affected, err = BackfillLegacyQuotaAccounts()
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)

	u2 := getUserBalances(t, 2)
	assert.Equal(t, 3000, u2.QuotaPaid)
}

func TestReconcileQuotaAccounts(t *testing.T) {
	setupLedgerTestState(t)

	require.NoError(t, DB.Create(&User{Id: 1, Username: "mismatch-1", AffCode: "mm1", Quota: 9999, QuotaPaid: 1000, QuotaBonus: 2000, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, DB.Create(&User{Id: 2, Username: "mismatch-2", AffCode: "mm2", Quota: 1000, QuotaPaid: 1000, Status: common.UserStatusEnabled}).Error)

	mismatches, err := FindQuotaAccountMismatches()
	require.NoError(t, err)
	require.Len(t, mismatches, 1)
	assert.Equal(t, 1, mismatches[0].Id)

	fixed, err := ReconcileQuotaAccounts()
	require.NoError(t, err)
	assert.Equal(t, int64(1), fixed)

	require.Len(t, mustFindMismatches(t), 0)
	u1 := getUserBalances(t, 1)
	assert.Equal(t, 3000, u1.Quota)
	assertQuotaInvariant(t, u1)
}

func mustFindMismatches(t *testing.T) []User {
	t.Helper()
	users, err := FindQuotaAccountMismatches()
	require.NoError(t, err)
	return users
}

// ---------------------------------------------------------------------------
// credit / debit / override / transfer 基础语义
// ---------------------------------------------------------------------------

func TestCreditQuota(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 100, 1000)

	result, err := CreditQuota(LedgerEntry{
		UserId:        100,
		Account:       AccountPaid,
		Delta:         500,
		Reason:        LedgerReasonTopup,
		ReferenceType: "topup",
		ReferenceId:   "TN-1",
	})
	require.NoError(t, err)
	require.False(t, result.Replayed)
	assert.Equal(t, 1000, result.TotalBefore)
	assert.Equal(t, 1500, result.TotalAfter)
	require.Len(t, result.Rows, 1)
	row := result.Rows[0]
	assert.Equal(t, "paid", row.Account)
	assert.Equal(t, 500, row.Delta)
	assert.Equal(t, 1000, row.BalanceBefore)
	assert.Equal(t, 1500, row.BalanceAfter)

	user := getUserBalances(t, 100)
	assert.Equal(t, 1500, user.QuotaPaid)
	assertQuotaInvariant(t, user)
}

func TestDebitQuotaDefaultOrderPaidOnly(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 101, 1000)

	// 默认顺序只有 paid：paid 不足时即使 bonus 有余额也扣减失败
	_, err := CreditQuota(LedgerEntry{UserId: 101, Account: AccountBonus, Delta: 800, Reason: "test", ReferenceType: "x", ReferenceId: "b1"})
	require.NoError(t, err)

	_, err = DebitQuota(LedgerEntry{UserId: 101, Delta: 1200, Reason: LedgerReasonBillingPreConsume, ReferenceType: "x", ReferenceId: "d1"})
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)

	user := getUserBalances(t, 101)
	assert.Equal(t, 1000, user.QuotaPaid)
	assert.Equal(t, 800, user.QuotaBonus)
}

func TestDebitQuotaWithAccountOrder(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 102, 1000)

	_, err := CreditQuota(LedgerEntry{UserId: 102, Account: AccountBonus, Delta: 800, Reason: "test", ReferenceType: "x", ReferenceId: "b2"})
	require.NoError(t, err)

	// 顺序 [bonus, paid]：先扣 bonus 800，再从 paid 扣 300
	result, err := DebitQuota(LedgerEntry{UserId: 102, Delta: 1100, Reason: LedgerReasonBillingPreConsume, ReferenceType: "x", ReferenceId: "d2"}, AccountBonus, AccountPaid)
	require.NoError(t, err)
	require.Len(t, result.Rows, 2)

	user := getUserBalances(t, 102)
	assert.Equal(t, 0, user.QuotaBonus)
	assert.Equal(t, 700, user.QuotaPaid)
	assertQuotaInvariant(t, user)

	// 行内记录了两个账户各自的 before/after
	rowsByAccount := map[string]BalanceLedger{}
	for _, row := range result.Rows {
		rowsByAccount[row.Account] = row
	}
	assert.Equal(t, 800, rowsByAccount["bonus"].BalanceBefore)
	assert.Equal(t, 0, rowsByAccount["bonus"].BalanceAfter)
	assert.Equal(t, 1000, rowsByAccount["paid"].BalanceBefore)
	assert.Equal(t, 700, rowsByAccount["paid"].BalanceAfter)
}

func TestDebitQuotaInsufficientDoesNotChangeBalance(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 103, 500)

	_, err := DebitQuota(LedgerEntry{UserId: 103, Delta: 501, Reason: "test", ReferenceType: "x", ReferenceId: "d3"})
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)

	user := getUserBalances(t, 103)
	assert.Equal(t, 500, user.QuotaPaid)
	assert.Equal(t, 500, user.Quota)
	assertQuotaInvariant(t, user)
	assert.Zero(t, countLedgerRows(t, "debit:x:d3"))
}

func TestDebitQuotaExactBalance(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 104, 500)

	result, err := DebitQuota(LedgerEntry{UserId: 104, Delta: 500, Reason: "test", ReferenceType: "x", ReferenceId: "d4"})
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalAfter)

	user := getUserBalances(t, 104)
	assert.Equal(t, 0, user.QuotaPaid)
	assert.Equal(t, 0, user.Quota)
}

func TestOverrideQuota(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 105, 1000)
	_, err := CreditQuota(LedgerEntry{UserId: 105, Account: AccountBonus, Delta: 300, Reason: "test", ReferenceType: "x", ReferenceId: "bo5"})
	require.NoError(t, err)

	// 覆盖 paid 到 2500，bonus 保持 300
	result, err := OverrideQuota(LedgerEntry{
		UserId: 105, Account: AccountPaid, Delta: 2500, Reason: LedgerReasonAdminOverride,
		ReferenceType: "user", ReferenceId: "105",
	})
	require.NoError(t, err)
	assert.Equal(t, 2800, result.TotalAfter)

	user := getUserBalances(t, 105)
	assert.Equal(t, 2500, user.QuotaPaid)
	assert.Equal(t, 300, user.QuotaBonus)
	assert.Equal(t, 2800, user.Quota)
	assertQuotaInvariant(t, user)

	// 覆盖到相同值：无实际变动，不写账本
	before := countLedgerRows(t, "override:user:105")
	result, err = OverrideQuota(LedgerEntry{
		UserId: 105, Account: AccountPaid, Delta: 2500, Reason: LedgerReasonAdminOverride,
		ReferenceType: "user", ReferenceId: "105",
	})
	require.NoError(t, err)
	assert.Equal(t, before, countLedgerRows(t, "override:user:105"))
	assert.Equal(t, 2800, result.TotalAfter)
}

func TestTransferQuota(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 106, 1000)

	result, err := TransferQuota(LedgerEntry{
		UserId: 106, Account: AccountPaid, Delta: 400, Reason: "test",
		ReferenceType: "transfer", ReferenceId: "t1",
	}, AccountReward)
	require.NoError(t, err)
	require.Len(t, result.Rows, 2)

	user := getUserBalances(t, 106)
	assert.Equal(t, 600, user.QuotaPaid)
	assert.Equal(t, 400, user.QuotaReward)
	assert.Equal(t, 1000, user.Quota)
	assertQuotaInvariant(t, user)

	// 转出账户余额不足
	_, err = TransferQuota(LedgerEntry{
		UserId: 106, Account: AccountPaid, Delta: 99999, Reason: "test",
		ReferenceType: "transfer", ReferenceId: "t2",
	}, AccountBonus)
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
}

func TestLedgerInvalidEntries(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 107, 100)

	_, err := CreditQuota(LedgerEntry{UserId: 0, Account: AccountPaid, Delta: 10, Reason: "x"})
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	_, err = CreditQuota(LedgerEntry{UserId: 107, Account: "unknown", Delta: 10, Reason: "x"})
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	_, err = CreditQuota(LedgerEntry{UserId: 107, Account: AccountPaid, Delta: -10, Reason: "x"})
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	_, err = CreditQuota(LedgerEntry{UserId: 107, Account: AccountPaid, Delta: 10, Reason: ""})
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	_, err = TransferQuota(LedgerEntry{UserId: 107, Account: AccountPaid, Delta: 10, Reason: "x"}, AccountPaid)
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
}

// ---------------------------------------------------------------------------
// 幂等重放与参数冲突
// ---------------------------------------------------------------------------

func TestCreditQuotaIdempotentReplay(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 200, 0)

	entry := LedgerEntry{
		UserId:        200,
		Account:       AccountPaid,
		Delta:         777,
		Reason:        LedgerReasonRedemption,
		ReferenceType: "redemption",
		ReferenceId:   "CODE-1",
	}
	first, err := CreditQuota(entry)
	require.NoError(t, err)
	require.False(t, first.Replayed)

	// 同键同参数重放：不重复入账，返回已有结果
	second, err := CreditQuota(entry)
	require.NoError(t, err)
	require.True(t, second.Replayed)
	assert.Equal(t, first.TotalAfter, second.TotalAfter)
	assert.Equal(t, 777, second.TotalAfter)

	user := getUserBalances(t, 200)
	assert.Equal(t, 777, user.QuotaPaid)
	assert.Equal(t, 777, user.Quota)
	assert.Equal(t, int64(1), countLedgerRows(t, "credit:redemption:CODE-1"))
}

func TestCreditQuotaIdempotencyConflict(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 201, 0)

	entry := LedgerEntry{
		UserId:        201,
		Account:       AccountPaid,
		Delta:         777,
		Reason:        LedgerReasonRedemption,
		ReferenceType: "redemption",
		ReferenceId:   "CODE-2",
	}
	require.NoError(t, mustCredit(t, entry))

	// 同键不同参数：明确报冲突，且不改动余额
	conflictEntry := entry
	conflictEntry.Delta = 999
	_, err := CreditQuota(conflictEntry)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)

	conflictEntry = entry
	conflictEntry.Reason = "other_reason"
	_, err = CreditQuota(conflictEntry)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)

	user := getUserBalances(t, 201)
	assert.Equal(t, 777, user.QuotaPaid)
	assert.Equal(t, int64(1), countLedgerRows(t, "credit:redemption:CODE-2"))
}

func TestIdempotencyKeyIsGlobalAcrossAccounts(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 203, 1000)

	entry := LedgerEntry{
		UserId:         203,
		Account:        AccountPaid,
		Delta:          100,
		Reason:         "test",
		ReferenceType:  "x",
		ReferenceId:    "global-key",
		IdempotencyKey: "global-operation-key",
	}
	_, err := CreditQuota(entry)
	require.NoError(t, err)

	entry.Account = AccountBonus
	_, err = CreditQuota(entry)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)

	user := getUserBalances(t, 203)
	assert.Equal(t, 1100, user.QuotaPaid)
	assert.Zero(t, user.QuotaBonus)
}

func TestIdempotencySignatureCoversOverrideTransferAndDebitOrder(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 204, 1000)
	_, err := CreditQuota(LedgerEntry{UserId: 204, Account: AccountBonus, Delta: 300, Reason: "seed", ReferenceType: "x", ReferenceId: "bonus"})
	require.NoError(t, err)

	override := LedgerEntry{UserId: 204, Account: AccountPaid, Delta: 900, Reason: "override", IdempotencyKey: "override-fixed"}
	_, err = OverrideQuota(override)
	require.NoError(t, err)
	override.Delta = 800
	_, err = OverrideQuota(override)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)

	transfer := LedgerEntry{UserId: 204, Account: AccountPaid, Delta: 100, Reason: "transfer", IdempotencyKey: "transfer-fixed"}
	_, err = TransferQuota(transfer, AccountBonus)
	require.NoError(t, err)
	_, err = TransferQuota(transfer, AccountReward)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)

	debit := LedgerEntry{UserId: 204, Delta: 50, Reason: "debit", IdempotencyKey: "debit-fixed"}
	_, err = DebitQuota(debit, AccountPaid, AccountBonus)
	require.NoError(t, err)
	_, err = DebitQuota(debit, AccountBonus, AccountPaid)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)
}

func TestInsertLinuxDOWithTxCreditsThreeDollarsOnce(t *testing.T) {
	setupLedgerTestState(t)
	previousEnabled := common.SignupBonusEnabled
	previousQuota := common.QuotaForSignupBonus
	previousQuotaPerUnit := common.QuotaPerUnit
	common.SignupBonusEnabled = true
	common.QuotaForSignupBonus = 1234
	common.QuotaPerUnit = 500000
	t.Cleanup(func() {
		common.SignupBonusEnabled = previousEnabled
		common.QuotaForSignupBonus = previousQuota
		common.QuotaPerUnit = previousQuotaPerUnit
	})

	user := &User{Username: "ledger-new-user", Password: "Cedar-river-782!quiet", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return user.InsertLinuxDOWithTx(tx, 0, "88001")
	}))

	// P1-1：注册赠金进入 bonus 账户，reason 为 signup_bonus，不进入 paid。
	created := getUserBalances(t, user.Id)
	assert.Zero(t, created.QuotaPaid)
	assert.Equal(t, 1500000, created.QuotaBonus)
	assert.Equal(t, 1500000, created.Quota)
	assertQuotaInvariant(t, created)
	assert.Equal(t, int64(1), countLedgerRows(t, "credit:linuxdo_signup:88001"))
	var rows []BalanceLedger
	require.NoError(t, DB.Where("user_id = ?", user.Id).Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, LedgerReasonSignupBonus, rows[0].Reason)
	assert.Equal(t, string(AccountBonus), rows[0].Account)
}

func TestInsertIgnoresLegacyGlobalSignupBonus(t *testing.T) {
	setupLedgerTestState(t)
	previousEnabled := common.SignupBonusEnabled
	previousQuota := common.QuotaForSignupBonus
	common.SignupBonusEnabled = true
	common.QuotaForSignupBonus = 1234
	t.Cleanup(func() {
		common.SignupBonusEnabled = previousEnabled
		common.QuotaForSignupBonus = previousQuota
	})

	user := &User{Username: "ledger-new-user-disabled", Password: "Cedar-river-782!quiet", Status: common.UserStatusEnabled}
	require.NoError(t, user.Insert(0))

	// Ordinary signup never receives a grant, even with legacy options enabled.
	created := getUserBalances(t, user.Id)
	assert.Zero(t, created.QuotaPaid)
	assert.Zero(t, created.QuotaBonus)
	assert.Zero(t, created.Quota)
	assertQuotaInvariant(t, created)
	var count int64
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

// TestInsertWithTxOAuthRollbackLeavesNoUserAndNoLedger 覆盖 OAuth 注册路径：
// 用户创建与赠金在同一事务内，外层事务回滚时用户与账本流水一并消失，
// 不会出现“用户不存在但赠金已发放”的半状态。
func TestInsertWithTxOAuthRollbackLeavesNoUserAndNoLedger(t *testing.T) {
	setupLedgerTestState(t)
	previousEnabled := common.SignupBonusEnabled
	previousQuota := common.QuotaForSignupBonus
	common.SignupBonusEnabled = true
	common.QuotaForSignupBonus = 888
	t.Cleanup(func() {
		common.SignupBonusEnabled = previousEnabled
		common.QuotaForSignupBonus = previousQuota
	})

	err := DB.Transaction(func(tx *gorm.DB) error {
		user := &User{Username: "oauth-rollback-user", Password: "password", Status: common.UserStatusEnabled}
		if err := user.InsertLinuxDOWithTx(tx, 0, "88002"); err != nil {
			return err
		}
		// 模拟 OAuth 绑定等后续步骤失败 → 整体回滚
		return errors.New("oauth binding failed")
	})
	require.Error(t, err)

	var userCount int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", "oauth-rollback-user").Count(&userCount).Error)
	assert.Zero(t, userCount, "回滚后用户不应存在")
	var ledgerCount int64
	require.NoError(t, DB.Model(&BalanceLedger{}).Count(&ledgerCount).Error)
	assert.Zero(t, ledgerCount, "回滚后不应残留账本流水")
}

func TestPurchaseSubscriptionWithBalanceDebitsRequiredQuota(t *testing.T) {
	setupLedgerTestState(t)
	previousQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })

	createLedgerTestUser(t, 205, 5000)
	allowBalance := true
	plan := &SubscriptionPlan{
		Title:           "ledger-balance-plan",
		PriceAmount:     1,
		Currency:        "USD",
		DurationUnit:    "month",
		DurationValue:   1,
		Enabled:         true,
		AllowBalancePay: &allowBalance,
		TotalAmount:     10000,
	}
	require.NoError(t, DB.Create(plan).Error)

	require.NoError(t, PurchaseSubscriptionWithBalance(205, plan.Id))
	user := getUserBalances(t, 205)
	assert.Equal(t, 4000, user.QuotaPaid)
	assert.Equal(t, 4000, user.Quota)

	var rows []BalanceLedger
	require.NoError(t, DB.Where("user_id = ? AND reason = ?", 205, LedgerReasonSubscriptionPurchase).Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, -1000, rows[0].Delta)
}

func TestDebitQuotaReplayAfterBalanceChange(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 202, 1000)

	entry := LedgerEntry{
		UserId:        202,
		Delta:         400,
		Reason:        LedgerReasonBillingPreConsume,
		ReferenceType: "relay",
		ReferenceId:   "REQ-1",
	}
	first, err := DebitQuota(entry)
	require.NoError(t, err)
	require.False(t, first.Replayed)
	assert.Equal(t, 600, first.TotalAfter)

	// 余额后来发生变化，重放仍然成功且不再扣减
	_, err = CreditQuota(LedgerEntry{UserId: 202, Account: AccountPaid, Delta: 100, Reason: "test", ReferenceType: "x", ReferenceId: "c2"})
	require.NoError(t, err)

	second, err := DebitQuota(entry)
	require.NoError(t, err)
	require.True(t, second.Replayed)
	assert.Equal(t, 600, second.TotalAfter)

	user := getUserBalances(t, 202)
	assert.Equal(t, 700, user.QuotaPaid)
	assert.Equal(t, int64(1), countLedgerRows(t, "debit:relay:REQ-1"))
}

func TestTransferQuotaReplay(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 203, 1000)

	entry := LedgerEntry{UserId: 203, Account: AccountPaid, Delta: 300, Reason: "test", ReferenceType: "transfer", ReferenceId: "tf-1"}
	first, err := TransferQuota(entry, AccountBonus)
	require.NoError(t, err)
	require.False(t, first.Replayed)

	second, err := TransferQuota(entry, AccountBonus)
	require.NoError(t, err)
	require.True(t, second.Replayed)

	user := getUserBalances(t, 203)
	assert.Equal(t, 700, user.QuotaPaid)
	assert.Equal(t, 300, user.QuotaBonus)
	assert.Equal(t, int64(2), countLedgerRows(t, "transfer:transfer:tf-1"))
}

func TestExplicitIdempotencyKey(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 204, 0)

	entry := LedgerEntry{
		UserId:         204,
		Account:        AccountPaid,
		Delta:          100,
		Reason:         "test",
		IdempotencyKey: "custom-key-1",
	}
	_, err := CreditQuota(entry)
	require.NoError(t, err)
	_, err = CreditQuota(entry)
	require.NoError(t, err) // 重放

	user := getUserBalances(t, 204)
	assert.Equal(t, 100, user.QuotaPaid)
	assert.Equal(t, int64(1), countLedgerRows(t, "custom-key-1"))
}

// ---------------------------------------------------------------------------
// 并发扣减：只有可用余额内成功，余额不为负
// ---------------------------------------------------------------------------

func TestConcurrentDebitsOnlyWithinAvailableBalance(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 300, 300)

	const workers = 8
	const amount = 100
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, results[idx] = DebitQuota(LedgerEntry{
				UserId:         300,
				Delta:          amount,
				Reason:         LedgerReasonBillingPreConsume,
				IdempotencyKey: fmt.Sprintf("conc-debit-%d", idx),
			})
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, err := range results {
		if err == nil {
			successCount++
		} else {
			require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
		}
	}
	assert.Equal(t, 3, successCount, "exactly 3 of 8 debits may succeed")

	user := getUserBalances(t, 300)
	assert.Equal(t, 0, user.QuotaPaid)
	assert.Equal(t, 0, user.Quota)
	assertQuotaInvariant(t, user)
	require.GreaterOrEqual(t, user.Quota, 0, "balance must never go negative")
}

func TestConcurrentSameIdempotencyKeyCreditsOnce(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 301, 0)

	const workers = 6
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, results[idx] = CreditQuota(LedgerEntry{
				UserId:        301,
				Account:       AccountPaid,
				Delta:         500,
				Reason:        LedgerReasonTopup,
				ReferenceType: "topup",
				ReferenceId:   "TN-CONC",
			})
		}(i)
	}
	wg.Wait()

	for _, err := range results {
		require.NoError(t, err)
	}

	user := getUserBalances(t, 301)
	assert.Equal(t, 500, user.QuotaPaid)
	assert.Equal(t, int64(1), countLedgerRows(t, "credit:topup:TN-CONC"))
}

// ---------------------------------------------------------------------------
// 回滚语义：账本插入失败时余额回滚；余额更新失败时无账本
// ---------------------------------------------------------------------------

func TestLedgerInsertFailureRollsBackBalance(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 400, 1000)

	// 预置同幂等键但不同参数的账本行 → 插入必然冲突 → 余额不得变动
	_, err := CreditQuota(LedgerEntry{
		UserId: 400, Account: AccountPaid, Delta: 100, Reason: "test",
		ReferenceType: "x", ReferenceId: "r1",
	})
	require.NoError(t, err)

	_, err = CreditQuota(LedgerEntry{
		UserId: 400, Account: AccountPaid, Delta: 999, Reason: "other",
		ReferenceType: "x", ReferenceId: "r1",
	})
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)

	user := getUserBalances(t, 400)
	assert.Equal(t, 1100, user.QuotaPaid)
	assert.Equal(t, 1100, user.Quota)
}

func TestBalanceUpdateFailureLeavesNoLedgerRow(t *testing.T) {
	setupLedgerTestState(t)

	// 用户不存在：行锁查询失败，不写任何账本
	_, err := CreditQuota(LedgerEntry{
		UserId: 999999, Account: AccountPaid, Delta: 100, Reason: "test",
		ReferenceType: "x", ReferenceId: "nouser",
	})
	require.Error(t, err)
	assert.Zero(t, countLedgerRows(t, "credit:x:nouser"))

	// 事务内余额更新失败（同一事务中先删除用户再入账）→ 全部回滚，无账本行
	createLedgerTestUser(t, 401, 100)
	_ = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", 401).Delete(&User{}).Error; err != nil {
			return err
		}
		_, err := CreditQuotaTx(tx, LedgerEntry{
			UserId: 401, Account: AccountPaid, Delta: 50, Reason: "test",
			ReferenceType: "x", ReferenceId: "deleted",
		})
		return err
	})
	assert.Zero(t, countLedgerRows(t, "credit:x:deleted"))
}

// ---------------------------------------------------------------------------
// 兑换码 / 充值重复回调不重复入账
// ---------------------------------------------------------------------------

func TestRedemptionDuplicateCallbackNoDoubleCredit(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	createLedgerTestUser(t, 500, 0)

	redemption := &Redemption{
		Name:   "ledger-test-code",
		Key:    "LEDGER-TEST-CODE",
		Status: common.RedemptionCodeStatusEnabled,
		Quota:  500,
	}
	require.NoError(t, DB.Create(redemption).Error)

	quota, err := Redeem("LEDGER-TEST-CODE", 500)
	require.NoError(t, err)
	assert.Equal(t, 500, quota)

	// 重复兑换：被兑换码状态拦截，且不重复入账
	_, err = Redeem("LEDGER-TEST-CODE", 500)
	require.Error(t, err)

	user := getUserBalances(t, 500)
	assert.Equal(t, 500, user.QuotaPaid)
	assert.Equal(t, 500, user.Quota)
	assert.Equal(t, int64(1), countLedgerRows(t, "credit:redemption:"+fmt.Sprint(redemption.Id)))
}

func TestTopupDuplicateCallbackNoDoubleCredit(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 501, 0)

	topUp := &TopUp{
		UserId:          501,
		Amount:          100,
		Money:           1.0,
		TradeNo:         "LEDGER-TN-1",
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(topUp).Error)

	// 第一次回调成功入账
	require.NoError(t, Recharge("LEDGER-TN-1", "cus-1", "127.0.0.1"))

	// 重复回调返回成功，让支付平台停止重试，但不重复入账。
	require.NoError(t, Recharge("LEDGER-TN-1", "cus-1", "127.0.0.1"))

	// 账本幂等键兜底：同一 trade_no 只有一行
	assert.Equal(t, int64(1), countLedgerRows(t, "credit:topup:LEDGER-TN-1"))

	user := getUserBalances(t, 501)
	assert.Equal(t, int(common.QuotaPerUnit), user.QuotaPaid)
	assert.Equal(t, user.QuotaPaid, user.Quota)
}

// ---------------------------------------------------------------------------
// legacy GetUserQuota / 缓存兼容
// ---------------------------------------------------------------------------

func TestGetUserQuotaReadsMirrorAfterLedgerOps(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 600, 0)

	_, err := CreditQuota(LedgerEntry{UserId: 600, Account: AccountPaid, Delta: 2500, Reason: "test", ReferenceType: "x", ReferenceId: "g1"})
	require.NoError(t, err)
	_, err = DebitQuota(LedgerEntry{UserId: 600, Delta: 700, Reason: "test", ReferenceType: "x", ReferenceId: "g2"})
	require.NoError(t, err)

	quota, err := GetUserQuota(600, true)
	require.NoError(t, err)
	assert.Equal(t, 1800, quota)
}

func TestRefreshUserQuotaCacheDisabledRedisNoop(t *testing.T) {
	// Redis 关闭时不得报错（覆盖 common.RedisEnabled=false 分支）
	RefreshUserQuotaCache(1, 100)
}

func mustCredit(t *testing.T, entry LedgerEntry) error {
	t.Helper()
	_, err := CreditQuota(entry)
	return err
}
