package model

import (
	"errors"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenQuotaWriteThroughWithBatchEnabled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		redis     bool
		unlimited bool
	}{
		{name: "limited_redis", redis: true},
		{name: "limited_database"},
		{name: "unlimited_redis", redis: true, unlimited: true},
		{name: "unlimited_database", unlimited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			resetBatchUpdateTestState(t)
			if tc.redis {
				useUserCacheMiniRedis(t)
			}
			common.BatchUpdateEnabled = true
			token := createReserveTestToken(t, 100)
			reserved, err := TryReserveTokenQuota(token.Id, token.Key, 40, tc.unlimited)
			require.NoError(t, err)
			require.True(t, reserved)
			reserved, err = TryReserveTokenQuota(token.Id, token.Key, 30, tc.unlimited)
			require.NoError(t, err)
			require.True(t, reserved)
			if !tc.unlimited {
				reserved, err = TryReserveTokenQuota(token.Id, token.Key, 31, false)
				require.NoError(t, err)
				assert.False(t, reserved, "the durable path must preserve the atomic token limit")
			}
			held := getTokenFromDB(t, token.Id)
			assert.Equal(t, 30, held.RemainQuota, "a persisted task cannot depend on the in-memory batch queue")
			assert.Equal(t, 70, held.UsedQuota)
			require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, 10))
			refunded := getTokenFromDB(t, token.Id)
			assert.Equal(t, 40, refunded.RemainQuota)
			assert.Equal(t, 60, refunded.UsedQuota)
			batchUpdate()
			afterFlush := getTokenFromDB(t, token.Id)
			assert.Equal(t, refunded.RemainQuota, afterFlush.RemainQuota, "durable mutations must not also enter the batch queue")
			assert.Equal(t, refunded.UsedQuota, afterFlush.UsedQuota)
			if tc.redis {
				cached, err := GetTokenByKey(token.Key, false)
				require.NoError(t, err)
				assert.Equal(t, 40, cached.RemainQuota)
				assert.Equal(t, 60, cached.UsedQuota)
			}
		})
	}
}

func TestTokenQuotaWriteThroughRollsBackOnPersistenceFailure(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	const name = "test:durable-token-write-failure"
	speculativeCredit := false
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			if cached, err := cacheGetTokenByKey(token.Key); err == nil && cached.RemainQuota > 100 {
				speculativeCredit = true
			}
			tx.AddError(errors.New("forced durable token write failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(name)) })
	for _, unlimited := range []bool{false, true} {
		reserved, err := TryReserveTokenQuota(token.Id, token.Key, 30, unlimited)
		require.Error(t, err)
		assert.False(t, reserved)
		cached, err := GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		assert.Equal(t, 100, cached.RemainQuota)
		assert.Zero(t, cached.UsedQuota)
	}
	require.Error(t, IncreaseTokenQuota(token.Id, token.Key, 20))
	cached, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 100, cached.RemainQuota)
	assert.Zero(t, cached.UsedQuota)
	stored := getTokenFromDB(t, token.Id)
	assert.Equal(t, 100, stored.RemainQuota)
	assert.Zero(t, stored.UsedQuota)
	assert.False(t, speculativeCredit, "a failed database refund must never advertise uncommitted quota")
}

func TestTokenQuotaWriteThroughRejectsStaleHighCacheBalance(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).
		Updates(map[string]any{"remain_quota": 20, "used_quota": 80}).Error)
	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 30, false)
	require.NoError(t, err)
	assert.False(t, reserved, "Redis is not authoritative for token spending")
	stored := getTokenFromDB(t, token.Id)
	assert.Equal(t, 20, stored.RemainQuota)
	assert.Equal(t, 80, stored.UsedQuota)
}

func TestTokenQuotaWriteThroughPreservesUnlimitedAccounting(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	token := createReserveTestToken(t, 10)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Update("unlimited_quota", true).Error)
	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 25, true)
	require.NoError(t, err)
	require.True(t, reserved, "unlimited tokens still account for usage beyond their remaining quota")
	require.NoError(t, DecreaseTokenQuota(token.Id, token.Key, 5))
	charged := getTokenFromDB(t, token.Id)
	assert.Equal(t, -20, charged.RemainQuota)
	assert.Equal(t, 30, charged.UsedQuota)
	require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, 30))
	refunded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 10, refunded.RemainQuota)
	assert.Zero(t, refunded.UsedQuota)
	batchUpdate()
	assert.Equal(t, 10, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestTokenQuotaWriteThroughConcurrentInvalidation(t *testing.T) {
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "token-interleaving.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(2)
	require.NoError(t, db.AutoMigrate(&Token{}))
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB; require.NoError(t, sqlDB.Close()) })
	token := Token{Key: "token-controlled-interleaving", RemainQuota: 100, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	_, err = GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	paused, release := make(chan struct{}), make(chan struct{})
	var pausedOnce atomic.Bool
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:token-invalidation-interleaving", func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" && pausedOnce.CompareAndSwap(false, true) {
			close(paused)
			<-release
		}
	}))
	type reservationResult struct {
		reserved bool
		err      error
	}
	first := make(chan reservationResult, 1)
	go func() {
		reserved, err := TryReserveTokenQuota(token.Id, token.Key, 60, false)
		first <- reservationResult{reserved, err}
	}()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("first reservation did not reach the controlled database boundary")
	}
	require.NoError(t, invalidateTokenCacheForMutation(token.Key))
	secondReserved, secondErr := TryReserveTokenQuota(token.Id, token.Key, 60, false)
	releaseOnce.Do(func() { close(release) })
	firstResult := <-first
	require.NoError(t, firstResult.err)
	require.NoError(t, secondErr)
	assert.False(t, firstResult.reserved, "the delayed reservation must recheck the committed database balance")
	assert.True(t, secondReserved)
	stored := getTokenFromDB(t, token.Id)
	assert.Equal(t, 40, stored.RemainQuota)
	assert.Equal(t, 60, stored.UsedQuota)
}

func TestTokenQuotaWriteThroughPreservesStatsBatching(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	user := createReserveTestUser(t, 100)
	channel := Channel{Name: "token-write-through-stats", Key: "unused", Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(&channel).Error)
	UpdateUserUsedQuotaAndRequestCount(user.Id, 20)
	UpdateChannelUsedQuota(channel.Id, 20)
	token := createReserveTestToken(t, 100)
	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 20, false)
	require.NoError(t, err)
	require.True(t, reserved)
	assert.Equal(t, 80, getTokenFromDB(t, token.Id).RemainQuota, "ordinary token reservations also persist before returning")
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 30, false)
	require.NoError(t, err)
	require.True(t, reserved)
	require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, 10))
	cached, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 60, cached.RemainQuota, "a refund must not erase another request's held quota")
	assert.Equal(t, 40, cached.UsedQuota)
	assert.Equal(t, 60, getTokenFromDB(t, token.Id).RemainQuota)
	var beforeUser User
	var beforeChannel Channel
	require.NoError(t, DB.First(&beforeUser, user.Id).Error)
	require.NoError(t, DB.First(&beforeChannel, channel.Id).Error)
	assert.Zero(t, beforeUser.UsedQuota, "user usage remains batched")
	assert.Zero(t, beforeUser.RequestCount, "request counts remain batched")
	assert.Zero(t, beforeChannel.UsedQuota, "channel usage remains batched")
	batchUpdate()
	stored := getTokenFromDB(t, token.Id)
	assert.Equal(t, cached.RemainQuota, stored.RemainQuota)
	assert.Equal(t, cached.UsedQuota, stored.UsedQuota)
	var afterUser User
	var afterChannel Channel
	require.NoError(t, DB.First(&afterUser, user.Id).Error)
	require.NoError(t, DB.First(&afterChannel, channel.Id).Error)
	assert.Equal(t, 20, afterUser.UsedQuota)
	assert.Equal(t, 1, afterUser.RequestCount)
	assert.EqualValues(t, 20, afterChannel.UsedQuota)
}

func TestTokenQuotaWriteThroughConcurrentReservationsRespectLimit(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make([]bool, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			<-start
			results[i], errs[i] = TryReserveTokenQuota(token.Id, token.Key, 60, false)
		})
	}
	close(start)
	wg.Wait()
	succeeded := 0
	for i, reserved := range results {
		require.NoError(t, errs[i])
		if reserved {
			succeeded++
		}
	}
	assert.Equal(t, 1, succeeded)
	stored := getTokenFromDB(t, token.Id)
	assert.Equal(t, 40, stored.RemainQuota)
	assert.Equal(t, 60, stored.UsedQuota)
	cached, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, stored.RemainQuota, cached.RemainQuota)
	assert.Equal(t, stored.UsedQuota, cached.UsedQuota)
}

func createReserveTestUser(t *testing.T, quota int) User {
	t.Helper()
	user := User{
		Username:    "reserve-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       quota,
		QuotaPaid:   quota,
		AffCode:     "reserve-aff-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func createReserveTestToken(t *testing.T, remainQuota int) Token {
	t.Helper()
	token := Token{
		UserId:      1,
		Key:         "reserve-token-" + common.GetRandomString(8),
		Name:        "reserve-test",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: remainQuota,
	}
	require.NoError(t, token.Insert())
	return token
}

func getUserQuotaFromDB(t *testing.T, id int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").First(&user, id).Error)
	return user.Quota
}

func getTokenFromDB(t *testing.T, id int) Token {
	t.Helper()
	var token Token
	require.NoError(t, DB.First(&token, id).Error)
	return token
}

func resetBatchUpdateTestState(t *testing.T) {
	t.Helper()
	oldBatchEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchEnabled
		for i := range BatchUpdateTypeCount {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = make(map[int]int)
			batchUpdateLocks[i].Unlock()
		}
	})
}

func TestTryReserveUserQuotaFailsClosed(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)

	// ChuanAPI 下 users.quota 是三账户兼容镜像，只能由账本事务维护；
	// 上游单列预扣 API 无安全调用条件，Redis 关闭时也必须 fail-closed。
	user := createReserveTestUser(t, 100)
	reserved, err := TryReserveUserQuota(user.Id, 60)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "reserve must not touch the quota mirror")
}

// TestTryReserveUserQuotaContractViaLedger 验证 ChuanAPI 的等价预扣契约：
// 余额只通过 DebitQuota 账本路径扣减，paid 优先、余额不足拒绝、quota 镜像
// 同步、balance_ledger 落账。
func TestTryReserveUserQuotaContractViaLedger(t *testing.T) {
	truncateTables(t)
	setupLedgerTestState(t)

	user := User{
		Username:    "reserve-ledger-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       100,
		QuotaPaid:   100,
		AffCode:     "reserve-aff-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(&user).Error)

	reserved, err := TryReserveUserQuota(user.Id, 60)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)

	// 账本等价路径：预扣 60，paid 全额承担。
	result, err := DebitQuota(LedgerEntry{
		UserId:         user.Id,
		Delta:          60,
		Reason:         LedgerReasonBillingPreConsume,
		ReferenceType:  "test",
		ReferenceId:    "reserve-ledger-debit-1",
		IdempotencyKey: "test:reserve-ledger-debit-1",
	}, AccountPaid, AccountBonus)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	got := getUserBalances(t, user.Id)
	assert.Equal(t, 40, got.QuotaPaid)
	assert.Equal(t, 0, got.QuotaBonus)
	assert.Equal(t, 0, got.QuotaReward)
	assert.Equal(t, 40, got.Quota)
	assertQuotaInvariant(t, got)

	// 余额不足时拒绝，且不破坏镜像。
	_, err = DebitQuota(LedgerEntry{
		UserId:         user.Id,
		Delta:          41,
		Reason:         LedgerReasonBillingPreConsume,
		ReferenceType:  "test",
		ReferenceId:    "reserve-ledger-debit-2",
		IdempotencyKey: "test:reserve-ledger-debit-2",
	}, AccountPaid, AccountBonus)
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
	got = getUserBalances(t, user.Id)
	assert.Equal(t, 40, got.Quota)
	assertQuotaInvariant(t, got)

	// 幂等重放：同键同参数不重复扣减。
	replayed, err := DebitQuota(LedgerEntry{
		UserId:         user.Id,
		Delta:          60,
		Reason:         LedgerReasonBillingPreConsume,
		ReferenceType:  "test",
		ReferenceId:    "reserve-ledger-debit-1",
		IdempotencyKey: "test:reserve-ledger-debit-1",
	}, AccountPaid, AccountBonus)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	assert.Equal(t, 40, getUserBalances(t, user.Id).Quota)
}

func TestTryReserveTokenQuotaWithoutRedis(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)

	token := createReserveTestToken(t, 80)
	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 25, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reloaded := getTokenFromDB(t, token.Id)
	assert.Equal(t, 55, reloaded.RemainQuota)
	assert.Equal(t, 25, reloaded.UsedQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 56, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 55, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestBatchReserveUserQuotaFailsClosedWhileTokenWritesThrough(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := User{
		Username:    "reserve-batch-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       10,
		QuotaPaid:   10,
		AffCode:     "reserve-aff-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(&user).Error)
	// 批量模式启用时，上游会把缓存预扣增量入队并在 batchUpdate 时落库；
	// ChuanAPI 下该路径会绕过账本，因此 reserve 必须 fail-closed，且不触碰
	// 缓存与数据库余额。
	reserved, err := TryReserveUserQuota(user.Id, 8)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)
	assert.Equal(t, 10, getUserQuotaFromDB(t, user.Id))

	// 等价账本路径立即可见，不依赖批量落盘。
	result, err := DebitQuota(LedgerEntry{
		UserId:         user.Id,
		Delta:          8,
		Reason:         LedgerReasonBillingPreConsume,
		ReferenceType:  "test",
		ReferenceId:    "batch-ledger-debit",
		IdempotencyKey: "test:batch-ledger-debit",
	}, AccountPaid, AccountBonus)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	assert.Equal(t, 2, getUserQuotaFromDB(t, user.Id), "ledger debit updates the mirror immediately")

	token := createReserveTestToken(t, 9)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 3, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 2, getTokenFromDB(t, token.Id).RemainQuota)

	batchUpdate()
	// Token balance mutations already persisted; flushing statistics cannot debit them again.
	assert.Equal(t, 2, getUserQuotaFromDB(t, user.Id))
	reloadedToken := getTokenFromDB(t, token.Id)
	assert.Equal(t, 2, reloadedToken.RemainQuota)
	assert.Equal(t, 7, reloadedToken.UsedQuota)
}

func TestLedgerAccumulatesTwoMaximumRequestCharges(t *testing.T) {
	truncateTables(t)
	setupLedgerTestState(t)

	user := createReserveTestUser(t, common.MaxQuota*2+100)
	for _, requestID := range []string{"max-charge-1", "max-charge-2"} {
		result, err := DebitQuota(LedgerEntry{
			UserId:         user.Id,
			Delta:          common.MaxQuota,
			Reason:         LedgerReasonBillingPreConsume,
			ReferenceType:  "test",
			ReferenceId:    requestID,
			RequestId:      requestID,
			IdempotencyKey: "test:" + requestID,
		}, AccountPaid)
		require.NoError(t, err)
		require.False(t, result.Replayed)
	}

	balances := getUserBalances(t, user.Id)
	assert.Equal(t, 100, balances.QuotaPaid)
	assert.Equal(t, 100, balances.Quota)
	assertQuotaInvariant(t, balances)

	var ledgerCount int64
	require.NoError(t, DB.Model(&BalanceLedger{}).
		Where("user_id = ? AND reason = ?", user.Id, LedgerReasonBillingPreConsume).
		Count(&ledgerCount).Error)
	assert.EqualValues(t, 2, ledgerCount)
}

func TestBatchUpdateAccumulatorSaturatesOverflow(t *testing.T) {
	resetBatchUpdateTestState(t)

	addNewRecord(BatchUpdateTypeUserQuota, 1, math.MaxInt)
	addNewRecord(BatchUpdateTypeUserQuota, 1, 1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	assert.Equal(t, math.MaxInt, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()

	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	batchUpdateStores[BatchUpdateTypeUserQuota] = make(map[int]int)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
	addNewRecord(BatchUpdateTypeUserQuota, 1, math.MinInt)
	addNewRecord(BatchUpdateTypeUserQuota, 1, -1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	assert.Equal(t, math.MinInt, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
}

func TestReserveFallsBackToDatabaseWhenRedisIsUnavailable(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 20)
	require.NoError(t, populateUserCache(user))
	server.Close()

	// Redis 故障时 user reserve 同样 fail-closed：不降级为绕过账本的单列扣减。
	reserved, err := TryReserveUserQuota(user.Id, 5)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)
	assert.Equal(t, 20, getUserQuotaFromDB(t, user.Id))

	// token 降级路径保留：数据库条件更新保证服务可用且不会超扣。
	token := createReserveTestToken(t, 20)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 5, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 15, getTokenFromDB(t, token.Id).RemainQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 16, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 15, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestSynchronousReserveRejectsStaleDeletedTokenCache(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	// fail-closed 的 user reserve 不触碰缓存：已水合的缓存余额保持不变。
	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, DB.Delete(&user).Error)

	reserved, err := TryReserveUserQuota(user.Id, 6)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)
	cached, cacheErr := cacheGetUserBase(user.Id)
	require.NoError(t, cacheErr)
	assert.Equal(t, 10, cached.Quota)

	// A deleted database token cannot spend through its stale Redis snapshot.
	token := createReserveTestToken(t, 12)
	_, err = GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.NoError(t, DB.Delete(&token).Error)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	assert.False(t, reserved)
	assert.NoError(t, err, "a non-matching conditional debit is rejected without a database error")
	_, cacheErr = cacheGetTokenByKey(token.Key)
	assert.Error(t, cacheErr, "the stale token snapshot must be invalidated")
}

func TestTokenCacheInitPreservesLiveQuotaAndFenceBlocksStaleSnapshot(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 100)
	loaded, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	stale := *loaded

	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -70)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)

	// 已存在的哈希只刷新 TTL：数据库快照不得覆盖已被原子预扣的余额。
	code, err := cacheInitToken(stale)
	require.NoError(t, err)
	assert.Equal(t, 2, code)
	cached, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 30, cached.RemainQuota)

	// 变更期间：fence 删除缓存并拦截并发读者手中的过期快照。
	require.NoError(t, invalidateTokenCacheForMutation(token.Key))
	code, err = cacheInitToken(stale)
	require.NoError(t, err)
	assert.Zero(t, code, "the pre-mutation snapshot must not be published while fenced")
	_, err = cacheGetTokenByKey(token.Key)
	assert.Error(t, err)

	// fence 过期后可重新从数据库水合。
	server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
	fresh, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 100, fresh.RemainQuota)
	cached, err = cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 100, cached.RemainQuota)
}
