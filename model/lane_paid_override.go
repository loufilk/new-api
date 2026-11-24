package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrLanePaidOverrideAuthority = errors.New("paid wallet override requires verified custody authority")
	ErrLanePaidOverrideHeld      = errors.New("paid wallet override requires the affected wallet to finish all outstanding holds")
)

// lanePaidOverrideCustody is a private execution input, not authentication or
// a release capability. Its caller must have pinned the store/epoch in this
// same transaction before acquiring any User or slot locks, and then
// authorized the still-current operator and exact target. No live code
// constructs it: the normal owner cannot mint a wallet writer permit yet.
type lanePaidOverrideCustody struct {
	tx          *gorm.DB
	schema      string
	userID      int
	walletEpoch int64
	operatorID  int
	requestID   string
	target      int
}

// applyLanePaidOverrideTx sets spendable paid (raw + idle) to the exact
// requested absolute value. Anonymous legacy write paths cannot invoke it.
// The caller must rollback on error and invalidate the ordinary mirror only
// after the parent transaction commits (or reports an uncertain commit).
func applyLanePaidOverrideTx(tx *gorm.DB, custody lanePaidOverrideCustody, entry LedgerEntry) (*LedgerResult, error) {
	if entry.UserId != custody.userID || entry.Account != AccountPaid || entry.Reason != LedgerReasonAdminOverride ||
		entry.Delta != custody.target || entry.Delta > common.MaxWalletQuota ||
		entry.OperatorId <= 0 || entry.OperatorId != custody.operatorID ||
		entry.ReferenceType != "admin_quota" || entry.ReferenceId != custody.requestID ||
		len(entry.ReferenceId) > 128 || strings.TrimSpace(entry.ReferenceId) == "" ||
		entry.IdempotencyKey != fmt.Sprintf("admin_quota:%d:%s", custody.operatorID, custody.requestID) {
		return nil, ErrLanePaidOverrideAuthority
	}
	scope := laneWalletCustodyScope{tx: custody.tx, schema: custody.schema, userID: custody.userID, walletEpoch: custody.walletEpoch}
	tx, err := scope.lockableTx(tx, entry.UserId)
	if err != nil {
		return nil, ErrLanePaidOverrideAuthority
	}
	if err := validateOverrideEntry(entry); err != nil {
		return nil, err
	}
	entry.lanePaidSetEpoch = custody.walletEpoch
	return applyLedgerTx(tx, entry, entry.IdempotencyKey, ledgerOpLanePaidSet, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		if err := reclaimPaidOverrideIdleTx(tx, custody, entry, user); err != nil {
			return nil, err
		}
		if delta := entry.Delta - user.QuotaPaid; delta != 0 {
			return []accountDelta{{account: AccountPaid, delta: delta}}, nil
		}
		return nil, nil
	})
}

func reclaimPaidOverrideIdleTx(tx *gorm.DB, custody lanePaidOverrideCustody, entry LedgerEntry, u *User) error {
	if u.Id != custody.userID {
		return ErrSharedWalletEvidence
	}
	wallet, err := readLockedLaneWalletCustodyTx(tx, u, custody.walletEpoch)
	if err != nil {
		return err
	}
	if wallet.held != 0 {
		return ErrLanePaidOverrideHeld
	}
	if entry.Delta > common.MaxWalletQuota-u.QuotaBonus-u.QuotaReward {
		return ErrWalletQuotaLimitExceeded
	}
	for _, s := range wallet.slots {
		if s.Available == 0 {
			continue
		}
		opID := sha256.Sum256([]byte(fmt.Sprintf("lane-paid-set-reclaim-v1|%d|%d|%d|%s", u.Id, s.Epoch, s.Slot, entry.IdempotencyKey)))
		_, replayed, _, err := ApplySharedWalletTransferTx(tx, SharedWalletTransfer{
			UserID: u.Id, Epoch: s.Epoch, Slot: s.Slot, OperationID: hex.EncodeToString(opID[:]),
			Direction: "reclaim", Amount: s.Available,
		})
		if err != nil {
			return err
		}
		if replayed { // an old partial reclaim is not authority for a fresh set
			return ErrSharedWalletEvidence
		}
	}
	var actual User
	if err := tx.Select("id", "quota", "quota_paid", "quota_bonus", "quota_reward").First(&actual, u.Id).Error; err != nil {
		return err
	}
	if actual.Quota != u.Quota+wallet.idle || actual.QuotaPaid != u.QuotaPaid+wallet.idle ||
		actual.QuotaBonus != u.QuotaBonus || actual.QuotaReward != u.QuotaReward {
		return ErrSharedWalletEvidence
	}
	u.Quota, u.QuotaPaid = actual.Quota, actual.QuotaPaid
	return nil
}

// A signed header identifies the requested set, not the existence or
// consistency of its ledger detail after a partial restore. A genuine
// zero-delta set writes no detail; every nonzero set writes exactly one.
func validLanePaidOverrideReplay(entry LedgerEntry, op *BalanceLedgerOperation, rows []BalanceLedger) bool {
	if op == nil || op.Id <= 0 || op.UserId != entry.UserId ||
		op.TotalBefore < 0 || op.TotalBefore > common.MaxWalletQuota ||
		op.TotalAfter < entry.Delta || op.TotalAfter > common.MaxWalletQuota {
		return false
	}
	if op.TotalBefore == op.TotalAfter {
		return len(rows) == 0
	}
	if len(rows) != 1 {
		return false
	}
	row := rows[0]
	return row.OperationId == op.Id && row.UserId == op.UserId && row.Account == string(AccountPaid) &&
		row.Delta == op.TotalAfter-op.TotalBefore && row.BalanceBefore >= 0 &&
		row.BalanceBefore <= op.TotalBefore && row.BalanceAfter == entry.Delta &&
		row.Delta == row.BalanceAfter-row.BalanceBefore &&
		row.TotalBefore == op.TotalBefore && row.TotalAfter == op.TotalAfter &&
		row.Reason == entry.Reason && row.ReferenceType == entry.ReferenceType && row.ReferenceId == entry.ReferenceId &&
		row.IdempotencyKey == op.IdempotencyKey && row.OperatorId == entry.OperatorId &&
		row.Metadata == entry.Metadata && row.CreatedAt == op.CreatedAt
}
