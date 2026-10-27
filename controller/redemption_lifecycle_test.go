package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func redemptionLifecycleDatabase(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_" + strings.ToUpper(dialect) + "_DSN")
	if dialect != "sqlite" && dsn == "" {
		t.Skip("TEST_" + strings.ToUpper(dialect) + "_DSN not configured")
	}
	db, _ := newAuditTestDatabase(t, dialect, dsn)
	useRedemptionLifecycleDatabase(t, db, dialect)
	return db
}

func useRedemptionLifecycleDatabase(t *testing.T, db *gorm.DB, dialect string) {
	t.Helper()
	connection, err := db.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(4)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousBatch := common.RedisEnabled, common.BatchUpdateEnabled
	previousPayment := *operation_setting.GetPaymentSetting()
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.BatchUpdateEnabled = false, false
	databaseTypes := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}
	common.SetDatabaseTypes(databaseTypes[dialect], databaseTypes[dialect])
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled, common.BatchUpdateEnabled = previousRedis, previousBatch
		*operation_setting.GetPaymentSetting() = previousPayment
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Redemption{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Log{}, &model.AuditLog{}))
	query := "SELECT version()"
	if dialect == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("redemption database: %s %s", dialect, version)
}

func redemptionLifecycleSeed(t *testing.T, db *gorm.DB, name string) (model.User, model.Redemption) {
	t.Helper()
	user := model.User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Quota: 60, QuotaPaid: 10, QuotaBonus: 20, QuotaReward: 30}
	require.NoError(t, db.Create(&user).Error)
	code := model.Redemption{Name: name, Key: fmt.Sprintf("%032d", user.Id), Quota: 500, Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp()}
	require.NoError(t, code.Insert())
	return user, code
}

func redemptionLifecycleRequest(t *testing.T, handler gin.HandlerFunc, path string, userID int, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := common.Marshal(body)
	require.NoError(t, err)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", userID)
	c.Set("role", common.RoleAdminUser)
	c.Set(common.RequestIdKey, "redemption-regression-request")
	handler(c)
	return response
}

func redemptionResponseSuccess(t *testing.T, response *httptest.ResponseRecorder) bool {
	t.Helper()
	assert.Equal(t, http.StatusOK, response.Code)
	var result struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	return result.Success
}

func TestRedemptionLifecycleDatabaseMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := redemptionLifecycleDatabase(t, dialect)

			t.Run("credits_paid_wallet_once_and_rejects_replay_by_another_user", func(t *testing.T) {
				user, code := redemptionLifecycleSeed(t, db, "redeem-once")
				response := redemptionLifecycleRequest(t, TopUp, "/api/user/topup", user.Id, gin.H{"key": code.Key})
				require.True(t, redemptionResponseSuccess(t, response), response.Body.String())
				var result struct {
					Data int `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.Equal(t, 500, result.Data)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 560, user.Quota)
				assert.Equal(t, 510, user.QuotaPaid)
				assert.Equal(t, 20, user.QuotaBonus)
				assert.Equal(t, 30, user.QuotaReward)
				require.NoError(t, db.First(&code, code.Id).Error)
				assert.Equal(t, common.RedemptionCodeStatusUsed, code.Status)
				assert.Equal(t, user.Id, code.UsedUserId)
				assert.Positive(t, code.RedeemedTime)
				other, _ := redemptionLifecycleSeed(t, db, "redeem-other")
				for _, userID := range []int{user.Id, other.Id} {
					response = redemptionLifecycleRequest(t, TopUp, "/api/user/topup", userID, gin.H{"key": code.Key})
					assert.False(t, redemptionResponseSuccess(t, response), response.Body.String())
				}
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&other, other.Id).Error)
				assert.Equal(t, 560, user.Quota)
				assert.Equal(t, 60, other.Quota)
				var entries []model.BalanceLedger
				require.NoError(t, db.Where("reference_type = ? AND reference_id = ?", "redemption", strconv.Itoa(code.Id)).Find(&entries).Error)
				require.Len(t, entries, 1)
				assert.Equal(t, string(model.AccountPaid), entries[0].Account)
				assert.Equal(t, 500, entries[0].Delta)
				assert.Equal(t, user.Id, entries[0].UserId)
				var operationCount int64
				require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Where("idempotency_key = ?", "credit:redemption:"+strconv.Itoa(code.Id)).Count(&operationCount).Error)
				assert.EqualValues(t, 1, operationCount)
			})

			for _, state := range []string{"disabled", "expired", "redeemed_time", "used_user_id"} {
				t.Run("rejects_"+state+"_without_wallet_changes", func(t *testing.T) {
					user, code := redemptionLifecycleSeed(t, db, "redeem-"+state)
					updates := map[string]any{"status": common.RedemptionCodeStatusDisabled}
					if state == "expired" {
						updates = map[string]any{"expired_time": common.GetTimestamp() - 60}
					} else if state == "redeemed_time" {
						updates = map[string]any{"redeemed_time": common.GetTimestamp() - 60}
					} else if state == "used_user_id" {
						updates = map[string]any{"used_user_id": user.Id}
					}
					require.NoError(t, db.Model(&code).Updates(updates).Error)
					response := redemptionLifecycleRequest(t, TopUp, "/api/user/topup", user.Id, gin.H{"key": code.Key})
					assert.False(t, redemptionResponseSuccess(t, response), response.Body.String())
					var actual model.User
					require.NoError(t, db.First(&actual, user.Id).Error)
					assert.Equal(t, user.Quota, actual.Quota)
					var count int64
					require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
					if state == "redeemed_time" || state == "used_user_id" {
						assert.Error(t, code.Update(), "a single old usage marker also blocks metadata edits")
						code.Status = common.RedemptionCodeStatusDisabled
						assert.Error(t, code.SelectUpdate(), "a single old usage marker also blocks status edits")
					}
				})
			}

			t.Run("concurrent_users_cannot_redeem_the_same_code_twice", func(t *testing.T) {
				first, code := redemptionLifecycleSeed(t, db, "concurrent-first")
				second, _ := redemptionLifecycleSeed(t, db, "concurrent-second")
				users := []model.User{first, second}
				results := make([]error, len(users))
				start := make(chan struct{})
				var workers sync.WaitGroup
				for index := range users {
					workers.Go(func() {
						<-start
						_, results[index] = model.Redeem(code.Key, users[index].Id)
					})
				}
				close(start)
				workers.Wait()
				successes := 0
				winnerID := 0
				for index, err := range results {
					wantQuota := 60
					if err == nil {
						successes++
						winnerID = users[index].Id
						wantQuota += code.Quota
					} else {
						assert.ErrorIs(t, err, model.ErrRedeemFailed)
					}
					var actual model.User
					require.NoError(t, db.First(&actual, users[index].Id).Error)
					assert.Equal(t, wantQuota, actual.Quota)
				}
				assert.Equal(t, 1, successes)
				require.NoError(t, db.First(&code, code.Id).Error)
				assert.Equal(t, winnerID, code.UsedUserId)
				assert.Equal(t, common.RedemptionCodeStatusUsed, code.Status)
				var count int64
				require.NoError(t, db.Model(&model.BalanceLedger{}).Where("reference_type = ? AND reference_id = ?", "redemption", strconv.Itoa(code.Id)).Count(&count).Error)
				assert.EqualValues(t, 1, count)
			})

			t.Run("wallet_limit_failure_leaves_code_available_and_creates_no_receipt", func(t *testing.T) {
				user, code := redemptionLifecycleSeed(t, db, "wallet-limit")
				require.NoError(t, db.Model(&user).Updates(map[string]any{"quota": common.MaxWalletQuota - 10, "quota_paid": common.MaxWalletQuota - 60}).Error)
				_, err := model.Redeem(code.Key, user.Id)
				require.ErrorIs(t, err, model.ErrRedeemFailed)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, common.MaxWalletQuota-10, user.Quota)
				require.NoError(t, db.First(&code, code.Id).Error)
				assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
				assert.Zero(t, code.RedeemedTime)
				assert.Zero(t, code.UsedUserId)
				for _, table := range []any{&model.BalanceLedger{}, &model.BalanceLedgerOperation{}} {
					var count int64
					require.NoError(t, db.Model(table).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
				}
			})

			t.Run("ledger_failure_rolls_back_code_wallet_and_receipt_then_allows_retry", func(t *testing.T) {
				user, code := redemptionLifecycleSeed(t, db, "redeem-rollback")
				injected := errors.New("redemption regression: reject ledger insert")
				var hits atomic.Int32
				const callback = "redemption-regression:ledger-insert"
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeFor[model.BalanceLedger]() {
						hits.Add(1)
						tx.AddError(injected)
					}
				}))
				t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
				_, err := model.Redeem(code.Key, user.Id)
				require.ErrorIs(t, err, model.ErrRedeemFailed)
				assert.EqualValues(t, 1, hits.Load())
				var actual model.User
				require.NoError(t, db.First(&actual, user.Id).Error)
				assert.Equal(t, user.Quota, actual.Quota)
				require.NoError(t, db.First(&code, code.Id).Error)
				assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
				assert.Zero(t, code.UsedUserId)
				assert.Zero(t, code.RedeemedTime)
				for _, table := range []any{&model.BalanceLedger{}, &model.BalanceLedgerOperation{}} {
					var count int64
					require.NoError(t, db.Model(table).Where("user_id = ?", user.Id).Count(&count).Error)
					assert.Zero(t, count)
				}
				require.NoError(t, db.Callback().Create().Remove(callback))
				quota, err := model.Redeem(code.Key, user.Id)
				require.NoError(t, err)
				assert.Equal(t, 500, quota)
			})

			for _, statusOnly := range []bool{false, true} {
				t.Run(fmt.Sprintf("admin_save_after_redeem_rejects_stale_status_only_%v", statusOnly), func(t *testing.T) {
					user, code := redemptionLifecycleSeed(t, db, fmt.Sprintf("stale-save-%v", statusOnly))
					var interleaved atomic.Bool
					var redeemErr error
					const callback = "redemption-regression:redeem-after-admin-read"
					require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
						if tx.Error == nil && tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeFor[model.Redemption]() && interleaved.CompareAndSwap(false, true) {
							_, redeemErr = model.Redeem(code.Key, user.Id)
						}
					}))
					t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
					path := "/api/redemption/"
					if statusOnly {
						path += "?status_only=true"
					}
					response := redemptionLifecycleRequest(t, UpdateRedemption, path, 1, gin.H{"id": code.Id, "name": "stale edit", "quota": 900, "status": common.RedemptionCodeStatusEnabled})
					require.True(t, interleaved.Load())
					require.NoError(t, redeemErr)
					assert.False(t, redemptionResponseSuccess(t, response), response.Body.String())
					var actual model.Redemption
					require.NoError(t, db.First(&actual, code.Id).Error)
					assert.Equal(t, common.RedemptionCodeStatusUsed, actual.Status)
					assert.Equal(t, user.Id, actual.UsedUserId)
					assert.Positive(t, actual.RedeemedTime)
					assert.Equal(t, code.Name, actual.Name)
					assert.Equal(t, code.Quota, actual.Quota)
				})
			}

			t.Run("full_edit_preserves_concurrent_disable_and_status_edit_preserves_metadata", func(t *testing.T) {
				_, code := redemptionLifecycleSeed(t, db, "independent-edits")
				staleFull := code
				code.Status = common.RedemptionCodeStatusDisabled
				require.NoError(t, code.SelectUpdate())
				staleFull.Name, staleFull.Quota = "updated name", 700
				require.NoError(t, staleFull.Update())
				assert.Equal(t, common.RedemptionCodeStatusDisabled, staleFull.Status, "the API response must reflect the locked current status")
				require.NoError(t, db.First(&code, code.Id).Error)
				assert.Equal(t, common.RedemptionCodeStatusDisabled, code.Status)
				assert.Equal(t, 700, code.Quota)
				assert.Equal(t, "updated name", code.Name)
				staleStatus := code
				code.Quota = 800
				require.NoError(t, code.Update())
				staleStatus.Status = common.RedemptionCodeStatusEnabled
				require.NoError(t, staleStatus.SelectUpdate())
				assert.Equal(t, 800, staleStatus.Quota, "the API response must preserve concurrent metadata")
				require.NoError(t, db.First(&code, code.Id).Error)
				assert.Equal(t, 800, code.Quota)
				assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
				assert.NoError(t, code.SelectUpdate(), "same-value status update must succeed on MySQL too")
				assert.NoError(t, code.Update(), "same-value metadata update must succeed on MySQL too")
			})

			t.Run("status_api_accepts_only_enable_disable_and_preserves_redemption_metadata", func(t *testing.T) {
				_, code := redemptionLifecycleSeed(t, db, "status-validation")
				for _, status := range []int{0, -1, common.RedemptionCodeStatusUsed, 99} {
					response := redemptionLifecycleRequest(t, UpdateRedemption, "/api/redemption/?status_only=true", 1, gin.H{"id": code.Id, "status": status, "redeemed_time": 123, "used_user_id": 999})
					assert.False(t, redemptionResponseSuccess(t, response), response.Body.String())
				}
				require.NoError(t, db.First(&code, code.Id).Error)
				assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
				assert.Zero(t, code.RedeemedTime)
				assert.Zero(t, code.UsedUserId)
			})

			t.Run("compliance_gate_blocks_creation_and_redemption_until_current_terms_confirmed", func(t *testing.T) {
				user, code := redemptionLifecycleSeed(t, db, "compliance-gate")
				setting := operation_setting.GetPaymentSetting()
				saved := *setting
				t.Cleanup(func() { *setting = saved })
				for _, confirmed := range []bool{false, true} {
					setting.ComplianceConfirmed = confirmed
					setting.ComplianceTermsVersion = "outdated"
					response := redemptionLifecycleRequest(t, TopUp, "/api/user/topup", user.Id, gin.H{"key": code.Key})
					assert.False(t, redemptionResponseSuccess(t, response), response.Body.String())
					response = redemptionLifecycleRequest(t, AddRedemption, "/api/redemption/", user.Id, gin.H{"name": "gated-create", "quota": 250, "count": 1})
					assert.False(t, redemptionResponseSuccess(t, response), response.Body.String())
				}
				var count int64
				require.NoError(t, db.Model(&model.Redemption{}).Where("name = ?", "gated-create").Count(&count).Error)
				assert.Zero(t, count)
				require.NoError(t, db.First(&code, code.Id).Error)
				assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
				require.NoError(t, db.First(&user, user.Id).Error)
				assert.Equal(t, 60, user.Quota)
				setting.ComplianceConfirmed = true
				setting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
				response := redemptionLifecycleRequest(t, AddRedemption, "/api/redemption/", user.Id, gin.H{"name": "gated-create", "quota": 250, "count": 1})
				assert.True(t, redemptionResponseSuccess(t, response), response.Body.String())
				require.NoError(t, db.Model(&model.Redemption{}).Where("name = ?", "gated-create").Count(&count).Error)
				assert.EqualValues(t, 1, count)
				response = redemptionLifecycleRequest(t, TopUp, "/api/user/topup", user.Id, gin.H{"key": code.Key})
				assert.True(t, redemptionResponseSuccess(t, response), response.Body.String())
			})
		})
	}
}

// SQLite's production IMMEDIATE transaction serializes the reader and writer.
// Also exercise WAL/deferred mode: if another connection commits after the admin
// eligibility read, its stale transaction must fail rather than revive the code.
func TestRedemptionAdminEditRejectsConcurrentRedeemAfterDeferredRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redemption-deferred.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=deferred"
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	useRedemptionLifecycleDatabase(t, db, "sqlite")
	user, code := redemptionLifecycleSeed(t, db, "deferred-admin")
	stale := code
	stale.Name, stale.Quota = "stale admin edit", 900
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	model.DB = db.WithContext(ctx)
	readReady, proceed, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	release := sync.OnceFunc(func() { close(proceed) })
	var interleaved atomic.Bool
	const callback = "redemption-regression:pause-admin-transaction-read"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeFor[model.Redemption]() && interleaved.CompareAndSwap(false, true) {
			close(readReady)
			select {
			case <-proceed:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
	go func() {
		defer close(finished)
		result <- stale.Update()
	}()
	t.Cleanup(func() {
		release()
		// t.Context is canceled before cleanup starts. Give the released
		// worker its own cleanup budget rather than randomly selecting an
		// already-canceled test context when the worker has also finished.
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		select {
		case <-finished:
		case <-cleanupContext.Done():
			t.Error("admin update did not terminate after releasing its transaction read")
		}
	})
	select {
	case <-readReady:
	case err := <-result:
		t.Fatalf("admin update completed without a transactional eligibility read: %v", err)
	case <-ctx.Done():
		t.Fatal("admin update never reached the eligibility read")
	}
	quota, err := model.Redeem(code.Key, user.Id)
	require.NoError(t, err)
	require.Equal(t, 500, quota)
	release()
	select {
	case err = <-result:
		require.Error(t, err, "the stale deferred snapshot must never commit an administrative edit")
		t.Logf("admin deferred-snapshot update rejected: %v", err)
	case <-ctx.Done():
		t.Fatal("admin update did not finish after the competing redemption committed")
	}
	var actual model.Redemption
	require.NoError(t, db.First(&actual, code.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, actual.Status)
	assert.Equal(t, user.Id, actual.UsedUserId)
	assert.Positive(t, actual.RedeemedTime)
	assert.Equal(t, code.Name, actual.Name)
	assert.Equal(t, code.Quota, actual.Quota)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 560, user.Quota)
	var entries []model.BalanceLedger
	require.NoError(t, db.Where("reference_type = ? AND reference_id = ?", "redemption", strconv.Itoa(code.Id)).Find(&entries).Error)
	require.Len(t, entries, 1)
	assert.Equal(t, 500, entries[0].Delta)
}

func TestFailedRedemptionDoesNotLogSubmittedCode(t *testing.T) {
	redemptionLifecycleDatabase(t, "sqlite")
	var logs bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = &logs, &logs
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
		common.LogWriterMu.Unlock()
	})
	const code = "synthetic-unused-redemption-code"
	response := redemptionLifecycleRequest(t, TopUp, "/api/user/topup", 123, gin.H{"key": code})
	assert.False(t, redemptionResponseSuccess(t, response), response.Body.String())
	assert.NotContains(t, logs.String(), code, "failed redemption attempts must never disclose a usable bearer code")
	assert.Contains(t, logs.String(), "123", "retain the user identifier for investigating failures")
	assert.Contains(t, logs.String(), "redemption-regression-request")
}
