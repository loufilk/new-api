package middleware

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const modelLimitRedisTimeout = 2 * time.Second
const modelLimitReservationLease = 10 * time.Minute

// Keep completed timestamps in the legacy UTC list: upgrading must not erase
// existing successful-request limits. Pending admissions are leased separately.
var modelSuccessReserveScript = redis.NewScript(`
local kind = redis.call('TYPE', KEYS[1]).ok
local pendingKind = redis.call('TYPE', KEYS[2]).ok
if (kind ~= 'none' and kind ~= 'list') or (pendingKind ~= 'none' and pendingKind ~= 'zset') then
  return redis.error_reply('invalid model limiter key type')
end
local clock = redis.call('TIME')
local now = tonumber(clock[1])*1000 + math.floor(tonumber(clock[2])/1000)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
if redis.call('ZSCORE', KEYS[2], ARGV[3]) then return 1 end
local completed = 0
for _, value in ipairs(redis.call('LRANGE', KEYS[1], 0, -1)) do
  if value > ARGV[1] then completed = completed + 1 end
end
if completed + redis.call('ZCARD', KEYS[2]) >= tonumber(ARGV[2]) then return 0 end
redis.call('ZADD', KEYS[2], now+tonumber(ARGV[4]), ARGV[3])
redis.call('PEXPIRE', KEYS[2], tonumber(ARGV[4])*2)
return 1
`)

var modelSuccessRenewScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = tonumber(clock[1])*1000 + math.floor(tonumber(clock[2])/1000)
local deadline = redis.call('ZSCORE', KEYS[1], ARGV[1])
if not deadline or tonumber(deadline) <= now then return 0 end
redis.call('ZADD', KEYS[1], now+tonumber(ARGV[2]), ARGV[1])
redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[2])*2)
return 1
`)

var modelSuccessCompleteScript = redis.NewScript(`
local kind = redis.call('TYPE', KEYS[1]).ok
local pendingKind = redis.call('TYPE', KEYS[2]).ok
if (kind ~= 'none' and kind ~= 'list') or (pendingKind ~= 'none' and pendingKind ~= 'zset') then
  return redis.error_reply('invalid model limiter key type')
end
if redis.call('EXISTS', KEYS[3]) == 1 then return 0 end
redis.call('ZREM', KEYS[2], ARGV[1])
if ARGV[2] == '1' then
  redis.call('LPUSH', KEYS[1], ARGV[3])
  redis.call('LTRIM', KEYS[1], 0, tonumber(ARGV[4])-1)
  redis.call('EXPIRE', KEYS[1], ARGV[5])
end
redis.call('SET', KEYS[3], '1', 'PX', ARGV[6])
return 1
`)

// Use memory's sliding-window policy instead of a continuously refilling token
// bucket. The operation ID also makes client retries of the Lua command safe.
var modelTotalRequestScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = tonumber(clock[1])*1000 + math.floor(tonumber(clock[2])/1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now-tonumber(ARGV[1]))
if redis.call('ZSCORE', KEYS[1], ARGV[3]) then return 1 end
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[2]) then return 0 end
redis.call('ZADD', KEYS[1], now, ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[1])
return 1
`)

type redisModelReservation struct {
	client     *redis.Client
	key, id    string
	limit      int
	window     int64
	once       sync.Once
	stop, done chan struct{}
}

func reserveRedisModelSuccess(ctx context.Context, client *redis.Client, key string, limit int, window int64, cancelRequest context.CancelFunc) (*redisModelReservation, bool, error) {
	if limit <= 0 {
		return nil, true, nil
	}
	if client == nil {
		return nil, false, errors.New("model limiter Redis is unavailable")
	}
	if window <= 0 || window > int64((3650*24*time.Hour)/time.Second) {
		return nil, false, errors.New("invalid model limiter window")
	}
	id := uuid.NewString()
	cutoff := time.Now().UTC().Add(-time.Duration(window) * time.Second).Format(modelRateLimitTimeFormat)
	query, cancel := context.WithTimeout(ctx, modelLimitRedisTimeout)
	allowed, err := modelSuccessReserveScript.Run(query, client, []string{key, key + ":pending-v2"}, cutoff, limit, id, modelLimitReservationLease.Milliseconds()).Int64()
	cancel()
	if err != nil {
		// The server may have accepted a command whose reply was lost.
		cleanup, done := context.WithTimeout(context.Background(), modelLimitRedisTimeout)
		_ = client.ZRem(cleanup, key+":pending-v2", id).Err()
		done()
		return nil, false, err
	}
	if allowed != 1 {
		return nil, false, nil
	}
	r := &redisModelReservation{client: client, key: key, id: id, limit: limit, window: window, stop: make(chan struct{}), done: make(chan struct{})}
	go r.renew(cancelRequest)
	return r, true, nil
}

func (r *redisModelReservation) renew(cancelRequest context.CancelFunc) {
	defer close(r.done)
	ticker := time.NewTicker(modelLimitReservationLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), modelLimitRedisTimeout)
			kept, err := modelSuccessRenewScript.Run(ctx, r.client, []string{r.key + ":pending-v2"}, r.id, modelLimitReservationLease.Milliseconds()).Int64()
			cancel()
			if err != nil || kept != 1 {
				common.SysError("model success-limit reservation renewal failed")
				if cancelRequest != nil {
					cancelRequest()
				}
				return
			}
		}
	}
}

func (r *redisModelReservation) Complete(success bool) {
	if r == nil {
		return
	}
	r.once.Do(func() {
		close(r.stop)
		<-r.done
		completed := 0
		if success {
			completed = 1
		}
		stamp := time.Now().UTC().Format(modelRateLimitTimeFormat)
		retention := max(time.Duration(r.window)*time.Second, modelLimitReservationLease*2)
		// Idempotency permits retry after an ambiguous timeout. Cleanup is
		// bounded but intentionally independent of client cancellation.
		for range 3 {
			ctx, cancel := context.WithTimeout(context.Background(), modelLimitRedisTimeout)
			err := modelSuccessCompleteScript.Run(ctx, r.client, []string{r.key, r.key + ":pending-v2", r.key + ":done:" + r.id}, r.id, completed, stamp, r.limit, r.window, retention.Milliseconds()).Err()
			cancel()
			if err == nil {
				return
			}
		}
		common.SysError("model success-limit completion failed; leased admission expires automatically")
	})
}

func allowRedisModelAttempt(ctx context.Context, client *redis.Client, key string, limit int, window int64) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	if client == nil {
		return false, errors.New("model limiter Redis is unavailable")
	}
	if window <= 0 || window > int64((3650*24*time.Hour)/time.Second) {
		return false, errors.New("invalid model limiter window")
	}
	query, cancel := context.WithTimeout(ctx, modelLimitRedisTimeout)
	defer cancel()
	allowed, err := modelTotalRequestScript.Run(query, client, []string{key}, window*1000, limit, uuid.NewString()).Int64()
	return allowed == 1, err
}
