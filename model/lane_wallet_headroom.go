package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// This private scope describes already-authorized custody; it is not proof
// of an active release. No ordinary request can construct it today. A future
// runtime has to bind the exact store and epoch before this user/slot lock.
type laneWalletCustodyScope struct {
	tx          *gorm.DB
	schema      string
	userID      int
	walletEpoch int64
}

func (s laneWalletCustodyScope) lockableTx(tx *gorm.DB, userID int) (*gorm.DB, error) {
	if tx == nil || tx != s.tx || tx.Config == nil || tx.Statement == nil || tx.Dialector == nil ||
		tx.Dialector.Name() != "postgres" || tx.DryRun || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) ||
		userID <= 0 || userID != s.userID || s.walletEpoch < 1 || !laneBillingOwnerIDValid(s.schema) {
		return nil, ErrSharedWalletScope
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrSharedWalletScope
	}
	tx = tx.Session(&gorm.Session{NewDB: true})
	var isolated bool
	if err := tx.Raw(`SELECT pg_catalog.current_schemas(false) = ARRAY[CAST(? AS name)]
		AND pg_catalog.pg_my_temp_schema() = 0`, s.schema).Row().Scan(&isolated); err != nil || !isolated {
		return nil, ErrSharedWalletScope
	}
	return tx, nil
}

// Only an exact canonical new-net-credit operation is admitted by this
// form. A claimed reason string alone cannot assert either scope or type.
type laneNetCreditApproval struct {
	scope     laneWalletCustodyScope
	ledgerKey string
	signature string
}

type laneWalletCustodyAmounts struct {
	slots                []SharedWalletShare
	idle, held, headroom int
}

// Caller holds the original user's row. Lock all bounded slots in order;
// include outstanding held value in headroom even though it cannot be spent.
// The exact-schema transfer-tip index belongs to the future active schema
// contract. Fixture custody already has the indexed bounded slot probes.
func readLockedLaneWalletCustodyTx(tx *gorm.DB, u *User, epoch int64) (laneWalletCustodyAmounts, error) {
	if u == nil || u.Id <= 0 || epoch <= 0 || u.Quota < 0 || u.Quota > common.MaxWalletQuota ||
		u.QuotaPaid < 0 || u.QuotaPaid > common.MaxWalletQuota || u.QuotaBonus < 0 || u.QuotaBonus > common.MaxWalletQuota ||
		u.QuotaReward < 0 || u.QuotaReward > common.MaxWalletQuota || u.QuotaPaid+u.QuotaBonus+u.QuotaReward != u.Quota {
		return laneWalletCustodyAmounts{}, ErrSharedWalletEvidence
	}
	var slots []SharedWalletShare
	if err := lockForUpdate(tx).Where("user_id = ?", u.Id).Order("epoch, slot").
		Limit(SharedWalletMaxSlots + 1).Find(&slots).Error; err != nil {
		return laneWalletCustodyAmounts{}, err
	}
	if len(slots) == 0 || len(slots) > SharedWalletMaxSlots {
		return laneWalletCustodyAmounts{}, ErrSharedWalletEvidence
	}
	var outside bool
	if err := tx.Raw(`SELECT EXISTS (
		SELECT FROM shared_wallet_transfers WHERE user_id = ? AND (epoch, slot) < (?, 0)
	) OR EXISTS (
		SELECT FROM shared_wallet_transfers WHERE user_id = ? AND (epoch, slot) >= (?, ?)
	)`, u.Id, epoch, u.Id, epoch, SharedWalletMaxSlots).Row().Scan(&outside); err != nil || outside {
		return laneWalletCustodyAmounts{}, ErrSharedWalletEvidence
	}
	// Independently fetch the latest transfer for every slot, including
	// missing slots. A row's own last_operation_id is not freshness proof.
	var tips []SharedWalletTransfer
	if err := tx.Raw(`SELECT tip.* FROM generate_series(0, ?) AS slots(slot)
		CROSS JOIN LATERAL (
			SELECT * FROM shared_wallet_transfers
			WHERE user_id = ? AND epoch = ? AND slot = slots.slot
			ORDER BY share_version DESC LIMIT 1
		) AS tip ORDER BY tip.slot`, SharedWalletMaxSlots-1, u.Id, epoch).Scan(&tips).Error; err != nil || len(tips) != len(slots) {
		return laneWalletCustodyAmounts{}, ErrSharedWalletEvidence
	}
	amounts := laneWalletCustodyAmounts{slots: slots, headroom: common.MaxWalletQuota - u.Quota}
	for i, s := range slots {
		tip := tips[i]
		if !s.valid() || s.UserID != u.Id || s.Epoch != epoch ||
			tip.UserID != s.UserID || tip.Epoch != s.Epoch || tip.Slot != s.Slot ||
			tip.OperationID != s.LastOperationID || tip.ShareVersion > s.Version ||
			tip.GrantedAfter != s.Granted || tip.ReturnedAfter != s.Returned ||
			verifySharedWalletTransferEvidenceTx(tx.Limit(2), tip) != nil {
			return laneWalletCustodyAmounts{}, ErrSharedWalletEvidence
		}
		if s.Available > amounts.headroom || s.Held > amounts.headroom-s.Available {
			return laneWalletCustodyAmounts{}, ErrSharedWalletEvidence
		}
		amounts.idle += s.Available
		amounts.held += s.Held
		amounts.headroom -= s.Available + s.Held
	}
	return amounts, nil
}

// A verified capture must not become "credited" merely because a restored
// idempotency header survives. Single-account net credit requires one exact
// positive detail, with original accounting identity and raw totals.
func validLaneNetCreditReplay(entry LedgerEntry, op *BalanceLedgerOperation, rows []BalanceLedger) bool {
	if entry.Delta <= 0 || op == nil || op.Id <= 0 || op.UserId != entry.UserId || len(rows) != 1 ||
		op.TotalBefore < 0 || op.TotalBefore > common.MaxWalletQuota ||
		op.TotalAfter < 0 || op.TotalAfter > common.MaxWalletQuota || op.TotalAfter-op.TotalBefore != entry.Delta {
		return false
	}
	row := rows[0]
	return row.OperationId == op.Id && row.UserId == op.UserId && row.Account == string(entry.Account) &&
		row.Delta == entry.Delta && row.BalanceBefore >= 0 && row.BalanceBefore <= op.TotalBefore &&
		row.BalanceAfter >= row.BalanceBefore && row.BalanceAfter <= op.TotalAfter &&
		row.BalanceAfter-row.BalanceBefore == entry.Delta &&
		row.TotalBefore == op.TotalBefore && row.TotalAfter == op.TotalAfter &&
		row.Reason == entry.Reason && row.ReferenceType == entry.ReferenceType && row.ReferenceId == entry.ReferenceId &&
		row.IdempotencyKey == op.IdempotencyKey && row.OperatorId == entry.OperatorId &&
		row.Metadata == entry.Metadata && row.CreatedAt == op.CreatedAt
}
