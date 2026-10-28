package model

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func getTokenCacheKey(key string) string {
	return fmt.Sprintf("token:%s", common.GenerateHMAC(key))
}

func getTokenCacheFenceKey(key string) string {
	return fmt.Sprintf("token:fence:%s", common.GenerateHMAC(key))
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

// cacheInitToken publishes a database snapshot only when no mutation fence is
// active and the hash is cold. An existing hash only gets its TTL refreshed:
// a database snapshot may predate a concurrent mutation or cache publication,
// so it must never overwrite a newer live hash. Quota mutations fence this
// derived cache before updating the authoritative database counters.
// 返回值：0=被 fence 拦截，1=完成初始化，2=哈希已存在，仅刷新 TTL。
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
if redis.call('EXISTS', KEYS[1]) == 1 then
  if redis.call('HGET', KEYS[1], 'CacheRevision') == ARGV[19] then
    redis.call('EXPIRE', KEYS[1], ARGV[18])
    return 2
  end
  redis.call('DEL', KEYS[1])
end
redis.call('HSET', KEYS[1],
  'Id', ARGV[1], 'UserId', ARGV[2], 'Status', ARGV[3], 'Name', ARGV[4],
  'CreatedTime', ARGV[5], 'AccessedTime', ARGV[6], 'ExpiredTime', ARGV[7],
  'UnlimitedQuota', ARGV[8], 'ModelLimitsEnabled', ARGV[9], 'ModelLimits', ARGV[10],
  'AllowIps', ARGV[11], 'Group', ARGV[12], 'CrossGroupRetry', ARGV[13],
  'AutoGroups', ARGV[14], 'RemainQuota', ARGV[15], 'UsedQuota', ARGV[16],
  'AllowUserQuotaQuery', ARGV[17], 'CacheRevision', ARGV[19])
redis.call('EXPIRE', KEYS[1], ARGV[18])
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
	return &token, nil
}
