package model

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

func subscriptionPeriodDatabase(t *testing.T, kind common.DatabaseType, dsn string) *gorm.DB {
	t.Helper()
	fixtureName := fmt.Sprintf("speriod_%x", time.Now().UnixNano())
	var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "period.db"))
	if kind == common.DatabaseTypeMySQL {
		driver = mysql.Open(dsn)
	}
	if kind == common.DatabaseTypePostgreSQL {
		// Explicit index names are schema-global on PostgreSQL. A table prefix
		// alone collides with idx_user_sub_active from production startup tests.
		admin, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
		require.NoError(t, err)
		adminSQL, err := admin.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, adminSQL.Close()) })
		var existingIndex []string
		indexQuery := "SELECT indexdef FROM pg_catalog.pg_indexes WHERE schemaname = current_schema() AND indexname = ?"
		require.NoError(t, admin.Raw(indexQuery, "idx_user_sub_active").Scan(&existingIndex).Error)
		t.Cleanup(func() {
			var preservedIndex []string
			require.NoError(t, admin.Raw(indexQuery, "idx_user_sub_active").Scan(&preservedIndex).Error)
			assert.Equal(t, existingIndex, preservedIndex, "the pre-existing schema's index must remain untouched")
		})
		require.NoError(t, admin.Exec("CREATE SCHEMA ?", clause.Table{Name: fixtureName}).Error)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA ? CASCADE", clause.Table{Name: fixtureName}).Error)
		})
		driver = postgres.New(postgres.Config{
			DSN: dsn, PreferSimpleProtocol: true,
			OptionOpenDB: []stdlib.OptionOpenDB{stdlib.OptionBeforeConnect(func(_ context.Context, config *pgx.ConnConfig) error {
				config.RuntimeParams["search_path"] = fixtureName
				return nil
			})},
		})
	}
	naming := schema.NamingStrategy{TablePrefix: fixtureName + "_"}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: naming})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	if kind == common.DatabaseTypeSQLite {
		sqlDB.SetMaxOpenConns(1)
	}
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	if kind == common.DatabaseTypePostgreSQL {
		// Hold two physical connections simultaneously: SET on just the first
		// connection would silently send concurrent work back into public.
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for range 2 {
				conn, err := sqlDB.Conn(ctx)
				require.NoError(t, err)
				defer conn.Close()
				var actualSchema string
				require.NoError(t, conn.QueryRowContext(ctx, "SELECT current_schema()").Scan(&actualSchema))
				require.Equal(t, fixtureName, actualSchema)
			}
		}()
	}
	for _, item := range []any{&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &Option{}} {
		require.False(t, db.Migrator().HasTable(item), "fixture must never reuse an existing table")
	}
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().DropTable(&SubscriptionPreConsumeRecord{}, &UserSubscription{}, &SubscriptionPlan{}, &Option{}))
	})
	require.NoError(t, db.AutoMigrate(&Option{}))
	previousDB, previousKind, previousRedis := DB, common.MainDatabaseType(), common.RedisEnabled
	DB = db
	common.SetMainDatabaseType(kind)
	common.RedisEnabled = false
	t.Cleanup(func() { DB = previousDB; common.SetMainDatabaseType(previousKind); common.RedisEnabled = previousRedis })
	query := "SELECT version()"
	if kind == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("%s version: %s", kind, version)
	return db
}

func subscriptionPeriodSeed(t *testing.T, db *gorm.DB) (*SubscriptionPlan, *UserSubscription) {
	t.Helper()
	now := GetDBTimestamp(db)
	plan := &SubscriptionPlan{Title: "period fixture", TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetCustom, QuotaResetCustomSeconds: 60}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() { InvalidateSubscriptionPlanCache(plan.Id) })
	sub := &UserSubscription{UserId: 73, PlanId: plan.Id, AmountTotal: 1000, StartTime: now - 30, EndTime: now + 3600, Status: "active", LastResetTime: now - 30, NextResetTime: now + 30}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(sub).Error)
	return plan, sub
}
