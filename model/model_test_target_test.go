package model

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	sqlmysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Select a production engine before creating subtests, so unselected engines
// do not create skipped results in a required verification report.
func modelFingerprintTestDialects(t *testing.T) []common.DatabaseType {
	t.Helper()
	dialects := []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL}
	if selected := common.DatabaseType(os.Getenv("TEST_FINGERPRINT_DIALECT")); selected != "" {
		require.Contains(t, dialects, selected, "unsupported TEST_FINGERPRINT_DIALECT")
		return []common.DatabaseType{selected}
	}
	return dialects
}

func setupModelFingerprintDatabase(t *testing.T, dialect common.DatabaseType) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case common.DatabaseTypeSQLite:
		driver = sqlite.Open(filepath.Join(t.TempDir(), "fingerprint.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	case common.DatabaseTypeMySQL:
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			if os.Getenv("TEST_FINGERPRINT_DIALECT") != "" {
				t.Fatal("selected MySQL verification requires TEST_MYSQL_DSN")
			}
			t.Skip("TEST_MYSQL_DSN is not configured")
		}
		config, err := sqlmysql.ParseDSN(dsn)
		require.NoError(t, err)
		host, _, err := net.SplitHostPort(config.Addr)
		require.NoError(t, err)
		require.True(t, net.ParseIP(host).IsLoopback(), "only disposable loopback databases are permitted")
		driver = mysql.Open(dsn)
	case common.DatabaseTypePostgreSQL:
		dsn := os.Getenv("TEST_POSTGRES_DSN")
		if dsn == "" {
			if os.Getenv("TEST_FINGERPRINT_DIALECT") != "" {
				t.Fatal("selected PostgreSQL verification requires TEST_POSTGRES_DSN")
			}
			t.Skip("TEST_POSTGRES_DSN is not configured")
		}
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback(), "only disposable loopback databases are permitted")
		// Explicit GORM index names are schema-scoped on PostgreSQL. Isolate
		// the schema too, so other native fixtures cannot share those indexes.
		admin, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
		require.NoError(t, err)
		adminConnection, err := admin.DB()
		require.NoError(t, err)
		fixtureSchema := fmt.Sprintf("fpt_%x", time.Now().UnixNano())
		require.NoError(t, admin.Exec("CREATE SCHEMA "+fixtureSchema).Error)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+fixtureSchema+" CASCADE").Error)
			require.NoError(t, adminConnection.Close())
		})
		query := parsed.Query()
		query.Set("search_path", fixtureSchema)
		parsed.RawQuery = query.Encode()
		driver = postgres.New(postgres.Config{DSN: parsed.String(), PreferSimpleProtocol: true})
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("fpt_%x_", time.Now().UnixNano())}})
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(4)
	if dialect == common.DatabaseTypeSQLite {
		connection.SetMaxOpenConns(1)
	}
	previousDB, previousLogDB := DB, LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousMemory, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	DB, LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.MemoryCacheEnabled, common.RedisEnabled = false, false
	initCol()
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.MemoryCacheEnabled, common.RedisEnabled = previousMemory, previousRedis
		initCol()
		require.NoError(t, connection.Close())
	})
	captureGroupRenameRuntimeState(t)
	models := []any{&Option{}, &ModelTestGate{}, &ModelFingerprintSiteConfig{}, &ModelFingerprintSiteAudit{},
		&ModelTestRun{}, &ModelFingerprintSiteRun{}, &ModelFingerprintSiteProbe{}, &GroupRenameOperation{},
		&User{}, &Token{}, &Channel{}, &Ability{}, &SubscriptionPlan{}, &UserSubscription{}}
	for _, entity := range models {
		require.False(t, db.Migrator().HasTable(entity), "fixture must never reuse existing tables")
	}
	for range 2 {
		require.NoError(t, db.AutoMigrate(models...))
	}
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
	query := "SELECT version()"
	if dialect == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("%s %s: fresh schema and repeated migration", dialect, version)
	return db
}

func TestModelTestTargetAuthorizationDatabaseMatrix(t *testing.T) {
	for _, dialect := range modelFingerprintTestDialects(t) {
		t.Run(string(dialect), func(t *testing.T) {
			db := setupModelFingerprintDatabase(t, dialect)
			ctx := t.Context()
			require.ErrorIs(t, RequireModelTestTarget(ctx, "default", "allowed"), ErrModelTestsDisabled)
			var count int64
			require.NoError(t, db.Model(&ModelFingerprintSiteConfig{}).Count(&count).Error)
			assert.Zero(t, count, "a read must not create settings")
			require.NoError(t, db.Model(&Option{}).Where(Option{Key: ModelTestEnabledKey}).Count(&count).Error)
			assert.Zero(t, count, "a read must not enable the feature")
			owner := User{Username: "target-owner", AffCode: "target-owner", Group: "default", Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&owner).Error)
			config, err := UpdateModelFingerprintSiteConfig(ctx, 0, owner.Id, ModelFingerprintSiteConfig{
				Enabled: true, DefaultPeriodSecs: 3600, Targets: []ModelFingerprintSiteTarget{
					{Group: "default", Model: "allowed", Enabled: true},
					{Group: "default", Model: "disabled"},
					{Group: "premium", Model: "premium-only", Enabled: true},
				},
			})
			require.NoError(t, err)
			require.NoError(t, RequireModelTestTarget(ctx, "default", "allowed"))
			require.NoError(t, RequireModelTestTarget(ctx, "premium", "premium-only"))
			for _, selection := range [][2]string{{"default", "disabled"}, {"default", "unconfigured"}, {"premium", "allowed"}, {"default", "premium-only"}, {"", "allowed"}, {"default", ""}, {"default", "Allowed"}} {
				assert.ErrorIs(t, RequireModelTestTarget(ctx, selection[0], selection[1]), ErrModelTestTargetUnavailable, selection)
			}
			// Revocation after a successful selection/quote must still prevent the
			// durable run from being created under the admission gate.
			config.Targets[0].Enabled = false
			config, err = UpdateModelFingerprintSiteConfig(ctx, config.Version, owner.Id, *config)
			require.NoError(t, err)
			run := &ModelTestRun{ID: uuid.NewString(), UserID: owner.Id, Model: "allowed", Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix()}
			_, err = CreateModelTestRun(run)
			require.ErrorIs(t, err, ErrModelTestTargetUnavailable)
			require.NoError(t, db.Model(&ModelTestRun{}).Count(&count).Error)
			assert.Zero(t, count)
			config.Targets[0].Enabled = true
			config, err = UpdateModelFingerprintSiteConfig(ctx, config.Version, owner.Id, *config)
			require.NoError(t, err)
			replayed, err := CreateModelTestRun(run)
			require.NoError(t, err)
			assert.False(t, replayed)
			config.Targets = config.Targets[1:]
			config, err = UpdateModelFingerprintSiteConfig(ctx, config.Version, owner.Id, *config)
			require.NoError(t, err)
			assert.ErrorIs(t, RequireModelTestTarget(ctx, "default", "allowed"), ErrModelTestTargetUnavailable)
			replayed, err = CreateModelTestRun(run)
			require.NoError(t, err)
			assert.True(t, replayed, "revocation does not prevent reconciliation of existing work")
			// Neither side of this settings change permits premium-only: before
			// the save its target is off, afterwards the entire feature is off.
			// Commit the save immediately after the authority read to prove the
			// switch and targets cannot be combined from different snapshots.
			config.Targets[1].Enabled = false
			config, err = UpdateModelFingerprintSiteConfig(ctx, config.Version, owner.Id, *config)
			require.NoError(t, err)
			var changed atomic.Bool
			callback := "test:model-test-target-settings-snapshot"
			require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Error != nil || tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Option" || !changed.CompareAndSwap(false, true) {
					return
				}
				config.Enabled = false
				config.Targets[1].Enabled = true
				config, err = UpdateModelFingerprintSiteConfig(ctx, config.Version, owner.Id, *config)
				require.NoError(t, err)
			}))
			assert.ErrorIs(t, RequireModelTestTarget(ctx, "premium", "premium-only"), ErrModelTestTargetUnavailable)
			require.True(t, changed.Load(), "the settings change must commit at the read boundary")
			require.NoError(t, db.Callback().Query().Remove(callback))
			assert.ErrorIs(t, RequireModelTestTarget(ctx, "premium", "premium-only"), ErrModelTestsDisabled)
			config.Enabled = true
			_, err = UpdateModelFingerprintSiteConfig(ctx, config.Version, owner.Id, *config)
			require.NoError(t, err)
			require.NoError(t, db.Model(&ModelFingerprintSiteConfig{}).Where("id = ?", 1).Update("targets_json", "invalid-json").Error)
			assert.ErrorIs(t, RequireModelTestTarget(ctx, "premium", "premium-only"), ErrModelTestSettingsUnavailable)
			require.NoError(t, db.Delete(&ModelFingerprintSiteConfig{}, 1).Error)
			assert.ErrorIs(t, RequireModelTestTarget(ctx, "premium", "premium-only"), ErrModelTestTargetUnavailable)
			require.NoError(t, db.Migrator().DropTable(&ModelFingerprintSiteConfig{}))
			assert.ErrorIs(t, RequireModelTestTarget(ctx, "premium", "premium-only"), ErrModelTestSettingsUnavailable)
		})
	}
}
