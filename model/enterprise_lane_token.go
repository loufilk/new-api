//go:build enterprise_lane_lab

package model

import (
	"crypto/subtle"
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var errEnterpriseLaneTokenIdentity = errors.New("enterprise lane requires the original finite token")

// Escrow is a quota transfer, not spend. The parent transaction owns the user
// and the durable lane grant; both the token and wallet changes must commit or
// roll back with that grant. This does not enable a key for lane admission.
func escrowEnterpriseLaneTokenTx(tx *gorm.DB, identity EnterpriseLaneIdentity, amount int) error {
	if amount <= 0 || amount > common.MaxWalletQuota {
		return errors.New("invalid enterprise lane token allocation")
	}
	token, err := lockEnterpriseLaneTokenTx(tx, identity)
	if err != nil {
		return err
	}
	if token.DeletedAt.Valid || token.Status != common.TokenStatusEnabled ||
		(token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp()) {
		return errEnterpriseLaneTokenIdentity
	}
	if token.RemainQuota < amount {
		return ErrBillingTokenInsufficient
	}
	return writeEnterpriseLaneTokenBudgetTx(tx, token, token.RemainQuota-amount, token.UsedQuota)
}

// Drain returns unused escrow and projects spent quota once, under the caller's
// locked durable drain marker. Settlement must not mutate the legacy token row
// on each request. A deleted original may retain accounting, but is not revived.
func releaseEnterpriseLaneTokenTx(tx *gorm.DB, identity EnterpriseLaneIdentity, remaining, spent int) error {
	if remaining < 0 || spent < 0 || spent > common.MaxWalletQuota || remaining > common.MaxWalletQuota-spent || remaining+spent == 0 {
		return errors.New("invalid enterprise lane token release")
	}
	token, err := lockEnterpriseLaneTokenTx(tx, identity)
	if err != nil {
		return err
	}
	if token.RemainQuota > math.MaxInt-remaining || token.UsedQuota > math.MaxInt-spent {
		return billingRefundReviewError("token_counter_bounds", "token quota counter is out of integer range")
	}
	return writeEnterpriseLaneTokenBudgetTx(tx, token, token.RemainQuota+remaining, token.UsedQuota+spent)
}

func lockEnterpriseLaneTokenTx(tx *gorm.DB, identity EnterpriseLaneIdentity) (Token, error) {
	var token Token
	if tx == nil || identity.UserID <= 0 || identity.TokenID <= 0 || len(identity.TokenFingerprint) != 64 {
		return token, errEnterpriseLaneTokenIdentity
	}
	query := tx.Session(&gorm.Session{NewDB: true}).Unscoped()
	if err := lockForUpdate(query).First(&token, identity.TokenID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return token, errEnterpriseLaneTokenIdentity
		}
		return token, err
	}
	if token.UserId != identity.UserID || token.UnlimitedQuota ||
		subtle.ConstantTimeCompare([]byte(BillingTokenFingerprint(token.Key)), []byte(identity.TokenFingerprint)) != 1 {
		return token, errEnterpriseLaneTokenIdentity
	}
	return token, nil
}

func writeEnterpriseLaneTokenBudgetTx(tx *gorm.DB, token Token, remaining, used int) error {
	if token.CacheRevision < 1 || token.CacheRevision >= maxPermissionRevision {
		return errors.New("invalid enterprise lane token permission revision")
	}
	// Unlike a best-effort cache delete, the existing permission fence stays
	// pending until a current row-locked read publishes the settled revision.
	// This also handles rolled-back writes and ambiguous commit acknowledgments.
	pending, revision := tokenPermissionKeys(token.Key)
	if err := beginPermissionCacheMutation(tx.Statement.Context, pending, getTokenCacheKey(token.Key), revision, false); err != nil {
		return err
	}
	updates := map[string]any{"remain_quota": remaining, "cache_revision": token.CacheRevision + 1}
	if used != token.UsedQuota {
		updates["used_quota"] = used
	}
	result := tx.Session(&gorm.Session{NewDB: true}).Unscoped().Model(&Token{}).
		Where("id = ? AND user_id = ? AND cache_revision = ?", token.Id, token.UserId, token.CacheRevision).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errEnterpriseLaneTokenIdentity
	}
	return nil
}
