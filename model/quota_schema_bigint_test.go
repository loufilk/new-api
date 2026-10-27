package model

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// moneyColumnsByTable 是必须为 64 位的资金持久化列。
// users.quota 是三账户合计的兼容镜像，quota_paid/bonus/reward 是资金真源。
var moneyColumnsByTable = map[string][]string{
	"users": {
		"quota", "used_quota", "aff_quota", "aff_history",
		"quota_paid", "quota_bonus", "quota_reward",
	},
	"balance_ledger_operations": {"total_before", "total_after"},
	"balance_ledger": {
		"delta", "balance_before", "balance_after", "total_before", "total_after",
	},
	"referral_commissions": {"paid_quota", "reward_quota"},
}

// openScratchDB 提供与共享测试库隔离的空库，用于构造遗留 32 位 schema。
// 单连接：内存库在连接关闭后即销毁，连接池若开出第二条连接会拿到另一个空库。
func openScratchDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// createLegacyUsersTable 建一张所有额度列均为 32 位 INT 的 users 表，
// 模拟未执行 bin/migration_quota_bigint_*.sql 的存量库。
func createLegacyUsersTable(t *testing.T, db *gorm.DB, bigintColumns ...string) {
	t.Helper()
	columnType := func(name string) string {
		for _, bigint := range bigintColumns {
			if bigint == name {
				return "BIGINT"
			}
		}
		return "INT"
	}
	require.NoError(t, db.Exec(fmt.Sprintf(`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		quota %s DEFAULT 0,
		used_quota %s DEFAULT 0,
		aff_quota %s DEFAULT 0,
		aff_history %s DEFAULT 0,
		quota_paid %s DEFAULT 0,
		quota_bonus %s DEFAULT 0,
		quota_reward %s DEFAULT 0
	)`,
		columnType("quota"), columnType("used_quota"), columnType("aff_quota"),
		columnType("aff_history"), columnType("quota_paid"), columnType("quota_bonus"),
		columnType("quota_reward"))).Error)
}

// TestEnsureUserQuotaColumnsRejectsLegacyChuanAPIWalletColumns 覆盖 P0 的核心契约：
// 官方四列已是 BIGINT、但万川钱包三列仍是 32 位时，启动检查必须拒绝。
func TestEnsureUserQuotaColumnsRejectsLegacyChuanAPIWalletColumns(t *testing.T) {
	db := openScratchDB(t)
	createLegacyUsersTable(t, db, "quota", "used_quota", "aff_quota", "aff_history")

	// dbType 传 MySQL：SQLite 会被 ensureUserQuotaColumns 直接放行，
	// 而 SQLite migrator 如实返回声明类型，因此可用来驱动 MySQL 判定分支。
	err := ensureUserQuotaColumns(db, common.DatabaseTypeMySQL)
	require.Error(t, err, "32 位钱包列必须阻断启动")
	assert.Contains(t, err.Error(), "quota_paid")
	assert.Contains(t, err.Error(), "SKIP_64BIT_QUOTA_SCHEMA_CHECK does not apply")
}

// TestEnsureUserQuotaColumnsSkipEnvDoesNotSkipWalletColumns 验证主 Agent 的决定：
// SKIP_64BIT_QUOTA_SCHEMA_CHECK 保留对官方四列的既有语义，但对万川钱包三列无效。
func TestEnsureUserQuotaColumnsSkipEnvDoesNotSkipWalletColumns(t *testing.T) {
	t.Setenv("SKIP_64BIT_QUOTA_SCHEMA_CHECK", "true")

	db := openScratchDB(t)
	// 官方四列与钱包三列全部为 32 位。
	createLegacyUsersTable(t, db)

	err := ensureUserQuotaColumns(db, common.DatabaseTypeMySQL)
	require.Error(t, err, "SKIP=true 不得跳过万川钱包列")
	// 错误必须来自钱包列，证明官方列确实被跳过、而钱包列仍被检查。
	assert.Contains(t, err.Error(), "quota_paid")
	// 断言依赖错误格式 "users.<column> uses ..."：钱包列的 "users.quota_paid uses"
	// 不会被 "users.quota uses" 命中（quota 之后是 '_' 而非空格），
	// 因此逐列 NotContains 能真正区分官方列与钱包列。
	for _, official := range userQuotaColumns {
		assert.NotContains(t, err.Error(), "users."+official+" uses")
	}
}

// TestEnsureUserQuotaColumnsAllowsMissingWalletColumns 锁定 rc.26 -> rc.30 的升级
// 路径：钱包列尚不存在时检查必须放行，否则存量库永远无法升级（这些列由随后的
// AutoMigrate 建出）。这条放行使 model/user.go 的 gorm tag 成为唯一保障——
// tag 必须显式 bigint，否则本检查放行、AutoMigrate 建出 32 位列。
func TestEnsureUserQuotaColumnsAllowsMissingWalletColumns(t *testing.T) {
	db := openScratchDB(t)
	// 只有官方四列的 rc.26 形态 users 表，完全没有 quota_paid/bonus/reward。
	require.NoError(t, db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		quota BIGINT DEFAULT 0,
		used_quota BIGINT DEFAULT 0,
		aff_quota BIGINT DEFAULT 0,
		aff_history BIGINT DEFAULT 0
	)`).Error)

	require.NoError(t, ensureUserQuotaColumns(db, common.DatabaseTypeMySQL))
	require.NoError(t, ensureUserQuotaColumns(db, common.DatabaseTypePostgreSQL))
}

// TestEnsureUserQuotaColumnsAcceptsFullyMigratedSchema 全部列升级后必须放行，
// 且 SKIP 未设置时也不得误报。
func TestEnsureUserQuotaColumnsAcceptsFullyMigratedSchema(t *testing.T) {
	db := openScratchDB(t)
	createLegacyUsersTable(t, db,
		"quota", "used_quota", "aff_quota", "aff_history",
		"quota_paid", "quota_bonus", "quota_reward")

	require.NoError(t, ensureUserQuotaColumns(db, common.DatabaseTypeMySQL))
	require.NoError(t, ensureUserQuotaColumns(db, common.DatabaseTypePostgreSQL))
}

// moneyFieldsByModel 是资金列对应的 Go 字段名，用于方言无关的 tag 断言。
var moneyFieldsByModel = []struct {
	model  any
	fields []string
}{
	{&User{}, []string{
		"Quota", "UsedQuota", "AffQuota", "AffHistoryQuota",
		"QuotaPaid", "QuotaBonus", "QuotaReward",
	}},
	{&BalanceLedgerOperation{}, []string{"TotalBefore", "TotalAfter"}},
	{&BalanceLedger{}, []string{
		"Delta", "BalanceBefore", "BalanceAfter", "TotalBefore", "TotalAfter",
	}},
	{&ReferralCommission{}, []string{"PaidQuota", "RewardQuota"}},
}

// TestMoneyColumnTagsDeclareBigint 方言无关地断言资金字段的 GORM tag 显式声明 bigint。
// Go 的 int 在 32 位平台会被映射为 32 位列，因此不能依赖隐式推导。
func TestMoneyColumnTagsDeclareBigint(t *testing.T) {
	for _, target := range moneyFieldsByModel {
		statement := &gorm.Statement{DB: DB}
		require.NoError(t, statement.Parse(target.model))
		for _, fieldName := range target.fields {
			field := statement.Schema.LookUpField(fieldName)
			require.NotNil(t, field, "%T.%s must exist", target.model, fieldName)
			assert.Equal(t, "bigint", strings.ToLower(field.TagSettings["TYPE"]),
				"%T.%s must declare gorm type:bigint", target.model, fieldName)
		}
	}
}

// TestAutoMigrateDeclaresMoneyColumnsAs64Bit 验证 GORM tag 本身产出 64 位列：
// 新库经 AutoMigrate 后，全部资金列的声明类型在 MySQL 与 PostgreSQL 判定下
// 都必须是 64 位。这保证新装实例不会再生成 32 位资金列。
func TestAutoMigrateDeclaresMoneyColumnsAs64Bit(t *testing.T) {
	db := openScratchDB(t)
	require.NoError(t, db.AutoMigrate(
		&User{},
		&BalanceLedgerOperation{},
		&BalanceLedger{},
		&ReferralCommission{},
	))

	tableModels := map[string]any{
		"users":                     &User{},
		"balance_ledger_operations": &BalanceLedgerOperation{},
		"balance_ledger":            &BalanceLedger{},
		"referral_commissions":      &ReferralCommission{},
	}

	for table, expectedColumns := range moneyColumnsByTable {
		assertColumnsAre64Bit(t, db, tableModels[table], table, expectedColumns,
			common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL)
	}
}

// assertColumnsAre64Bit 读取真实 schema 的声明类型，并按调用方指定的数据库
// 方言断言其为 64 位。SQLite scratch schema 声明为 BIGINT，可同时验证两套规则；
// 真实数据库则只验证自身方言，避免把 PostgreSQL 的 int8 交给 MySQL 规则误判。
func assertColumnsAre64Bit(t *testing.T, db *gorm.DB, model any, table string, expectedColumns []string, dbTypes ...common.DatabaseType) {
	t.Helper()
	columnTypes, err := db.Migrator().ColumnTypes(model)
	require.NoError(t, err, "inspect %s", table)

	declared := make(map[string]string, len(columnTypes))
	for _, column := range columnTypes {
		declared[strings.ToLower(column.Name())] = column.DatabaseTypeName()
	}

	for _, column := range expectedColumns {
		dataType, ok := declared[column]
		require.True(t, ok, "%s.%s must exist after AutoMigrate", table, column)
		for _, dbType := range dbTypes {
			assert.True(t, is64BitIntegerType(dbType, dataType),
				"%s.%s declared as %q is not 64-bit under database type %d", table, column, dataType, dbType)
		}
	}
}

// TestMoneyColumnsAre64BitOnConfiguredDatabases 在真实 MySQL / PostgreSQL 上验证
// AutoMigrate 产出的资金列确实是 BIGINT。未配置 DSN 时跳过。
//
// DSN 必须指向可丢弃的测试库：每个模型迁移到唯一临时表，结束后删除。
func TestMoneyColumnsAre64BitOnConfiguredDatabases(t *testing.T) {
	dialects := []struct {
		name      string
		env       string
		dbType    common.DatabaseType
		dialector func(string) gorm.Dialector
	}{
		{"mysql", "TEST_MYSQL_DSN", common.DatabaseTypeMySQL,
			func(dsn string) gorm.Dialector { return mysql.Open(dsn) }},
		{"postgres", "TEST_POSTGRES_DSN", common.DatabaseTypePostgreSQL,
			func(dsn string) gorm.Dialector {
				return postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}},
	}

	for _, dialect := range dialects {
		t.Run(dialect.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(dialect.env))
			if dsn == "" {
				t.Skip(dialect.env + " is not configured")
			}
			db, err := gorm.Open(dialect.dialector(dsn), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })

			// 每个模型单独迁移到唯一临时表：资金四表之间无外键，可独立建表；
			// 顺序执行 + 立即清理可避免 tag 内显式索引名在 PostgreSQL 下冲突。
			models := map[string]any{
				"users":                     &User{},
				"balance_ledger_operations": &BalanceLedgerOperation{},
				"balance_ledger":            &BalanceLedger{},
				"referral_commissions":      &ReferralCommission{},
			}
			for table, expectedColumns := range moneyColumnsByTable {
				model := models[table]
				tempTable := "p0_bigint_" + table
				require.NoError(t, db.Table(tempTable).Migrator().DropTable(tempTable))
				require.NoError(t, db.Table(tempTable).AutoMigrate(model))
				assertColumnsAre64Bit(t, db.Table(tempTable), model, tempTable, expectedColumns, dialect.dbType)
				require.NoError(t, db.Table(tempTable).Migrator().DropTable(tempTable))
			}
		})
	}
}

// TestBackfillLegacyQuotaAccountsPreserves64BitBalance 覆盖 P0 的真实风险点：
// quota 回填 DataStep -> BackfillLegacyQuotaAccounts 每次启动都会把 users.quota
// 回填进 quota_paid。余额超过 2^31-1 时不得截断，且镜像与账本必须一致。
func TestBackfillLegacyQuotaAccountsPreserves64BitBalance(t *testing.T) {
	setupLedgerTestState(t)

	const legacyQuota = 3_000_000_000 // > math.MaxInt32 (2147483647)
	user := User{
		Id:       9001,
		Username: "legacy-bigint-user",
		AffCode:  "affbigint",
		Quota:    legacyQuota,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(&user).Error)

	affected, err := BackfillLegacyQuotaAccounts()
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)

	migrated := getUserBalances(t, user.Id)
	require.Equal(t, legacyQuota, migrated.QuotaPaid, "quota_paid 不得截断")
	assert.Equal(t, legacyQuota, migrated.Quota)
	assert.Zero(t, migrated.QuotaBonus)
	assert.Zero(t, migrated.QuotaReward)
	assertQuotaInvariant(t, migrated)

	key := fmt.Sprintf("migration:legacy_quota:%d", user.Id)
	var operation BalanceLedgerOperation
	require.NoError(t, DB.Where("idempotency_key = ?", key).First(&operation).Error)
	assert.Equal(t, legacyQuota, operation.TotalBefore)
	assert.Equal(t, legacyQuota, operation.TotalAfter)

	var rows []BalanceLedger
	require.NoError(t, DB.Where("idempotency_key = ?", key).Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, operation.Id, rows[0].OperationId)
	assert.Equal(t, string(AccountPaid), rows[0].Account)
	assert.Equal(t, legacyQuota, rows[0].Delta)
	assert.Zero(t, rows[0].BalanceBefore)
	assert.Equal(t, legacyQuota, rows[0].BalanceAfter)
	assert.Equal(t, legacyQuota, rows[0].TotalAfter)

	// 幂等：重复执行不再回填，也不产生第二条账本。
	repeated, err := BackfillLegacyQuotaAccounts()
	require.NoError(t, err)
	assert.EqualValues(t, 0, repeated)
	var rowCount int64
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("idempotency_key = ?", key).Count(&rowCount).Error)
	assert.EqualValues(t, 1, rowCount)
}
