package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// These cases run against isolated prefixed tables on each real CI database;
// absent local MySQL/PostgreSQL DSNs are explicitly skipped, never called a pass.
func TestSensitiveExemptionGroupRenameDatabaseMatrix(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case common.DatabaseTypeSQLite:
				driver = sqlite.Open(filepath.Join(t.TempDir(), "groups.db"))
			case common.DatabaseTypeMySQL:
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case common.DatabaseTypePostgreSQL:
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("gex_%x_", time.Now().UnixNano())}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(4)
			if dialect == common.DatabaseTypeSQLite {
				sqlDB.SetMaxOpenConns(1)
			}
			previousDB, previousLogDB := DB, LOG_DB
			previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
			previousMemory, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
			DB, LOG_DB = db, db
			common.SetDatabaseTypes(dialect, dialect)
			common.MemoryCacheEnabled, common.RedisEnabled = false, false
			initCol()
			t.Cleanup(func() {
				DB, LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMainType, previousLogType)
				common.MemoryCacheEnabled, common.RedisEnabled = previousMemory, previousRedis
				initCol()
				require.NoError(t, sqlDB.Close())
			})
			models := []any{&Option{}, &Channel{}, &Ability{}, &User{}, &Token{}, &SubscriptionPlan{}, &UserSubscription{}}
			for _, item := range models {
				require.False(t, db.Migrator().HasTable(item), "fixture must not reuse existing tables")
			}
			require.NoError(t, db.AutoMigrate(models...))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
			query := "SELECT version()"
			if dialect == common.DatabaseTypeSQLite {
				query = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("%s version: %s", dialect, version)

			t.Run("rename_preserves_policy_not_old_name", func(t *testing.T) {
				setupSensitiveGroupRenameCase(t, " old \n\nkeep \nold ")
				result, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
				require.NoError(t, err)
				assert.Equal(t, "new\nkeep", result.AppliedOptions[setting.SensitiveCheckExemptGroupsOptionKey])
				assertSensitiveGroupPolicy(t, "new\nkeep")
				require.NoError(t, UpdateRequestPolicyOptions(map[string]string{"RetryTimes": CurrentRequestPolicy().Options["RetryTimes"]}))
				assertSensitiveGroupPolicy(t, "new\nkeep")

				// A second node must refresh both the runtime filter and settings API.
				require.NoError(t, updateOptionMap(setting.SensitiveCheckExemptGroupsOptionKey, "old\nkeep"))
				require.NoError(t, refreshRequestPolicySnapshot())
				require.NoError(t, applyGroupConfigInvalidation(groupConfigAppliedVersion.Load()+1, nil, true))
				assertSensitiveGroupPolicy(t, "new\nkeep")

				require.NoError(t, UpdateOption("GroupRatio", `{"new":1,"old":2,"keep":1}`))
				assert.False(t, setting.IsSensitiveCheckExemptGroup("old"), "recreating the old group must not inherit its predecessor's bypass")
				assertSensitiveGroupPolicy(t, "new\nkeep")
			})

			t.Run("collision_and_coordinated_removal", func(t *testing.T) {
				setupSensitiveGroupRenameCase(t, " target \ntarget ")
				_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "target"}})
				require.ErrorIs(t, err, ErrGroupRenameCollision)
				require.NoError(t, UpdateRequestPolicyOptions(map[string]string{setting.SensitiveCheckExemptGroupsOptionKey: "old"}))
				_, err = ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{"GroupRatio": `{"keep":1}`}})
				require.ErrorIs(t, err, ErrGroupRenameReferencedGroup)
				assertSensitiveGroupPolicy(t, "old")
				_, err = ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{
					"GroupRatio": `{"keep":1}`, setting.SensitiveCheckExemptGroupsOptionKey: " \n ",
				}})
				require.NoError(t, err)
				assertSensitiveGroupPolicy(t, "")
				require.NoError(t, UpdateOption("GroupRatio", `{"old":1,"keep":1}`))
				assert.False(t, setting.IsSensitiveCheckExemptGroup("old"))
			})

			t.Run("explicit_options_normalize_and_revoke", func(t *testing.T) {
				setupSensitiveGroupRenameCase(t, "old")
				_, err := ApplyGroupRename(GroupRenameApplyRequest{
					Mappings: []GroupRenameMapping{{From: "old", To: "new"}},
					Options:  map[string]string{setting.SensitiveCheckExemptGroupsOptionKey: " new \nnew\n keep "},
				})
				require.NoError(t, err)
				assertSensitiveGroupPolicy(t, "new\nkeep")
				_, err = ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{setting.SensitiveCheckExemptGroupsOptionKey: ""}})
				require.NoError(t, err, "revoking an exemption does not delete an existing group")
				assertSensitiveGroupPolicy(t, "")
			})

			t.Run("invalid_or_stale_explicit_options_rollback", func(t *testing.T) {
				setupSensitiveGroupRenameCase(t, "old")
				for _, value := range []string{"old", "auto", "new\n*"} {
					_, err := ApplyGroupRename(GroupRenameApplyRequest{
						Mappings: []GroupRenameMapping{{From: "old", To: "new"}},
						Options:  map[string]string{setting.SensitiveCheckExemptGroupsOptionKey: value},
					})
					require.Error(t, err)
					assertSensitiveGroupPolicy(t, "old")
				}
				for _, target := range []string{"*", "new\u200b"} {
					_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: target}})
					require.Error(t, err, "a rename cannot turn an exact exemption into invalid policy")
					assertSensitiveGroupPolicy(t, "old")
				}
			})

			t.Run("stale_policy_writes_and_no_group_definition", func(t *testing.T) {
				setupSensitiveGroupRenameCase(t, "old")
				_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
				require.NoError(t, err)
				for _, write := range []func(map[string]string) error{UpdateRequestPolicyOptions, UpdateOptionsBulk} {
					require.ErrorIs(t, write(map[string]string{setting.SensitiveCheckExemptGroupsOptionKey: "old"}), ErrGroupReferenceRenamed)
					assertSensitiveGroupPolicy(t, "new")
				}
				require.ErrorIs(t, UpdateOption(setting.SensitiveCheckExemptGroupsOptionKey, "old"), ErrGroupReferenceRenamed)
				require.NoError(t, UpdateRequestPolicyOptions(map[string]string{setting.SensitiveCheckExemptGroupsOptionKey: "unconfigured"}))
				plan := SubscriptionPlan{Title: "policy-only group is not a subscription target"}
				require.NoError(t, DB.Create(&plan).Error)
				group := "unconfigured"
				err = DB.Transaction(func(tx *gorm.DB) error {
					return UpdateSubscriptionPlanTx(tx, plan.Id, SubscriptionPlanUpdate{Plan: plan, UpgradeGroup: &group})
				})
				require.ErrorContains(t, err, "not configured")
			})

			t.Run("explicit_reintroduction_remains_possible", func(t *testing.T) {
				setupSensitiveGroupRenameCase(t, "old")
				_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
				require.NoError(t, err)
				_, err = ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{
					"GroupRatio": `{"new":1,"old":2,"keep":1}`, setting.SensitiveCheckExemptGroupsOptionKey: "old\nnew",
				}})
				require.NoError(t, err, "an explicit new group definition can deliberately grant its own exemption")
				assertSensitiveGroupPolicy(t, "old\nnew")
			})

			t.Run("policy_save_precedes_rename", func(t *testing.T) {
				setupSensitiveGroupRenameCase(t, "old")
				atSave, release := make(chan struct{}), make(chan struct{})
				releaseBarrier := sync.OnceFunc(func() { close(release) })
				require.NoError(t, DB.Callback().Update().Before("gorm:update").Register("sensitive_exemption_pause", func(tx *gorm.DB) {
					option, ok := tx.Statement.Model.(*Option)
					if ok && option.Key == setting.SensitiveCheckExemptGroupsOptionKey {
						close(atSave)
						<-release
					}
				}))
				t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove("sensitive_exemption_pause")) })
				writerResult := startSensitiveGroupWrite(t, releaseBarrier, func() error {
					return UpdateRequestPolicyOptions(map[string]string{setting.SensitiveCheckExemptGroupsOptionKey: "old\nkeep"})
				})
				select {
				case <-atSave:
				case err := <-writerResult:
					t.Fatalf("policy save completed before the expected barrier: %v", err)
				}
				renameStarted := make(chan struct{})
				renameResult := startSensitiveGroupWrite(t, releaseBarrier, func() error {
					close(renameStarted)
					_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
					return err
				})
				<-renameStarted
				releaseBarrier()
				writeErr, renameErr := <-writerResult, <-renameResult
				require.NoError(t, writeErr)
				require.NoError(t, renameErr)
				assertSensitiveGroupPolicy(t, "new\nkeep")
			})

			for _, rollback := range []bool{false, true} {
				t.Run(fmt.Sprintf("concurrent_rename_policy_save/rollback=%t", rollback), func(t *testing.T) {
					setupSensitiveGroupRenameCase(t, "old")
					atCommit, release := make(chan struct{}), make(chan struct{})
					releaseBarrier := sync.OnceFunc(func() { close(release) })
					forcedErr := errors.New("forced rename rollback")
					groupRenameTxFailureHook = func() error {
						close(atCommit)
						<-release
						if rollback {
							return forcedErr
						}
						return nil
					}
					t.Cleanup(func() { groupRenameTxFailureHook = nil })
					renameResult := startSensitiveGroupWrite(t, releaseBarrier, func() error {
						_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
						return err
					})
					select {
					case <-atCommit:
					case err := <-renameResult:
						t.Fatalf("rename completed before the expected barrier: %v", err)
					}
					writerStarted := make(chan struct{})
					writerResult := startSensitiveGroupWrite(t, releaseBarrier, func() error {
						close(writerStarted)
						return UpdateRequestPolicyOptions(map[string]string{setting.SensitiveCheckExemptGroupsOptionKey: "old\nkeep"})
					})
					<-writerStarted
					releaseBarrier()
					renameErr, writeErr := <-renameResult, <-writerResult
					if rollback {
						require.ErrorIs(t, renameErr, forcedErr)
						require.NoError(t, writeErr)
						assertSensitiveGroupPolicy(t, "old\nkeep")
					} else {
						require.NoError(t, renameErr)
						require.ErrorIs(t, writeErr, ErrGroupReferenceRenamed)
						assertSensitiveGroupPolicy(t, "new")
					}
				})
			}
		})
	}
}

// Join every worker before fixture cleanup even when a database error prevents
// its barrier from being reached. Releasing the barrier is safe more than once.
func startSensitiveGroupWrite(t *testing.T, releaseBarrier func(), write func() error) <-chan error {
	t.Helper()
	result, done := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(done)
		result <- write()
	}()
	t.Cleanup(func() {
		releaseBarrier()
		<-done
	})
	return result
}

func setupSensitiveGroupRenameCase(t *testing.T, exempt string) {
	t.Helper()
	captureGroupRenameRuntimeState(t)
	previousVersion, previousInitialized := groupConfigAppliedVersion.Load(), groupConfigVersionInitialized.Load()
	t.Cleanup(func() {
		groupConfigAppliedVersion.Store(previousVersion)
		groupConfigVersionInitialized.Store(previousInitialized)
	})
	for _, item := range []any{&Option{}, &Channel{}, &Ability{}, &User{}, &Token{}, &SubscriptionPlan{}, &UserSubscription{}} {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(item).Error)
	}
	for key, value := range map[string]string{
		"GroupRatio": `{"old":1,"keep":1}`, "TopupGroupRatio": `{}`, "UserUsableGroups": `{}`,
		"GroupGroupRatio": `{}`, "AutoGroups": `[]`, "ModelRequestRateLimitGroup": `{}`,
		"group_ratio_setting.group_special_usable_group": `{}`, setting.SensitiveCheckExemptGroupsOptionKey: exempt,
	} {
		require.NoError(t, DB.Create(&Option{Key: key, Value: value}).Error)
		require.NoError(t, updateOptionMap(key, value))
	}
	require.NoError(t, refreshRequestPolicySnapshot())
}

func assertSensitiveGroupPolicy(t *testing.T, want string) {
	t.Helper()
	var option Option
	require.NoError(t, DB.Where(&Option{Key: setting.SensitiveCheckExemptGroupsOptionKey}).First(&option).Error)
	assert.Equal(t, want, option.Value)
	assert.Equal(t, want, setting.SensitiveCheckExemptGroupsToString())
	assert.Equal(t, want, CurrentRequestPolicy().Options[setting.SensitiveCheckExemptGroupsOptionKey])
	groups := []string(nil)
	if want != "" {
		groups = strings.Split(want, "\n")
	}
	assert.Equal(t, groups, CurrentRequestPolicy().TextExemptGroups)
}
