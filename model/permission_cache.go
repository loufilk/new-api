package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Permission revisions are independent of login AuthVersion: subscription
// membership changes must revoke group authority without logging out sessions.
// A writer owns the DB row lock before publishing a non-expiring pending marker.
// Only a fresh row-locked read may seed a missing floor or clear pending. Thus a
// rollback/crash is repaired by a cold read, never by a timer or an old snapshot.
// Lock order is namespace (when needed), DB row, then bounded Redis operations;
// the recovery path never obtains the namespace lock.
const permissionCacheTimeout = 2 * time.Second
const permissionCacheReadTimeout = 5 * time.Second
const maxPermissionRevision int64 = 1<<53 - 1 // exact Redis Lua integer range

var ErrPermissionCacheStale = fmt.Errorf("%w: stale permission cache revision", ErrUserAuthCachePending)

func userPermissionKeys(id int) (pending, revision string) {
	return fmt.Sprintf("permission:user:pending:%d", id), fmt.Sprintf("permission:user:revision:%d", id)
}

func tokenPermissionKeys(key string) (pending, revision string) {
	fingerprint := common.GenerateHMAC(key)
	return "permission:token:pending:" + fingerprint, "permission:token:revision:" + fingerprint
}

func beginPermissionCacheMutation(ctx context.Context, pending, hash, revision string, preserveQuota bool) error {
	if !common.RedisEnabled {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, permissionCacheTimeout)
	defer cancel()
	// User quota remains owned by the existing atomic quota paths. Keeping its
	// hash is safe: readers and full/field writers all check pending before use.
	const script = `
redis.call('SET', KEYS[1], ARGV[2])
redis.call('DEL', KEYS[3])
if ARGV[1] == '0' then redis.call('DEL', KEYS[2]) end
return 1`
	keep := "0"
	if preserveQuota {
		keep = "1"
	}
	return common.RDB.Eval(ctx, script, []string{pending, hash, revision}, keep, common.GetUUID()).Err()
}

func advanceUserPermissionRevisionTx(tx *gorm.DB, id int) error {
	var user User
	if err := lockForUpdate(tx.Unscoped()).Select("id", "cache_revision").First(&user, id).Error; err != nil {
		return err
	}
	if user.CacheRevision < 1 || user.CacheRevision >= maxPermissionRevision {
		return fmt.Errorf("invalid user permission revision")
	}
	pending, revision := userPermissionKeys(id)
	if err := beginPermissionCacheMutation(tx.Statement.Context, pending, getUserCacheKey(id), revision, true); err != nil {
		return err
	}
	return tx.Unscoped().Model(&User{}).Where("id = ?", id).Update("cache_revision", user.CacheRevision+1).Error
}

func advanceTokenPermissionRevisionTx(tx *gorm.DB, id int) error {
	var token Token
	if err := lockForUpdate(tx).Select("id", commonKeyCol, "cache_revision").First(&token, id).Error; err != nil {
		return err
	}
	if token.CacheRevision < 1 || token.CacheRevision >= maxPermissionRevision {
		return fmt.Errorf("invalid token permission revision")
	}
	pending, revision := tokenPermissionKeys(token.Key)
	if err := beginPermissionCacheMutation(tx.Statement.Context, pending, getTokenCacheKey(token.Key), revision, false); err != nil {
		return err
	}
	return tx.Model(&Token{}).Where("id = ?", id).Update("cache_revision", token.CacheRevision+1).Error
}

// The claim must never be retried: replaying its SET after a Redis flush
// could resurrect an old publication lease. go-redis v9's per-command NoRetry
// contract keeps the existing pool, TLS, credentials and global retry policy.
type permissionClaimCommand struct{ *redis.StatusCmd }

func (*permissionClaimCommand) NoRetry() bool { return true }

var _ redis.Cmder = (*permissionClaimCommand)(nil)

// Called only with the DB row locked. An acknowledged, non-empty unique claim
// is mandatory even on a cold cache. A failed/unknown SET is not followed by
// publication; its late arrival can only cause a cold read, never old authority.
func claimPermissionCacheAuthority(ctx context.Context, key string) (string, error) {
	nonce := common.GetUUID()
	cmd := &permissionClaimCommand{redis.NewStatusCmd(ctx, "set", key, nonce)}
	if err := common.RDB.Process(ctx, cmd); err != nil {
		return "", err
	}
	return nonce, nil
}

// Check after reading the hash. A mutation begun since that read makes the
// snapshot unusable. Missing floors (including Redis flush/restart) are cold
// misses, not permission for an arbitrary writer to establish authority.
func getPermissionCacheRevision(pending, revision string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), permissionCacheTimeout)
	defer cancel()
	const script = `
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
return redis.call('GET', KEYS[2]) or '0'`
	current, err := common.RDB.Eval(ctx, script, []string{pending, revision}).Int64()
	if err != nil {
		return 0, err
	}
	if current < 1 || current > maxPermissionRevision {
		return 0, ErrPermissionCacheStale
	}
	return current, nil
}

func checkPermissionCacheRevision(pending, revision string, incoming int64) error {
	current, err := getPermissionCacheRevision(pending, revision)
	if err != nil {
		return err
	}
	if current != incoming {
		return ErrPermissionCacheStale
	}
	return nil
}

// readUserPermissionAuthority is a cold/uncertain-path operation only. Holding
// the row lock prevents reconciliation from clearing an in-flight writer's
// pending marker. Redis errors never authorize its old hash; the current DB
// row is still authoritative, and a retained pending marker is safe to retry.
func readUserPermissionAuthority(userID int, includeQuota bool) (*UserBase, error) {
	ctx, cancel := context.WithTimeout(context.Background(), permissionCacheReadTimeout)
	defer cancel()
	var user User
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if user.CacheRevision < 1 || user.CacheRevision > maxPermissionRevision {
			return ErrPermissionCacheStale
		}
		err := writeUserCacheAtAuthority(ctx, user.ToBaseUser(), includeQuota, true)
		if errors.Is(err, ErrPermissionCacheStale) || errors.Is(err, ErrUserAuthCachePending) {
			return err
		}
		// Failure to update a derived cache cannot make a freshly locked DB row stale.
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user.ToBaseUser(), nil
}
