package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// planWalletPaidIdleDebit uses the ordinary account order, not an
// independently chosen funding class. The caller supplies authoritative
// post-expiry raw balances, idle paid escrow, and nonspendable held escrow.
// No reward, bonus, hold or settled amount can be reclaimed as paid.
func planWalletPaidIdleDebit(user *User, amount int, order []LedgerAccount, idle, held int) ([]accountDelta, int, error) {
	if user == nil || amount < 0 || amount > common.MaxQuota || idle < 0 || held < 0 ||
		user.QuotaPaid < 0 || user.QuotaBonus < 0 || user.QuotaReward < 0 || user.Quota < 0 ||
		user.QuotaPaid > common.MaxWalletQuota || user.QuotaBonus > common.MaxWalletQuota || user.QuotaReward > common.MaxWalletQuota ||
		user.Quota != user.QuotaPaid+user.QuotaBonus+user.QuotaReward {
		return nil, 0, ErrLedgerInvalidEntry
	}
	left := common.MaxWalletQuota - user.Quota
	if left < 0 || idle > left || held > left-idle {
		return nil, 0, ErrWalletQuotaLimitExceeded
	}
	order = defaultDebitOrder(order)
	if err := validateDebitOrder(order); err != nil {
		return nil, 0, err
	}
	logical := *user
	logical.QuotaPaid += idle
	deltas, err := planDebit(&logical, amount, order)
	if err != nil {
		return nil, 0, err
	}
	for _, d := range deltas {
		if d.account == AccountPaid {
			return deltas, max(0, -d.delta-user.QuotaPaid), nil
		}
	}
	return deltas, 0, nil
}

// walletPaidIdleDebit is a model-internal splice for slots already pinned
// under the canonical/user transaction. No production code can construct
// this from a request or preference alone. Runtime needs an active
// release-backed, original-custody permit before any non-fixture caller.
type walletPaidIdleDebit struct {
	tx      *gorm.DB
	userID  int
	epoch   int64
	request string
	slots   []SharedWalletShare
}

// Reclaims belong to an exact usual debit key, never a generated or
// arbitrary operation. The other wallet writers still use raw semantics.
func walletPaidIdleDebitKeyValid(entry LedgerEntry) bool {
	if !laneBillingOwnerIDValid(entry.RequestId) {
		return false
	}
	switch entry.Reason {
	case LedgerReasonBillingPreConsume:
		return entry.IdempotencyKey == "billing:preconsume:"+entry.RequestId
	case LedgerReasonBillingReserve:
		prefix := "billing:reserve:" + entry.RequestId + ":"
		tail := strings.TrimPrefix(entry.IdempotencyKey, prefix)
		target, err := strconv.Atoi(tail)
		return strings.HasPrefix(entry.IdempotencyKey, prefix) && err == nil && target >= entry.Delta &&
			target <= common.MaxQuota && strconv.Itoa(target) == tail
	case LedgerReasonBillingSettle:
		return entry.IdempotencyKey == fmt.Sprintf("billing:settle:%s:%d", entry.RequestId, entry.Delta)
	default:
		return false
	}
}

// prepare reclaims exactly the paid shortfall in the existing transaction,
// after normalize/expiry and idempotency check. The caller must propagate any
// error to outer rollback. Used only by the isolated integration fixture.
func (p *walletPaidIdleDebit) prepare(tx *gorm.DB, entry LedgerEntry, user *User, order []LedgerAccount) error {
	if p.tx != tx || tx == nil || tx.Statement == nil || user == nil || p.userID != user.Id || entry.UserId != p.userID ||
		p.epoch <= 0 || p.request == "" || entry.ReferenceType != "relay" || entry.RequestId != p.request ||
		entry.ReferenceId != p.request || !walletPaidIdleDebitKeyValid(entry) || len(p.slots) > SharedWalletMaxSlots {
		return ErrSharedWalletEvidence
	}
	if _, owned := tx.Statement.ConnPool.(gorm.TxCommitter); !owned {
		return ErrSharedWalletEvidence
	}
	idle, held := 0, 0
	lastSlot := -1
	for _, s := range p.slots {
		if !s.valid() || s.UserID != p.userID || s.Epoch != p.epoch || s.Slot <= lastSlot {
			return ErrSharedWalletEvidence
		}
		if s.Available > common.MaxWalletQuota-idle || s.Held > common.MaxWalletQuota-held {
			return ErrWalletQuotaLimitExceeded
		}
		idle, held, lastSlot = idle+s.Available, held+s.Held, s.Slot
	}
	_, needed, err := planWalletPaidIdleDebit(user, entry.Delta, order, idle, held)
	if err != nil || needed == 0 {
		return err
	}
	originalNeed := needed
	for _, slot := range p.slots {
		if needed == 0 {
			break
		}
		n := min(slot.Available, needed)
		if n == 0 {
			continue
		}
		// A debit operation (initial/grow/final positive delta) can reclaim
		// from the same slot again only under a distinct ledger operation.
		operation := sha256.Sum256([]byte(fmt.Sprintf("wallet-paid-idle-v1|%d|%d|%d|%s",
			p.userID, p.epoch, slot.Slot, entry.IdempotencyKey)))
		_, replayed, _, err := ApplySharedWalletTransferTx(tx, SharedWalletTransfer{
			UserID: p.userID, Epoch: p.epoch, Slot: slot.Slot, OperationID: hex.EncodeToString(operation[:]),
			Direction: "reclaim", Amount: n,
		})
		if err != nil {
			return err
		}
		if replayed {
			return ErrSharedWalletEvidence // don't adopt a partial older operation
		}
		needed -= n
	}
	if needed != 0 {
		return ErrSharedWalletEvidence
	}
	// Do not make the outer ledger plan trust a caller's arithmetic or a
	// stale local User. Read back the committed-to-this-Tx raw balances.
	var fresh User
	if err := lockForUpdate(tx).Select("id", "quota", "quota_paid", "quota_bonus", "quota_reward").Take(&fresh, p.userID).Error; err != nil {
		return err
	}
	if fresh.QuotaPaid != user.QuotaPaid+originalNeed || fresh.Quota != user.Quota+originalNeed ||
		fresh.QuotaBonus != user.QuotaBonus || fresh.QuotaReward != user.QuotaReward {
		return ErrSharedWalletEvidence
	}
	user.Quota, user.QuotaPaid = fresh.Quota, fresh.QuotaPaid
	return nil
}
