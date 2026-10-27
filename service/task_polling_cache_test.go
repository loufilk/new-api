package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTaskPollingDefersStaleChannelCache(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			if dialect != "sqlite" {
				openTaskPollingSQLFixture(t, dialect)
			}
			oldMemory, oldRedis, oldBatch := common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled
			common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled = false, false, false
			t.Cleanup(func() {
				common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled = oldMemory, oldRedis, oldBatch
			})
			for _, batch := range []bool{false, true} {
				for _, funding := range []string{BillingSourceWallet, BillingSourceSubscription} {
					t.Run(fmt.Sprintf("batch=%t/%s", batch, funding), func(t *testing.T) {
						const userID, tokenID, channelID, subscriptionID = 907, 907, 907, 907
						const balance, tokenRemain, reserved = 10_000, 8_000, 1_400
						db := model.DB
						t.Cleanup(func() {
							for _, row := range []any{&model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.Task{}, &model.Log{}, &model.SubscriptionPreConsumeRecord{}, &model.UserSubscription{}, &model.Channel{}, &model.Token{}, &model.User{}} {
								require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(row).Error)
							}
						})
						seedUser(t, userID, balance)
						seedToken(t, tokenID, userID, "sk-polling-cache-fixture", tokenRemain)
						seedTaskPollingChannel(t, channelID, true)
						seedChargedAccounting(t, userID, channelID, tokenID, reserved, 1)
						seedSubscription(t, subscriptionID, userID, 20_000, reserved)
						task := makeTask(userID, channelID, reserved, tokenID, funding, subscriptionID)
						task.TaskID = "task_cache_retry"
						task.Platform = constant.TaskPlatform("kling")
						task.Progress = "30%"
						task.PrivateData.UpstreamTaskID = "upstream_cache_retry"
						task.PrivateData.PollFailures = 2
						if funding == BillingSourceSubscription {
							seedTaskSubscriptionReceipt(t, task)
						}
						require.NoError(t, db.Create(task).Error)
						upstreamID := task.GetUpstreamTaskID()
						adaptor := &batchPollingAdaptor{}
						oldFactory := GetTaskAdaptorFunc
						GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
						t.Cleanup(func() { GetTaskAdaptorFunc = oldFactory })
						poll := func() error {
							if batch {
								return updateBatchTasks(context.Background(), adaptor, channelID, []string{upstreamID}, map[string]*model.Task{upstreamID: task})
							}
							return updateVideoTasks(context.Background(), task.Platform, channelID, []string{upstreamID}, map[string]*model.Task{upstreamID: task})
						}
						// Model expiry is tested separately with the actual cache clock.
						// Inject its wrapped error at the lookup boundary while all money
						// and task tables remain writable, as during a refresh failure.
						const hook = "test:stale-channel-cache"
						require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
							if tx.Statement.Table == "channels" {
								tx.AddError(fmt.Errorf("channel lookup: %w", model.ErrChannelCacheStale))
							}
						}))
						t.Cleanup(func() { _ = db.Callback().Query().Remove(hook) })
						for range 2 {
							require.ErrorIs(t, poll(), model.ErrChannelCacheStale)
						}
						var persisted model.Task
						require.NoError(t, db.First(&persisted, task.ID).Error)
						assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), persisted.Status)
						assert.Equal(t, reserved, persisted.Quota)
						assert.Equal(t, "30%", persisted.Progress)
						assert.Empty(t, persisted.FailReason)
						assert.Zero(t, persisted.FinishTime)
						assert.Equal(t, 2, persisted.PrivateData.PollFailures, "cache failures must not consume the upstream failure budget")
						var user model.User
						require.NoError(t, db.First(&user, userID).Error)
						assert.Equal(t, balance, user.QuotaPaid)
						assert.Zero(t, user.QuotaBonus)
						assert.Zero(t, user.QuotaReward)
						assert.Equal(t, reserved, user.UsedQuota)
						assert.Equal(t, 1, user.RequestCount)
						assert.Equal(t, balance, getUserQuota(t, userID))
						assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
						var subscription model.UserSubscription
						require.NoError(t, db.First(&subscription, subscriptionID).Error)
						assert.EqualValues(t, reserved, subscription.AmountUsed)
						var ledgerRows int64
						require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&ledgerRows).Error)
						assert.Zero(t, ledgerRows, "a cache outage must have no financial effect")
						var operationRows, logRows int64
						require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&operationRows).Error)
						require.NoError(t, db.Model(&model.Log{}).Count(&logRows).Error)
						assert.Zero(t, operationRows)
						assert.Zero(t, logRows)
						assert.Zero(t, adaptor.batchCalls)
						assert.Zero(t, adaptor.fetchCount())
						require.NoError(t, db.Callback().Query().Remove(hook))
						require.NoError(t, poll(), "the original task must resume after cache recovery")
						if batch {
							assert.Equal(t, 1, adaptor.batchCalls)
						} else {
							assert.Equal(t, []string{upstreamID}, adaptor.fetchedTaskIDs())
						}
						require.NoError(t, db.First(&persisted, task.ID).Error)
						assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), persisted.Status)
						assert.Equal(t, reserved, persisted.Quota)
						// A deleted local channel leaves delivery unknown, not failed.
						// Its outstanding charge remains visible for operator review.
						privateBeforeMissing := persisted.PrivateData
						replay := *task
						require.NoError(t, db.Delete(&model.Channel{}, channelID).Error)
						for range 2 {
							err := poll()
							require.ErrorIs(t, err, gorm.ErrRecordNotFound)
							assertPollingFailureAccountingUnchanged(t, userID, tokenID, channelID, balance, tokenRemain, reserved, false)
							task = &replay
						}
						require.NoError(t, db.First(&persisted, task.ID).Error)
						assert.Equal(t, model.TaskStatus(model.TaskStatusUnknown), persisted.Status)
						assert.Equal(t, reserved, persisted.Quota)
						assert.Equal(t, "30%", persisted.Progress)
						assert.Equal(t, "Missing channel metadata; upstream delivery requires operator review", persisted.FailReason)
						privateBeforeMissing.FinancialReviewReason = persisted.FailReason
						assert.Equal(t, privateBeforeMissing, persisted.PrivateData)
						assert.Zero(t, persisted.FinishTime)
						require.NoError(t, db.First(&subscription, subscriptionID).Error)
						assert.Equal(t, int64(reserved), subscription.AmountUsed)
						if batch {
							assert.Equal(t, 1, adaptor.batchCalls)
						} else {
							assert.Equal(t, []string{upstreamID}, adaptor.fetchedTaskIDs())
						}
					})
				}
			}
		})
	}
}

func openTaskPollingSQLFixture(t *testing.T, dialect string) {
	t.Helper()
	env := "TEST_POSTGRES_DSN"
	if dialect == "mysql" {
		env = "TEST_MYSQL_DSN"
	}
	dsn := strings.TrimSpace(os.Getenv(env))
	if dsn == "" {
		t.Skip(env + " must point at an isolated SQL test instance")
	}
	var opener gorm.Dialector = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	if dialect == "mysql" {
		opener = mysql.Open(dsn)
	}
	admin, err := gorm.Open(opener, &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminSQL.Close()) })
	var version string
	require.NoError(t, admin.Raw("SELECT version()").Scan(&version).Error)
	t.Logf("%s %s", dialect, version)
	schema := "task_polling_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if dialect == "postgres" {
		require.NoError(t, admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
		t.Cleanup(func() { require.NoError(t, admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error) })
		config, err := pgx.ParseConfig(dsn)
		require.NoError(t, err)
		config.RuntimeParams["search_path"] = schema
		config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		sqlDB := stdlib.OpenDB(*config)
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
		opener = postgres.New(postgres.Config{Conn: sqlDB})
	} else {
		require.Equal(t, "mysql", dialect)
		config, err := mysqldriver.ParseDSN(dsn)
		require.NoError(t, err)
		require.NoError(t, admin.Exec("CREATE DATABASE `"+schema+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error)
		t.Cleanup(func() { require.NoError(t, admin.Exec("DROP DATABASE `"+schema+"`").Error) })
		config.DBName = schema
		config.ParseTime = true
		opener = mysql.Open(config.FormatDSN())
	}
	db, err := gorm.Open(opener, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	if dialect == "mysql" {
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	}
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	if dialect == "mysql" {
		common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
	} else {
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	}
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLogDB; common.SetDatabaseTypes(oldType, oldLogType) })
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Task{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Log{}))
}
