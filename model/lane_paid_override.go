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
	if tx == nil || tx != custody.tx || tx.Config == nil || tx.Statement == nil || tx.Dialector == nil ||
		tx.Dialector.Name() != "postgres" || tx.DryRun || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) ||
		entry.UserId != custody.userID || custody.walletEpoch < 1 || !laneBillingOwnerIDValid(custody.schema) ||
		entry.Account != AccountPaid || entry.Reason != LedgerReasonAdminOverride ||
		entry.Delta != custody.target || entry.Delta > common.MaxWalletQuota ||
		entry.OperatorId <= 0 || entry.OperatorId != custody.operatorID ||
		entry.ReferenceType != "admin_quota" || entry.ReferenceId != custody.requestID ||
		len(entry.ReferenceId) > 128 || strings.TrimSpace(entry.ReferenceId) == "" ||
		entry.IdempotencyKey != fmt.Sprintf("admin_quota:%d:%s", custody.operatorID, custody.requestID) {
		return nil, ErrLanePaidOverrideAuthority
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrLanePaidOverrideAuthority
	}
	tx = tx.Session(&gorm.Session{NewDB: true})
	var isolated bool
	if err := tx.Raw(`SELECT pg_catalog.current_schemas(false) = ARRAY[CAST(? AS name)]
		AND pg_catalog.pg_my_temp_schema() = 0`, custody.schema).Row().Scan(&isolated); err != nil || !isolated {
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
	if u.Id != custody.userID || u.Quota < 0 || u.Quota > common.MaxWalletQuota ||
		u.QuotaPaid < 0 || u.QuotaPaid > common.MaxWalletQuota || u.QuotaBonus < 0 || u.QuotaBonus > common.MaxWalletQuota ||
		u.QuotaReward < 0 || u.QuotaReward > common.MaxWalletQuota || u.QuotaPaid+u.QuotaBonus+u.QuotaReward != u.Quota {
		return ErrSharedWalletEvidence
	}
	if entry.Delta > common.MaxWalletQuota-u.QuotaBonus-u.QuotaReward {
		return ErrWalletQuotaLimitExceeded
	}
	var slots []SharedWalletShare
	if err := lockForUpdate(tx).Where("user_id = ?", u.Id).Order("epoch, slot").
		Limit(SharedWalletMaxSlots + 1).Find(&slots).Error; err != nil {
		return err
	}
	if len(slots) == 0 || len(slots) > SharedWalletMaxSlots {
		return ErrSharedWalletEvidence
	}
	var outside bool
	if err := tx.Raw(`SELECT EXISTS (
		SELECT FROM shared_wallet_transfers WHERE user_id = ? AND (epoch, slot) < (?, 0)
	) OR EXISTS (
		SELECT FROM shared_wallet_transfers WHERE user_id = ? AND (epoch, slot) >= (?, ?)
	)`, u.Id, custody.walletEpoch, u.Id, custody.walletEpoch, SharedWalletMaxSlots).Row().Scan(&outside); err != nil || outside {
		return ErrSharedWalletEvidence
	}
	// Independently fetch the latest transfer for every slot, including
	// missing slots. A row's own last_operation_id is not freshness proof.
	var tips []SharedWalletTransfer
	if err := tx.Raw(`SELECT tip.* FROM generate_series(0, ?) AS slots(slot)
		CROSS JOIN LATERAL (
			SELECT * FROM shared_wallet_transfers
			WHERE user_id = ? AND epoch = ? AND slot = slots.slot
			ORDER BY share_version DESC LIMIT 1
		) AS tip ORDER BY tip.slot`, SharedWalletMaxSlots-1, u.Id, custody.walletEpoch).Scan(&tips).Error; err != nil || len(tips) != len(slots) {
		return ErrSharedWalletEvidence
	}
	idle := 0
	for i, s := range slots {
		tip := tips[i]
		if !s.valid() || s.UserID != u.Id || s.Epoch != custody.walletEpoch ||
			tip.UserID != s.UserID || tip.Epoch != s.Epoch || tip.Slot != s.Slot ||
			tip.OperationID != s.LastOperationID || tip.ShareVersion > s.Version ||
			tip.GrantedAfter != s.Granted || tip.ReturnedAfter != s.Returned ||
			verifySharedWalletTransferEvidenceTx(tx.Limit(2), tip) != nil {
			return ErrSharedWalletEvidence
		}
		if s.Held != 0 {
			return ErrLanePaidOverrideHeld
		}
		if s.Available > common.MaxWalletQuota-u.Quota-idle {
			return ErrWalletQuotaLimitExceeded
		}
		idle += s.Available
	}
	for _, s := range slots {
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
	if actual.Quota != u.Quota+idle || actual.QuotaPaid != u.QuotaPaid+idle ||
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
