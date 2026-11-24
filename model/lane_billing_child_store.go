package model

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// The concrete store is deliberately package-private and unregistered. Its
// only installation path is in the separately tagged dev fixture. The actual
// normal relay must validate authoritative funding policy and its final wire
// evidence before it may ever supply this immutable binding.
type laneBillingChildBinding struct {
	Owner               LaneBillingOwnerIdentity
	WalletEpoch         int64
	WalletSlot          int
	KeyEpoch            int64
	KeySlot             int
	ChannelID           int
	FundingPolicySHA256 string
	FrozenPriceSHA256   string
	RouteSHA256         string
	FinalWireSHA256     string
}

func (b laneBillingChildBinding) valid() bool {
	return laneBillingOwnerIDValid(b.Owner.RequestID) && laneBillingOwnerIDValid(b.Owner.LaneID) && b.Owner.Epoch > 0 &&
		b.Owner.UserID > 0 && b.Owner.TokenID > 0 && laneChildSHAValid(b.Owner.TokenFingerprint) && b.WalletEpoch > 0 && b.WalletSlot >= 0 && b.WalletSlot < 32 && b.KeyEpoch > 0 && b.KeySlot >= 0 && b.KeySlot < 32 && b.ChannelID > 0 &&
		laneChildSHAValid(b.FundingPolicySHA256) && laneChildSHAValid(b.FrozenPriceSHA256) && laneChildSHAValid(b.RouteSHA256) && laneChildSHAValid(b.FinalWireSHA256)
}

func laneChildSHAValid(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// laneBillingFiniteKeyShareV1 is separate from the shared wallet and keyed to
// an independent finite Key generation. There is no provisioning, rollover,
// or reclaim API here; the dev fixture supplies isolated pre-existing shares.
type laneBillingFiniteKeyShareV1 struct {
	UserID           int    `gorm:"primaryKey;autoIncrement:false"`
	TokenID          int    `gorm:"primaryKey;autoIncrement:false"`
	Epoch            int64  `gorm:"primaryKey;autoIncrement:false"`
	Slot             int    `gorm:"primaryKey;autoIncrement:false"`
	TokenFingerprint string `gorm:"type:varchar(64);not null" json:"-"`
	Granted          int    `gorm:"type:bigint;not null"`
	Available        int    `gorm:"type:bigint;not null"`
	Held             int    `gorm:"type:bigint;not null"`
	Spent            int    `gorm:"type:bigint;not null"`
	Returned         int    `gorm:"type:bigint;not null"`
	Version          int64  `gorm:"type:bigint;not null"`
}

func (laneBillingFiniteKeyShareV1) TableName() string { return "lane_billing_key_shares_v1" }

func (s laneBillingFiniteKeyShareV1) valid() bool {
	if s.UserID <= 0 || s.TokenID <= 0 || s.Epoch <= 0 || s.Slot < 0 || s.Slot >= 32 || s.Granted <= 0 || s.Granted > common.MaxWalletQuota ||
		!laneChildSHAValid(s.TokenFingerprint) || s.Version < 1 || s.Version >= common.MaxWalletQuota {
		return false
	}
	left := s.Granted
	for _, n := range []int{s.Available, s.Held, s.Spent, s.Returned} {
		if n < 0 || n > left {
			return false
		}
		left -= n
	}
	return left == 0
}

const (
	laneBillingChildDispatch = "dispatch_started"
	laneBillingChildUnknown  = "delivery_unknown"
	laneBillingChildObserved = "observed"
	laneBillingChildReview   = "settlement_review"
	laneBillingChildSettled  = "settled"
	laneBillingChildRefunded = "refunded"
)

// laneBillingChildReceiptV1 contains no bearer material or executable task
// fields. Projection/accounting must read it separately from ordinary money.
type laneBillingChildReceiptV1 struct {
	RequestID           string `gorm:"type:varchar(64);primaryKey"`
	LaneID              string `gorm:"type:varchar(64);not null"`
	Epoch               int64  `gorm:"not null"`
	UserID              int    `gorm:"not null"`
	TokenID             int    `gorm:"not null"`
	TokenFingerprint    string `gorm:"type:varchar(64);not null" json:"-"`
	WalletEpoch         int64  `gorm:"not null"`
	WalletSlot          int    `gorm:"not null"`
	KeyEpoch            int64  `gorm:"not null"`
	KeySlot             int    `gorm:"not null"`
	ChannelID           int    `gorm:"not null"`
	FundingPolicySHA256 string `gorm:"type:varchar(64);not null"`
	FrozenPriceSHA256   string `gorm:"type:varchar(64);not null"`
	RouteSHA256         string `gorm:"type:varchar(64);not null"`
	FinalWireSHA256     string `gorm:"type:varchar(64);not null"`
	Cap                 int    `gorm:"type:bigint;not null"`
	Actual              int    `gorm:"type:bigint;not null"`
	UsageSHA256         string `gorm:"type:varchar(64);not null"`
	State               string `gorm:"type:varchar(24);not null"`
}

func (laneBillingChildReceiptV1) TableName() string { return "lane_billing_child_receipts_v1" }

func (r laneBillingChildReceiptV1) binding() laneBillingChildBinding {
	return laneBillingChildBinding{
		Owner:       LaneBillingOwnerIdentity{RequestID: r.RequestID, LaneID: r.LaneID, Epoch: r.Epoch, UserID: r.UserID, TokenID: r.TokenID, TokenFingerprint: r.TokenFingerprint},
		WalletEpoch: r.WalletEpoch, WalletSlot: r.WalletSlot, KeyEpoch: r.KeyEpoch, KeySlot: r.KeySlot, ChannelID: r.ChannelID,
		FundingPolicySHA256: r.FundingPolicySHA256, FrozenPriceSHA256: r.FrozenPriceSHA256, RouteSHA256: r.RouteSHA256, FinalWireSHA256: r.FinalWireSHA256,
	}
}

func (r laneBillingChildReceiptV1) valid() bool {
	if r.Cap <= 0 || r.Cap > common.MaxQuota || r.Actual < 0 || r.Actual > common.MaxQuota {
		return false
	}
	switch r.State {
	case LaneBillingReceiptHeld, laneBillingChildDispatch, laneBillingChildUnknown, laneBillingChildRefunded:
		return r.Actual == 0 && r.UsageSHA256 == ""
	case laneBillingChildObserved, laneBillingChildSettled:
		return r.Actual <= r.Cap && laneChildSHAValid(r.UsageSHA256)
	case laneBillingChildReview:
		return r.Actual > r.Cap && laneChildSHAValid(r.UsageSHA256)
	default:
		return false
	}
}

var (
	errLaneBillingChildCapacity = errors.New("locked lane slots cannot cover the requested exposure")
	errLaneBillingChildState    = errors.New("lane child transition is not authorized")
	errLaneBillingChildExposure = errors.New("measured lane usage exceeds held exposure; review required")
)

type laneBillingPGChildStore struct{ binding laneBillingChildBinding }

func (s *laneBillingPGChildStore) LockLaneBillingReceiptTx(tx *gorm.DB, requestID string) (LaneBillingOwnerReceipt, error) {
	if !s.binding.valid() || requestID != s.binding.Owner.RequestID {
		return LaneBillingOwnerReceipt{}, ErrLaneBillingOwnerEvidence
	}
	var row laneBillingChildReceiptV1
	if err := lockForUpdate(tx).Where("request_id = ?", requestID).Take(&row).Error; err != nil {
		return LaneBillingOwnerReceipt{}, err
	}
	if row.binding() != s.binding || !row.valid() {
		return LaneBillingOwnerReceipt{}, ErrLaneBillingOwnerEvidence
	}
	return LaneBillingOwnerReceipt{Identity: row.binding().Owner, Held: row.Cap, State: row.State}, nil
}

func (s *laneBillingPGChildStore) ReserveLaneBillingReceiptTx(tx *gorm.DB, id LaneBillingOwnerIdentity, target int) (bool, error) {
	b := s.binding
	if tx == nil || tx.Statement == nil || tx.Statement.Context == nil {
		return false, ErrLaneBillingOwnerEvidence
	}
	claim, fresh := tx.Statement.Context.Value(laneBillingFreshClaimContextKey{}).(*laneBillingFreshClaim)
	if !fresh || claim == nil || claim.tx != tx || claim.identity != id || claim.target != target || id != b.Owner || !b.valid() {
		return false, ErrLaneBillingOwnerEvidence
	}
	row := laneBillingChildReceiptV1{RequestID: id.RequestID, LaneID: id.LaneID, Epoch: id.Epoch, UserID: id.UserID, TokenID: id.TokenID, TokenFingerprint: id.TokenFingerprint,
		WalletEpoch: b.WalletEpoch, WalletSlot: b.WalletSlot, KeyEpoch: b.KeyEpoch, KeySlot: b.KeySlot, ChannelID: b.ChannelID,
		FundingPolicySHA256: b.FundingPolicySHA256, FrozenPriceSHA256: b.FrozenPriceSHA256, RouteSHA256: b.RouteSHA256, FinalWireSHA256: b.FinalWireSHA256,
		Cap: target, State: LaneBillingReceiptHeld}
	if !row.valid() {
		return false, ErrLaneBillingOwnerEvidence
	}
	insert := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&row)
	if insert.Error != nil || insert.RowsAffected != 1 {
		return false, insert.Error
	}
	if err := changeLaneChildSlots(tx, b, target, 0, false); err != nil {
		return false, err
	}
	return true, nil
}

// changeLaneChildSlots never allocates from the canonical wallet or token.
// It owns the exact shared-wallet then finite-Key locks inside its caller's
// canonical/child transaction. Returning an error must roll back the caller.
func changeLaneChildSlots(tx *gorm.DB, b laneBillingChildBinding, cap, actual int, finish bool) error {
	if cap <= 0 || cap > common.MaxQuota || actual < 0 || actual > cap || (!finish && actual != 0) {
		return ErrLaneBillingOwnerEvidence
	}
	var wallet SharedWalletShare
	if err := lockForUpdate(tx).Where("user_id = ? AND epoch = ? AND slot = ?", b.Owner.UserID, b.WalletEpoch, b.WalletSlot).Take(&wallet).Error; err != nil {
		return err
	}
	if !wallet.valid() {
		return ErrLaneBillingOwnerEvidence
	}
	if !finish && wallet.Available < cap || finish && wallet.Held < cap {
		return errLaneBillingChildCapacity
	}
	walletVersion := wallet.Version
	if finish {
		wallet.Held -= cap
		wallet.Available += cap - actual
		wallet.Spent += actual
	} else {
		wallet.Available -= cap
		wallet.Held += cap
	}
	wallet.Version++
	if !wallet.valid() {
		return ErrLaneBillingOwnerEvidence
	}
	walletWrite := tx.Model(&SharedWalletShare{}).Where("user_id = ? AND epoch = ? AND slot = ? AND version = ?", wallet.UserID, wallet.Epoch, wallet.Slot, walletVersion).
		UpdateColumns(map[string]any{"available": wallet.Available, "held": wallet.Held, "spent": wallet.Spent, "version": wallet.Version})
	if walletWrite.Error != nil {
		return walletWrite.Error
	}
	if walletWrite.RowsAffected != 1 {
		return ErrLaneBillingOwnerEvidence
	}

	var key laneBillingFiniteKeyShareV1
	if err := lockForUpdate(tx).Where("user_id = ? AND token_id = ? AND epoch = ? AND slot = ?", b.Owner.UserID, b.Owner.TokenID, b.KeyEpoch, b.KeySlot).Take(&key).Error; err != nil {
		return err
	}
	if !key.valid() || subtle.ConstantTimeCompare([]byte(key.TokenFingerprint), []byte(b.Owner.TokenFingerprint)) != 1 {
		return ErrLaneBillingOwnerEvidence
	}
	if !finish && key.Available < cap || finish && key.Held < cap {
		return errLaneBillingChildCapacity
	}
	keyVersion := key.Version
	if finish {
		key.Held -= cap
		key.Available += cap - actual
		key.Spent += actual
	} else {
		key.Available -= cap
		key.Held += cap
	}
	key.Version++
	if !key.valid() {
		return ErrLaneBillingOwnerEvidence
	}
	keyWrite := tx.Model(&laneBillingFiniteKeyShareV1{}).Where("user_id = ? AND token_id = ? AND epoch = ? AND slot = ? AND version = ?", key.UserID, key.TokenID, key.Epoch, key.Slot, keyVersion).
		UpdateColumns(map[string]any{"available": key.Available, "held": key.Held, "spent": key.Spent, "version": key.Version})
	if keyWrite.Error != nil {
		return keyWrite.Error
	}
	if keyWrite.RowsAffected != 1 {
		return ErrLaneBillingOwnerEvidence
	}
	return nil
}

// transitionLaneBillingChild locks the canonical owner and exact immutable
// child before any state/grant mutation. No successful COMMIT ACK means no
// dispatch authorization, even if the database actually accepted the claim.
func transitionLaneBillingChild(ctx context.Context, db *gorm.DB, b laneBillingChildBinding, apply func(*gorm.DB, *laneBillingChildReceiptV1) error) error {
	if ctx == nil || db == nil || db.Config == nil || db.Dialector == nil || db.Statement == nil || db.DryRun ||
		db.Dialector.Name() != "postgres" || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) || !b.valid() {
		return ErrLaneBillingOwnerUnavailable
	}
	_, nestedStatement := db.Statement.ConnPool.(gorm.TxCommitter)
	_, nestedConfig := db.Config.ConnPool.(gorm.TxCommitter)
	if nestedStatement || nestedConfig {
		return ErrLaneBillingOwnerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	prepared := false
	err := db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		var marker BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", b.Owner.RequestID).Take(&marker).Error; err != nil {
			return err
		}
		m := BillingFinalization{RequestID: b.Owner.RequestID, Source: laneBillingOwnerSource, UserID: b.Owner.UserID, TokenID: b.Owner.TokenID, TokenFingerprint: b.Owner.TokenFingerprint}
		if !sameBillingIdentity(marker, m) || marker.State != laneBillingOwnerState || marker.ActualQuota != 0 || marker.TaskID != 0 || marker.TaskPublicID != "" ||
			marker.SettlementRecovery != "" || marker.SettlementLogRetryAt != 0 || marker.NextRetryAt != 0 {
			return ErrBillingRequestOwnerConflict
		}
		var row laneBillingChildReceiptV1
		if err := lockForUpdate(tx).Where("request_id = ?", b.Owner.RequestID).Take(&row).Error; err != nil {
			return err
		}
		if row.binding() != b || !row.valid() {
			return ErrLaneBillingOwnerEvidence
		}
		if err := apply(tx, &row); err != nil {
			return err
		}
		prepared = true
		return nil
	})
	if err == nil {
		return nil
	}
	if prepared {
		return ErrLaneBillingOwnerCommitUncertain
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case errors.Is(err, ErrBillingRequestOwnerConflict):
		return ErrBillingRequestOwnerConflict
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, ErrLaneBillingOwnerEvidence):
		return ErrLaneBillingOwnerEvidence
	case errors.Is(err, errLaneBillingChildState):
		return errLaneBillingChildState
	default:
		return ErrLaneBillingOwnerStore
	}
}

func saveLaneBillingChildState(tx *gorm.DB, r *laneBillingChildReceiptV1, state string, actual int, usage string) error {
	next := *r
	next.State, next.Actual, next.UsageSHA256 = state, actual, usage
	if !next.valid() {
		return ErrLaneBillingOwnerEvidence
	}
	result := tx.Model(&laneBillingChildReceiptV1{}).Where("request_id = ? AND state = ?", r.RequestID, r.State).
		UpdateColumns(map[string]any{"state": state, "actual": actual, "usage_sha256": usage})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLaneBillingOwnerEvidence
	}
	return nil
}

func claimLaneBillingChildDispatch(ctx context.Context, db *gorm.DB, b laneBillingChildBinding, verifiedWireSHA256 string) error {
	if subtle.ConstantTimeCompare([]byte(b.FinalWireSHA256), []byte(verifiedWireSHA256)) != 1 {
		return ErrLaneBillingOwnerEvidence
	}
	return transitionLaneBillingChild(ctx, db, b, func(tx *gorm.DB, r *laneBillingChildReceiptV1) error {
		if r.State != LaneBillingReceiptHeld {
			return errLaneBillingChildState
		}
		return saveLaneBillingChildState(tx, r, laneBillingChildDispatch, 0, "")
	})
}

func markLaneBillingChildUnknown(ctx context.Context, db *gorm.DB, b laneBillingChildBinding) error {
	return transitionLaneBillingChild(ctx, db, b, func(tx *gorm.DB, r *laneBillingChildReceiptV1) error {
		if r.State == laneBillingChildUnknown {
			return nil
		}
		if r.State != laneBillingChildDispatch {
			return errLaneBillingChildState
		}
		return saveLaneBillingChildState(tx, r, laneBillingChildUnknown, 0, "")
	})
}

func freezeLaneBillingChildUsage(ctx context.Context, db *gorm.DB, b laneBillingChildBinding, actual int, usageSHA256 string) error {
	if actual < 0 || actual > common.MaxQuota || !laneChildSHAValid(usageSHA256) {
		return ErrLaneBillingOwnerEvidence
	}
	overCap := false
	err := transitionLaneBillingChild(ctx, db, b, func(tx *gorm.DB, r *laneBillingChildReceiptV1) error {
		if r.State == laneBillingChildObserved || r.State == laneBillingChildReview || r.State == laneBillingChildSettled {
			if r.Actual != actual || r.UsageSHA256 != usageSHA256 {
				return ErrLaneBillingOwnerEvidence
			}
			overCap = r.State == laneBillingChildReview
			return nil
		}
		if r.State != laneBillingChildDispatch {
			return errLaneBillingChildState
		}
		state := laneBillingChildObserved
		if actual > r.Cap {
			state, overCap = laneBillingChildReview, true
		}
		return saveLaneBillingChildState(tx, r, state, actual, usageSHA256)
	})
	if err == nil && overCap {
		return errLaneBillingChildExposure // review evidence committed, never a debit/refund grant
	}
	return err
}

func finishLaneBillingChild(ctx context.Context, db *gorm.DB, b laneBillingChildBinding, refund bool) error {
	return transitionLaneBillingChild(ctx, db, b, func(tx *gorm.DB, r *laneBillingChildReceiptV1) error {
		expected, terminal := laneBillingChildObserved, laneBillingChildSettled
		if refund {
			expected, terminal = LaneBillingReceiptHeld, laneBillingChildRefunded
		}
		if r.State == terminal {
			return nil
		}
		if r.State != expected {
			return errLaneBillingChildState
		}
		if err := changeLaneChildSlots(tx, b, r.Cap, r.Actual, true); err != nil {
			return err
		}
		return saveLaneBillingChildState(tx, r, terminal, r.Actual, r.UsageSHA256)
	})
}

var _ LaneBillingOwnerStore = (*laneBillingPGChildStore)(nil)
