package model

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These tests use only a disposable PG 18.6 + Redis 8.8 fixture. Barriers expose
// the exact read/commit/cache-write ordering, without relying on sleeps.
func nativeAuthCacheFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" || os.Getenv("TEST_AUTH_REDIS_DSN") == "" {
		t.Skip("requires TEST_MODEL_POSTGRES_DSN and TEST_AUTH_REDIS_DSN")
	}
	options, err := redis.ParseURL(os.Getenv("TEST_AUTH_REDIS_DSN"))
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(options.Addr)
	require.NoError(t, err)
	require.True(t, net.ParseIP(host).IsLoopback())
	options.ContextTimeoutEnabled = true
	options.ReadTimeout, options.WriteTimeout, options.DialTimeout = time.Second, time.Second, time.Second
	options.MaxRetries = -1
	rdb := redis.NewClient(options)
	info, err := rdb.Info(t.Context(), "server").Result()
	require.NoError(t, err)
	require.True(t, strings.Contains(info, "redis_version:8.8.0"), info)
	size, err := rdb.DBSize(t.Context()).Result()
	require.NoError(t, err)
	require.Zero(t, size, "fixture Redis DB must start empty")
	previousRDB, previousEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = rdb, true
	t.Cleanup(func() {
		require.NoError(t, rdb.FlushDB(context.Background()).Err())
		require.NoError(t, rdb.Close())
		common.RDB, common.RedisEnabled = previousRDB, previousEnabled
	})
	// Only this fixture's synthetic rows are removed; the DSN is loopback-only.
	for _, table := range []any{&UserSubscription{}, &Token{}, &User{}, &Option{}} {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error)
	}
}

func nativeAuthUser(t *testing.T, group string) User {
	t.Helper()
	user := User{Username: "synthetic-cache-user", AffCode: "synthetic-cache-user", Password: "synthetic-not-login", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: group, AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func nativeAuthToken(t *testing.T, user User) Token {
	t.Helper()
	token := Token{UserId: user.Id, Key: "synthetic-only-cache-race-key", Group: user.Group, Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
	require.NoError(t, token.Insert())
	_, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	return token
}

func TestAuthCacheNativeTokenLongMutation(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "default")
	token := nativeAuthToken(t, user)
	stale := token
	beforeWrite, resumeWrite := make(chan struct{}), make(chan struct{})
	var intercepted atomic.Bool
	const callback = "native:token_mutation_barrier"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" && intercepted.CompareAndSwap(false, true) {
			close(beforeWrite)
			<-resumeWrite
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Update().Remove(callback) })
	token.Status = common.TokenStatusDisabled
	done := make(chan error, 1)
	go func() { done <- token.Update() }()
	<-beforeWrite
	// Deterministically represent the old ten-second fence expiring while SQL
	// is still waiting. This removes only the legacy, bounded quota fence.
	require.NoError(t, common.RDB.Del(t.Context(), getTokenCacheFenceKey(token.Key)).Err())
	_, fillErr := cacheInitToken(stale)
	close(resumeWrite)
	require.NoError(t, <-done)
	require.NoError(t, fillErr)
	cached, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusDisabled, cached.Status, "a stale snapshot must not outlive the committed disable")
}

func TestAuthCacheNativeSubscriptionDowngrade(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "enterprise")
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	// A completed self-profile transaction is paused before its full-user Redis
	// write; the production expiration path commits and refreshes meanwhile.
	profileRead, profileResume := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		snapshot, err := GetUserById(user.Id, false)
		if err != nil {
			done <- err
			close(profileRead)
			return
		}
		close(profileRead)
		<-profileResume
		done <- updateUserCache(*snapshot)
	}()
	<-profileRead
	sub := UserSubscription{UserId: user.Id, Status: "active", EndTime: common.GetTimestamp() - 1, UpgradeGroup: "enterprise", DowngradeGroup: "default", PrevUserGroup: "default"}
	require.NoError(t, DB.Create(&sub).Error)
	count, err := ExpireDueSubscriptions(10)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	close(profileResume)
	// Rejection of the stale best-effort cache write is an expected outcome.
	<-done
	cached, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "default", cached.Group, "old full-user profile cache write must not restore enterprise membership")
	assert.Equal(t, user.AuthVersion, cached.AuthVersion, "subscription group changes must not log out every session")
}

func TestAuthCacheNativeFloorLossAndDelayedWrites(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "enterprise")
	token := nativeAuthToken(t, user)
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	oldUser, oldToken := user, token
	token.Status = common.TokenStatusDisabled
	require.NoError(t, token.Update())
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return updateUserGroupTx(tx, user.Id, "default") }))
	// Full Redis state loss also drops committed floors. Arbitrary old writers
	// must not reconstruct authority using their pre-revocation snapshots.
	require.NoError(t, common.RDB.FlushDB(t.Context()).Err())
	result, err := cacheInitToken(oldToken)
	require.NoError(t, err)
	assert.Zero(t, result)
	assert.ErrorIs(t, writeUserCache(oldUser.ToBaseUser(), true), ErrPermissionCacheStale)
	tokenPending, tokenFloor := tokenPermissionKeys(token.Key)
	userPending, userFloor := userPermissionKeys(user.Id)
	count, err := common.RDB.Exists(t.Context(), tokenFloor, userFloor).Result()
	require.NoError(t, err)
	assert.Zero(t, count)
	freshToken, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	freshUser, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusDisabled, freshToken.Status)
	assert.Equal(t, "default", freshUser.Group)
	assert.Equal(t, user.AuthVersion, freshUser.AuthVersion)
	// Delays longer than any hash lifetime remain safe after reconciliation.
	require.NoError(t, common.RDB.Del(t.Context(), getTokenCacheKey(token.Key), getUserCacheKey(user.Id)).Err())
	result, err = cacheInitToken(oldToken)
	require.NoError(t, err)
	assert.Zero(t, result)
	assert.ErrorIs(t, writeUserCache(oldUser.ToBaseUser(), true), ErrPermissionCacheStale)
	count, err = common.RDB.Exists(t.Context(), tokenPending, userPending).Result()
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestAuthCacheNativeRollbackReconcilesWithoutTimer(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "enterprise")
	token := nativeAuthToken(t, user)
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	tx := DB.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, advanceTokenPermissionRevisionTx(tx, token.Id))
	require.NoError(t, tx.Model(&Token{}).Where("id = ?", token.Id).Update("status", common.TokenStatusDisabled).Error)
	tokenPending, _ := tokenPermissionKeys(token.Key)
	ttl, err := common.RDB.TTL(t.Context(), tokenPending).Result()
	require.NoError(t, err)
	assert.Equal(t, time.Duration(-1), ttl, "permission pending never expires during a long transaction")
	require.NoError(t, tx.Rollback().Error)
	gotToken, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusEnabled, gotToken.Status)
	assert.Equal(t, token.CacheRevision, gotToken.CacheRevision)
	tx = DB.Begin()
	require.NoError(t, tx.Error)
	_, err = IncrementUserAuthVersionWithTx(tx, user.Id)
	require.NoError(t, err)
	require.NoError(t, tx.Model(&User{}).Where("id = ?", user.Id).Update("group", "default").Error)
	userPending, _ := userPermissionKeys(user.Id)
	ttl, err = common.RDB.TTL(t.Context(), userPending).Result()
	require.NoError(t, err)
	assert.Equal(t, time.Duration(-1), ttl)
	require.NoError(t, tx.Rollback().Error)
	gotUser, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "enterprise", gotUser.Group)
	assert.Equal(t, user.AuthVersion, gotUser.AuthVersion)
	assert.Equal(t, user.CacheRevision, gotUser.CacheRevision)
	count, err := common.RDB.Exists(t.Context(), tokenPending, userPending, getUserAuthFenceKey(user.Id)).Result()
	require.NoError(t, err)
	assert.Zero(t, count, "row-locked recovery proves rollback and clears pending without waiting for TTL")
}

type nativeAuthRedisFailure struct{ fail atomic.Bool }

func (h *nativeAuthRedisFailure) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *nativeAuthRedisFailure) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *nativeAuthRedisFailure) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.fail.Load() {
			return errors.New("synthetic Redis command failure")
		}
		return next(ctx, cmd)
	}
}

func TestAuthCacheNativeRedisFailures(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "enterprise")
	token := nativeAuthToken(t, user)
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	failure := &nativeAuthRedisFailure{}
	common.RDB.AddHook(failure)
	t.Cleanup(func() { failure.fail.Store(false) })
	failure.fail.Store(true)
	token.Status = common.TokenStatusDisabled
	require.Error(t, token.Update(), "metadata mutation must abort if pre-commit pending cannot be established")
	require.Error(t, DB.Transaction(func(tx *gorm.DB) error { return updateUserGroupTx(tx, user.Id, "default") }))
	var dbToken Token
	var dbUser User
	require.NoError(t, DB.First(&dbToken, token.Id).Error)
	require.NoError(t, DB.First(&dbUser, user.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, dbToken.Status)
	assert.Equal(t, "enterprise", dbUser.Group)
	assert.Equal(t, token.CacheRevision, dbToken.CacheRevision)
	assert.Equal(t, user.CacheRevision, dbUser.CacheRevision)
	failure.fail.Store(false)
	require.NoError(t, token.Update())
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return updateUserGroupTx(tx, user.Id, "default") }))
	// Redis becomes unavailable after SQL commit, before authority publication.
	failure.fail.Store(true)
	gotToken, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	gotUser, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusDisabled, gotToken.Status)
	assert.Equal(t, "default", gotUser.Group)
	failure.fail.Store(false)
	gotToken, err = GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	gotUser, err = GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusDisabled, gotToken.Status)
	assert.Equal(t, "default", gotUser.Group)
}

func TestAuthCacheNativeHotPathUsesNoDatabaseQueries(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "default")
	token := nativeAuthToken(t, user)
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	_, err = cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	_, err = cacheGetUserBase(user.Id)
	require.NoError(t, err)
	var queries atomic.Int64
	const callback = "native:hot_cache_query_counter"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(*gorm.DB) { queries.Add(1) }))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
	for range 20 {
		_, err := GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		_, err = GetUserCache(user.Id)
		require.NoError(t, err)
	}
	assert.Zero(t, queries.Load(), "40 warm authorization cache reads must issue zero PG SELECTs")
	t.Logf("warm authorization cache reads=40 database queries=%d", queries.Load())
}

func TestAuthCacheNativeDeletionCannotResurrect(t *testing.T) {
	for _, mode := range []string{"single", "batch", "user-hard-delete"} {
		t.Run(mode, func(t *testing.T) {
			nativeAuthCacheFixture(t)
			user := nativeAuthUser(t, "default")
			token := nativeAuthToken(t, user)
			_, err := GetUserCache(user.Id)
			require.NoError(t, err)
			switch mode {
			case "single":
				require.NoError(t, token.Delete())
			case "batch":
				count, err := BatchDeleteTokens([]int{token.Id}, user.Id)
				require.NoError(t, err)
				require.Equal(t, 1, count)
			case "user-hard-delete":
				_, err := user.HardDelete()
				require.NoError(t, err)
			}
			require.NoError(t, common.RDB.FlushDB(t.Context()).Err())
			result, err := cacheInitToken(token)
			require.NoError(t, err)
			assert.Zero(t, result)
			_, err = GetTokenByKey(token.Key, false)
			assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
			if mode == "user-hard-delete" {
				assert.ErrorIs(t, writeUserCache(user.ToBaseUser(), true), ErrPermissionCacheStale)
				_, err = GetUserCache(user.Id)
				assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
			}
		})
	}
}

func TestAuthCacheNativeRevisionMigrationAndQuotaPreservation(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "enterprise")
	token := nativeAuthToken(t, user)
	require.EqualValues(t, 1, user.CacheRevision)
	require.EqualValues(t, 1, token.CacheRevision)
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id), "Quota", 123).Err())
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return updateUserGroupTx(tx, user.Id, "default") }))
	require.NoError(t, RefreshUserGroupCache(user.Id))
	got, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 123, got.Quota, "group refresh must not overwrite independently maintained cached quota")
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.EqualValues(t, 2, user.CacheRevision, "repeat migration must not reset a committed revision")
	assert.EqualValues(t, 1, user.AuthVersion)
	// A pre-upgrade hash missing the revision cannot be used despite valid Id.
	require.NoError(t, common.RDB.HDel(t.Context(), getTokenCacheKey(token.Key), "CacheRevision").Err())
	_, err = cacheGetTokenByKey(token.Key)
	require.Error(t, err)
	require.NoError(t, common.RDB.HDel(t.Context(), getUserCacheKey(user.Id), "CacheRevision").Err())
	_, err = cacheGetUserBase(user.Id)
	require.Error(t, err)
}

func TestAuthCacheNativeGroupRenameAndStatusWriters(t *testing.T) {
	nativeAuthCacheFixture(t)
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	user := nativeAuthUser(t, "old")
	token := nativeAuthToken(t, user)
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	_, err = RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	gotUser, err := GetUserCache(user.Id)
	require.NoError(t, err)
	gotToken, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, "new", gotUser.Group)
	assert.Equal(t, "new", gotToken.Group)
	assert.Greater(t, gotUser.CacheRevision, user.CacheRevision)
	assert.Greater(t, gotToken.CacheRevision, token.CacheRevision)
	assert.Equal(t, user.AuthVersion, gotUser.AuthVersion)
	gotToken.Status = common.TokenStatusDisabled
	require.NoError(t, gotToken.SelectUpdate())
	_, err = ValidateUserToken(token.Key)
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

type nativeDelayedAuthority struct {
	armed   atomic.Bool
	command []any
	match   string
}

func (h *nativeDelayedAuthority) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *nativeDelayedAuthority) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *nativeDelayedAuthority) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		if cmd.Name() == "eval" && len(args) > 1 && strings.Contains(fmt.Sprint(args[1]), h.match) && h.armed.CompareAndSwap(true, false) {
			h.command = append([]any(nil), args...)
			return context.DeadlineExceeded
		}
		return next(ctx, cmd)
	}
}

func TestAuthCacheNativeTimedOutPublicationCannotClearSuccessor(t *testing.T) {
	for _, entity := range []string{"token", "user"} {
		t.Run(entity, func(t *testing.T) {
			nativeAuthCacheFixture(t)
			user := nativeAuthUser(t, "enterprise")
			token := nativeAuthToken(t, user)
			_, err := GetUserCache(user.Id)
			require.NoError(t, err)
			delay := &nativeDelayedAuthority{match: "ARGV[20] == '1'"}
			if entity == "user" {
				delay.match = "ARGV[14] == '1'"
			}
			common.RDB.AddHook(delay)
			delay.armed.Store(true)
			_, tokenFloor := tokenPermissionKeys(token.Key)
			_, userFloor := userPermissionKeys(user.Id)
			require.NoError(t, common.RDB.Del(t.Context(), getTokenCacheKey(token.Key), getUserCacheKey(user.Id), tokenFloor, userFloor).Err())
			if entity == "token" {
				_, err = GetTokenByKey(token.Key, false)
			} else {
				_, err = GetUserCache(user.Id)
			}
			require.NoError(t, err, "a failed cache publication still returns the fresh locked database row")
			require.NotEmpty(t, delay.command)
			// The prior reader has returned and released its row lock. A successor
			// mutation now owns it; replay the old timed-out Redis command verbatim.
			tx := DB.Begin()
			require.NoError(t, tx.Error)
			defer tx.Rollback()
			pending := ""
			if entity == "token" {
				require.NoError(t, advanceTokenPermissionRevisionTx(tx, token.Id))
				require.NoError(t, tx.Model(&Token{}).Where("id = ?", token.Id).Update("status", common.TokenStatusDisabled).Error)
				pending, _ = tokenPermissionKeys(token.Key)
			} else {
				require.NoError(t, updateUserGroupTx(tx, user.Id, "default"))
				pending, _ = userPermissionKeys(user.Id)
			}
			nonce, err := common.RDB.Get(t.Context(), pending).Result()
			require.NoError(t, err)
			result, err := common.RDB.Do(t.Context(), delay.command...).Int()
			require.NoError(t, err)
			assert.Equal(t, -1, result, "late authority command must fail nonce CAS")
			after, err := common.RDB.Get(t.Context(), pending).Result()
			require.NoError(t, err)
			assert.Equal(t, nonce, after)
			require.NoError(t, tx.Commit().Error)
			if entity == "token" {
				got, err := GetTokenByKey(token.Key, false)
				require.NoError(t, err)
				assert.Equal(t, common.TokenStatusDisabled, got.Status)
			} else {
				got, err := GetUserCache(user.Id)
				require.NoError(t, err)
				assert.Equal(t, "default", got.Group)
			}
		})
	}
}

func TestAuthCacheNativeRecoveryHasBoundedRowLockWait(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "enterprise")
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	tx := DB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	require.NoError(t, updateUserGroupTx(tx, user.Id, "default"))
	_, err = GetUserCache(user.Id)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "an unresolved mutation must fail closed on the bounded recovery deadline")
	require.NoError(t, tx.Rollback().Error)
	got, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "enterprise", got.Group)
}

func TestAuthCacheNativePendingEvictionCannotRestoreAuthority(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "enterprise")
	token := nativeAuthToken(t, user)
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	oldToken := token
	token.Status = common.TokenStatusDisabled
	require.NoError(t, token.Update())
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return updateUserGroupTx(tx, user.Id, "default") }))
	tokenPending, tokenFloor := tokenPermissionKeys(token.Key)
	userPending, userFloor := userPermissionKeys(user.Id)
	// Simulate allkeys eviction of pending alone; the old user hash survives.
	require.NoError(t, common.RDB.Del(t.Context(), tokenPending, userPending).Err())
	oldGroup, err := common.RDB.HGet(t.Context(), getUserCacheKey(user.Id), "Group").Result()
	require.NoError(t, err)
	assert.Equal(t, "enterprise", oldGroup)
	count, err := common.RDB.Exists(t.Context(), tokenFloor, userFloor).Result()
	require.NoError(t, err)
	assert.Zero(t, count, "prepare atomically removes old floors as well as setting pending")
	result, err := cacheInitToken(oldToken)
	require.NoError(t, err)
	assert.Zero(t, result)
	assert.ErrorIs(t, writeUserCache(user.ToBaseUser(), true), ErrPermissionCacheStale)
	gotToken, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	gotUser, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusDisabled, gotToken.Status)
	assert.Equal(t, "default", gotUser.Group)
}

type nativeFailedClaim struct {
	armed        atomic.Bool
	command      []any
	noRetry      bool
	publications atomic.Int64
}

func (h *nativeFailedClaim) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *nativeFailedClaim) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *nativeFailedClaim) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "set" && h.armed.CompareAndSwap(true, false) {
			h.command = append([]any(nil), cmd.Args()...)
			h.noRetry = cmd.NoRetry()
			return context.DeadlineExceeded
		}
		if cmd.Name() == "eval" && strings.Contains(fmt.Sprint(cmd.Args()[1]), "ARGV[20] == '1'") {
			h.publications.Add(1)
		}
		return next(ctx, cmd)
	}
}

func TestAuthCacheNativeUnknownClaimDoesNotPublishOrRetry(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "default")
	token := nativeAuthToken(t, user)
	failure := &nativeFailedClaim{}
	common.RDB.AddHook(failure)
	failure.armed.Store(true)
	_, tokenFloor := tokenPermissionKeys(token.Key)
	require.NoError(t, common.RDB.Del(t.Context(), getTokenCacheKey(token.Key), tokenFloor).Err())
	_, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	require.NotEmpty(t, failure.command)
	assert.True(t, failure.noRetry, "only the claim command disables retries, via go-redis's per-command interface")
	assert.Zero(t, failure.publications.Load(), "an unknown claim outcome must not be followed by an authority Eval")
	token.Status = common.TokenStatusDisabled
	require.NoError(t, token.Update())
	_, err = GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	require.NoError(t, common.RDB.FlushDB(t.Context()).Err())
	// A late single SET has no matching publication command and can only make
	// the next request reconcile from current SQL under the row lock.
	require.NoError(t, common.RDB.Do(t.Context(), failure.command...).Err())
	got, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusDisabled, got.Status)
}

func TestAuthCacheNativeReferralCodeWritesOnlyReferralCode(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "default")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{"aff_code": "", "status": common.UserStatusDisabled}).Error)
	var before User
	require.NoError(t, DB.First(&before, user.Id).Error)
	code, err := EnsureUserAffCode(user.Id)
	require.NoError(t, err)
	require.NotEmpty(t, code)
	again, err := EnsureUserAffCode(user.Id)
	require.NoError(t, err)
	assert.Equal(t, code, again)
	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	assert.Equal(t, before.Status, after.Status)
	assert.Equal(t, before.Role, after.Role)
	assert.Equal(t, before.Group, after.Group)
	assert.Equal(t, before.AuthVersion, after.AuthVersion)
	assert.Equal(t, before.CacheRevision, after.CacheRevision)
}

type nativeClaimCounter struct{ claims atomic.Int64 }

func (h *nativeClaimCounter) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *nativeClaimCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *nativeClaimCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "set" && cmd.NoRetry() {
			h.claims.Add(1)
		}
		return next(ctx, cmd)
	}
}

func TestAuthCacheNativeQuotaColdPathUsesOneSelectWithoutLock(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "default")
	token := nativeAuthToken(t, user)
	_, err := GetUserCache(user.Id)
	require.NoError(t, err)
	require.NoError(t, invalidateTokenCacheForMutation(token.Key))
	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())
	counter := &nativeClaimCounter{}
	common.RDB.AddHook(counter)
	var queries, locks atomic.Int64
	const callback = "native:quota_cold_query_counter"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		queries.Add(1)
		if strings.Contains(tx.Statement.SQL.String(), "FOR UPDATE") {
			locks.Add(1)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
	_, err = GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	_, err = GetUserCache(user.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 2, queries.Load(), "each ordinary quota/hash miss uses exactly one SELECT")
	assert.Zero(t, locks.Load())
	assert.Zero(t, counter.claims.Load(), "ordinary fills never establish publication leases")
	t.Logf("ordinary cold reads=2 SELECTs=%d FOR UPDATE=%d authority claims=%d", queries.Load(), locks.Load(), counter.claims.Load())
}

func TestAuthCacheNativeOrdinaryColdReadRechecksConcurrentRevocation(t *testing.T) {
	for _, entity := range []string{"token", "user"} {
		t.Run(entity, func(t *testing.T) {
			nativeAuthCacheFixture(t)
			user := nativeAuthUser(t, "enterprise")
			token := nativeAuthToken(t, user)
			_, err := GetUserCache(user.Id)
			require.NoError(t, err)
			require.NoError(t, common.RDB.Del(t.Context(), getTokenCacheKey(token.Key), getUserCacheKey(user.Id)).Err())
			read, resume := make(chan struct{}), make(chan struct{})
			var intercepted atomic.Bool
			var wasLocked atomic.Bool
			const callback = "native:ordinary_cold_snapshot_barrier"
			table := "tokens"
			if entity == "user" {
				table = "users"
			}
			require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == table && intercepted.CompareAndSwap(false, true) {
					wasLocked.Store(strings.Contains(tx.Statement.SQL.String(), "FOR UPDATE"))
					close(read)
					<-resume
				}
			}))
			t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
			type result struct {
				token *Token
				user  *UserBase
				err   error
			}
			done := make(chan result, 1)
			go func() {
				if entity == "token" {
					got, err := GetTokenByKey(token.Key, false)
					done <- result{token: got, err: err}
				} else {
					got, err := GetUserCache(user.Id)
					done <- result{user: got, err: err}
				}
			}()
			<-read
			if entity == "token" {
				token.Status = common.TokenStatusDisabled
				require.NoError(t, token.Update())
				_, err = GetTokenByKey(token.Key, false)
			} else {
				require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return updateUserGroupTx(tx, user.Id, "default") }))
				_, err = GetUserCache(user.Id)
			}
			require.NoError(t, err)
			close(resume)
			got := <-done
			require.NoError(t, got.err)
			assert.False(t, wasLocked.Load(), "the first SELECT must exercise the ordinary, unlocked cold path")
			if entity == "token" {
				assert.Equal(t, common.TokenStatusDisabled, got.token.Status)
			} else {
				assert.Equal(t, "default", got.user.Group)
				assert.Equal(t, user.AuthVersion, got.user.AuthVersion)
			}
		})
	}
}

func TestAuthCacheNativeLegacySchemaUpgradePreservesExistingRows(t *testing.T) {
	nativeAuthCacheFixture(t)
	user := nativeAuthUser(t, "enterprise")
	token := nativeAuthToken(t, user)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{"quota": 1368, "quota_paid": 1234, "quota_bonus": 56, "quota_reward": 78, "used_quota": 12, "request_count": 3}).Error)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{"remain_quota": 789, "used_quota": 11, "model_limits_enabled": true, "model_limits": "synthetic-model", "allow_ips": "127.0.0.1"}).Error)
	var beforeUser User
	var beforeToken Token
	require.NoError(t, DB.First(&beforeUser, user.Id).Error)
	require.NoError(t, DB.First(&beforeToken, token.Id).Error)
	require.NoError(t, common.RDB.FlushDB(t.Context()).Err())
	// These are exclusively synthetic rows in the owned loopback fixture. Drop
	// only the newly introduced columns to represent the pre-upgrade schema.
	require.NoError(t, DB.Migrator().DropColumn(&User{}, "CacheRevision"))
	require.NoError(t, DB.Migrator().DropColumn(&Token{}, "CacheRevision"))
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	var afterUser User
	var afterToken Token
	require.NoError(t, DB.First(&afterUser, user.Id).Error)
	require.NoError(t, DB.First(&afterToken, token.Id).Error)
	assert.EqualValues(t, 1, afterUser.CacheRevision)
	assert.EqualValues(t, 1, afterToken.CacheRevision)
	assert.Equal(t, beforeUser, afterUser, "migration must preserve every existing user field, including all balances and AuthVersion")
	assert.Equal(t, beforeToken, afterToken, "migration must preserve every existing token field, including quota and restrictions")
}
