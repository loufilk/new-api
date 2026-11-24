//go:build enterprise_lane_lab

package model

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// EnterpriseLaneGatewayMaxLanes bounds the explicitly configured lab bundle
// and its admission statistics. It is not a default lane count.
const EnterpriseLaneGatewayMaxLanes = 32

func enterpriseLaneGatewayManifest(id EnterpriseLaneIdentity, count int) []EnterpriseLaneIdentity {
	lanes := make([]EnterpriseLaneIdentity, count)
	for i := range lanes {
		lanes[i] = id
		lanes[i].LaneID = fmt.Sprintf("gateway-lab-%d-%02d", id.TokenID, i)
		lanes[i].Epoch = 1
	}
	return lanes
}

func enterpriseLaneGatewayTokenValid(token Token) bool {
	return token.Status == common.TokenStatusEnabled &&
		(token.ExpiredTime == -1 || token.ExpiredTime > common.GetTimestamp()) &&
		!token.UnlimitedQuota && !token.SmartRouting && token.Group != "" && !strings.EqualFold(token.Group, "auto")
}

// BootstrapEnterpriseLaneGateway is local-fixture-only. The host validates the
// isolated launch environment and explicitly prepares the tagged schema. A
// funded bundle is never reopened or partially activated on restart. Stop
// closes all active lanes atomically, but neither releases funds nor settles.
func BootstrapEnterpriseLaneGateway(ctx context.Context, db *gorm.DB, tokenID, count, paidBudget, tokenBudget int, stop bool) ([]EnterpriseLaneIdentity, error) {
	if db == nil || db.Dialector.Name() != "postgres" || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return nil, ErrEnterpriseLaneUnavailable
	}
	if tokenID <= 0 || count <= 0 || count > EnterpriseLaneGatewayMaxLanes || paidBudget <= 0 || tokenBudget <= 0 ||
		paidBudget > common.MaxWalletQuota/count || tokenBudget > common.MaxWalletQuota/count {
		return nil, ErrEnterpriseLaneIdentity
	}
	var (
		manifest []EnterpriseLaneIdentity
		userID   int
		funded   bool
	)
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize cold bootstrap/stop attempts even for different Keys.
		// This database-wide PostgreSQL lab lock is never part of ingress.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(77131253285648310)).Error; err != nil {
			return err
		}
		var token Token
		if !stop {
			if err := tx.First(&token, tokenID).Error; err != nil {
				return ErrEnterpriseLaneIdentity
			}
			userID = token.UserId
			// Respect the canonical user->token lock order.
			var user User
			if err := lockForUpdate(tx).Select("id", "status").First(&user, userID).Error; err != nil || user.Status != common.UserStatusEnabled {
				return ErrEnterpriseLaneIdentity
			}
			var current Token
			if err := lockForUpdate(tx).First(&current, tokenID).Error; err != nil ||
				current.UserId != userID || current.Key != token.Key || !enterpriseLaneGatewayTokenValid(current) {
				return ErrEnterpriseLaneIdentity
			}
			token = current
			manifest = enterpriseLaneGatewayManifest(EnterpriseLaneIdentity{UserID: userID, TokenID: tokenID, TokenFingerprint: BillingTokenFingerprint(token.Key)}, count)
		}
		var rows []EnterpriseLane
		// Requests may hold their own row first, so cold bundle transitions
		// lock lanes in deterministic order and never lock request rows here.
		if err := lockForUpdate(tx).Where("state <> ? OR token_id = ?", EnterpriseLaneDrained, tokenID).Order("lane_key").Limit(count + 1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) > count || len(rows) != 0 && len(rows) != count || stop && len(rows) == 0 {
			return ErrEnterpriseLaneState
		}
		if stop {
			// Trust escrow's original identity, never a replacement/deleted Key.
			// Stop changes no money and must remain possible after revocation.
			if rows[0].TokenID != tokenID || enterpriseLaneValidate(tx, rows[0].EnterpriseLaneIdentity) != nil {
				return ErrEnterpriseLaneIdentity
			}
			manifest = enterpriseLaneGatewayManifest(rows[0].EnterpriseLaneIdentity, count)
		}
		for i, lane := range rows {
			if lane.EnterpriseLaneIdentity != manifest[i] || lane.LaneKey != manifest[i].laneKey() || !lane.valid() ||
				lane.PaidBudget != paidBudget || lane.TokenBudget != tokenBudget {
				return ErrEnterpriseLaneIdentity
			}
			if stop {
				if lane.State == EnterpriseLaneOpen {
					lane.State = EnterpriseLaneStopped
					if err := saveEnterpriseLane(tx, lane); err != nil {
						return err
					}
				}
			} else if lane.State != EnterpriseLaneOpen {
				return ErrEnterpriseLaneState
			}
		}
		if stop || len(rows) != 0 {
			return nil
		}
		// New admission may not inherit a legacy generation or task owner.
		// In this isolated fixture use a dedicated never-spent Key, with no
		// other gateway running against the same database during bootstrap.
		var legacyOwners int64
		if err := tx.Model(&BillingFinalization{}).Where("token_id = ?", tokenID).Count(&legacyOwners).Error; err != nil {
			return err
		}
		if token.UsedQuota != 0 || legacyOwners != 0 {
			return ErrEnterpriseLaneState
		}
		if token.RemainQuota < count*tokenBudget {
			return ErrEnterpriseLaneCapacity
		}
		for _, identity := range manifest {
			// Create's nested transaction uses a savepoint; the original paid
			// and finite-Key bundle still commits in the outer transaction.
			if _, err := CreateEnterpriseLane(ctx, tx, identity, paidBudget, tokenBudget); err != nil {
				return err
			}
			funded = true
		}
		return nil
	})
	if funded {
		// Refresh after the OUTER commit or an uncertain completion too.
		RefreshUserQuotaCacheAfterBillingFinalization(userID)
	}
	if err != nil || stop {
		return nil, err
	}
	return manifest, nil
}

// AuthorizeEnterpriseLaneGateway checks fresh original-token and user
// authority without locking the canonical quota rows on the hot path. The
// separate lane-state lock still decides whether reserve/dispatch can proceed.
func AuthorizeEnterpriseLaneGateway(ctx context.Context, db *gorm.DB, id EnterpriseLaneIdentity, revision int64) error {
	if err := enterpriseLaneValidate(db, id); err != nil {
		return err
	}
	if revision <= 0 {
		return ErrEnterpriseLaneIdentity
	}
	var current struct {
		Token
		UserStatus int
	}
	err := db.WithContext(ctx).Model(&Token{}).
		Select("tokens.*, users.status AS user_status").
		Joins("JOIN users ON users.id = tokens.user_id AND users.deleted_at IS NULL").
		Where("tokens.id = ? AND tokens.user_id = ?", id.TokenID, id.UserID).Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrEnterpriseLaneIdentity
	}
	if err != nil {
		return ErrEnterpriseLaneUnavailable
	}
	if !enterpriseLaneGatewayTokenValid(current.Token) || current.UserStatus != common.UserStatusEnabled ||
		current.CacheRevision != revision || subtle.ConstantTimeCompare([]byte(BillingTokenFingerprint(current.Key)), []byte(id.TokenFingerprint)) != 1 {
		return ErrEnterpriseLaneIdentity
	}
	return nil
}
