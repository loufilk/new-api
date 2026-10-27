package controller

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const setupWalletPassword = "synthetic-setup-wallet-password"

func setupWalletDatabase(t *testing.T, dialect, dsn string) *gorm.DB {
	t.Helper()
	// Reuse the loopback-only fixture: every case gets a newly created database,
	// never tables in the database named by the caller's DSN.
	db, _ := newAuditTestDatabase(t, dialect, dsn)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.Setup{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.BalanceLedger{}))
	var version string
	query := "SELECT version()"
	if dialect == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("setup wallet database: %s %s", dialect, version)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	oldSetup := constant.Setup
	oldSelfUse, oldDemo := operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled
	common.OptionMapRWMutex.Lock()
	oldOptions := maps.Clone(common.OptionMap)
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseType(dialect), common.DatabaseType(dialect))
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	constant.Setup = false
	operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled = false, false
	t.Setenv("ACCOUNT_PASSWORD_HASH_ALGORITHM", "argon2id")
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
		constant.Setup = oldSetup
		operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled = oldSelfUse, oldDemo
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
	})
	return db
}

func postWalletSetup(t *testing.T, body string) bool {
	t.Helper()
	if body == "" {
		body = fmt.Sprintf(`{"username":"wallet-root","password":%q,"confirmPassword":%q,"quota":1,"quota_paid":1,"quota_bonus":999}`, setupWalletPassword, setupWalletPassword)
	}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	PostSetup(c)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Empty(t, response.Result().Cookies(), "setup must not create an authenticated session")
	var payload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	return payload.Success
}

func assertSetupWalletGrant(t *testing.T, db *gorm.DB) model.User {
	t.Helper()
	var users []model.User
	require.NoError(t, db.Find(&users).Error)
	require.Len(t, users, 1)
	user := users[0]
	assert.Equal(t, common.RoleRootUser, user.Role)
	assert.Equal(t, 100000000, user.Quota)
	assert.Equal(t, 100000000, user.QuotaPaid)
	assert.Zero(t, user.QuotaBonus)
	assert.Zero(t, user.QuotaReward)
	assert.Equal(t, user.QuotaPaid+user.QuotaBonus+user.QuotaReward, user.Quota)
	assert.True(t, common.ValidatePasswordAndHash(setupWalletPassword, user.Password))
	assert.NotEqual(t, setupWalletPassword, user.Password)
	var rows []model.BalanceLedger
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, user.Id, row.UserId)
	assert.Equal(t, string(model.AccountPaid), row.Account)
	assert.Equal(t, model.LedgerReasonSystem, row.Reason)
	assert.Equal(t, "root_init", row.ReferenceType)
	assert.Equal(t, fmt.Sprint(user.Id), row.ReferenceId)
	assert.Equal(t, fmt.Sprintf("credit:root_init:%d", user.Id), row.IdempotencyKey)
	assert.Equal(t, 100000000, row.Delta)
	assert.Zero(t, row.BalanceBefore)
	assert.Zero(t, row.TotalBefore)
	assert.Equal(t, user.QuotaPaid, row.BalanceAfter)
	assert.Equal(t, user.Quota, row.TotalAfter)
	assert.Empty(t, row.Metadata, "credentials do not belong in wallet audit metadata")
	var operations []model.BalanceLedgerOperation
	require.NoError(t, db.Find(&operations).Error)
	require.Len(t, operations, 1)
	assert.Equal(t, row.OperationId, operations[0].Id)
	assert.Equal(t, row.IdempotencyKey, operations[0].IdempotencyKey)
	assert.Equal(t, row.TotalBefore, operations[0].TotalBefore)
	assert.Equal(t, row.TotalAfter, operations[0].TotalAfter)
	return user
}

func TestSetupWalletDatabaseMatrix(t *testing.T) {
	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	if err := service.ConfigureFundingFactoryRegistry(registry); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}
	for _, database := range []struct{ dialect, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(database.dialect, func(t *testing.T) {
			dsn := os.Getenv(database.env)
			if database.env != "" && dsn == "" {
				t.Skip("set " + database.env + " to run this real database")
			}
			t.Run("fresh_root_and_replay", func(t *testing.T) {
				db := setupWalletDatabase(t, database.dialect, dsn)
				require.True(t, postWalletSetup(t, ""))
				require.True(t, constant.Setup)
				assertSetupWalletGrant(t, db)
				assert.False(t, postWalletSetup(t, ""))
				assertSetupWalletGrant(t, db)
				var count int64
				require.NoError(t, db.Model(&model.Setup{}).Count(&count).Error)
				assert.EqualValues(t, 1, count)
			})
			t.Run("concurrent_fresh_setup_single_credit", func(t *testing.T) {
				db := setupWalletDatabase(t, database.dialect, dsn)
				// Hold both initial root-existence queries before either account is
				// inserted. Distinct usernames must not mint two bootstrap credits.
				var rootQueries atomic.Int32
				ready := make(chan struct{})
				const callback = "test:concurrent_root_lookup"
				require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table != "users" {
						return
					}
					switch rootQueries.Add(1) {
					case 1:
						<-ready
					case 2:
						close(ready)
					}
				}))
				t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
				var responses [2]*httptest.ResponseRecorder
				var workers sync.WaitGroup
				for i := range responses {
					workers.Go(func() {
						responses[i] = httptest.NewRecorder()
						c, _ := gin.CreateTestContext(responses[i])
						body := fmt.Sprintf(`{"username":"root-%d","password":%q,"confirmPassword":%q}`, i, setupWalletPassword, setupWalletPassword)
						c.Request = httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
						c.Request.Header.Set("Content-Type", "application/json")
						PostSetup(c)
					})
				}
				workers.Wait()
				succeeded := 0
				for _, response := range responses {
					var payload struct{ Success bool }
					require.Equal(t, http.StatusOK, response.Code)
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
					if payload.Success {
						succeeded++
					}
				}
				assert.Equal(t, 1, succeeded)
				assertSetupWalletGrant(t, db)
			})
			for _, table := range []string{"balance_ledger_operations", "balance_ledger"} {
				t.Run("rollback_"+table, func(t *testing.T) {
					db := setupWalletDatabase(t, database.dialect, dsn)
					const callback = "test:setup_wallet_failure"
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
						if tx.Statement.Table == table {
							tx.AddError(errors.New("synthetic ledger persistence failure"))
						}
					}))
					t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
					assert.False(t, postWalletSetup(t, ""))
					assert.False(t, constant.Setup)
					for _, entity := range []any{&model.User{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.Setup{}} {
						var count int64
						require.NoError(t, db.Model(entity).Count(&count).Error)
						assert.Zero(t, count, "root and credit must roll back together")
					}
					require.NoError(t, db.Callback().Create().Remove(callback))
					require.True(t, postWalletSetup(t, ""))
					assertSetupWalletGrant(t, db)
				})
			}
			t.Run("setup_record_failure_retry", func(t *testing.T) {
				db := setupWalletDatabase(t, database.dialect, dsn)
				const callback = "test:setup_record_failure"
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "setups" {
						tx.AddError(errors.New("synthetic setup persistence failure"))
					}
				}))
				t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
				assert.False(t, postWalletSetup(t, ""))
				assert.False(t, constant.Setup, "an unsaved setup must remain retryable")
				user := assertSetupWalletGrant(t, db)
				require.NoError(t, db.Callback().Create().Remove(callback))
				require.True(t, postWalletSetup(t, ""))
				assert.True(t, constant.Setup)
				assert.Equal(t, user.Id, assertSetupWalletGrant(t, db).Id)
			})
			for _, balances := range []struct {
				name                       string
				quota, paid, bonus, reward int
			}{{"empty", 0, 0, 0, 0}, {"legacy", 37, 0, 0, 0}, {"bonus_only", 37, 0, 37, 0}, {"split", 37, 11, 13, 13}} {
				t.Run("existing_root_"+balances.name, func(t *testing.T) {
					db := setupWalletDatabase(t, database.dialect, dsn)
					user := model.User{Username: "existing-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: balances.quota, QuotaPaid: balances.paid, QuotaBonus: balances.bonus, QuotaReward: balances.reward}
					require.NoError(t, db.Create(&user).Error)
					require.True(t, postWalletSetup(t, ""))
					var stored model.User
					require.NoError(t, db.First(&stored, user.Id).Error)
					assert.Equal(t, user, stored, "setup must not grant, reclassify or reset an existing account")
					var count int64
					require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&count).Error)
					assert.Zero(t, count)
					if balances.name == "bonus_only" {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						info := &relaycommon.RelayInfo{UserId: user.Id, IsPlayground: true, UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
						session, apiErr := service.NewBillingSession(c, info, 1)
						require.NotNil(t, apiErr)
						assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
						assert.ErrorIs(t, apiErr.Err, service.ErrModelBonusNotSupported)
						assert.Nil(t, session)
						require.NoError(t, db.First(&stored, user.Id).Error)
						assert.Equal(t, user, stored, "the paid-only gate must not consume bonus")
					}
				})
			}
			t.Run("paid_only_preconsume", func(t *testing.T) {
				db := setupWalletDatabase(t, database.dialect, dsn)
				require.True(t, postWalletSetup(t, ""))
				user := assertSetupWalletGrant(t, db)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				info := &relaycommon.RelayInfo{UserId: user.Id, RequestId: "synthetic-root-paid-consume", IsPlayground: true, ForcePreConsume: true, UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
				assert.False(t, service.WalletBonusEligible(info), "unspecified model eligibility must fail closed")
				session, apiErr := service.NewBillingSession(c, info, 100)
				require.Nil(t, apiErr)
				require.NotNil(t, session)
				var stored model.User
				require.NoError(t, db.First(&stored, user.Id).Error)
				assert.Equal(t, 99999900, stored.QuotaPaid)
				assert.Equal(t, stored.QuotaPaid, stored.Quota)
				require.NoError(t, session.Settle(100))
				_, apiErr = service.NewBillingSession(c, info, 100000000)
				require.NotNil(t, apiErr)
				assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
			})
			t.Run("invalid_credentials_no_account_or_grant", func(t *testing.T) {
				db := setupWalletDatabase(t, database.dialect, dsn)
				for _, body := range []string{`{"username":"root","password":"short","confirmPassword":"short"}`, `{"username":"root","password":"synthetic-password","confirmPassword":"different-password"}`} {
					assert.False(t, postWalletSetup(t, body))
				}
				for _, entity := range []any{&model.User{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.Setup{}} {
					var count int64
					require.NoError(t, db.Model(entity).Count(&count).Error)
					assert.Zero(t, count)
				}
			})
		})
	}
}
