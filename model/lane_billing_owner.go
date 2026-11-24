package model

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	laneBillingOwnerSource  = "enterprise_lane"
	laneBillingOwnerState   = "lane_owner_v1"
	laneBillingOwnerTimeout = 5 * time.Second
)

var (
	// This error must never be treated as insufficient quota or as permission
	// to choose another payer/owner. It contains no ownership identities.
	ErrBillingRequestOwnerConflict     = errors.New("request already has a different billing owner")
	ErrLaneBillingOwnerUnavailable     = errors.New("lane billing owner requires a verified PostgreSQL store and scope")
	ErrLaneBillingOwnerEvidence        = errors.New("lane billing owner receipt is missing or inconsistent")
	ErrLaneBillingOwnerStore           = errors.New("lane billing owner reservation failed")
	ErrLaneBillingOwnerCommitUncertain = errors.New("lane billing owner commit is uncertain")
)

// LaneBillingOwnerIdentity pins an original finite Key and wallet to a single
// generation and request. The identity must come from trusted authorization,
// not request headers. A fingerprint is never a usable credential.
type LaneBillingOwnerIdentity struct {
	RequestID        string
	LaneID           string
	Epoch            int64
	UserID           int
	TokenID          int
	TokenFingerprint string `json:"-"`
}

// LaneBillingOwnerReceipt is sourced from the store's persisted request row;
// it is not a client quote or a preflight funding snapshot. The marker pins
// wallet/Key; only this linked durable receipt pins the lane/generation.
type LaneBillingOwnerReceipt struct {
	Identity LaneBillingOwnerIdentity
	Held     int
	State    string
}

// LaneBillingReceiptHeld is the only state eligible for reservation replay.
// A held receipt is NOT evidence of permission to dispatch or to refund.
const LaneBillingReceiptHeld = "held"

// LaneBillingOwnerStore is a trusted, transactional backend. Both methods
// must use only the provided transaction; locking reads never write or return
// inferred state, and absent receipts return gorm.ErrRecordNotFound. The
// reserve callback must win a fresh child INSERT and return true only for
// that insert, never a pre-existing row or a legacy reserve replay. It records
// the original finite Key, wallet, generation, and dual hold without touching
// the canonical row. Funding/policy validation belongs in this transaction.
// A malicious or buggy store can lie about its own read-back; live use requires
// a concrete audited backend, with no independent legacy writers. There is no
// default store, migration, gateway wiring, or startup activation.
type LaneBillingOwnerStore interface {
	LockLaneBillingReceiptTx(tx *gorm.DB, requestID string) (LaneBillingOwnerReceipt, error)
	ReserveLaneBillingReceiptTx(tx *gorm.DB, id LaneBillingOwnerIdentity, target int) (freshChild bool, err error)
}

// LaneBillingOwnerReservation exists only after a successful COMMIT ACK. A
// replay is read-only and never a new dispatch grant; dispatch requires its
// separate, single-use durable claim even when this result is fresh.
type LaneBillingOwnerReservation struct {
	Receipt LaneBillingOwnerReceipt
	Fresh   bool
}

func isLaneBillingOwnerMarker(r BillingFinalization) bool {
	// These fields mark foreign evidence for the ordinary reservation/read/claim paths.
	return r.Source == laneBillingOwnerSource || r.State == laneBillingOwnerState
}

func laneBillingOwnerIDValid(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

type laneBillingFreshClaimContextKey struct{}

type laneBillingFreshClaim struct {
	identity LaneBillingOwnerIdentity
	target   int
	tx       *gorm.DB
}

// ReserveLaneBillingOwner is an internal kernel with no existing runtime
// caller. The original ordinary owner PK arbitrates both modes. This call
// owns the top-level transaction so errors roll back marker, receipt, and
// accounting together; never call it inside a partially committed operation.
// A backend must prove a deploy/visibility/escrow contract before being wired
// in. No error from an attempted reservation permits a second billing path.
func ReserveLaneBillingOwner(ctx context.Context, db *gorm.DB, id LaneBillingOwnerIdentity, target int, store LaneBillingOwnerStore) (LaneBillingOwnerReservation, error) {
	var out LaneBillingOwnerReservation
	if ctx == nil || db == nil || db.Config == nil || db.Dialector == nil || db.Statement == nil || db.DryRun || store == nil ||
		db.Dialector.Name() != "postgres" || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) ||
		!laneBillingOwnerIDValid(id.RequestID) || !laneBillingOwnerIDValid(id.LaneID) || id.Epoch <= 0 || id.UserID <= 0 || id.TokenID <= 0 ||
		len(id.TokenFingerprint) != 64 || id.TokenFingerprint != strings.ToLower(id.TokenFingerprint) || target <= 0 || target > common.MaxQuota {
		return out, ErrLaneBillingOwnerUnavailable
	}
	if _, err := hex.DecodeString(id.TokenFingerprint); err != nil {
		return out, ErrLaneBillingOwnerUnavailable
	}
	_, nestedStatement := db.Statement.ConnPool.(gorm.TxCommitter)
	_, nestedConfig := db.Config.ConnPool.(gorm.TxCommitter)
	if nestedStatement || nestedConfig {
		return out, ErrLaneBillingOwnerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	reader := db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard})
	prepared := false
	err := reader.Transaction(func(tx *gorm.DB) error {
		marker := BillingFinalization{RequestID: id.RequestID, UserID: id.UserID, Source: laneBillingOwnerSource,
			TokenID: id.TokenID, TokenFingerprint: id.TokenFingerprint, State: laneBillingOwnerState}
		claim := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&marker)
		if claim.Error != nil {
			return claim.Error
		}
		var owner BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", id.RequestID).Take(&owner).Error; err != nil {
			return err
		}
		if !sameBillingIdentity(marker, owner) || owner.State != laneBillingOwnerState || owner.ActualQuota != 0 || owner.TaskID != 0 ||
			owner.TaskPublicID != "" || owner.SettlementRecovery != "" || owner.SettlementLogRetryAt != 0 || owner.NextRetryAt != 0 {
			return ErrBillingRequestOwnerConflict
		}
		fresh := claim.RowsAffected == 1
		receipt, err := store.LockLaneBillingReceiptTx(tx, id.RequestID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if fresh {
			if err == nil {
				return ErrLaneBillingOwnerEvidence // no foreign history adoption
			}
			// The in-package concrete store refuses direct writers without
			// this fresh canonical claim, bound to this exact transaction.
			freshClaim := &laneBillingFreshClaim{identity: id, target: target}
			claimCtx := context.WithValue(tx.Statement.Context, laneBillingFreshClaimContextKey{}, freshClaim)
			freshClaim.tx = tx.WithContext(claimCtx)
			freshChild, reserveErr := store.ReserveLaneBillingReceiptTx(freshClaim.tx, id, target)
			if reserveErr != nil {
				return reserveErr
			}
			if !freshChild {
				return ErrLaneBillingOwnerEvidence
			}
			receipt, err = store.LockLaneBillingReceiptTx(tx, id.RequestID)
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrLaneBillingOwnerEvidence // never recreate a missing owner
		}
		if err != nil {
			return err
		}
		if receipt.Identity != id || receipt.Held != target || receipt.State != LaneBillingReceiptHeld {
			return ErrLaneBillingOwnerEvidence
		}
		out = LaneBillingOwnerReservation{Receipt: receipt, Fresh: fresh}
		prepared = true
		return nil
	})
	if err == nil {
		return out, nil
	}
	if prepared {
		return LaneBillingOwnerReservation{}, ErrLaneBillingOwnerCommitUncertain
	}
	if ctx.Err() != nil {
		return LaneBillingOwnerReservation{}, ctx.Err()
	}
	switch {
	case errors.Is(err, ErrBillingRequestOwnerConflict):
		return LaneBillingOwnerReservation{}, ErrBillingRequestOwnerConflict
	case errors.Is(err, ErrLaneBillingOwnerEvidence):
		return LaneBillingOwnerReservation{}, ErrLaneBillingOwnerEvidence
	default:
		return LaneBillingOwnerReservation{}, ErrLaneBillingOwnerStore // never expose SQL/store details
	}
}
