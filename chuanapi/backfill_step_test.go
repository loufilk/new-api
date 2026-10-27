package chuanapi

// backfill_step_test.go 覆盖 M4 的真实风险点：注册表驱动的 quota 回填必须作用于
// 注入的 *gorm.DB，而不是包级 model.DB；且启动可能重复执行，必须幂等。

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newWalletDB opens an isolated SQLite database and migrates the users table
// plus the registered wallet models, in registration order — the same sequence
// the host performs. model.DB is deliberately left pointing elsewhere so a step
// that reaches for the package-level handle fails these tests.
func newWalletDB(t *testing.T) *gorm.DB {
	t.Helper()

	// SQLite must be the declared main type: lockForUpdate only skips the
	// FOR UPDATE clause under SQLite, which SQLite would reject as a syntax
	// error. Every global touched here is restored on cleanup.
	previousMain := common.MainDatabaseType()
	previousLog := common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	previousBatch := common.BatchUpdateEnabled
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled = previousRedis
		common.BatchUpdateEnabled = previousBatch
	})

	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/wallet.db"), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		if sqlDB, closeErr := db.DB(); closeErr == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, db.AutoMigrate(append([]any{&model.User{}}, WalletModels()...)...))
	return db
}

// legacyUser builds a pre-wallet user row: users.quota carries the whole
// balance and the three sub-accounts are still zero. username and aff_code are
// unique columns, so both are derived from id.
func legacyUser(id int, quota int) *model.User {
	return &model.User{
		Id:       id,
		Username: fmt.Sprintf("legacy-user-%d", id),
		AffCode:  fmt.Sprintf("afflegacy%d", id),
		Quota:    quota,
		Status:   common.UserStatusEnabled,
	}
}

func readUser(t *testing.T, db *gorm.DB, id int) model.User {
	t.Helper()
	var user model.User
	require.NoError(t, db.Where("id = ?", id).First(&user).Error)
	return user
}

// The step must migrate the database the registry hands it. model.DB is pinned
// to a separate empty database that has no users table at all: a step reading
// the package-level handle would error instead of backfilling.
func TestBackfillStepUsesTheInjectedDatabase(t *testing.T) {
	target := newWalletDB(t)

	decoy, err := gorm.Open(sqlite.Open(t.TempDir()+"/decoy.db"), &gorm.Config{})
	require.NoError(t, err)
	previousDB := model.DB
	model.DB = decoy
	t.Cleanup(func() {
		model.DB = previousDB
		if sqlDB, closeErr := decoy.DB(); closeErr == nil {
			_ = sqlDB.Close()
		}
	})

	const legacyQuota = 5000
	require.NoError(t, target.Create(legacyUser(1, legacyQuota)).Error)

	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	require.NoError(t, reg.Freeze())
	require.NoError(t, reg.RunStepsFor(target, extension.PhaseAfterCoreData))

	migrated := readUser(t, target, 1)
	assert.Equal(t, legacyQuota, migrated.QuotaPaid, "legacy quota must land in the paid account")
	assert.Equal(t, 0, migrated.QuotaBonus)
	assert.Equal(t, 0, migrated.QuotaReward)
	assert.Equal(t, migrated.QuotaPaid+migrated.QuotaBonus+migrated.QuotaReward, migrated.Quota,
		"quota mirror must equal paid+bonus+reward")

	var ledgerRows int64
	require.NoError(t, target.Model(&model.BalanceLedger{}).Count(&ledgerRows).Error)
	assert.EqualValues(t, 1, ledgerRows, "backfill must leave exactly one ledger row")
}

// Every restart re-runs the phase, so a second execution must not double-credit
// the paid account or append a second ledger row.
func TestBackfillStepIsIdempotentAcrossRepeatedRuns(t *testing.T) {
	db := newWalletDB(t)

	const legacyQuota = 3_000_000_000 // > math.MaxInt32, must not truncate
	require.NoError(t, db.Create(legacyUser(2, legacyQuota)).Error)
	// A user whose balance is already split must be left untouched.
	split := legacyUser(3, 1200)
	split.QuotaPaid = 1200
	require.NoError(t, db.Create(split).Error)

	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	require.NoError(t, reg.Freeze())

	for run := 1; run <= 2; run++ {
		require.NoError(t, reg.RunStepsFor(db, extension.PhaseAfterCoreData), "run %d", run)

		migrated := readUser(t, db, 2)
		assert.Equal(t, legacyQuota, migrated.QuotaPaid, "run %d must not double-credit or truncate", run)
		assert.Equal(t, legacyQuota, migrated.Quota, "run %d must preserve the quota mirror", run)

		untouched := readUser(t, db, 3)
		assert.Equal(t, 1200, untouched.QuotaPaid, "run %d must not re-split an already split balance", run)

		var ledgerRows int64
		require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&ledgerRows).Error)
		assert.EqualValues(t, 1, ledgerRows, "run %d must not append a duplicate ledger row", run)

		var operations int64
		require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&operations).Error)
		assert.EqualValues(t, 1, operations, "run %d must not append a duplicate operation header", run)
	}
}

// A nil handle must fail closed rather than fall back to the package-level DB
// and migrate a database the caller never asked for.
func TestBackfillWithNilDatabaseFailsClosed(t *testing.T) {
	affected, err := model.BackfillLegacyQuotaAccountsWithDB(nil)
	require.Error(t, err)
	assert.Zero(t, affected)
}
