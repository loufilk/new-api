package middleware

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelRedisRateLimitUsesUTCRegardlessOfLocalTimezone(t *testing.T) {
	redisServer, redisClient := useRateLimitMiniRedis(t)
	previousLocation := time.Local
	time.Local = time.FixedZone("test-utc-plus-eight", 8*60*60)
	t.Cleanup(func() { time.Local = previousLocation })

	ctx := context.Background()
	recordKey := "rateLimit:model-utc-record"
	reservation, allowed, err := reserveRedisModelSuccess(ctx, redisClient, recordKey, 2, 60, nil)
	require.NoError(t, err)
	require.True(t, allowed)
	reservation.Complete(true)
	recorded, err := redisClient.LIndex(ctx, recordKey, 0).Result()
	require.NoError(t, err)
	recordedAt, err := time.Parse(modelRateLimitTimeFormat, recorded)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().UTC(), recordedAt, 2*time.Second)

	checkKey := "rateLimit:model-utc-check"
	withinWindow := time.Now().UTC().Add(-30 * time.Second).Format(modelRateLimitTimeFormat)
	_, err = redisServer.Push(checkKey, withinWindow, withinWindow)
	require.NoError(t, err)
	_, allowed, err = reserveRedisModelSuccess(ctx, redisClient, checkKey, 2, 60, nil)
	require.NoError(t, err)
	assert.False(t, allowed, "an existing UTC timestamp inside the window must remain limited on a non-UTC host")
}

var modelRateLimitTestUsers atomic.Int64

func TestModelRateLimitStreamFailuresDoNotConsumeSuccessLimit(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		for _, totalLimit := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s/total=%d", backend, totalLimit), func(t *testing.T) {
				subject := strconv.Itoa(7200000 + int(modelRateLimitTestUsers.Add(1)))
				handler := memoryRateLimitHandler(subject, 60, totalLimit, 1)
				if backend == "redis" {
					useRateLimitMiniRedis(t)
					handler = redisRateLimitHandler(subject, 60, totalLimit, 1)
				}
				router := gin.New()
				router.GET("/:outcome", handler, func(c *gin.Context) {
					status := relaycommon.NewStreamStatus()
					if c.Param("outcome") == "failed" {
						status.MarkFailed("server_error", "", 0)
					} else {
						status.MarkCompleted()
					}
					common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
					c.Status(http.StatusOK)
				})
				assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/failed", "127.0.0.1:1000").Code)
				if totalLimit > 0 {
					assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/failed", "127.0.0.1:1000").Code)
				} else {
					assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
				}
				assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
			})
		}
	}
}

func TestModelRateLimitReservesConcurrentSuccessAdmission(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			subject := strconv.Itoa(7200000 + int(modelRateLimitTestUsers.Add(1)))
			handler := memoryRateLimitHandler(subject, 60, 0, 1)
			if backend == "redis" {
				useRateLimitMiniRedis(t)
				handler = redisRateLimitHandler(subject, 60, 0, 1)
			}
			userID := 7200000 + int(modelRateLimitTestUsers.Add(1))
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseFirst := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseFirst)
			router := gin.New()
			router.GET("/:outcome", func(c *gin.Context) { c.Set("id", userID) }, handler, func(c *gin.Context) {
				if c.Param("outcome") == "failed" {
					close(entered)
					<-release
					status := relaycommon.NewStreamStatus()
					status.MarkFailed("server_error", "", 0)
					common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
				}
				c.Status(http.StatusOK)
			})
			go func() {
				defer close(finished)
				assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/failed", "127.0.0.1:1000").Code)
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("first request did not enter the handler")
			}
			assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
			releaseFirst()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("first request did not finish")
			}
			assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
			assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
		})
	}
}

func TestModelRedisReservationCleanupOutlivesCancelledRequest(t *testing.T) {
	_, client := useRateLimitMiniRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	key := "rateLimit:cleanup:" + uuid.NewString()
	reservation, allowed, err := reserveRedisModelSuccess(ctx, client, key, 1, 60, cancel)
	require.NoError(t, err)
	require.True(t, allowed)
	cancel()
	reservation.Complete(false)
	reservation.Complete(true) // Cannot turn an already released failure into success.
	assert.EqualValues(t, 0, client.LLen(context.Background(), key).Val())
	second, allowed, err := reserveRedisModelSuccess(context.Background(), client, key, 1, 60, nil)
	require.NoError(t, err)
	require.True(t, allowed)
	second.Complete(true)
	second.Complete(true)
	assert.EqualValues(t, 1, client.LLen(context.Background(), key).Val())
}

func TestModelRedisReservationReplayAndLeaseExpiry(t *testing.T) {
	_, client := useRateLimitMiniRedis(t)
	checkModelRedisReservationContract(t, client)
}

func checkModelRedisReservationContract(t *testing.T, client *redis.Client) {
	t.Helper()
	ctx := context.Background()
	key := "rateLimit:runtime-test:" + uuid.NewString()
	t.Cleanup(func() {
		keys, err := client.Keys(ctx, key+"*").Result()
		if err == nil && len(keys) > 0 {
			require.NoError(t, client.Del(ctx, keys...).Err())
		}
	})
	first, allowed, err := reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.NoError(t, err)
	require.True(t, allowed)
	defer first.Complete(false)
	_, allowed, err = reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.NoError(t, err)
	require.False(t, allowed)
	// Redis client retry of the same reservation is idempotent, even at capacity.
	cutoff := time.Now().UTC().Add(-time.Minute).Format(modelRateLimitTimeFormat)
	replay, err := modelSuccessReserveScript.Run(ctx, client, []string{key, key + ":pending-v2"}, cutoff, 1, first.id, modelLimitReservationLease.Milliseconds()).Int64()
	require.NoError(t, err)
	assert.EqualValues(t, 1, replay)
	renewed, err := modelSuccessRenewScript.Run(ctx, client, []string{key + ":pending-v2"}, first.id, modelLimitReservationLease.Milliseconds()).Int64()
	require.NoError(t, err)
	assert.EqualValues(t, 1, renewed)
	// Expire the admission without sleeping. A crashed process must not retain
	// a permanent success slot; a late completion still records its outcome.
	require.NoError(t, client.ZAdd(ctx, key+":pending-v2", redis.Z{Score: 0, Member: first.id}).Err())
	second, allowed, err := reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.NoError(t, err)
	require.True(t, allowed)
	second.Complete(false)
	first.Complete(true)
	replay, err = modelSuccessCompleteScript.Run(ctx, client, []string{key, key + ":pending-v2", key + ":done:" + first.id}, first.id, 1, time.Now().UTC().Format(modelRateLimitTimeFormat), 1, 60, 1200000).Int64()
	require.NoError(t, err)
	assert.Zero(t, replay)
	assert.EqualValues(t, 1, client.LLen(ctx, key).Val())
	_, allowed, err = reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.NoError(t, err)
	assert.False(t, allowed)

	totalKey := key + ":total"
	allowed, err = allowRedisModelAttempt(ctx, client, totalKey, 1, 60)
	require.NoError(t, err)
	assert.True(t, allowed)
	allowed, err = allowRedisModelAttempt(ctx, client, totalKey, 1, 60)
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestModelRedisRateLimitFailsClosedWithoutClobberingInvalidKeys(t *testing.T) {
	_, client := useRateLimitMiniRedis(t)
	ctx := context.Background()
	key := "rateLimit:invalid:" + uuid.NewString()
	require.NoError(t, client.Set(ctx, key, "not-a-list", time.Minute).Err())
	_, allowed, err := reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.Error(t, err)
	assert.False(t, allowed)
	assert.Equal(t, "not-a-list", client.Get(ctx, key).Val())
	_, allowed, err = reserveRedisModelSuccess(ctx, nil, key, 1, 60, nil)
	require.Error(t, err)
	assert.False(t, allowed)
}

// SCRIPT FLUSH clears the server-wide script cache: use a disposable Redis
// instance dedicated to this test, never a shared cache or production service.
func TestModelRateLimitRealRedis(t *testing.T) {
	if os.Getenv("TEST_RUNTIME_REDIS") != "1" {
		t.Skip("set TEST_RUNTIME_REDIS=1 for isolated real Redis verification")
	}
	dsn := os.Getenv("TEST_REDIS_DSN")
	require.NotEmpty(t, dsn)
	options, err := redis.ParseURL(dsn)
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())
	t.Run("reservation_replay_and_expiry", func(t *testing.T) {
		checkModelRedisReservationContract(t, client)
	})
	t.Run("script_flush_resumes_request_pipeline", func(t *testing.T) {
		previousClient := common.RDB
		common.RDB = client
		t.Cleanup(func() { common.RDB = previousClient })
		ctx := t.Context()
		subject := "script-flush:" + uuid.NewString()
		totalKey := "rateLimit:" + ModelRequestRateLimitCountMark + ":" + subject + ":v2"
		successKey := "rateLimit:" + ModelRequestRateLimitSuccessCountMark + ":" + subject
		t.Cleanup(func() {
			keys, err := client.Keys(context.Background(), "rateLimit:*:"+subject+"*").Result()
			require.NoError(t, err)
			if len(keys) > 0 {
				require.NoError(t, client.Del(context.Background(), keys...).Err())
			}
		})

		handled := 0
		router := gin.New()
		router.GET("/:outcome", redisRateLimitHandler(subject, 60, 4, 2), func(c *gin.Context) {
			handled++
			if c.Param("outcome") == "failed" {
				c.Status(http.StatusBadGateway)
				return
			}
			if c.Param("outcome") == "flush-before-completion" {
				// In-flight requests must finish accounting after Redis evicts scripts,
				// not just requests admitted after the eviction.
				require.NoError(t, client.ScriptFlush(ctx).Err())
			}
			c.Status(http.StatusOK)
		})

		// Warm all admission/completion scripts without consuming a success slot.
		require.Equal(t, http.StatusBadGateway, performRateLimitRequest(router, "/failed", "127.0.0.1:1000").Code)
		assertModelRedisRequestCounters(t, client, totalKey, successKey, 1, 0)
		hashes := []string{modelTotalRequestScript.Hash(), modelSuccessReserveScript.Hash(), modelSuccessCompleteScript.Hash()}
		exists, err := client.ScriptExists(ctx, hashes...).Result()
		require.NoError(t, err)
		require.Equal(t, []bool{true, true, true}, exists)
		require.NoError(t, client.ScriptFlush(ctx).Err())
		exists, err = client.ScriptExists(ctx, hashes...).Result()
		require.NoError(t, err)
		require.Equal(t, []bool{false, false, false}, exists)

		require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/flush-before-completion", "127.0.0.1:1000").Code)
		assertModelRedisRequestCounters(t, client, totalKey, successKey, 2, 1)
		require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
		successLimited := performRateLimitRequest(router, "/completed", "127.0.0.1:1000")
		require.Equal(t, http.StatusTooManyRequests, successLimited.Code)
		assert.Contains(t, successLimited.Body.String(), "model_success_request_limit_exceeded")
		totalLimited := performRateLimitRequest(router, "/completed", "127.0.0.1:1000")
		require.Equal(t, http.StatusTooManyRequests, totalLimited.Code)
		assert.Contains(t, totalLimited.Body.String(), "model_total_request_limit_exceeded")
		assert.Equal(t, 3, handled, "rejected requests must not reach the upstream handler")
		assertModelRedisRequestCounters(t, client, totalKey, successKey, 4, 2)
		for _, key := range []string{totalKey, successKey} {
			ttl, err := client.PTTL(ctx, key).Result()
			require.NoError(t, err)
			assert.Positive(t, ttl, "script recovery must retain counter expiry")
			assert.LessOrEqual(t, ttl, time.Minute)
		}
	})
}

func assertModelRedisRequestCounters(t *testing.T, client *redis.Client, totalKey, successKey string, attempts, successes int64) {
	t.Helper()
	ctx := t.Context()
	pipe := client.Pipeline()
	total := pipe.ZCard(ctx, totalKey)
	completed := pipe.LLen(ctx, successKey)
	pending := pipe.ZCard(ctx, successKey+":pending-v2")
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, attempts, total.Val())
	assert.Equal(t, successes, completed.Val())
	assert.Zero(t, pending.Val(), "finished requests must release all leased admissions")
}

// The memory limiter answers like the Redis limiter: an OpenAI error body
// naming the limit that was reached, not an empty 429.
func TestModelMemoryRateLimitRejectionsReturnOpenAIError(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		totalLimit, successLimit int
		messageKey               string
	}{
		{name: "total", totalLimit: 1, messageKey: "model_total_request_limit_exceeded"},
		{name: "success", successLimit: 1, messageKey: "model_success_request_limit_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := strconv.Itoa(7200000 + int(modelRateLimitTestUsers.Add(1)))
			router := gin.New()
			router.GET("/", func(c *gin.Context) {
				c.Set(common.RequestIdKey, "req-limited")
			}, memoryRateLimitHandler(subject, 60, tc.totalLimit, tc.successLimit), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})
			require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/", "127.0.0.1:1000").Code)

			resp := performRateLimitRequest(router, "/", "127.0.0.1:1000")
			assert.Equal(t, http.StatusTooManyRequests, resp.Code)
			message := tc.messageKey
			want, err := common.Marshal(gin.H{"error": gin.H{
				"message": common.MessageWithRequestId(message, "req-limited"),
				"type":    "new_api_error",
				"code":    "",
			}})
			require.NoError(t, err)
			assert.JSONEq(t, string(want), resp.Body.String())
		})
	}
}

// A group with its own limit counts each user's requests on its own; groups
// without one keep sharing the user's global limit.
func TestModelRateLimitCountsConfiguredGroupsSeparately(t *testing.T) {
	previousEnabled, previousMinutes := setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes
	previousTotal, previousSuccess := setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount
	previousGroups, previousRedisEnabled := setting.ModelRequestRateLimitGroup, common.RedisEnabled
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes = previousEnabled, previousMinutes
		setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = previousTotal, previousSuccess
		setting.ModelRequestRateLimitGroup, common.RedisEnabled = previousGroups, previousRedisEnabled
	})
	setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes = true, 1
	common.RedisEnabled = false

	// Groups a and b have their own limits; c and d have none and share the global one.
	steps := []struct {
		group string
		want  int
	}{
		{"a", http.StatusOK}, {"a", http.StatusTooManyRequests},
		{"b", http.StatusOK}, {"b", http.StatusTooManyRequests},
		{"c", http.StatusOK}, {"d", http.StatusTooManyRequests},
	}
	for _, tc := range []struct {
		backend, limit string
		counts         [2]int // [total, success] for groups a and b and for the global limit
	}{
		{"memory", "success", [2]int{0, 1}},
		{"redis", "success", [2]int{0, 1}},
		{"memory", "total", [2]int{1, 1000}},
		{"redis", "total", [2]int{1, 1000}},
	} {
		t.Run(tc.backend+"/"+tc.limit, func(t *testing.T) {
			if tc.backend == "redis" {
				useRateLimitMiniRedis(t)
			}
			setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = tc.counts[0], tc.counts[1]
			setting.ModelRequestRateLimitGroup = map[string][2]int{"a": tc.counts, "b": tc.counts}
			userID := 7200000 + int(modelRateLimitTestUsers.Add(1))
			router := gin.New()
			router.GET("/:group", func(c *gin.Context) {
				c.Set("id", userID)
				common.SetContextKey(c, constant.ContextKeyTokenGroup, c.Param("group"))
			}, ModelRequestRateLimit(), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			for _, step := range steps {
				assert.Equal(t, step.want, performRateLimitRequest(router, "/"+step.group, "127.0.0.1:1000").Code, "group %s", step.group)
			}
		})
	}
}
