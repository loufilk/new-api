package limiter

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useLimiterRedis(t *testing.T, address string) *redis.Client {
	t.Helper()

	previousInstance := instance
	require.NotEmpty(t, strings.TrimSpace(address), "the configured Redis address must not be empty")
	redisClient := redis.NewClient(&redis.Options{Addr: address})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, redisClient.Ping(ctx).Err(), "the explicitly configured Redis instance must be reachable")

	instance = nil
	once = sync.Once{}

	t.Cleanup(func() {
		_ = redisClient.Close()
		instance = previousInstance
		once = sync.Once{}
		if previousInstance != nil {
			once.Do(func() {})
		}
	})

	return redisClient
}

func TestAllowFallsBackToEvalAfterScriptFlush(t *testing.T) {
	t.Run("miniredis", func(t *testing.T) {
		testAllowAfterScriptFlush(t, useLimiterRedis(t, miniredis.RunT(t).Addr()))
	})
	t.Run("real_redis", func(t *testing.T) {
		address, configured := os.LookupEnv("TEST_REDIS_ADDR")
		if !configured {
			t.Skip("TEST_REDIS_ADDR must point to a dedicated Redis instance: this test executes SCRIPT FLUSH")
		}
		testAllowAfterScriptFlush(t, useLimiterRedis(t, address))
	})
}

func testAllowAfterScriptFlush(t *testing.T, redisClient *redis.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	redisLimiter := New(ctx, redisClient)
	key := "rateLimit:" + t.Name()
	require.NoError(t, redisClient.Del(ctx, key).Err())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = redisClient.Del(cleanupCtx, key).Err()
	})

	allowed, err := redisLimiter.Allow(
		ctx,
		key,
		WithCapacity(60),
		WithRate(0),
		WithRequested(20),
	)
	require.NoError(t, err)
	assert.True(t, allowed)
	tokens, err := redisClient.HGet(ctx, key, "tokens").Int()
	require.NoError(t, err)
	assert.Equal(t, 40, tokens)

	exists, err := redisClient.ScriptExists(ctx, redisLimiter.limitScriptSHA).Result()
	require.NoError(t, err)
	require.Len(t, exists, 1)
	assert.True(t, exists[0])

	require.NoError(t, redisClient.ScriptFlush(ctx).Err())

	exists, err = redisClient.ScriptExists(ctx, redisLimiter.limitScriptSHA).Result()
	require.NoError(t, err)
	require.Len(t, exists, 1)
	assert.False(t, exists[0])

	allowed, err = redisLimiter.Allow(
		ctx,
		key,
		WithCapacity(60),
		WithRate(0),
		WithRequested(30),
	)
	require.NoError(t, err)
	assert.True(t, allowed)
	tokens, err = redisClient.HGet(ctx, key, "tokens").Int()
	require.NoError(t, err)
	assert.Equal(t, 10, tokens, "fallback must keep the existing bucket and deduct exactly once")

	exists, err = redisClient.ScriptExists(ctx, redisLimiter.limitScriptSHA).Result()
	require.NoError(t, err)
	require.Len(t, exists, 1)
	assert.True(t, exists[0])

	allowed, err = redisLimiter.Allow(ctx, key, WithCapacity(60), WithRate(0), WithRequested(11))
	require.NoError(t, err)
	assert.False(t, allowed, "fallback must not reset the exhausted bucket")
	tokens, err = redisClient.HGet(ctx, key, "tokens").Int()
	require.NoError(t, err)
	assert.Equal(t, 10, tokens, "denied requests must leave remaining tokens unchanged")

	allowed, err = redisLimiter.Allow(ctx, key, WithCapacity(60), WithRate(0), WithRequested(10))
	require.NoError(t, err)
	assert.True(t, allowed)
	tokens, err = redisClient.HGet(ctx, key, "tokens").Int()
	require.NoError(t, err)
	assert.Zero(t, tokens)
}
