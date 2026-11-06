package model

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func (token Token) cacheMAC() (string, error) {
	key := getTokenCacheKey(token.Key)
	token.Key = ""
	token.RemainQuota = 0
	token.UsedQuota = 0
	token.AccessedTime = 0
	if token.AllowIps != nil && *token.AllowIps == "" {
		token.AllowIps = nil
	}
	return authCacheMAC(key, struct {
		Token          Token
		AutoGroups     string
		CacheRevision  int64
		CacheExpiresAt int64
	}{token, token.AutoGroups, token.CacheRevision, token.CacheExpiresAt})
}

func getTokenCacheKey(key string) string {
	return fmt.Sprintf("token:%s", common.GenerateHMAC(key))
}

// The fence uses the cache key as its hash tag so cacheInitToken's script
// reads both keys from one slot on sharded Redis.
func getTokenCacheFenceKey(key string) string {
	return fmt.Sprintf("token:fence:{%s}", getTokenCacheKey(key))
}

func tokenCacheTTLSeconds() int {
	ttl := common.RedisKeyCacheSeconds()
	if ttl <= 0 {
		return 60
	}
	return ttl
}

// The bounded fence is only a quota-cache optimization. Permission mutations
// use independent, non-expiring revision fences; authorization must never rely
// on a database transaction or delayed snapshot finishing inside ten seconds.
const tokenCacheFenceSeconds = 10

// invalidateTokenCacheForMutation is called before a token metadata or quota mutation
// writes to the database: it raises the fence and drops the cached hash so no
// reader can act on (or re-publish) the pre-mutation state.
func invalidateTokenCacheForMutation(key string) error {
	return invalidateTokenCacheForMutationContext(context.Background(), key)
}

// Bounded transactional callers must propagate their deadline through both
// parts of the pre-mutation fence. This never removes or shortens the fence.
func invalidateTokenCacheForMutationContext(ctx context.Context, key string) error {
	if !common.RedisEnabled || key == "" {
		return nil
	}
	err := common.RDB.Set(ctx, getTokenCacheFenceKey(key), 1, time.Duration(tokenCacheFenceSeconds)*time.Second).Err()
	if err != nil {
		return err
	}
	return common.RDB.Del(ctx, getTokenCacheKey(key)).Err()
}

// cacheInitToken publishes or repairs database metadata behind the existing
// permission and quota fences. Repairs preserve valid live quota counters and
// compare the observed MAC to avoid replacing a concurrent publication.
// Returns 0 when fenced, 1 after initialization, or 2 for an existing hash.
func cacheInitToken(token Token) (int, error) {
	return cacheInitTokenAtAuthority(context.Background(), token, false)
}

// authoritative may only be set while holding the token's DB row lock.
func cacheInitTokenAtAuthority(ctx context.Context, token Token, authoritative bool) (int, error) {
	if !common.RedisEnabled {
		return 0, nil
	}
	if token.CacheRevision < 1 || token.CacheRevision > maxPermissionRevision {
		return 0, ErrPermissionCacheStale
	}
	ctx, cancel := context.WithTimeout(ctx, permissionCacheTimeout)
	defer cancel()
	authority := "0"
	if authoritative {
		authority = "1"
	}
	pendingKey, revisionKey := tokenPermissionKeys(token.Key)
	expectedPending := ""
	if authoritative {
		var err error
		expectedPending, err = claimPermissionCacheAuthority(ctx, pendingKey)
		if err != nil {
			return 0, err
		}
	}
	allowIps := ""
	if token.AllowIps != nil {
		allowIps = *token.AllowIps
	}
	cacheKey := getTokenCacheKey(token.Key)
	var existing Token
	readErr := common.RedisHGetObj(cacheKey, &existing)
	if readErr != nil {
		// Decoding may stop before the signature on an unreadable field.
		existing.CacheMAC, _ = common.RDB.HGet(ctx, cacheKey, "CacheMAC").Result()
	}
	existing.Key = token.Key
	existingMAC, macErr := existing.cacheMAC()
	repair := readErr != nil || macErr != nil || !validAuthCacheMAC(existingMAC, existing.CacheMAC, existing.CacheExpiresAt)
	token.CacheExpiresAt = time.Now().Add(time.Duration(tokenCacheTTLSeconds()) * time.Second).UnixMilli()
	mac, err := token.cacheMAC()
	if err != nil {
		return 0, err
	}
	const script = `
local incoming = tonumber(ARGV[19])
local floor = tonumber(redis.call('GET', KEYS[4]) or '0')
if ARGV[20] == '1' then
  if ARGV[21] == '' or redis.call('GET', KEYS[3]) ~= ARGV[21] then return -1 end
  if floor > incoming then return -1 end
  redis.call('SET', KEYS[4], ARGV[19])
  redis.call('DEL', KEYS[3])
else
  if redis.call('EXISTS', KEYS[3]) == 1 or floor ~= incoming then return 0 end
end
if redis.call('EXISTS', KEYS[2]) == 1 then
  return 0
end
local exists = redis.call('EXISTS', KEYS[1])
if exists == 1 then
  if redis.call('HGET', KEYS[1], 'CacheRevision') ~= ARGV[19] then
    redis.call('DEL', KEYS[1])
    exists = 0
  elseif ARGV[28] ~= 'true' or (redis.call('HGET', KEYS[1], 'CacheMAC') or '') ~= ARGV[29] then
    redis.call('EXPIRE', KEYS[1], ARGV[18])
    return 2
  end
end
redis.call('HDEL', KEYS[1], 'DeletedAt')
redis.call('HSET', KEYS[1],
  'Id', ARGV[1], 'UserId', ARGV[2], 'Status', ARGV[3], 'Name', ARGV[4],
  'CreatedTime', ARGV[5], 'ExpiredTime', ARGV[7],
  'UnlimitedQuota', ARGV[8], 'ModelLimitsEnabled', ARGV[9], 'ModelLimits', ARGV[10],
  'AllowIps', ARGV[11], 'Group', ARGV[12], 'CrossGroupRetry', ARGV[13],
  'AutoGroups', ARGV[14],
  'AllowUserQuotaQuery', ARGV[17], 'CacheRevision', ARGV[19],
  'SmartRouting', ARGV[22], 'RoutingStrategy', ARGV[23],
  'NoRetryWithinGroup', ARGV[24], 'ProjectLabel', ARGV[25],
  'CacheExpiresAt', ARGV[26], 'CacheMAC', ARGV[27])
for _, counter in ipairs({{'AccessedTime', ARGV[6]}, {'RemainQuota', ARGV[15]}, {'UsedQuota', ARGV[16]}}) do
  if not string.match(redis.call('HGET', KEYS[1], counter[1]) or '', '^-?%d+$') then
    redis.call('HSET', KEYS[1], counter[1], counter[2])
  end
end
redis.call('EXPIRE', KEYS[1], ARGV[18])
if exists == 1 then return 2 end
return 1`

	result, err := common.RDB.Eval(ctx, script, []string{
		getTokenCacheKey(token.Key), getTokenCacheFenceKey(token.Key), pendingKey, revisionKey,
	},
		token.Id, token.UserId, token.Status, token.Name,
		token.CreatedTime, token.AccessedTime, token.ExpiredTime,
		strconv.FormatBool(token.UnlimitedQuota), strconv.FormatBool(token.ModelLimitsEnabled),
		token.ModelLimits, allowIps, token.Group, strconv.FormatBool(token.CrossGroupRetry),
		token.AutoGroups, token.RemainQuota, token.UsedQuota,
		strconv.FormatBool(token.AllowUserQuotaQuery),
		tokenCacheTTLSeconds(), token.CacheRevision, authority, expectedPending,
		strconv.FormatBool(token.SmartRouting), token.RoutingStrategy,
		strconv.FormatBool(token.NoRetryWithinGroup), token.ProjectLabel,
		token.CacheExpiresAt, mac, strconv.FormatBool(repair), existing.CacheMAC,
	).Int()
	if err == nil && result == -1 {
		return 0, ErrPermissionCacheStale
	}
	return result, err
}

// cacheGetTokenByKey 从缓存读取 token；不完整的哈希（如仅有配额字段）会被拒绝。
func cacheGetTokenByKey(key string) (*Token, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var token Token
	if err := common.RedisHGetObj(getTokenCacheKey(key), &token); err != nil {
		return nil, err
	}
	if token.Id <= 0 {
		return nil, fmt.Errorf("token cache is incomplete")
	}
	pending, revision := tokenPermissionKeys(key)
	if err := checkPermissionCacheRevision(pending, revision, token.CacheRevision); err != nil {
		return nil, err
	}
	token.Key = key
	mac, err := token.cacheMAC()
	if err != nil || !validAuthCacheMAC(mac, token.CacheMAC, token.CacheExpiresAt) {
		return nil, fmt.Errorf("token cache integrity check failed")
	}
	return &token, nil
}
