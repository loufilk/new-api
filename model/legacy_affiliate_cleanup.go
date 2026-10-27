package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// ErrLegacyAffiliateCleanupBlocked 表示旧邀请体系仍有奖励、余额或资金账本
// 记录，清理被安全守卫拒绝。守卫通过前不得删除任何表或列。
var ErrLegacyAffiliateCleanupBlocked = errors.New("legacy affiliate cleanup blocked: non-zero old reward, balance, or fund records")

const (
	// legacyAffiliateFreezeOptionKey 是旧冻结期配置的 option key；新体系不再
	// 使用冻结期，清理时显式删除该项，AffiliateRewardEnabled/Rate 与其它配置保留。
	legacyAffiliateFreezeOptionKey = "AffiliateRewardFreezeDays"
	// 清理命令的有界超时：锁等待与单条语句都不得无限阻塞。
	legacyCleanupLockTimeout      = "5s"
	legacyCleanupStatementTimeout = "90s"
)

// legacyAffiliateFundReasons 是旧邀请体系写下的资金账本 reason；新的
// referral_transfer（划转到 reward）不属于旧资金，不得作为清理阻断理由。
var legacyAffiliateFundReasons = []string{"referral_bonus", "aff_transfer", "affiliate_transfer"}

// legacyAffiliateTables 是旧邀请/返利体系遗留表。前三个承载奖励与资金记录，
// 必须为空才能清理；最后一个仅为历史扫描/对账状态，允许非空但会记录数量。
var (
	legacyAffiliateGuardedTables = []string{
		"affiliate_rewards",
		"referral_coin_ledger",
		"referral_coin_rewards",
	}
	legacyAffiliateReconcileTable = "affiliate_reward_reconcile_states"
	legacyAffiliateDropTables     = []string{
		"affiliate_rewards",
		"affiliate_reward_reconcile_states",
		"referral_coin_ledger",
		"referral_coin_rewards",
	}
	// PostgreSQL 固定加锁顺序：先锁要删除/改结构的表，再锁守卫查询涉及的
	// 资金、任务与选项表。固定顺序避免与其它清理进程互相死锁。
	legacyAffiliateExclusiveLockTables = []string{
		"affiliate_rewards",
		"affiliate_reward_reconcile_states",
		"referral_coin_ledger",
		"referral_coin_rewards",
		"users",
	}
	legacyAffiliateSharedLockTables = []string{
		"balance_ledger",
		"balance_ledger_operations",
		"system_tasks",
		"options",
	}
)

// LegacyAffiliateCleanupReport 记录清理前后计数与已执行的破坏性操作，供运维留存。
type LegacyAffiliateCleanupReport struct {
	AffiliateRewardRows          int64    `json:"affiliate_reward_rows"`
	AffiliateRewardReconcileRows int64    `json:"affiliate_reward_reconcile_rows"`
	ReferralCoinLedgerRows       int64    `json:"referral_coin_ledger_rows"`
	ReferralCoinRewardRows       int64    `json:"referral_coin_reward_rows"`
	QuotaAffiliateUsers          int64    `json:"quota_affiliate_users"`
	AffiliateLedgerRows          int64    `json:"affiliate_ledger_rows"`
	LegacyFundLedgerRows         int64    `json:"legacy_fund_ledger_rows"`
	FreezeOptionRows             int64    `json:"affiliate_reward_freeze_option_rows"`
	RemovedSystemTasks           int64    `json:"removed_system_tasks"`
	RemovedFreezeOptions         int64    `json:"removed_affiliate_reward_freeze_options"`
	DroppedTables                []string `json:"dropped_tables"`
	DroppedColumns               []string `json:"dropped_columns"`
}

// CleanupLegacyAffiliateData 显式删除旧邀请体系遗留表、users.quota_affiliate 列与
// 旧维护任务/冻结期配置。绝不随启动自动执行，且必须由运维在所有旧进程停止后手动
// 运行 cmd/referral-cleanup 触发。
//
// 守卫（任一命中即拒绝清理，不清零、不删表/列）：
//   - affiliate_rewards / referral_coin_ledger / referral_coin_rewards 任意行；
//   - users.quota_affiliate 任一非零用户（含已软删用户）；
//   - balance_ledger 中 account='affiliate'，或 reason 属于旧体系
//     （referral_bonus/aff_transfer/affiliate_transfer）的记录；新的
//     referral_transfer 是保留字段的划转账本，不作为阻断。
//
// affiliate_reward_reconcile_states 只是旧扫描/对账状态，允许非空并记录行数。
// 幂等：已清理过的库重复执行为空操作。PostgreSQL（支持事务 DDL）在单个事务内
// 完成加锁、守卫与删除；失败整体回滚，结构、资金与 option 原样保留。
func CleanupLegacyAffiliateData(db *gorm.DB) (*LegacyAffiliateCleanupReport, error) {
	if db == nil {
		return nil, errors.New("legacy affiliate cleanup: database must not be nil")
	}
	var report *LegacyAffiliateCleanupReport
	run := func(tx *gorm.DB) error {
		// 先加表锁再检查，保证 count-then-drop 期间没有并发写入。
		if err := lockLegacyAffiliateCleanupTx(tx); err != nil {
			return err
		}
		var err error
		report, err = cleanupLegacyAffiliateData(tx)
		return err
	}
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		// MySQL 不支持事务 DDL，守卫与删除只能顺序执行。
		if err := run(db); err != nil {
			return report, err
		}
		return report, nil
	}
	if err := db.Transaction(run); err != nil {
		return report, err
	}
	return report, nil
}

// lockLegacyAffiliateCleanupTx 在 PostgreSQL 上设置有限的 lock/statement 超时并按
// 固定顺序对旧表、users、资金账本以及涉及的任务/选项表加表锁：待删除/改结构的
// 表用 ACCESS EXCLUSIVE，守卫与行删除涉及的表用 SHARE ROW EXCLUSIVE（允许读、
// 阻止写）。其它方言由单写者（SQLite）或既有顺序执行语义约束，不在此模拟。
func lockLegacyAffiliateCleanupTx(tx *gorm.DB) error {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return nil
	}
	if err := tx.Exec("SET LOCAL lock_timeout = '" + legacyCleanupLockTimeout + "'").Error; err != nil {
		return err
	}
	if err := tx.Exec("SET LOCAL statement_timeout = '" + legacyCleanupStatementTimeout + "'").Error; err != nil {
		return err
	}
	lock := func(tables []string, mode string) error {
		quoted := make([]string, 0, len(tables))
		for _, table := range tables {
			var present int64
			if err := tx.Raw(
				`SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE c.relname = ? AND n.nspname = current_schema()`,
				table,
			).Scan(&present).Error; err != nil {
				return err
			}
			if present > 0 {
				quoted = append(quoted, fmt.Sprintf("%q", table))
			}
		}
		if len(quoted) == 0 {
			return nil
		}
		return tx.Exec("LOCK TABLE " + strings.Join(quoted, ", ") + " IN " + mode + " MODE").Error
	}
	if err := lock(legacyAffiliateExclusiveLockTables, "ACCESS EXCLUSIVE"); err != nil {
		return err
	}
	return lock(legacyAffiliateSharedLockTables, "SHARE ROW EXCLUSIVE")
}

func cleanupLegacyAffiliateData(db *gorm.DB) (*LegacyAffiliateCleanupReport, error) {
	report, hasQuotaAffiliateColumn, err := inspectLegacyAffiliateData(db)
	if err != nil {
		return report, err
	}
	migrator := db.Migrator()

	// 只删除固定白名单内的旧表，且不得级联破坏外部依赖。PostgreSQL 驱动
	// （gorm.io/driver/postgres v1.5.9）的 Migrator.DropTable 固定生成
	// `DROP TABLE IF EXISTS ? CASCADE`，会连同依赖旧表的外键/视图一起删除，
	// 因此这里改为显式 RESTRICT：存在任何外部依赖时整笔清理失败并回滚。
	// 其它方言使用迁移器默认实现。
	for _, table := range legacyAffiliateDropTables {
		if !migrator.HasTable(table) {
			continue
		}
		var dropErr error
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			dropErr = db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %q RESTRICT", table)).Error
		} else {
			dropErr = migrator.DropTable(table)
		}
		if dropErr != nil {
			return report, fmt.Errorf("drop legacy affiliate table %s: %w", table, dropErr)
		}
		report.DroppedTables = append(report.DroppedTables, table)
	}
	if hasQuotaAffiliateColumn {
		if err := dropLegacyQuotaAffiliateColumn(db); err != nil {
			return report, fmt.Errorf("drop users.quota_affiliate: %w", err)
		}
		report.DroppedColumns = append(report.DroppedColumns, "users.quota_affiliate")
	}
	if migrator.HasTable(&SystemTask{}) {
		result := db.Where("type = ?", "affiliate_reward_maintenance").Delete(&SystemTask{})
		if result.Error != nil {
			return report, fmt.Errorf("remove legacy affiliate maintenance tasks: %w", result.Error)
		}
		report.RemovedSystemTasks = result.RowsAffected
	}
	if migrator.HasTable(&Option{}) {
		result := db.Where(&Option{Key: legacyAffiliateFreezeOptionKey}).Delete(&Option{})
		if result.Error != nil {
			return report, fmt.Errorf("remove legacy affiliate reward freeze option: %w", result.Error)
		}
		report.RemovedFreezeOptions = result.RowsAffected
	}
	return report, nil
}

// InspectLegacyAffiliateData 只执行清理守卫与计数，不做任何破坏性修改。
// PostgreSQL 下在只读事务中执行，从连接层保证 inspect 不会创建/更改表、选项或数据；
// 其它方言保持同样的只读语义（仅 SELECT）。
func InspectLegacyAffiliateData(db *gorm.DB) (*LegacyAffiliateCleanupReport, error) {
	if db == nil {
		return nil, errors.New("legacy affiliate cleanup: database must not be nil")
	}
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		var report *LegacyAffiliateCleanupReport
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
				return err
			}
			var inspectErr error
			report, _, inspectErr = inspectLegacyAffiliateData(tx)
			return inspectErr
		})
		return report, err
	}
	report, _, err := inspectLegacyAffiliateData(db)
	return report, err
}

func inspectLegacyAffiliateData(db *gorm.DB) (*LegacyAffiliateCleanupReport, bool, error) {
	report := &LegacyAffiliateCleanupReport{}
	migrator := db.Migrator()

	tableCounts := map[string]*int64{
		"affiliate_rewards":           &report.AffiliateRewardRows,
		"referral_coin_ledger":        &report.ReferralCoinLedgerRows,
		"referral_coin_rewards":       &report.ReferralCoinRewardRows,
		legacyAffiliateReconcileTable: &report.AffiliateRewardReconcileRows,
	}
	for _, table := range append(append([]string{}, legacyAffiliateGuardedTables...), legacyAffiliateReconcileTable) {
		if !migrator.HasTable(table) {
			continue
		}
		if err := db.Table(table).Count(tableCounts[table]).Error; err != nil {
			return report, false, fmt.Errorf("count legacy affiliate table %s: %w", table, err)
		}
	}

	hasQuotaAffiliateColumn := migrator.HasColumn(&User{}, "quota_affiliate")
	if hasQuotaAffiliateColumn {
		// Unscoped：已软删用户仍可能持有旧余额，必须一并检查。
		if err := db.Unscoped().Model(&User{}).Where("quota_affiliate <> 0").Count(&report.QuotaAffiliateUsers).Error; err != nil {
			return report, false, fmt.Errorf("count legacy quota_affiliate users: %w", err)
		}
	}
	if migrator.HasTable(&BalanceLedger{}) {
		if err := db.Model(&BalanceLedger{}).Where("account = ?", "affiliate").Count(&report.AffiliateLedgerRows).Error; err != nil {
			return report, false, fmt.Errorf("count legacy affiliate ledger rows: %w", err)
		}
		if err := db.Model(&BalanceLedger{}).Where("reason IN ?", legacyAffiliateFundReasons).Count(&report.LegacyFundLedgerRows).Error; err != nil {
			return report, false, fmt.Errorf("count legacy referral fund ledger rows: %w", err)
		}
	}
	if migrator.HasTable(&Option{}) {
		if err := db.Model(&Option{}).Where(&Option{Key: legacyAffiliateFreezeOptionKey}).Count(&report.FreezeOptionRows).Error; err != nil {
			return report, false, fmt.Errorf("count legacy affiliate reward freeze option: %w", err)
		}
	}

	blockers := make([]string, 0, 6)
	if report.AffiliateRewardRows > 0 {
		blockers = append(blockers, fmt.Sprintf("affiliate_rewards=%d", report.AffiliateRewardRows))
	}
	if report.ReferralCoinLedgerRows > 0 {
		blockers = append(blockers, fmt.Sprintf("referral_coin_ledger=%d", report.ReferralCoinLedgerRows))
	}
	if report.ReferralCoinRewardRows > 0 {
		blockers = append(blockers, fmt.Sprintf("referral_coin_rewards=%d", report.ReferralCoinRewardRows))
	}
	if report.QuotaAffiliateUsers > 0 {
		blockers = append(blockers, fmt.Sprintf("quota_affiliate_users=%d", report.QuotaAffiliateUsers))
	}
	if report.AffiliateLedgerRows > 0 {
		blockers = append(blockers, fmt.Sprintf("affiliate_ledger_rows=%d", report.AffiliateLedgerRows))
	}
	if report.LegacyFundLedgerRows > 0 {
		blockers = append(blockers, fmt.Sprintf("legacy_fund_ledger_rows=%d", report.LegacyFundLedgerRows))
	}
	if len(blockers) > 0 {
		return report, hasQuotaAffiliateColumn, fmt.Errorf("%w: %s", ErrLegacyAffiliateCleanupBlocked, strings.Join(blockers, ", "))
	}
	return report, hasQuotaAffiliateColumn, nil
}

// dropLegacyQuotaAffiliateColumn 按方言删除旧钱包账户列；PostgreSQL 使用
// IF EXISTS 保证重复执行为空操作，其它方言由调用方先行确认列存在。
func dropLegacyQuotaAffiliateColumn(db *gorm.DB) error {
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		return db.Exec(`ALTER TABLE "users" DROP COLUMN IF EXISTS "quota_affiliate"`).Error
	case common.UsingMainDatabase(common.DatabaseTypeMySQL):
		return db.Exec("ALTER TABLE `users` DROP COLUMN `quota_affiliate`").Error
	default:
		return db.Exec("ALTER TABLE users DROP COLUMN quota_affiliate").Error
	}
}
