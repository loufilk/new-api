package service

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// PostgreSQL 18.6 资金/清理验证（显式 opt-in）
//
// 这些用例只在 TEST_REFERRAL_POSTGRES_DSN 指向可丢弃的测试实例时运行，并且
// 每个用例在独立的临时 schema 中建表（search_path 固定在连接启动参数上），
// 不触碰 public 既有数据；结束后删除自己的 schema、还原全局状态并关闭连接。
// 日志只记录 SHOW server_version 的结果，绝不记录 DSN。
// ---------------------------------------------------------------------------

type referralPGFixture struct {
	db     *gorm.DB
	schema string
}

func openReferralPostgreSQLFixture(t *testing.T) *referralPGFixture {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_REFERRAL_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_REFERRAL_POSTGRES_DSN is not configured")
	}

	admin, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)

	var version string
	require.NoError(t, admin.Raw("SHOW server_version").Scan(&version).Error)
	require.NotEmpty(t, version)
	t.Logf("PostgreSQL %s", version)
	require.True(t, strings.HasPrefix(version, "18.6"),
		"Referral PostgreSQL 验证环境必须是生产目标 PostgreSQL 18.6，实际为 %s", version)

	schema := fmt.Sprintf("referral_pg_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)

	connConfig, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	connConfig.RuntimeParams["search_path"] = schema
	connConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	sqlDB := stdlib.OpenDB(*connConfig)

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	model.DB, model.LOG_DB = db, db

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainType, previousLogType)
		_ = sqlDB.Close()
		require.NoError(t, admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error)
		_ = adminSQL.Close()
	})

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.SubscriptionOrder{},
		&model.SubscriptionPlan{},
		&model.Redemption{},
		&model.BalanceLedgerOperation{},
		&model.BalanceLedger{},
		&model.ReferralCommission{},
		&model.Model{},
		&model.Option{},
		&model.SystemTask{},
		&model.Log{},
	))

	return &referralPGFixture{db: db, schema: schema}
}

func seedReferralPGUser(t *testing.T, id int, inviterId, affQuota, affHistory int) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.User{
		Id: id, Username: fmt.Sprintf("pg-referral-user-%d", id),
		AffCode: fmt.Sprintf("pgref%d", id), InviterId: inviterId,
		Status: common.UserStatusEnabled, AffQuota: affQuota, AffHistoryQuota: affHistory,
	}).Error)
}

func countReferralPGCommissions(t *testing.T, inviterId int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.ReferralCommission{}).Where("user_id = ?", inviterId).Count(&count).Error)
	return count
}

// TestReferralPostgreSQLCommissionConcurrentCapAndBoundary 在真实 PostgreSQL 上
// 验证并发充值的前三次计次上限与邀请人余额边界回滚。
func TestReferralPostgreSQLCommissionConcurrentCapAndBoundary(t *testing.T) {
	openReferralPostgreSQLFixture(t)
	setReferralRewardConfig(t, true, 0.05)

	// 场景一：同一被邀请人 5 笔并发充值，只允许前三次返佣。
	seedReferralPGUser(t, 8301, 0, 0, 0)
	seedReferralPGUser(t, 8302, 8301, 0, 0)
	const events = 5
	for i := range events {
		seedPendingOnlineTopUp(t, 8302, 2, fmt.Sprintf("PG-CAP-%d", i))
	}
	errs := make([]error, events)
	var wg sync.WaitGroup
	wg.Add(events)
	for i := range events {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = model.RechargeEpay(fmt.Sprintf("PG-CAP-%d", idx), "alipay", "2.00", "127.0.0.1")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "topup %d", i)
	}
	assert.EqualValues(t, 3, countReferralPGCommissions(t, 8301), "前三次后不再返佣")
	assert.Equal(t, 150_000, getUserBalances(t, 8301).AffQuota)
	assert.Equal(t, 5_000_000, getUserBalances(t, 8302).QuotaPaid, "充值本身必须全部入账")

	// 场景二：两个被邀请人并发充值至同一邀请人的上限边界，只允许一笔成功。
	seedReferralPGUser(t, 8303, 0, common.MaxWalletQuota-50_000, 0)
	seedReferralPGUser(t, 8304, 8303, 0, 0)
	seedReferralPGUser(t, 8305, 8303, 0, 0)
	seedPendingOnlineTopUp(t, 8304, 2, "PG-BOUNDARY-1")
	seedPendingOnlineTopUp(t, 8305, 2, "PG-BOUNDARY-2")

	boundaryErrs := make([]error, 2)
	wg.Add(2)
	for i, tradeNo := range []string{"PG-BOUNDARY-1", "PG-BOUNDARY-2"} {
		go func(idx int, trade string) {
			defer wg.Done()
			_, boundaryErrs[idx] = model.RechargeEpay(trade, "alipay", "2.00", "127.0.0.1")
		}(i, tradeNo)
	}
	wg.Wait()

	successes := 0
	for i, userId := range []int{8304, 8305} {
		if boundaryErrs[i] == nil {
			successes++
			assert.Equal(t, 1_000_000, getUserBalances(t, userId).QuotaPaid)
			continue
		}
		assert.Zero(t, getUserBalances(t, userId).QuotaPaid, "失败方充值必须回滚")
		var topUp model.TopUp
		require.NoError(t, model.DB.Where("trade_no = ?", fmt.Sprintf("PG-BOUNDARY-%d", i+1)).First(&topUp).Error)
		assert.Equal(t, common.TopUpStatusPending, topUp.Status)
	}
	assert.Equal(t, 1, successes, "边界上只允许一笔返佣成功")
	assert.EqualValues(t, 1, countReferralPGCommissions(t, 8303))
	assert.Equal(t, common.MaxWalletQuota, getUserBalances(t, 8303).AffQuota)
}

// TestReferralPostgreSQLCommissionOverflowRollsBackAtomically 在真实 PostgreSQL
// 上验证超限时订单状态、充值账本与返佣在同一事务整体回滚。
func TestReferralPostgreSQLCommissionOverflowRollsBackAtomically(t *testing.T) {
	openReferralPostgreSQLFixture(t)
	setReferralRewardConfig(t, true, 0.05)
	seedReferralPGUser(t, 8311, 0, common.MaxWalletQuota, common.MaxWalletQuota)
	seedReferralPGUser(t, 8312, 8311, 0, 0)

	seedPendingOnlineTopUp(t, 8312, 2, "PG-OVERFLOW-ROLLBACK")
	_, err := model.RechargeEpay("PG-OVERFLOW-ROLLBACK", "alipay", "2.00", "127.0.0.1")
	require.Error(t, err, "超限必须整体回滚")

	var topUp model.TopUp
	require.NoError(t, model.DB.Where("trade_no = ?", "PG-OVERFLOW-ROLLBACK").First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
	assert.Zero(t, getUserBalances(t, 8312).QuotaPaid, "失败充值不得入账")
	var ledgerRows int64
	require.NoError(t, model.DB.Model(&model.BalanceLedger{}).
		Where("reference_id = ?", "PG-OVERFLOW-ROLLBACK").Count(&ledgerRows).Error)
	assert.Zero(t, ledgerRows, "失败充值不得留下账本")
	assert.Zero(t, countReferralPGCommissions(t, 8311))
	assert.Equal(t, common.MaxWalletQuota, getUserBalances(t, 8311).AffQuota)
}

// TestReferralPostgreSQLTransferReplayAndConcurrency 在真实 PostgreSQL 上验证
// 划转的并发同键幂等、重放与参数冲突。
func TestReferralPostgreSQLTransferReplayAndConcurrency(t *testing.T) {
	openReferralPostgreSQLFixture(t)
	seedReferralPGUser(t, 8321, 0, 1_000_000, 1_000_000)

	const workers = 6
	results := make([]*model.LedgerResult, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = model.TransferAffQuota(8321, 500_000, "pg-transfer-concurrent")
		}(i)
	}
	wg.Wait()

	applied := 0
	for i, err := range errs {
		require.NoError(t, err, "worker %d", i)
		require.NotNil(t, results[i])
		if !results[i].Replayed {
			applied++
		}
	}
	assert.Equal(t, 1, applied, "并发同键只允许一次真实入账")

	user := getUserBalances(t, 8321)
	assert.Equal(t, 500_000, user.AffQuota)
	assert.Equal(t, 500_000, user.QuotaReward)
	assert.Equal(t, 1_000_000, user.AffHistoryQuota)
	var ledgerRows int64
	require.NoError(t, model.DB.Model(&model.BalanceLedger{}).
		Where("user_id = ? AND reason = ?", 8321, model.LedgerReasonReferralTransfer).Count(&ledgerRows).Error)
	assert.EqualValues(t, 1, ledgerRows)

	replayed, err := model.TransferAffQuota(8321, 500_000, "pg-transfer-concurrent")
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	_, err = model.TransferAffQuota(8321, 600_000, "pg-transfer-concurrent")
	require.ErrorIs(t, err, model.ErrLedgerIdempotencyConflict)

	// 余额不足不产生任何变动。
	seedReferralPGUser(t, 8322, 0, 500_000, 500_000)
	_, err = model.TransferAffQuota(8322, 600_000, "pg-transfer-insufficient")
	require.ErrorIs(t, err, model.ErrLedgerInsufficientBalance)
	assert.Equal(t, 500_000, getUserBalances(t, 8322).AffQuota)
	assert.Zero(t, getUserBalances(t, 8322).QuotaReward)
}

// TestReferralPostgreSQLCleanupGuardsLocksAndRepeat 在真实 PostgreSQL 上验证清理
// 守卫（软删用户、旧资金 reason）、表锁等待、失败回滚与重复执行。
func TestReferralPostgreSQLCleanupGuardsLocksAndRepeat(t *testing.T) {
	fixture := openReferralPostgreSQLFixture(t)
	db := fixture.db

	createPG := func(statement string) {
		t.Helper()
		require.NoError(t, db.Exec(statement).Error)
	}
	createPG("CREATE TABLE affiliate_rewards (id bigint primary key, status text)")
	createPG("CREATE TABLE affiliate_reward_reconcile_states (id bigint primary key, status text)")
	createPG("CREATE TABLE referral_coin_ledger (id bigint primary key)")
	createPG("CREATE TABLE referral_coin_rewards (id bigint primary key)")
	require.NoError(t, db.Exec("ALTER TABLE users ADD COLUMN quota_affiliate bigint DEFAULT 0").Error)
	require.NoError(t, db.Exec("INSERT INTO affiliate_reward_reconcile_states (id, status) VALUES (1, 'pending'), (2, 'pending')").Error)
	require.NoError(t, db.Create(&model.Option{Key: "AffiliateRewardFreezeDays", Value: "7"}).Error)
	require.NoError(t, db.Create(&model.Option{Key: "AffiliateRewardEnabled", Value: "true"}).Error)
	require.NoError(t, db.Create(&model.Option{Key: "AffiliateRewardRate", Value: "0.05"}).Error)

	// 锁等待：另一个连接持有 users 的 ACCESS SHARE 锁时，清理必须被有界超时
	// 拒绝并保持结构完整，而不是无限阻塞或删除。
	holder := db.Begin()
	require.NoError(t, holder.Exec("SELECT 1 FROM users LIMIT 1").Error)
	_, err := model.CleanupLegacyAffiliateData(db)
	require.Error(t, err, "持有冲突锁时清理必须失败而非无限等待")
	assert.True(t, db.Migrator().HasTable("affiliate_rewards"), "锁等待失败不得删表")
	require.NoError(t, holder.Rollback().Error)

	report, err := model.CleanupLegacyAffiliateData(db)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"affiliate_rewards", "affiliate_reward_reconcile_states",
		"referral_coin_ledger", "referral_coin_rewards",
	}, report.DroppedTables)
	assert.Contains(t, report.DroppedColumns, "users.quota_affiliate")
	assert.EqualValues(t, 2, report.AffiliateRewardReconcileRows)
	assert.EqualValues(t, 1, report.RemovedFreezeOptions)

	var freezeRows int64
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "AffiliateRewardFreezeDays").Count(&freezeRows).Error)
	assert.Zero(t, freezeRows, "旧冻结期 option 必须删除")
	for _, key := range []string{"AffiliateRewardEnabled", "AffiliateRewardRate"} {
		var count int64
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", key).Count(&count).Error)
		assert.EqualValues(t, 1, count, "%s 必须保留", key)
	}
	assert.False(t, db.Migrator().HasTable("affiliate_rewards"))
	assert.False(t, db.Migrator().HasColumn(&model.User{}, "quota_affiliate"))
	assert.True(t, db.Migrator().HasColumn(&model.User{}, "aff_quota"), "原生 aff_quota 必须保留")
	assert.True(t, db.Migrator().HasColumn(&model.User{}, "aff_history"), "原生 aff_history 必须保留")

	repeated, err := model.CleanupLegacyAffiliateData(db)
	require.NoError(t, err, "重复清理必须安全")
	assert.Empty(t, repeated.DroppedTables)
	assert.Empty(t, repeated.DroppedColumns)
}

// TestReferralPostgreSQLCleanupRefusesLegacyFunds 在真实 PostgreSQL 上验证旧资金
// 与软删用户余额守卫，失败时结构与资金原样保留。
func TestReferralPostgreSQLCleanupRefusesLegacyFunds(t *testing.T) {
	fixture := openReferralPostgreSQLFixture(t)
	db := fixture.db

	require.NoError(t, db.Exec("CREATE TABLE affiliate_rewards (id bigint primary key, status text)").Error)
	require.NoError(t, db.Exec("ALTER TABLE users ADD COLUMN quota_affiliate bigint DEFAULT 0").Error)
	require.NoError(t, db.Create(&model.Option{Key: "AffiliateRewardFreezeDays", Value: "7"}).Error)

	deleted := &model.User{
		Id: 8331, Username: "pg-legacy-deleted", AffCode: "pglegacydel",
		Status: common.UserStatusEnabled,
	}
	deleted.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
	require.NoError(t, db.Create(deleted).Error)
	require.NoError(t, db.Exec("UPDATE users SET quota_affiliate = 1000 WHERE id = ?", 8331).Error)
	require.NoError(t, db.Exec("INSERT INTO affiliate_rewards (id, status) VALUES (1, 'pending')").Error)

	report, err := model.CleanupLegacyAffiliateData(db)
	require.ErrorIs(t, err, model.ErrLegacyAffiliateCleanupBlocked)
	assert.EqualValues(t, 1, report.AffiliateRewardRows)
	assert.EqualValues(t, 1, report.QuotaAffiliateUsers)
	assert.True(t, db.Migrator().HasTable("affiliate_rewards"), "阻断时不得删表")
	assert.True(t, db.Migrator().HasColumn(&model.User{}, "quota_affiliate"), "阻断时不得删列")
	var freezeRows int64
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "AffiliateRewardFreezeDays").Count(&freezeRows).Error)
	assert.EqualValues(t, 1, freezeRows, "阻断时不得删除 option")

	// 旧资金 reason 也必须阻断；新的 referral_transfer 不属于旧资金。
	require.NoError(t, db.Exec("DELETE FROM affiliate_rewards").Error)
	require.NoError(t, db.Exec("UPDATE users SET quota_affiliate = 0 WHERE id = ?", 8331).Error)
	operation := &model.BalanceLedgerOperation{
		IdempotencyKey: "pg-legacy-aff-transfer", UserId: 8331, Operation: "credit",
		OperationSignature: "legacy", TotalBefore: 0, TotalAfter: 1000, CreatedAt: common.GetTimestamp(),
	}
	require.NoError(t, db.Create(operation).Error)
	require.NoError(t, db.Create(&model.BalanceLedger{
		OperationId: operation.Id, UserId: 8331, Account: "bonus", Delta: 1000,
		BalanceBefore: 0, BalanceAfter: 1000, TotalBefore: 0, TotalAfter: 1000,
		Reason: "affiliate_transfer", ReferenceType: "legacy", ReferenceId: "PG-OLD",
		IdempotencyKey: "pg-legacy-aff-transfer", CreatedAt: common.GetTimestamp(),
	}).Error)

	report, err = model.CleanupLegacyAffiliateData(db)
	require.ErrorIs(t, err, model.ErrLegacyAffiliateCleanupBlocked)
	assert.EqualValues(t, 1, report.LegacyFundLedgerRows)
}

// TestReferralPostgreSQLCleanupRefusesExternalDependents 在真实 PostgreSQL 上验证
// 旧表存在外部外键依赖时清理必须以 RESTRICT 失败并整体回滚，不得沿用驱动的
// CASCADE 级联删除；去掉依赖后同一清理幂等成功。
func TestReferralPostgreSQLCleanupRefusesExternalDependents(t *testing.T) {
	fixture := openReferralPostgreSQLFixture(t)
	db := fixture.db

	require.NoError(t, db.Exec("CREATE TABLE affiliate_rewards (id bigint primary key, status text)").Error)
	require.NoError(t, db.Exec("CREATE TABLE affiliate_reward_reconcile_states (id bigint primary key, status text)").Error)
	require.NoError(t, db.Exec("CREATE TABLE referral_coin_ledger (id bigint primary key)").Error)
	require.NoError(t, db.Exec("CREATE TABLE referral_coin_rewards (id bigint primary key)").Error)
	require.NoError(t, db.Exec("ALTER TABLE users ADD COLUMN quota_affiliate bigint DEFAULT 0").Error)
	require.NoError(t, db.Create(&model.Option{Key: "AffiliateRewardFreezeDays", Value: "7"}).Error)
	require.NoError(t, db.Exec(
		"CREATE TABLE pg_external_affiliate_ref (id bigint primary key, reward_id bigint REFERENCES affiliate_rewards(id))",
	).Error)

	_, err := model.CleanupLegacyAffiliateData(db)
	require.Error(t, err, "外部外键依赖必须让清理失败，而不是级联删除依赖对象")
	require.NotErrorIs(t, err, model.ErrLegacyAffiliateCleanupBlocked, "失败必须来自 DROP RESTRICT 而非守卫")

	for _, table := range []string{
		"affiliate_rewards", "affiliate_reward_reconcile_states",
		"referral_coin_ledger", "referral_coin_rewards",
	} {
		assert.True(t, db.Migrator().HasTable(table), "失败回滚后旧表 %s 必须仍在", table)
	}
	assert.True(t, db.Migrator().HasColumn(&model.User{}, "quota_affiliate"), "失败回滚后旧列必须仍在")
	var freezeRows int64
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "AffiliateRewardFreezeDays").Count(&freezeRows).Error)
	assert.EqualValues(t, 1, freezeRows, "失败回滚后旧 option 必须仍在")
	assert.True(t, db.Migrator().HasTable("pg_external_affiliate_ref"), "外部表不得被级联删除")

	var fkRows int64
	require.NoError(t, db.Raw(
		`SELECT count(*) FROM pg_catalog.pg_constraint c
		 JOIN pg_catalog.pg_class con ON con.oid = c.conrelid
		 JOIN pg_catalog.pg_namespace n ON n.oid = con.relnamespace
		 JOIN pg_catalog.pg_class ref ON ref.oid = c.confrelid AND ref.relnamespace = n.oid
		 WHERE c.contype = 'f' AND n.nspname = current_schema()
		   AND con.relname = 'pg_external_affiliate_ref' AND ref.relname = 'affiliate_rewards'`,
	).Scan(&fkRows).Error)
	assert.EqualValues(t, 1, fkRows, "外部外键必须仍在")

	// 去掉外部依赖后，同一清理成功且幂等。
	require.NoError(t, db.Exec("DROP TABLE pg_external_affiliate_ref").Error)
	report, err := model.CleanupLegacyAffiliateData(db)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"affiliate_rewards", "affiliate_reward_reconcile_states",
		"referral_coin_ledger", "referral_coin_rewards",
	}, report.DroppedTables)
	assert.Contains(t, report.DroppedColumns, "users.quota_affiliate")
	assert.False(t, db.Migrator().HasTable("affiliate_rewards"))
	assert.False(t, db.Migrator().HasColumn(&model.User{}, "quota_affiliate"))
}

// TestReferralPostgreSQLWalletFundingRewardAndBonusIsolation 在真实 PostgreSQL 上
// 通过真实钱包路径验证：非赠金模型使用 reward、失败退款回 reward、普通 bonus
// 不被洗成 paid。
func TestReferralPostgreSQLWalletFundingRewardAndBonusIsolation(t *testing.T) {
	openReferralPostgreSQLFixture(t)

	// 非 bonus_eligible 模型：reward 优先，退款精确回 reward。
	seedBonusEligibleModel(t, "pg-reward-model", boolPtr(false))
	seedRewardUser(t, 8341, 9_999, 1_000, 0)
	rewardFunding := &WalletFunding{
		userId:     8341,
		requestId:  "pg-reward-1",
		debitOrder: WalletDebitOrder(walletInfoFor(t, 8341, "pg-reward-model")),
	}
	require.NoError(t, rewardFunding.PreConsume(400))
	assertUserBalances(t, 8341, 9_999, 0, 600)
	require.NoError(t, rewardFunding.Refund())
	assertUserBalances(t, 8341, 9_999, 0, 1_000)

	// bonus_eligible 模型：bonus 优先，退款精确回 bonus，不得变成 paid。
	seedBonusEligibleModel(t, "pg-bonus-model", boolPtr(true))
	seedBonusUser(t, 8342, 500, 500)
	bonusFunding := &WalletFunding{
		userId:     8342,
		requestId:  "pg-bonus-1",
		debitOrder: WalletDebitOrder(walletInfoFor(t, 8342, "pg-bonus-model")),
	}
	require.NoError(t, bonusFunding.PreConsume(700))
	assertUserBalances(t, 8342, 0, 300, 0)
	require.NoError(t, bonusFunding.Refund())
	assertUserBalances(t, 8342, 500, 500, 0)
}
