package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type cacheQuotaResult int

const (
	cacheQuotaInsufficient cacheQuotaResult = iota
	cacheQuotaOK
	cacheQuotaMiss
)

const userQuotaDeltaScript = `
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= tonumber(ARGV[2])
  or tonumber(redis.call('HGET', KEYS[1], 'CacheSchema') or '0') ~= tonumber(ARGV[3])
  or redis.call('HEXISTS', KEYS[1], 'Quota') == 0 then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'Quota', ARGV[1])
return 1`

const tokenQuotaDeltaScript = `
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= tonumber(ARGV[2])
  or redis.call('HEXISTS', KEYS[1], 'RemainQuota') == 0
  or redis.call('HEXISTS', KEYS[1], 'UsedQuota') == 0 then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'RemainQuota', tonumber(ARGV[1]))
redis.call('HINCRBY', KEYS[1], 'UsedQuota', -tonumber(ARGV[1]))
redis.call('HSET', KEYS[1], 'AccessedTime', ARGV[3])
return 1`

func quotaResultFromLua(result int, err error) (cacheQuotaResult, error) {
	if err != nil {
		return cacheQuotaMiss, err
	}
	switch result {
	case 1:
		return cacheQuotaOK, nil
	case 0:
		return cacheQuotaInsufficient, nil
	default:
		return cacheQuotaMiss, nil
	}
}

func cacheApplyUserQuotaDelta(userID int, delta int64) (cacheQuotaResult, error) {
	result, err := common.RDB.Eval(context.Background(), userQuotaDeltaScript,
		[]string{getUserCacheKey(userID)}, delta, userID, userCacheSchemaVersion).Int()
	return quotaResultFromLua(result, err)
}

func cacheApplyTokenQuotaDelta(id int, key string, delta int64) (cacheQuotaResult, error) {
	result, err := common.RDB.Eval(context.Background(), tokenQuotaDeltaScript,
		[]string{getTokenCacheKey(key)}, delta, id, common.GetTimestamp()).Int()
	return quotaResultFromLua(result, err)
}

// ErrUserQuotaReserveUnavailable 是 user quota reserve API 在 ChuanAPI 下的
// fail-closed 哨兵错误。users.quota 只是 quota_paid+quota_bonus+quota_affiliate
// 的兼容镜像，只能由 CreditQuota*/DebitQuota* 在同一事务内维护；上游对单列
// 直接扣减（含缓存预扣 + 批量落库）的路径没有安全调用条件，一律拒绝。
var ErrUserQuotaReserveUnavailable = errors.New("user quota reserve unavailable: use the DebitQuota ledger path")

// TryReserveUserQuota fail-closed：上游实现直接对 users.quota 单列做缓存/数据库
// 原子扣减；ChuanAPI 下该路径会破坏三账户账本不变量（镜像覆盖、绕过
// bonus_eligible 约束、绕过 balance_ledger 审计）。生产代码没有调用方，
// 预扣必须走 DebitQuota(LedgerEntry{Reason: LedgerReasonBillingPreConsume, ...},
// AccountPaid, AccountBonus) 账本路径。本 API 不触碰余额与缓存，直接拒绝。
func TryReserveUserQuota(id int, quota int) (bool, error) {
	return false, ErrUserQuotaReserveUnavailable
}

func reserveTokenQuotaDB(id int, quota int) (bool, error) {
	result := DB.Model(&Token{}).
		Where("id = ? AND remain_quota >= ?", id, quota).
		Updates(map[string]any{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		})
	return result.RowsAffected == 1, result.Error
}

// TryReserveTokenQuota atomically checks and deducts a token quota. Every token
// mutation writes through to the database, even when usage statistics are
// batched: invalidating a shared token cache must not erase another request's
// unpersisted reservation. Unlimited tokens skip only the balance check.
func TryReserveTokenQuota(id int, key string, quota int, unlimited bool) (bool, error) {
	if quota < 0 {
		return false, errors.New("quota cannot be negative")
	}
	if quota == 0 {
		return true, nil
	}
	if unlimited {
		err := applyTokenQuotaDelta(id, key, -quota)
		return err == nil, err
	}
	// Redis is a derived read cache, never the authorization to spend. The
	// conditional database update also protects against a concurrent cache
	// invalidation while another reservation is waiting to persist.
	if err := invalidateTokenCacheForMutation(key); err != nil {
		common.SysLog("failed to invalidate token quota cache before reservation: " + err.Error())
	}
	return reserveTokenQuotaDB(id, quota)
}

// applyTokenQuotaDelta fences the derived cache and commits the counter before
// returning. No speculative credit or detached cache delta can race a rollback,
// task settlement or rehydration. User/channel usage statistics may still batch.
func applyTokenQuotaDelta(id int, key string, delta int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		return ApplyTokenQuotaDeltaTx(tx, id, key, delta)
	})
}
