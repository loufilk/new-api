package model

import (
	"context"
	"database/sql"
	"math"
	"net"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A read capability cannot enroll, install custody, reserve or dispatch.
// The issuer, not a request parameter, supplies all handles and epochs.
// CheckReadTx must accept a transaction that is both repeatable read and read-only.
type laneBillingDisplayStore interface {
	CheckReadTx(tx, primary, log *gorm.DB, walletEpoch int64) error
}

type LaneBillingDisplayV1 struct {
	primary, log *gorm.DB
	scope        LaneBillingFiniteKeyIdentity
	walletEpoch  int64
	store        laneBillingDisplayStore
}

// Account Used and Requests are the existing durable, projected statistics.
// The projector updates them in one commit with its acknowledgement. Never
// add slot Spent (or held requests); projected amounts would be counted twice.
type LaneAccountBillingDisplay struct {
	Available, Used, Requests int
}

type LaneTokenBillingDisplay struct {
	Token           Token // key material removed before returning
	Available, Used int
	Account         *LaneAccountBillingDisplay
}

func (r *LaneBillingDisplayV1) snapshot(ctx context.Context, read func(*gorm.DB) error) error {
	if ctx == nil || r.store == nil || !r.scope.valid() || r.walletEpoch <= 0 ||
		r.primary != DB || r.log != LOG_DB || !laneStorePGHandleOK(r.primary) ||
		!common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return ErrLaneBillingOwnerUnavailable
	}
	ctx, done := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer done()
	return r.primary.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard, SkipHooks: true}).
		Transaction(func(tx *gorm.DB) error {
			if err := r.store.CheckReadTx(tx, r.primary, r.log, r.walletEpoch); err != nil {
				return err
			}
			return read(tx)
		}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

// ReadUser is only for a session-authorized self view, with the authenticated
// version supplied by server auth, not a token, identifier or version in a URL.
// Matched scope always claims the read; errors must never turn into raw views.
func (r *LaneBillingDisplayV1) ReadUser(ctx context.Context, userID int, authVersion int64) (LaneAccountBillingDisplay, bool, error) {
	var view LaneAccountBillingDisplay
	if r == nil || userID != r.scope.UserID {
		return view, false, nil
	}
	err := r.snapshot(ctx, func(tx *gorm.DB) error {
		owner, err := readLaneDisplayUserTx(tx, userID)
		if err != nil {
			return err
		}
		if authVersion <= 0 || owner.AuthVersion != authVersion {
			return ErrTokenInvalid
		}
		view, err = readLaneDisplayAccountTx(tx, owner, r.walletEpoch)
		return err
	})
	if err != nil {
		return LaneAccountBillingDisplay{}, true, err
	}
	return view, true, nil
}

// ReadToken only consumes an identity established by the gateway. Match the
// original fingerprint, current Key generation and the account disclosure flag
// in PostgreSQL, not Redis. Match by ID first so replacement of scoped
// credentials can't silently trigger the raw path. Holds count as Used for
// legacy Key compatibility, but never as spendable or account actual usage.
func (r *LaneBillingDisplayV1) ReadToken(ctx context.Context, userID, tokenID int, fingerprint string, ip net.IP) (LaneTokenBillingDisplay, bool, error) {
	var view LaneTokenBillingDisplay
	if r == nil || (userID != r.scope.UserID && tokenID != r.scope.TokenID) {
		return view, false, nil
	}
	// The source owns display for this wallet's custody. An unmatched Key
	// of the same wallet cannot fall back to a raw-only account view.
	if userID != r.scope.UserID || tokenID != r.scope.TokenID || fingerprint != r.scope.TokenFingerprint {
		return view, true, ErrTokenInvalid
	}
	err := r.snapshot(ctx, func(tx *gorm.DB) error {
		owner, err := readLaneDisplayUserTx(tx, userID)
		if err != nil {
			return err
		}
		var tok Token
		if err := tx.Where("id = ? AND user_id = ?", tokenID, userID).Take(&tok).Error; err != nil {
			return err
		}
		if !originalLaneBillingFiniteKey(r.scope, tok) || tok.Status == common.TokenStatusDisabled {
			return ErrTokenInvalid
		}
		if limits := tok.GetIpLimits(); len(limits) > 0 && (ip == nil || !common.IsIpInCIDRList(ip, limits)) {
			return ErrTokenInvalid
		}
		var current laneBillingFiniteKeyEnrollmentV1
		if err := tx.Where("user_id = ? AND token_id = ?", userID, tokenID).Order("epoch DESC").Take(&current).Error; err != nil {
			return err
		}
		if current.identity() != r.scope || current.State != LaneBillingFiniteKeyActive {
			return ErrLaneBillingFiniteKeyState
		}
		custody, err := readLaneFiniteKeyCustodyTx(tx, r.scope, tok.RemainQuota, false)
		if err != nil {
			return err
		}
		if len(custody.slots) == 0 || tok.UsedQuota < 0 ||
			tok.UsedQuota > math.MaxInt-tok.RemainQuota-custody.idle-custody.held-custody.spent {
			return ErrLaneBillingOwnerEvidence
		}
		tok.Clean()
		view.Token = tok
		view.Available = tok.RemainQuota + custody.idle
		view.Used = tok.UsedQuota + custody.held + custody.spent
		if tok.AllowUserQuotaQuery {
			account, err := readLaneDisplayAccountTx(tx, owner, r.walletEpoch)
			if err != nil {
				return err
			}
			view.Account = &account
		}
		return nil
	})
	if err != nil {
		return LaneTokenBillingDisplay{}, true, err
	}
	return view, true, nil
}

func readLaneDisplayUserTx(tx *gorm.DB, userID int) (User, error) {
	var owner User
	err := tx.Select("id", "status", "auth_version", "quota", "quota_paid", "quota_bonus", "quota_reward", "used_quota", "request_count").
		Where("id = ?", userID).Take(&owner).Error
	if err == nil && owner.Status != common.UserStatusEnabled {
		err = ErrTokenInvalid
	}
	return owner, err
}

func readLaneDisplayAccountTx(tx *gorm.DB, owner User, walletEpoch int64) (LaneAccountBillingDisplay, error) {
	if owner.QuotaPaid < 0 || owner.QuotaPaid > common.MaxWalletQuota || owner.QuotaBonus < 0 || owner.QuotaBonus > common.MaxWalletQuota ||
		owner.QuotaReward < 0 || owner.QuotaReward > common.MaxWalletQuota || owner.QuotaPaid+owner.QuotaBonus+owner.QuotaReward != owner.Quota ||
		owner.UsedQuota < 0 || owner.RequestCount < 0 {
		return LaneAccountBillingDisplay{}, ErrLaneBillingOwnerEvidence
	}
	slots, idle, err := readLaneWalletCustodyTx(tx, owner.Id, walletEpoch, owner.Quota, false)
	if err != nil {
		return LaneAccountBillingDisplay{}, err
	}
	if len(slots) == 0 || owner.UsedQuota > math.MaxInt-owner.Quota-idle {
		return LaneAccountBillingDisplay{}, ErrLaneBillingOwnerEvidence
	}
	return LaneAccountBillingDisplay{Available: owner.Quota + idle, Used: owner.UsedQuota, Requests: owner.RequestCount}, nil
}
