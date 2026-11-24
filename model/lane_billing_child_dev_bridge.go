//go:build lane_billing_integration_dev

package model

import (
	"context"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This API exists only for the namespaced synthetic integration fixture. It
// never accepts real provider or customer credentials and has no router or
// startup binding. An enrolled production mode must supply independently
// audited authority, authenticated quote and original-Key lifecycle gates.
type LaneBillingChildDevBinding laneBillingChildBinding

type LaneBillingChildDev struct {
	db      *gorm.DB
	binding laneBillingChildBinding
	cap     int
}

// LaneBillingMeasuredDev is a sanitized synthetic projection receipt, not a
// sales/usage log. A separate worker would have to consume only ready receipts;
// no such worker or real reporting mutation is registered here.
type LaneBillingMeasuredDev struct {
	RequestID           string `gorm:"type:varchar(64);primaryKey"`
	ChannelID           int    `gorm:"not null"`
	Model               string `gorm:"type:varchar(128);not null"`
	Group               string `gorm:"type:varchar(128);not null"`
	PromptTokens        int    `gorm:"type:bigint;not null"`
	CompletionTokens    int    `gorm:"type:bigint;not null"`
	CacheReadTokens     int    `gorm:"type:bigint;not null"`
	CacheWriteTokens    int    `gorm:"type:bigint;not null"`
	CacheWrite12hTokens int    `gorm:"type:bigint;not null"`
	ActualQuota         int    `gorm:"type:bigint;not null"`
	UsageSHA256         string `gorm:"type:varchar(64);not null"`
	Ready               bool   `gorm:"not null"`
}

func (LaneBillingMeasuredDev) TableName() string { return "lane_billing_measured_dev_v1" }

func InstallLaneBillingSyntheticDevSchema(ctx context.Context, db *gorm.DB) error {
	if err := installLaneBillingChildDevSchema(ctx, db); err != nil {
		return err
	}
	return InstallLaneBillingFiniteKeyDevSchema(ctx, db)
}

func verifyLaneBillingSyntheticDevScope(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil || db.Config == nil || db.Statement == nil ||
		db.Dialector == nil || db.Dialector.Name() != "postgres" || db.DryRun {
		return ErrLaneBillingOwnerUnavailable
	}
	return laneBillingFiniteKeyDevScopeTx(db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard}))
}

type laneBillingVerifiedDevStore struct {
	laneBillingEnrolledPGChildStore
}

func (s *laneBillingVerifiedDevStore) LockLaneBillingReceiptTx(tx *gorm.DB, requestID string) (LaneBillingOwnerReceipt, error) {
	if err := verifyLaneBillingSyntheticDevScope(tx.Statement.Context, tx); err != nil {
		return LaneBillingOwnerReceipt{}, err
	}
	return s.laneBillingEnrolledPGChildStore.LockLaneBillingReceiptTx(tx, requestID)
}

func ReserveLaneBillingChildDev(ctx context.Context, db *gorm.DB, b LaneBillingChildDevBinding, cap int) (*LaneBillingChildDev, error) {
	if err := verifyLaneBillingSyntheticDevScope(ctx, db); err != nil {
		return nil, err
	}
	binding := laneBillingChildBinding(b)
	res, err := ReserveLaneBillingOwner(ctx, db, binding.Owner, cap, &laneBillingVerifiedDevStore{laneBillingEnrolledPGChildStore{laneBillingPGChildStore{binding: binding}}})
	if err != nil {
		return nil, err
	}
	if !res.Fresh {
		return nil, ErrBillingRequestOwnerConflict
	}
	return &LaneBillingChildDev{db: db, binding: binding, cap: cap}, nil
}

func (s *LaneBillingChildDev) transition(ctx context.Context, apply func(*gorm.DB, *laneBillingChildReceiptV1) error) error {
	return transitionLaneBillingChild(ctx, s.db, s.binding, func(tx *gorm.DB, r *laneBillingChildReceiptV1) error {
		if err := verifyLaneBillingSyntheticDevScope(tx.Statement.Context, tx); err != nil {
			return err
		}
		if r.Cap != s.cap {
			return ErrLaneBillingOwnerEvidence
		}
		if err := requireLaneBillingEnrolledChildProofTx(tx, r.RequestID); err != nil {
			return err
		}
		return apply(tx, r)
	})
}

func (s *LaneBillingChildDev) ClaimDispatch(ctx context.Context, wireSHA256 string) error {
	return claimEnrolledLaneBillingChildDispatch(ctx, s.db, s.binding, wireSHA256)
}

func (s *LaneBillingChildDev) PreserveUnknown(ctx context.Context) error {
	return s.transition(ctx, func(tx *gorm.DB, r *laneBillingChildReceiptV1) error {
		if r.State == laneBillingChildUnknown {
			return nil
		}
		if r.State != laneBillingChildDispatch {
			return errLaneBillingChildState
		}
		return saveLaneBillingChildState(tx, r, laneBillingChildUnknown, 0, "")
	})
}

// SettleMeasured commits immutable measured values and observed state before
// moving money. Second-phase money and the ready projection flag are atomic.
// In this fixture exact same evidence can drive either phase after a lost ACK.
// General production recovery, rollover and usage projection remain unwired.
func (s *LaneBillingChildDev) SettleMeasured(ctx context.Context, m LaneBillingMeasuredDev) error {
	if m.Ready || m.RequestID != s.binding.Owner.RequestID || m.ChannelID != s.binding.ChannelID ||
		m.PromptTokens <= 0 || m.PromptTokens > common.MaxQuota || m.CompletionTokens < 0 || m.CompletionTokens > common.MaxQuota-m.PromptTokens ||
		m.CacheReadTokens < 0 || m.CacheReadTokens > m.PromptTokens || m.CacheWriteTokens < 0 || m.CacheWriteTokens > m.PromptTokens ||
		m.CacheWrite12hTokens < 0 || m.CacheWrite12hTokens > m.CacheWriteTokens || m.ActualQuota <= 0 || m.ActualQuota > s.cap ||
		m.Model == "" || len(m.Model) > 128 || m.Group == "" || len(m.Group) > 128 || !laneChildSHAValid(m.UsageSHA256) {
		return ErrLaneBillingOwnerEvidence
	}
	readMeasurement := func(tx *gorm.DB, r *laneBillingChildReceiptV1) (bool, error) {
		var old LaneBillingMeasuredDev
		if err := lockForUpdate(tx).Where("request_id = ?", r.RequestID).Take(&old).Error; err != nil {
			return false, err
		}
		ready := old.Ready
		old.Ready = false
		if old != m || r.Actual != m.ActualQuota || r.UsageSHA256 != m.UsageSHA256 || ready != (r.State == laneBillingChildSettled) {
			return false, ErrLaneBillingOwnerEvidence
		}
		return ready, nil
	}
	if err := s.transition(ctx, func(tx *gorm.DB, r *laneBillingChildReceiptV1) error {
		if r.State == laneBillingChildObserved || r.State == laneBillingChildSettled {
			_, err := readMeasurement(tx, r)
			return err
		}
		if r.State != laneBillingChildDispatch {
			return errLaneBillingChildState
		}
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		return saveLaneBillingChildState(tx, r, laneBillingChildObserved, m.ActualQuota, m.UsageSHA256)
	}); err != nil {
		return err
	}
	return s.transition(ctx, func(tx *gorm.DB, r *laneBillingChildReceiptV1) error {
		if r.State != laneBillingChildObserved && r.State != laneBillingChildSettled {
			return errLaneBillingChildState
		}
		ready, err := readMeasurement(tx, r)
		if err != nil || ready {
			return err
		}
		if err := changeLaneChildSlots(tx, s.binding, r.Cap, r.Actual, true); err != nil {
			return err
		}
		if err := saveLaneBillingChildState(tx, r, laneBillingChildSettled, r.Actual, r.UsageSHA256); err != nil {
			return err
		}
		put := tx.Model(&LaneBillingMeasuredDev{}).Where("request_id = ? AND NOT ready", r.RequestID).UpdateColumn("ready", true)
		if put.Error != nil {
			return put.Error
		}
		if put.RowsAffected != 1 {
			return ErrLaneBillingOwnerEvidence
		}
		return nil
	})
}
