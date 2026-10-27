package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createLegacyAffiliateTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range legacyAffiliateDropTables {
		require.NoError(t, db.Migrator().DropTable(table))
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS affiliate_rewards (id INTEGER PRIMARY KEY, status TEXT)",
		"CREATE TABLE IF NOT EXISTS affiliate_reward_reconcile_states (id INTEGER PRIMARY KEY, status TEXT)",
		"CREATE TABLE IF NOT EXISTS referral_coin_ledger (id INTEGER PRIMARY KEY)",
		"CREATE TABLE IF NOT EXISTS referral_coin_rewards (id INTEGER PRIMARY KEY)",
		"CREATE TABLE IF NOT EXISTS options (key TEXT PRIMARY KEY, value TEXT)",
	} {
		require.NoError(t, db.Exec(statement).Error)
	}
	require.NoError(t, db.Exec("DELETE FROM options").Error)
	if !db.Migrator().HasColumn(&User{}, "quota_affiliate") {
		require.NoError(t, db.Exec("ALTER TABLE users ADD COLUMN quota_affiliate bigint DEFAULT 0").Error)
	}
}

func insertLegacyAffiliateReward(t *testing.T, db *gorm.DB, id int) {
	t.Helper()
	require.NoError(t, db.Exec("INSERT INTO affiliate_rewards (id, status) VALUES (?, 'pending')", id).Error)
}

func insertLegacyReconcileState(t *testing.T, db *gorm.DB, id int) {
	t.Helper()
	require.NoError(t, db.Exec("INSERT INTO affiliate_reward_reconcile_states (id, status) VALUES (?, 'pending')", id).Error)
}

func insertLegacyAffiliateLedgerRow(t *testing.T, db *gorm.DB, userId int, account string) {
	t.Helper()
	key := "legacy-cleanup-test:" + account
	operation := &BalanceLedgerOperation{
		IdempotencyKey: key, UserId: userId, Operation: "credit", OperationSignature: "legacy",
		TotalBefore: 0, TotalAfter: 1000, CreatedAt: common.GetTimestamp(),
	}
	require.NoError(t, db.Create(operation).Error)
	require.NoError(t, db.Create(&BalanceLedger{
		OperationId: operation.Id, UserId: userId, Account: account, Delta: 1000,
		BalanceBefore: 0, BalanceAfter: 1000, TotalBefore: 0, TotalAfter: 1000,
		Reason: "legacy_referral", ReferenceType: "legacy", ReferenceId: "LEGACY-1",
		IdempotencyKey: key, CreatedAt: common.GetTimestamp(),
	}).Error)
}

func insertLegacyLedgerRowWithReason(t *testing.T, db *gorm.DB, userId int, account, reason, key string) {
	t.Helper()
	operation := &BalanceLedgerOperation{
		IdempotencyKey: key, UserId: userId, Operation: "credit", OperationSignature: "legacy",
		TotalBefore: 0, TotalAfter: 1000, CreatedAt: common.GetTimestamp(),
	}
	require.NoError(t, db.Create(operation).Error)
	require.NoError(t, db.Create(&BalanceLedger{
		OperationId: operation.Id, UserId: userId, Account: account, Delta: 1000,
		BalanceBefore: 0, BalanceAfter: 1000, TotalBefore: 0, TotalAfter: 1000,
		Reason: reason, ReferenceType: "legacy", ReferenceId: key,
		IdempotencyKey: key, CreatedAt: common.GetTimestamp(),
	}).Error)
}

func TestCleanupLegacyAffiliateDataDropsOldSchemaAndKeepsReconcileCount(t *testing.T) {
	setupLedgerTestState(t)
	createLegacyAffiliateTables(t, DB)
	insertLegacyReconcileState(t, DB, 1)
	insertLegacyReconcileState(t, DB, 2)
	insertLegacyReconcileState(t, DB, 3)

	inspection, err := InspectLegacyAffiliateData(DB)
	require.NoError(t, err, "纯扫描/对账状态非空不得阻断清理")
	assert.EqualValues(t, 3, inspection.AffiliateRewardReconcileRows)
	assert.Zero(t, inspection.AffiliateRewardRows)

	report, err := CleanupLegacyAffiliateData(DB)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"affiliate_rewards",
		"affiliate_reward_reconcile_states",
		"referral_coin_ledger",
		"referral_coin_rewards",
	}, report.DroppedTables)
	assert.Contains(t, report.DroppedColumns, "users.quota_affiliate")
	assert.EqualValues(t, 3, report.AffiliateRewardReconcileRows)

	assert.False(t, DB.Migrator().HasTable("affiliate_rewards"))
	assert.False(t, DB.Migrator().HasTable("affiliate_reward_reconcile_states"))
	assert.False(t, DB.Migrator().HasColumn(&User{}, "quota_affiliate"))

	// 幂等：重复清理不再做任何破坏性操作，也不报错。
	repeated, err := CleanupLegacyAffiliateData(DB)
	require.NoError(t, err)
	assert.Empty(t, repeated.DroppedTables)
	assert.Empty(t, repeated.DroppedColumns)
}

func TestCleanupLegacyAffiliateDataRefusesWhenOldRewardRowsExist(t *testing.T) {
	setupLedgerTestState(t)
	createLegacyAffiliateTables(t, DB)
	insertLegacyAffiliateReward(t, DB, 1)

	_, err := InspectLegacyAffiliateData(DB)
	require.ErrorIs(t, err, ErrLegacyAffiliateCleanupBlocked)

	_, err = CleanupLegacyAffiliateData(DB)
	require.ErrorIs(t, err, ErrLegacyAffiliateCleanupBlocked)
	assert.True(t, DB.Migrator().HasTable("affiliate_rewards"), "守卫失败不得删表")
	assert.True(t, DB.Migrator().HasColumn(&User{}, "quota_affiliate"), "守卫失败不得删列")
}

func TestCleanupLegacyAffiliateDataRefusesWhenLegacyBalanceExists(t *testing.T) {
	setupLedgerTestState(t)
	createLegacyAffiliateTables(t, DB)
	require.NoError(t, DB.Exec("INSERT INTO users (id, username, password, aff_code, status, quota, quota_paid, quota_bonus, quota_reward, quota_affiliate) VALUES (401, 'legacy-balance-user', '', 'lg-bal', 1, 1000, 0, 0, 0, 1000)").Error)

	report, err := CleanupLegacyAffiliateData(DB)
	require.ErrorIs(t, err, ErrLegacyAffiliateCleanupBlocked)
	assert.EqualValues(t, 1, report.QuotaAffiliateUsers)
	assert.True(t, DB.Migrator().HasTable("affiliate_rewards"))
	assert.True(t, DB.Migrator().HasColumn(&User{}, "quota_affiliate"))
}

func TestCleanupLegacyAffiliateDataRefusesWhenLegacyFundLedgerExists(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.Create(&User{
		Id: 402, Username: "legacy-ledger-user", AffCode: "lg-ledger", Status: common.UserStatusEnabled,
	}).Error)
	createLegacyAffiliateTables(t, DB)
	insertLegacyAffiliateLedgerRow(t, DB, 402, "affiliate")

	report, err := CleanupLegacyAffiliateData(DB)
	require.ErrorIs(t, err, ErrLegacyAffiliateCleanupBlocked)
	assert.EqualValues(t, 1, report.AffiliateLedgerRows)
	assert.True(t, DB.Migrator().HasTable("affiliate_rewards"))
}

func TestCleanupLegacyAffiliateDataRefusesWhenReferralBonusLedgerExists(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.Create(&User{
		Id: 403, Username: "legacy-bonus-user", AffCode: "lg-bonus", Status: common.UserStatusEnabled,
	}).Error)
	createLegacyAffiliateTables(t, DB)
	insertLegacyAffiliateLedgerRow(t, DB, 403, "bonus")
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ?", 403).Update("reason", "referral_bonus").Error)

	report, err := CleanupLegacyAffiliateData(DB)
	require.ErrorIs(t, err, ErrLegacyAffiliateCleanupBlocked)
	assert.EqualValues(t, 1, report.LegacyFundLedgerRows)
}

// 旧资金 reason 集合（referral_bonus/aff_transfer/affiliate_transfer）任一存在
// 都必须阻断；新的 referral_transfer 是保留的划转账本，不属旧资金。
func TestCleanupLegacyAffiliateDataFundReasonScope(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.Create(&User{
		Id: 404, Username: "legacy-fund-user", AffCode: "lg-fund", Status: common.UserStatusEnabled,
	}).Error)
	createLegacyAffiliateTables(t, DB)

	insertLegacyLedgerRowWithReason(t, DB, 404, "reward", "referral_transfer", "legacy-scope-new-transfer")
	inspection, err := InspectLegacyAffiliateData(DB)
	require.NoError(t, err, "新 referral_transfer 不得作为旧资金阻断")
	assert.Zero(t, inspection.LegacyFundLedgerRows)

	for i, reason := range []string{"referral_bonus", "aff_transfer", "affiliate_transfer"} {
		insertLegacyLedgerRowWithReason(t, DB, 404, "bonus", reason, fmt.Sprintf("legacy-scope-old-%d", i))
	}
	report, err := CleanupLegacyAffiliateData(DB)
	require.ErrorIs(t, err, ErrLegacyAffiliateCleanupBlocked)
	assert.EqualValues(t, 3, report.LegacyFundLedgerRows)
	assert.True(t, DB.Migrator().HasTable("affiliate_rewards"), "阻断时不得删表")
}

// 已软删用户仍可能持有旧钱包余额：Unscoped 检查必须阻断清理。
func TestCleanupLegacyAffiliateDataRefusesSoftDeletedLegacyBalance(t *testing.T) {
	setupLedgerTestState(t)
	createLegacyAffiliateTables(t, DB)
	deleted := &User{Id: 405, Username: "legacy-deleted-user", AffCode: "lg-del", Status: common.UserStatusEnabled}
	deleted.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
	require.NoError(t, DB.Create(deleted).Error)
	require.NoError(t, DB.Exec("UPDATE users SET quota_affiliate = 1000 WHERE id = ?", 405).Error)

	report, err := CleanupLegacyAffiliateData(DB)
	require.ErrorIs(t, err, ErrLegacyAffiliateCleanupBlocked)
	assert.EqualValues(t, 1, report.QuotaAffiliateUsers)
	assert.True(t, DB.Migrator().HasColumn(&User{}, "quota_affiliate"), "阻断时不得删列")
}

// 清理必须删除旧冻结期 option，保留 Enabled/Rate 与其它配置；原生
// aff_quota/aff_history 是保留字段，不随清理删除。
func TestCleanupLegacyAffiliateDataRemovesFreezeOptionAndKeepsNativeFields(t *testing.T) {
	setupLedgerTestState(t)
	createLegacyAffiliateTables(t, DB)
	require.NoError(t, DB.Exec(`INSERT INTO options (key, value) VALUES
		('AffiliateRewardFreezeDays', '7'),
		('AffiliateRewardEnabled', 'true'),
		('AffiliateRewardRate', '0.05'),
		('QuotaRemindThreshold', '1000')`).Error)
	require.NoError(t, DB.Create(&User{
		Id: 406, Username: "native-aff-user", AffCode: "lg-native", Status: common.UserStatusEnabled,
		AffQuota: 123, AffHistoryQuota: 456,
	}).Error)

	inspection, err := InspectLegacyAffiliateData(DB)
	require.NoError(t, err)
	assert.EqualValues(t, 1, inspection.FreezeOptionRows)

	report, err := CleanupLegacyAffiliateData(DB)
	require.NoError(t, err)
	assert.EqualValues(t, 1, report.RemovedFreezeOptions)

	var freezeRows int64
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", legacyAffiliateFreezeOptionKey).Count(&freezeRows).Error)
	assert.Zero(t, freezeRows, "旧冻结期 option 必须删除")

	for _, key := range []string{"AffiliateRewardEnabled", "AffiliateRewardRate", "QuotaRemindThreshold"} {
		var count int64
		require.NoError(t, DB.Model(&Option{}).Where("key = ?", key).Count(&count).Error)
		assert.EqualValues(t, 1, count, "配置 %s 必须保留", key)
	}

	native := getUserBalances(t, 406)
	assert.Equal(t, 123, native.AffQuota, "原生 aff_quota 字段必须保留")
	assert.Equal(t, 456, native.AffHistoryQuota, "原生 aff_history 字段必须保留")
	assert.True(t, DB.Migrator().HasColumn(&User{}, "aff_quota"))
	assert.True(t, DB.Migrator().HasColumn(&User{}, "aff_history"))
}

func TestCleanupLegacyAffiliateDataRemovesLegacyMaintenanceTasks(t *testing.T) {
	setupLedgerTestState(t)
	createLegacyAffiliateTables(t, DB)
	require.NoError(t, DB.Create(&SystemTask{
		TaskID: "legacy-maint-task", Type: "affiliate_reward_maintenance",
		Status: SystemTaskStatusSucceeded, CreatedAt: common.GetTimestamp(), UpdatedAt: common.GetTimestamp(),
	}).Error)

	report, err := CleanupLegacyAffiliateData(DB)
	require.NoError(t, err)
	assert.EqualValues(t, 1, report.RemovedSystemTasks)
}

// Inspect 只读：即使守卫阻断，也不得创建/更改表、选项或数据。
func TestInspectLegacyAffiliateDataIsReadOnly(t *testing.T) {
	setupLedgerTestState(t)
	createLegacyAffiliateTables(t, DB)
	insertLegacyReconcileState(t, DB, 7)
	insertLegacyAffiliateReward(t, DB, 8)
	require.NoError(t, DB.Exec("INSERT INTO options (key, value) VALUES ('AffiliateRewardFreezeDays', '7')").Error)

	tableSnapshot := func() []string {
		var names []string
		require.NoError(t, DB.Table("sqlite_master").Where("type = ?", "table").Order("name").Pluck("name", &names).Error)
		return names
	}
	optionSnapshot := func() []Option {
		var options []Option
		require.NoError(t, DB.Order("key").Find(&options).Error)
		return options
	}
	userSnapshot := func() []User {
		var users []User
		require.NoError(t, DB.Unscoped().Order("id").Find(&users).Error)
		return users
	}

	tablesBefore, optionsBefore, usersBefore := tableSnapshot(), optionSnapshot(), userSnapshot()
	report, err := InspectLegacyAffiliateData(DB)
	require.ErrorIs(t, err, ErrLegacyAffiliateCleanupBlocked, "守卫必须阻断")
	assert.EqualValues(t, 1, report.FreezeOptionRows)
	require.Equal(t, tablesBefore, tableSnapshot(), "inspect 不得创建/删除表")
	require.Equal(t, optionsBefore, optionSnapshot(), "inspect 不得更改选项")
	require.Equal(t, usersBefore, userSnapshot(), "inspect 不得更改数据")
}
