package model

import (
	"context"
	"database/sql"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// LaneKeyAvailabilityV1 is a Key availability reader, not relay/funding
// authority. It deliberately has no untagged issuer or configuration hookup.
// Once a service has release/store and all-writer fences, its issuer must pin
// the current store and original generation and revalidate them on this read.
type LaneKeyAvailabilityV1 struct {
	db          *gorm.DB
	key         LaneBillingFiniteKeyIdentity
	verifyStore func(*gorm.DB) error
}

// Validate reports whether the gateway-selected scope owns this decision. An
// enrolled scope NEVER falls back to cached/raw quota or the legacy exhausted
// mutation on a failed proof, even if a stale cache says RemainQuota > 0.
// This snapshot neither reserves nor transfers any amount.
func (a *LaneKeyAvailabilityV1) Validate(ctx context.Context, seen *Token, actor *UserBase, groupVersion int64) (bool, error) {
	if a == nil || seen == nil || seen.Id != a.key.TokenID {
		return false, nil
	}
	if ctx == nil || a.verifyStore == nil || !a.key.valid() || a.db != DB || !laneStorePGHandleOK(a.db) ||
		!common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return true, ErrLaneBillingOwnerUnavailable
	}
	if actor == nil || actor.Id != a.key.UserID || !originalLaneBillingFiniteKey(a.key, *seen) {
		return true, ErrTokenInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	var spendable bool
	err := a.db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard, SkipHooks: true}).
		Transaction(func(tx *gorm.DB) error {
			if err := a.verifyStore(tx); err != nil {
				return err
			}
			if err := validateGroupConfigVersionTx(tx, &groupVersion); err != nil {
				return err
			}
			var owner User
			if err := tx.Select("id", "status", "auth_version", "cache_revision", "group").
				Where("id = ?", a.key.UserID).Take(&owner).Error; err != nil {
				return err
			}
			var token Token
			if err := tx.Select("id", "user_id", "key", "status", "cache_revision", "expired_time",
				"group", "unlimited_quota", "smart_routing", "cross_group_retry", "remain_quota",
				"model_limits_enabled", "model_limits", "allow_ips").
				Where("id = ?", a.key.TokenID).Take(&token).Error; err != nil {
				return err
			}
			// Authorize the object the gateway is about to use, including the
			// versions which guard its cached IP, routing and model restrictions.
			// RemainQuota is deliberately NOT compared to Redis.
			if !originalLaneBillingFiniteKey(a.key, token) || !liveLaneBillingFiniteKey(owner, token) ||
				token.CacheRevision <= 0 || owner.AuthVersion <= 0 || owner.CacheRevision <= 0 ||
				token.CacheRevision != seen.CacheRevision || owner.AuthVersion != actor.AuthVersion ||
				owner.CacheRevision != actor.CacheRevision || owner.Group != actor.Group ||
				token.Group == "" || token.Group == "auto" || token.Group != seen.Group ||
				token.SmartRouting || token.CrossGroupRetry || seen.SmartRouting || seen.CrossGroupRetry ||
				token.ExpiredTime != seen.ExpiredTime || token.ModelLimitsEnabled != seen.ModelLimitsEnabled ||
				token.ModelLimits != seen.ModelLimits || !slices.Equal(token.GetIpLimits(), seen.GetIpLimits()) {
				return ErrTokenInvalid
			}
			var enrollment laneBillingFiniteKeyEnrollmentV1
			if err := tx.Where("user_id = ? AND token_id = ?", a.key.UserID, a.key.TokenID).
				Order("epoch DESC").Take(&enrollment).Error; err != nil {
				return err
			}
			if enrollment.identity() != a.key || enrollment.State != LaneBillingFiniteKeyActive {
				return ErrLaneBillingFiniteKeyState
			}
			custody, err := readLaneFiniteKeyCustodyTx(tx, a.key, token.RemainQuota, false)
			if err != nil {
				return err
			}
			if len(custody.slots) == 0 {
				// A scope whose known custody vanished entirely must not
				// masquerade as a valid raw-only enrolled Key.
				return ErrLaneBillingOwnerEvidence
			}
			spendable = token.RemainQuota+custody.idle > 0
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		// The middleware must not reveal which proof or account field failed.
		return true, err
	}
	if !spendable {
		return true, ErrTokenInvalid
	}
	return true, nil
}
