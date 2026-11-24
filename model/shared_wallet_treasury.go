package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const SharedWalletMaxSlots = 32

var (
	ErrSharedWalletScope    = errors.New("invalid shared wallet treasury scope")
	ErrSharedWalletEvidence = errors.New("shared wallet treasury evidence is missing or inconsistent")
	ErrSharedWalletCapacity = errors.New("shared wallet treasury capacity exceeded")
)

// SharedWalletShare is paid treasury, not a Key budget, customer consumption,
// dispatch authority, or an active owner epoch. One wallet's Keys share these
// slots. This dormant kernel has no startup migration, route or runtime caller.
// Held/spent splits still require a real child-owner/epoch protocol before live
// reclaim. The treasury checkpoint alone is not a reconciliation of those splits.
type SharedWalletShare struct {
	UserID          int    `gorm:"primaryKey;autoIncrement:false"`
	Epoch           int64  `gorm:"primaryKey;autoIncrement:false"`
	Slot            int    `gorm:"primaryKey;autoIncrement:false"`
	Granted         int    `gorm:"type:bigint;not null"`
	Available       int    `gorm:"type:bigint;not null"`
	Held            int    `gorm:"type:bigint;not null"`
	Spent           int    `gorm:"type:bigint;not null"`
	Returned        int    `gorm:"type:bigint;not null"`
	Version         int64  `gorm:"not null"`
	LastOperationID string `gorm:"type:varchar(64);not null"`
}

// SharedWalletTransfer is immutable transfer evidence. OperationID is scoped
// to its original wallet, not to direction/slot/epoch: changing any signed
// field on retry must conflict rather than create a second operation.
type SharedWalletTransfer struct {
	UserID              int    `gorm:"primaryKey;autoIncrement:false"`
	OperationID         string `gorm:"primaryKey;type:varchar(64)"`
	Epoch               int64  `gorm:"not null"`
	Slot                int    `gorm:"not null"`
	Direction           string `gorm:"type:varchar(8);not null"`
	Amount              int    `gorm:"type:bigint;not null"`
	ShareVersion        int64  `gorm:"not null"`
	GrantedAfter        int    `gorm:"type:bigint;not null"`
	ReturnedAfter       int    `gorm:"type:bigint;not null"`
	PreviousOperationID string `gorm:"type:varchar(64);not null"`
}

func (SharedWalletTransfer) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (SharedWalletTransfer) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

// SharedWalletTreasuryModels is an explicit isolated migration seam only.
// Do not register until finite-Key accounting, epoch authority, logical reads,
// ordinary reclaim/override semantics, and mixed-version topology are ready.
func SharedWalletTreasuryModels() []any { return []any{&SharedWalletShare{}, &SharedWalletTransfer{}} }

func (op SharedWalletTransfer) valid() bool {
	if op.UserID <= 0 || op.Epoch <= 0 || op.Slot < 0 || op.Slot >= SharedWalletMaxSlots ||
		op.Amount <= 0 || op.Amount > common.MaxWalletQuota || (op.Direction != "grant" && op.Direction != "reclaim") ||
		len(op.OperationID) == 0 || len(op.OperationID) > 64 {
		return false
	}
	for _, c := range op.OperationID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (share SharedWalletShare) valid() bool {
	if share.UserID <= 0 || share.Epoch <= 0 || share.Slot < 0 || share.Slot >= SharedWalletMaxSlots || share.Version < 1 || share.Version >= common.MaxWalletQuota || share.LastOperationID == "" {
		return false
	}
	remaining := share.Granted
	if remaining <= 0 || remaining > common.MaxWalletQuota {
		return false
	}
	for _, n := range []int{share.Available, share.Held, share.Spent, share.Returned} {
		if n < 0 || n > remaining {
			return false
		}
		remaining -= n
	}
	return remaining == 0
}

func (op SharedWalletTransfer) ledgerEntry() LedgerEntry {
	// Existing ledger signatures exclude Metadata. Bind the full immutable
	// treasury checkpoint into the signed ReferenceId, not an unsigned note.
	// This is an integrity-consistency binding, not a secret MAC against a DB attacker.
	digest := sha256.Sum256([]byte(fmt.Sprintf("wallet-share-v1|%d|%d|%d|%s|%s|%d|%d|%d|%d|%s",
		op.UserID, op.Epoch, op.Slot, op.OperationID, op.Direction, op.Amount, op.ShareVersion, op.GrantedAfter, op.ReturnedAfter, op.PreviousOperationID)))
	entry := LedgerEntry{UserId: op.UserID, Account: AccountPaid, Delta: op.Amount,
		Reason: "wallet_share_" + op.Direction, ReferenceType: "wallet_share_transfer",
		ReferenceId: fmt.Sprintf("%x", digest), IdempotencyKey: fmt.Sprintf("wallet_share:%d:%s", op.UserID, op.OperationID)}
	if op.Direction == "grant" {
		entry.DebitOrder = []LedgerAccount{AccountPaid}
	}
	return entry
}

func verifySharedWalletTransferTx(tx *gorm.DB, receipt SharedWalletTransfer) error {
	if !receipt.valid() || receipt.ShareVersion < 1 || receipt.ShareVersion >= common.MaxWalletQuota || receipt.GrantedAfter <= 0 || receipt.GrantedAfter > common.MaxWalletQuota || receipt.ReturnedAfter < 0 || receipt.ReturnedAfter > receipt.GrantedAfter || receipt.PreviousOperationID == receipt.OperationID {
		return ErrSharedWalletEvidence
	}
	entry := receipt.ledgerEntry()
	kind, delta := ledgerOpCredit, receipt.Amount
	if receipt.Direction == "grant" {
		kind = ledgerOpDebit
		delta = -delta
	}
	operation, rows, err := loadLedgerOperation(lockForUpdate(tx), entry.IdempotencyKey)
	if err != nil || !verifyReplay(operation, entry, kind) || len(rows) != 1 {
		return ErrSharedWalletEvidence
	}
	row := rows[0]
	for _, amount := range []int{row.BalanceBefore, row.BalanceAfter, row.TotalBefore, row.TotalAfter, operation.TotalBefore, operation.TotalAfter} {
		if amount < 0 || amount > common.MaxWalletQuota {
			return ErrSharedWalletEvidence
		}
	}
	if row.OperationId != operation.Id || row.UserId != receipt.UserID || row.Account != string(AccountPaid) || row.Delta != delta ||
		row.BalanceAfter-row.BalanceBefore != delta || row.TotalAfter-row.TotalBefore != delta || row.TotalBefore != operation.TotalBefore || row.TotalAfter != operation.TotalAfter ||
		row.IdempotencyKey != entry.IdempotencyKey || row.Reason != entry.Reason || row.ReferenceType != entry.ReferenceType || row.ReferenceId != entry.ReferenceId {
		return ErrSharedWalletEvidence
	}
	return nil
}

// ApplySharedWalletTransfer owns the outer transaction and cache invalidation.
// A lost commit acknowledgement returns an error, not permission to do another
// transfer. Repeating the exact operation reloads the durable original proof.
// Process primary-database configuration must also be PostgreSQL because the
// canonical financial lock helper uses that configuration to choose row locks.
func ApplySharedWalletTransfer(ctx context.Context, db *gorm.DB, op SharedWalletTransfer) (SharedWalletTransfer, bool, error) {
	if ctx == nil || db == nil || db.Config == nil || db.Statement == nil || db.Dialector == nil || db.Dialector.Name() != "postgres" || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) || db.DryRun || !op.valid() || op.ShareVersion != 0 || op.GrantedAfter != 0 || op.ReturnedAfter != 0 || op.PreviousOperationID != "" {
		return SharedWalletTransfer{}, false, ErrSharedWalletScope
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return SharedWalletTransfer{}, false, ErrSharedWalletScope
	}
	if _, nested := db.Config.ConnPool.(gorm.TxCommitter); nested {
		return SharedWalletTransfer{}, false, ErrSharedWalletScope
	}
	var receipt SharedWalletTransfer
	var replayed, touched bool
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, replayed, touched, err = ApplySharedWalletTransferTx(tx, op)
		return err
	})
	if touched {
		RefreshUserQuotaCacheAfterBillingFinalization(op.UserID)
	}
	if err != nil {
		return SharedWalletTransfer{}, false, err
	}
	return receipt, replayed, nil
}

// ApplySharedWalletTransferTx performs only a cold paid transfer. Caller owns
// the transaction, must invalidate quota cache after its OUTER commit or an
// uncertain acknowledgement when touched is true. Every non-nil error MUST
// propagate to outer rollback, never catch-and-commit partial SQL. Do not infer customer
// spendability from the result. Lock order is original user -> operation -> slot.
// Reclaim may return idle funds while holds remain; no request is locked/expired.
func ApplySharedWalletTransferTx(tx *gorm.DB, op SharedWalletTransfer) (receipt SharedWalletTransfer, replayed, touched bool, err error) {
	if tx == nil || tx.Config == nil || tx.Statement == nil || tx.Dialector == nil || tx.Dialector.Name() != "postgres" || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) || tx.DryRun || !op.valid() || op.ShareVersion != 0 || op.GrantedAfter != 0 || op.ReturnedAfter != 0 || op.PreviousOperationID != "" {
		return receipt, false, false, ErrSharedWalletScope
	}
	if _, owned := tx.Statement.ConnPool.(gorm.TxCommitter); !owned {
		return receipt, false, false, ErrSharedWalletScope
	}
	var user User
	if err = lockForUpdate(tx).First(&user, op.UserID).Error; err != nil {
		return
	}
	var stored SharedWalletTransfer
	lookup := lockForUpdate(tx).Where("user_id = ? AND operation_id = ?", op.UserID, op.OperationID).Take(&stored).Error
	if lookup != nil && !errors.Is(lookup, gorm.ErrRecordNotFound) {
		err = lookup
		return
	}
	var share SharedWalletShare
	shareErr := lockForUpdate(tx).Where("user_id = ? AND epoch = ? AND slot = ?", op.UserID, op.Epoch, op.Slot).Take(&share).Error
	if shareErr != nil && !errors.Is(shareErr, gorm.ErrRecordNotFound) {
		err = shareErr
		return
	}
	entry := op.ledgerEntry()
	_, _, ledgerErr := loadLedgerOperation(lockForUpdate(tx), entry.IdempotencyKey)
	if lookup == nil {
		expected := stored
		expected.ShareVersion, expected.GrantedAfter, expected.ReturnedAfter, expected.PreviousOperationID = 0, 0, 0, ""
		if expected != op || shareErr != nil || !share.valid() || share.Version < stored.ShareVersion ||
			verifySharedWalletTransferTx(tx, stored) != nil {
			err = ErrSharedWalletEvidence
			return
		}
		return stored, true, false, nil
	}
	// A ledger-only operation must never bootstrap a missing transfer receipt.
	if ledgerErr == nil {
		err = ErrSharedWalletEvidence
		return
	}
	if !errors.Is(ledgerErr, gorm.ErrRecordNotFound) {
		err = ledgerErr
		return
	}
	fresh := errors.Is(shareErr, gorm.ErrRecordNotFound)
	if fresh {
		if op.Direction != "grant" {
			err = ErrSharedWalletEvidence
			return
		}
		share = SharedWalletShare{UserID: op.UserID, Epoch: op.Epoch, Slot: op.Slot}
	} else if !share.valid() {
		err = ErrSharedWalletEvidence
		return
	}
	if !fresh {
		var anchor SharedWalletTransfer
		if err = tx.Where("user_id = ? AND operation_id = ?", op.UserID, share.LastOperationID).Take(&anchor).Error; err != nil {
			err = ErrSharedWalletEvidence
			return
		}
		if anchor.Epoch != share.Epoch || anchor.Slot != share.Slot || anchor.ShareVersion > share.Version ||
			anchor.GrantedAfter != share.Granted || anchor.ReturnedAfter != share.Returned || verifySharedWalletTransferTx(tx, anchor) != nil {
			err = ErrSharedWalletEvidence
			return
		}
	}
	// Numeric SQL avoids SUM overflow across many closed/refilled generations.
	// Existing writers do not know logical balances, so fail closed if a raw
	// credit has already pushed raw + unspent escrow outside the wallet domain.
	var invalid bool
	err = tx.Raw(`SELECT COALESCE(SUM(available::numeric + held),0) + ? > ?
		OR COALESCE(BOOL_OR((granted BETWEEN 1 AND ? AND available >= 0 AND held >= 0 AND spent >= 0 AND returned >= 0
		AND granted::numeric = available::numeric + held + spent + returned AND last_operation_id <> '') IS NOT TRUE),false)
		FROM shared_wallet_shares WHERE user_id = ?`, user.Quota, common.MaxWalletQuota, common.MaxWalletQuota, op.UserID).Scan(&invalid).Error
	if err != nil {
		return
	}
	if invalid {
		err = ErrSharedWalletCapacity
		return
	}
	if op.Direction == "grant" {
		if user.Status != common.UserStatusEnabled || op.Amount > common.MaxWalletQuota-share.Granted || share.Version >= common.MaxWalletQuota-1 {
			err = ErrSharedWalletCapacity
			return
		}
		share.Granted += op.Amount
		share.Available += op.Amount
	} else {
		if op.Amount > share.Available || op.Amount > common.MaxWalletQuota-share.Returned || share.Version >= common.MaxWalletQuota-1 {
			err = ErrSharedWalletCapacity
			return
		}
		share.Available -= op.Amount
		share.Returned += op.Amount
	}
	receipt = op
	receipt.PreviousOperationID = share.LastOperationID
	share.LastOperationID = op.OperationID
	share.Version++
	receipt.ShareVersion, receipt.GrantedAfter, receipt.ReturnedAfter = share.Version, share.Granted, share.Returned
	entry = receipt.ledgerEntry()
	if !share.valid() {
		err = ErrSharedWalletEvidence
		return
	}
	var result *LedgerResult
	if op.Direction == "grant" {
		result, err = DebitQuotaTx(tx, entry, AccountPaid)
	} else {
		result, err = CreditQuotaTx(tx, entry)
	}
	if err != nil {
		return
	}
	touched = true
	if result.Replayed {
		err = ErrSharedWalletEvidence
		return
	}
	if fresh {
		err = tx.Create(&share).Error
	} else {
		result := tx.Model(&SharedWalletShare{}).Where("user_id = ? AND epoch = ? AND slot = ? AND version = ?", op.UserID, op.Epoch, op.Slot, share.Version-1).
			Updates(map[string]any{"granted": share.Granted, "available": share.Available, "returned": share.Returned, "version": share.Version, "last_operation_id": share.LastOperationID})
		err = result.Error
		if err == nil && result.RowsAffected != 1 {
			err = ErrSharedWalletEvidence
		}
	}
	if err != nil {
		return
	}
	err = tx.Create(&receipt).Error
	return
}
