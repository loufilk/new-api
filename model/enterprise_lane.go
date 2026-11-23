//go:build enterprise_lane_lab

package model

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrEnterpriseLaneUnavailable = errors.New("enterprise lane lab requires the isolated PostgreSQL database")
	ErrEnterpriseLaneIdentity    = errors.New("enterprise lane identity or funding mismatch")
	ErrEnterpriseLaneCapacity    = errors.New("enterprise lane paid or finite-token budget exhausted")
	ErrEnterpriseLaneState       = errors.New("enterprise lane transition not permitted")
	ErrEnterpriseLaneExposure    = errors.New("measured usage exceeds the reserved enterprise request cap; review required")
)

const (
	EnterpriseLaneOpen     = "open"
	EnterpriseLaneStopped  = "stopped"
	EnterpriseLaneDrained  = "drained"
	EnterpriseLaneHeld     = "held"
	EnterpriseLaneDispatch = "dispatch_started"
	EnterpriseLaneUnknown  = "delivery_unknown"
	EnterpriseLaneObserved = "observed"
	EnterpriseLaneReview   = "settlement_review"
	EnterpriseLaneSettled  = "settled"
	EnterpriseLaneRefunded = "refunded"

	enterpriseLaneReference = "enterprise_lane_lab"
	enterpriseLaneFund      = "enterprise_lane_fund"
	enterpriseLaneReturn    = "enterprise_lane_return"
)

// EnterpriseLaneIdentity binds a paid partition to one original finite API key
// and epoch. A refilled/replacement partition needs a new funded epoch. Do not
// construct it from unauthenticated request input or log its fingerprint.
type EnterpriseLaneIdentity struct {
	// GORM names this composite for its owning table. In particular, the
	// retained request receipt index covers the cold drain reconciliation scan.
	LaneID           string `gorm:"type:varchar(64);not null;index:,composite:lane_epoch,priority:1"`
	Epoch            int64  `gorm:"not null;index:,composite:lane_epoch,priority:2"`
	UserID           int    `gorm:"not null"`
	TokenID          int    `gorm:"not null"`
	TokenFingerprint string `json:"-" gorm:"type:varchar(64);not null"`
}

// EnterpriseLane is a PostgreSQL lab escrow, deliberately excluded from the
// standard build, migration, wallet API and authorization path. Each budget
// is conserved independently as available + held + spent + returned.
type EnterpriseLane struct {
	LaneKey string `gorm:"type:varchar(85);primaryKey"`
	EnterpriseLaneIdentity
	PaidBudget     int    `gorm:"type:bigint;not null"`
	TokenBudget    int    `gorm:"type:bigint;not null"`
	PaidAvailable  int    `gorm:"type:bigint;not null"`
	TokenAvailable int    `gorm:"type:bigint;not null"`
	Held           int    `gorm:"type:bigint;not null"`
	Spent          int    `gorm:"type:bigint;not null"`
	OpenRequests   int    `gorm:"type:bigint;not null"`
	PaidReturned   int    `gorm:"type:bigint;not null"`
	TokenReturned  int    `gorm:"type:bigint;not null"`
	State          string `gorm:"type:varchar(24);not null"`
	CreatedAt      int64  `gorm:"autoCreateTime"`
	UpdatedAt      int64  `gorm:"autoUpdateTime"`
}

// EnterpriseLaneRequest is the unique, retained owner of the request across
// all lanes/epochs. Held preserves the last cap even after money is released.
// Finalized rows are durable per-request accounting evidence, never a refund
// retry queue. No state expires a possibly delivered request.
type EnterpriseLaneRequest struct {
	RequestID string `gorm:"type:varchar(64);primaryKey;index:idx_enterprise_lane_usage_pending,where:usage_projection_pending"`
	EnterpriseLaneIdentity
	Held                   int                 `gorm:"type:bigint;not null"`
	Actual                 int                 `gorm:"type:bigint;not null"`
	State                  string              `gorm:"type:varchar(24);not null"`
	RefundProof            string              `gorm:"type:varchar(32);not null"`
	Usage                  EnterpriseLaneUsage `gorm:"embedded;embeddedPrefix:usage_"`
	UsageProjectionPending bool                `gorm:"not null;default:false"`
	CreatedAt              int64               `gorm:"autoCreateTime"`
	UpdatedAt              int64               `gorm:"autoUpdateTime"`
}

// EnterpriseLaneModels is an explicit lab fixture opt-in; never add to startup
// migrations. The ordinary service has no route or dependency on these types.
func EnterpriseLaneModels() []any {
	return []any{&EnterpriseLane{}, &EnterpriseLaneRequest{}, &EnterpriseLaneUsageProjection{}}
}

func enterpriseLaneIDValid(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func enterpriseLaneValidate(db *gorm.DB, identity EnterpriseLaneIdentity) error {
	if db == nil || db.Dialector.Name() != "postgres" || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return ErrEnterpriseLaneUnavailable
	}
	if !enterpriseLaneIDValid(identity.LaneID) || identity.Epoch <= 0 || identity.UserID <= 0 || identity.TokenID <= 0 || len(identity.TokenFingerprint) != 64 {
		return ErrEnterpriseLaneIdentity
	}
	if _, err := hex.DecodeString(identity.TokenFingerprint); err != nil {
		return ErrEnterpriseLaneIdentity
	}
	return nil
}

func (id EnterpriseLaneIdentity) laneKey() string {
	return fmt.Sprintf("%s:%d", id.LaneID, id.Epoch)
}

func (id EnterpriseLaneIdentity) walletEntry(amount int, reason string) LedgerEntry {
	return LedgerEntry{UserId: id.UserID, Account: AccountPaid, Delta: amount, Reason: reason,
		ReferenceType: enterpriseLaneReference, ReferenceId: id.laneKey(), IdempotencyKey: reason + ":" + id.laneKey()}
}

func (lane EnterpriseLane) valid() bool {
	if lane.PaidBudget <= 0 || lane.PaidBudget > common.MaxWalletQuota || lane.TokenBudget <= 0 || lane.TokenBudget > common.MaxWalletQuota {
		return false
	}
	for _, n := range []int{lane.PaidAvailable, lane.TokenAvailable, lane.Held, lane.Spent, lane.PaidReturned, lane.TokenReturned, lane.OpenRequests} {
		if n < 0 || n > common.MaxWalletQuota {
			return false
		}
	}
	if lane.PaidAvailable+lane.Held+lane.Spent+lane.PaidReturned != lane.PaidBudget ||
		lane.TokenAvailable+lane.Held+lane.Spent+lane.TokenReturned != lane.TokenBudget ||
		lane.OpenRequests > lane.Held || (lane.OpenRequests == 0) != (lane.Held == 0) {
		return false
	}
	switch lane.State {
	case EnterpriseLaneOpen, EnterpriseLaneStopped:
		return lane.PaidReturned == 0 && lane.TokenReturned == 0
	case EnterpriseLaneDrained:
		return lane.PaidAvailable == 0 && lane.TokenAvailable == 0 && lane.OpenRequests == 0
	default:
		return false
	}
}

func lockEnterpriseLane(tx *gorm.DB, identity EnterpriseLaneIdentity) (EnterpriseLane, error) {
	var lane EnterpriseLane
	if err := lockForUpdate(tx).Where("lane_key = ?", identity.laneKey()).Take(&lane).Error; err != nil {
		return lane, err
	}
	if lane.EnterpriseLaneIdentity != identity || !lane.valid() {
		return lane, ErrEnterpriseLaneIdentity
	}
	return lane, nil
}

func saveEnterpriseLane(tx *gorm.DB, lane EnterpriseLane) error {
	if !lane.valid() {
		return ErrEnterpriseLaneIdentity
	}
	result := tx.Model(&EnterpriseLane{}).Where("lane_key = ?", lane.LaneKey).Updates(map[string]any{
		"paid_available": lane.PaidAvailable, "token_available": lane.TokenAvailable,
		"held": lane.Held, "spent": lane.Spent, "open_requests": lane.OpenRequests,
		"paid_returned": lane.PaidReturned, "token_returned": lane.TokenReturned, "state": lane.State,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseLaneIdentity
	}
	return nil
}

// CreateEnterpriseLane moves real paid funds and finite token remain into one
// durable lab partition. All three operations commit together. Repeating the
// exact original tuple/budgets reads the same lane without another transfer.
// The caller must supply an isolated disposable PostgreSQL fixture, not a live
// wallet, and exclusively choose the lane and its stable epoch.
func CreateEnterpriseLane(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, paidBudget, tokenBudget int) (EnterpriseLane, error) {
	var lane EnterpriseLane
	if err := enterpriseLaneValidate(db, identity); err != nil {
		return lane, err
	}
	if paidBudget <= 0 || paidBudget > common.MaxWalletQuota || tokenBudget <= 0 || tokenBudget > common.MaxWalletQuota {
		return lane, ErrEnterpriseLaneCapacity
	}
	mayChangeWallet := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner User
		if err := lockForUpdate(tx).Select("id", "status").First(&owner, identity.UserID).Error; err != nil {
			return err
		}
		existing, err := lockEnterpriseLane(tx, identity)
		if err == nil {
			if existing.PaidBudget != paidBudget || existing.TokenBudget != tokenBudget {
				return ErrEnterpriseLaneIdentity
			}
			lane = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if owner.Status != common.UserStatusEnabled {
			return ErrEnterpriseLaneIdentity
		}
		debit, err := DebitQuotaTx(tx, identity.walletEntry(paidBudget, enterpriseLaneFund), AccountPaid)
		if err != nil {
			return err
		}
		// A standalone wallet debit cannot bootstrap a new funded partition.
		if debit.Replayed {
			return ErrEnterpriseLaneIdentity
		}
		if err := escrowEnterpriseLaneTokenTx(tx, identity, tokenBudget); err != nil {
			return err
		}
		lane = EnterpriseLane{LaneKey: identity.laneKey(), EnterpriseLaneIdentity: identity,
			PaidBudget: paidBudget, TokenBudget: tokenBudget, PaidAvailable: paidBudget, TokenAvailable: tokenBudget, State: EnterpriseLaneOpen}
		if err := tx.Create(&lane).Error; err != nil {
			return err
		}
		mayChangeWallet = true
		return nil
	})
	if mayChangeWallet {
		// Also invalidate if PostgreSQL committed but lost its acknowledgement.
		RefreshUserQuotaCacheAfterBillingFinalization(identity.UserID)
	}
	if err != nil {
		return EnterpriseLane{}, err
	}
	return lane, nil
}

// StopEnterpriseLane prevents fresh holds, extensions and dispatch claims on
// this epoch. Existing dispatched owners can settle; undispatched ones can
// refund. Stop/Drain never take an owner lock while holding a lane lock.
func StopEnterpriseLane(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity) (EnterpriseLane, error) {
	var lane EnterpriseLane
	if err := enterpriseLaneValidate(db, identity); err != nil {
		return lane, err
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		lane, err = lockEnterpriseLane(tx, identity)
		if err != nil || lane.State != EnterpriseLaneOpen {
			return err
		}
		lane.State = EnterpriseLaneStopped
		return saveEnterpriseLane(tx, lane)
	})
	if err != nil {
		return EnterpriseLane{}, err
	}
	return lane, nil
}

// DrainEnterpriseLane returns only unused original prefunding. Unresolved
// holds/unknown delivery block release without timeout. A lost ACK is safe to
// retry, and no balance is ever redirected to a replacement key or wallet.
func DrainEnterpriseLane(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity) (EnterpriseLane, error) {
	var lane EnterpriseLane
	if err := enterpriseLaneValidate(db, identity); err != nil {
		return lane, err
	}
	mayChangeWallet := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockFinancialUserTx(tx, identity.UserID); err != nil {
			return err
		}
		var err error
		lane, err = lockEnterpriseLane(tx, identity)
		if err != nil || lane.State == EnterpriseLaneDrained {
			return err
		}
		if lane.State != EnterpriseLaneStopped || lane.Held != 0 || lane.OpenRequests != 0 {
			return ErrEnterpriseLaneState
		}
		if err := verifyEnterpriseLanePrefunding(tx, lane); err != nil {
			return err
		}
		if err := verifyEnterpriseLaneTerminalReceipts(tx, lane); err != nil {
			return err
		}
		if err := releaseEnterpriseLaneTokenTx(tx, identity, lane.TokenAvailable, lane.Spent); err != nil {
			return err
		}
		if lane.PaidAvailable > 0 {
			credit, err := CreditQuotaTx(tx, identity.walletEntry(lane.PaidAvailable, enterpriseLaneReturn))
			if err != nil {
				return err
			}
			if credit.Replayed {
				return ErrEnterpriseLaneIdentity
			}
		}
		lane.PaidReturned, lane.TokenReturned = lane.PaidAvailable, lane.TokenAvailable
		lane.PaidAvailable, lane.TokenAvailable, lane.State = 0, 0, EnterpriseLaneDrained
		if err := saveEnterpriseLane(tx, lane); err != nil {
			return err
		}
		mayChangeWallet = lane.PaidReturned > 0
		return nil
	})
	if mayChangeWallet {
		RefreshUserQuotaCacheAfterBillingFinalization(identity.UserID)
	}
	if err != nil {
		return EnterpriseLane{}, err
	}
	return lane, nil
}

func verifyEnterpriseLanePrefunding(tx *gorm.DB, lane EnterpriseLane) error {
	entry := lane.walletEntry(lane.PaidBudget, enterpriseLaneFund)
	entry.DebitOrder = []LedgerAccount{AccountPaid}
	op, rows, err := loadLedgerOperation(lockForUpdate(tx), entry.IdempotencyKey)
	if err != nil {
		return err
	}
	if !verifyReplay(op, entry, ledgerOpDebit) || len(rows) != 1 || rows[0].OperationId != op.Id || rows[0].UserId != lane.UserID ||
		rows[0].Account != string(AccountPaid) || rows[0].Delta != -lane.PaidBudget || rows[0].BalanceBefore-rows[0].BalanceAfter != lane.PaidBudget ||
		rows[0].IdempotencyKey != entry.IdempotencyKey || rows[0].Reason != entry.Reason || rows[0].ReferenceType != entry.ReferenceType || rows[0].ReferenceId != entry.ReferenceId ||
		rows[0].TotalBefore != op.TotalBefore || rows[0].TotalAfter != op.TotalAfter || op.TotalBefore-op.TotalAfter != lane.PaidBudget {
		return ErrEnterpriseLaneIdentity
	}
	return nil
}

// The stopped lane is locked, so no committed financial transition can change
// these totals until drain finishes. Do not lock request rows here: an in-flight
// request may own its row while waiting for this lane. This is a cold lab scan.
func verifyEnterpriseLaneTerminalReceipts(tx *gorm.DB, lane EnterpriseLane) error {
	var summary struct {
		Invalid int64
		Spent   int
	}
	err := tx.Model(&EnterpriseLaneRequest{}).Where("lane_id = ? AND epoch = ?", lane.LaneID, lane.Epoch).
		Select(`COUNT(*) FILTER (WHERE user_id <> ? OR token_id <> ? OR token_fingerprint <> ? OR held <= 0 OR held > ?
			OR NOT ((state = ? AND actual BETWEEN 0 AND held AND refund_proof = '') OR
			(state = ? AND actual = 0 AND refund_proof = ?))) AS invalid,
			COALESCE(SUM(actual) FILTER (WHERE state = ?), 0) AS spent`,
			lane.UserID, lane.TokenID, lane.TokenFingerprint, common.MaxQuota, EnterpriseLaneSettled,
			EnterpriseLaneRefunded, EnterpriseLaneRefundNeverDispatched, EnterpriseLaneSettled).Scan(&summary).Error
	if err != nil {
		return err
	}
	if summary.Invalid != 0 || summary.Spent != lane.Spent {
		return ErrEnterpriseLaneIdentity
	}
	return nil
}
