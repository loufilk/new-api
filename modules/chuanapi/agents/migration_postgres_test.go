package agents

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func agentMigrationFixture(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_AGENT_MIGRATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL migration fixture")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.NotNil(t, net.ParseIP(parsed.Hostname()))
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback(), "fixture must never select production")
	admin, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	var version string
	require.NoError(t, admin.Raw("SHOW server_version").Scan(&version).Error)
	require.Contains(t, version, "18.6")
	schema := fmt.Sprintf("agent_migration_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	config.RuntimeParams["search_path"] = schema
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	sqlDB := stdlib.OpenDB(*config)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldDB := model.DB
	// Registration must exclusively use the injected handle, never model.DB.
	model.DB = nil
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB = oldDB
		common.SetDatabaseTypes(oldMain, oldLog)
		require.NoError(t, sqlDB.Close())
		require.NoError(t, admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error)
		pool, e := admin.DB()
		require.NoError(t, e)
		require.NoError(t, pool.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.UserAccessToken{}, &model.AgentRedemptionAccount{}, &model.AgentRedemptionLedger{}, &model.AgentRedemptionOperation{}, &model.Redemption{}, &model.TopUp{}, &model.ReferralCommission{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}))
	return db
}

func TestAgentPlatformRegisteredMigrationFreshPostgres(t *testing.T) {
	db := agentMigrationFixture(t)
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	require.NoError(t, reg.Freeze())
	for range 2 {
		require.NoError(t, db.AutoMigrate(reg.ModelsFor(extension.PhaseAfterCoreModels)...))
		require.NoError(t, reg.RunStepsFor(db, extension.PhaseAfterCoreData))
	}
	for _, table := range Models() {
		assert.True(t, db.Migrator().HasTable(table))
	}
	var counts int64
	require.NoError(t, db.Model(&model.AgentPolicy{}).Count(&counts).Error)
	assert.Zero(t, counts, "migration must not approve or publish any commercial terms")
	require.NoError(t, db.Model(&model.AgentCommissionOutbox{}).Count(&counts).Error)
	assert.Zero(t, counts, "migration must never manufacture payment/reward events")
}

func TestAgentPlatformRegisteredMigrationPreservesLegacyLiabilitiesPostgres(t *testing.T) {
	db := agentMigrationFixture(t)
	user := model.User{Id: 4101, Username: "migration-agent", AffCode: "migration-agent", Role: common.RoleAgentUser, Status: common.UserStatusEnabled, AuthVersion: 7, Quota: 600, QuotaPaid: 100, QuotaBonus: 200, QuotaReward: 300, AffQuota: 80}
	require.NoError(t, db.Create(&user).Error)
	active := model.Token{Id: 4101, UserId: user.Id, Key: "synthetic-active-migration-key", Status: common.TokenStatusEnabled}
	disabled := model.Token{Id: 4102, UserId: user.Id, Key: "synthetic-disabled-migration-key", Status: common.TokenStatusDisabled}
	require.NoError(t, db.Create(&active).Error)
	require.NoError(t, db.Create(&disabled).Error)
	pat := model.UserAccessToken{UserId: user.Id, TokenHash: model.AccessTokenFingerprint("synthetic-migration-pat"), Name: "migration"}
	require.NoError(t, db.Create(&pat).Error)
	account := model.AgentRedemptionAccount{AgentUserID: user.Id, AllocatedQuota: 1000, AvailableQuota: 600, ReservedQuota: 300, SpentQuota: 100}
	orphan := model.AgentRedemptionAccount{AgentUserID: 9999, AllocatedQuota: -7, AvailableQuota: -7}
	require.NoError(t, db.Create(&account).Error)
	require.NoError(t, db.Create(&orphan).Error)
	code := model.Redemption{Id: 4101, UserId: user.Id, BudgetAgentID: user.Id, BudgetReserved: true, Quota: 300, Key: "11111111111111111111111111111111", Status: common.RedemptionCodeStatusEnabled}
	historical := model.Redemption{Id: 4102, UserId: user.Id, BudgetAgentID: 0, Quota: 999, Key: "22222222222222222222222222222222", Status: common.RedemptionCodeStatusEnabled}
	require.NoError(t, db.Create(&code).Error)
	require.NoError(t, db.Create(&historical).Error)
	pending := model.TopUp{UserId: user.Id, TradeNo: "migration-pending-order", Amount: 3, Money: 3, Status: common.TopUpStatusPending, PaymentProvider: model.PaymentProviderEpay}
	require.NoError(t, db.Create(&pending).Error)
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	require.NoError(t, reg.Freeze())
	for range 2 {
		require.NoError(t, db.AutoMigrate(reg.ModelsFor(extension.PhaseAfterCoreModels)...))
		require.NoError(t, reg.RunStepsFor(db, extension.PhaseAfterCoreData))
	}
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, user.AuthVersion, stored.AuthVersion)
	assert.Equal(t, []int{600, 100, 200, 300, 80}, []int{stored.Quota, stored.QuotaPaid, stored.QuotaBonus, stored.QuotaReward, stored.AffQuota})
	require.NoError(t, db.First(&active, active.Id).Error)
	require.NoError(t, db.First(&disabled, disabled.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, active.Status)
	assert.Equal(t, common.TokenStatusDisabled, disabled.Status)
	var tokens int64
	require.NoError(t, db.Model(&model.UserAccessToken{}).Count(&tokens).Error)
	assert.EqualValues(t, 1, tokens)
	var accounts []model.AgentRedemptionAccount
	require.NoError(t, db.Order("agent_user_id").Find(&accounts).Error)
	require.Len(t, accounts, 2)
	assert.Equal(t, []int{600, 300, 100, 1000}, []int{accounts[0].AvailableQuota, accounts[0].ReservedQuota, accounts[0].SpentQuota, accounts[0].AllocatedQuota})
	assert.Equal(t, -7, accounts[1].AvailableQuota)
	var batches []model.AgentFundBatch
	require.NoError(t, db.Find(&batches).Error)
	require.Len(t, batches, 1)
	assert.Equal(t, model.AgentFundLegacy, batches[0].SourceKind)
	assert.False(t, batches[0].CashRefundable)
	assert.Equal(t, "0", batches[0].OriginalPaidMinor)
	var slices []model.AgentFundSlice
	require.NoError(t, db.Order("start_quota").Find(&slices).Error)
	require.Len(t, slices, 3)
	assert.Equal(t, []int{600, 300, 100}, []int{slices[0].Quota, slices[1].Quota, slices[2].Quota})
	assert.Equal(t, code.Id, slices[1].RedemptionID)
	require.NoError(t, db.First(&historical, historical.Id).Error)
	assert.Zero(t, historical.BudgetAgentID)
	var issues int64
	require.NoError(t, db.Model(&model.AgentMigrationIssue{}).Count(&issues).Error)
	assert.EqualValues(t, 2, issues)
	require.NoError(t, db.Model(&model.AgentFundAnomaly{}).Count(&issues).Error)
	assert.EqualValues(t, 1, issues)
	require.NoError(t, db.First(&pending, pending.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, pending.Status)
	require.NoError(t, db.Model(&model.AgentCommissionOutbox{}).Count(&issues).Error)
	assert.Zero(t, issues)
	require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&issues).Error)
	assert.Zero(t, issues, "provenance migration does not add any money")
}
