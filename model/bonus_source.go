package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// bonusSourceNow is the accounting clock; transactions never trust client timestamps.
var bonusSourceNow = common.GetTimestamp

var ErrBonusSourceInvariant = errors.New("Bonus source reconciliation requires review")

// BonusSourceLot is immutable provenance, not a second wallet. Only Available
// in its state contributes to users.quota_bonus. Debited quota, including an
// UNKNOWN delivery hold, never participates in expiry.
type BonusSourceLot struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	UserID            int    `json:"user_id" gorm:"not null;index"`
	SourceKey         string `json:"source_key" gorm:"type:varchar(255);not null;uniqueIndex"`
	Kind              string `json:"kind" gorm:"type:varchar(32);not null"`
	GrantQuota        int    `json:"grant_quota" gorm:"type:bigint;not null"`
	CampaignID        int64  `json:"campaign_id" gorm:"index"`
	ClaimID           int64  `json:"claim_id" gorm:"index"`
	LedgerOperationID int    `json:"ledger_operation_id" gorm:"index"`
	ExpiresAt         int64  `json:"expires_at" gorm:"index"`
	RefundTerms       string `json:"refund_terms" gorm:"type:varchar(32);not null"`
	Revocable         bool   `json:"revocable"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*BonusSourceLot) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*BonusSourceLot) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type BonusSourceState struct {
	LegacyRestored   int   `json:"legacy_restored" gorm:"type:bigint;not null"`
	RevocationActive bool  `json:"revocation_active"`
	LotID            int64 `json:"lot_id" gorm:"primaryKey;autoIncrement:false"`
	UserID           int   `json:"user_id" gorm:"not null;index"`
	Available        int   `json:"available" gorm:"type:bigint;not null"`
	Outstanding      int   `json:"outstanding" gorm:"type:bigint;not null"`
	Expired          int   `json:"expired" gorm:"type:bigint;not null"`
	Revoked          int   `json:"revoked" gorm:"type:bigint;not null"`
}

// Account marks the one-time cutover. A later unexplained balance is an
// anomaly, never permission to mint another legacy source.
type BonusSourceAccount struct {
	UserID          int   `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	CutoverLedgerID int   `json:"cutover_ledger_id"`
	CreatedAt       int64 `json:"created_at" gorm:"autoCreateTime"`
}

type BonusSourceAllocation struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	UserID        int    `json:"user_id" gorm:"not null;index"`
	LotID         int64  `json:"lot_id" gorm:"not null;index;uniqueIndex:idx_bonus_allocation,priority:2"`
	LedgerID      int    `json:"ledger_id" gorm:"not null;uniqueIndex:idx_bonus_allocation,priority:1"`
	ReferenceType string `json:"reference_type" gorm:"type:varchar(64);index:idx_bonus_ref,priority:2"`
	ReferenceID   string `json:"reference_id" gorm:"type:varchar(191);index:idx_bonus_ref,priority:3"`
	Amount        int    `json:"amount" gorm:"type:bigint;not null"`
	Refunded      int    `json:"refunded" gorm:"type:bigint;not null"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime"`
}

// Every restoration names the exact original allocation and refund operation.
type BonusSourceRestoration struct {
	ID           int64 `json:"id" gorm:"primaryKey"`
	AllocationID int64 `json:"allocation_id" gorm:"uniqueIndex:idx_bonus_restore,priority:1"`
	LedgerID     int   `json:"ledger_id" gorm:"uniqueIndex:idx_bonus_restore,priority:2"`
	Quota        int   `json:"quota" gorm:"type:bigint;not null"`
	CreatedAt    int64 `json:"created_at" gorm:"autoCreateTime"`
}

func (*BonusSourceRestoration) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*BonusSourceRestoration) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type BonusSourceRemoval struct {
	ID        int64  `json:"id" gorm:"primaryKey"`
	UserID    int    `json:"user_id" gorm:"index"`
	LotID     int64  `json:"lot_id" gorm:"uniqueIndex:idx_bonus_removal,priority:1"`
	LedgerID  int    `json:"ledger_id" gorm:"uniqueIndex:idx_bonus_removal,priority:2"`
	Kind      string `json:"kind" gorm:"type:varchar(32)"`
	Quota     int    `json:"quota" gorm:"type:bigint"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*BonusSourceRemoval) BeforeUpdate(*gorm.DB) error    { return ErrAgentLedgerImmutable }
func (*BonusSourceRemoval) BeforeDelete(*gorm.DB) error    { return ErrAgentLedgerImmutable }
func (*BonusSourceAccount) BeforeUpdate(*gorm.DB) error    { return ErrAgentLedgerImmutable }
func (*BonusSourceAccount) BeforeDelete(*gorm.DB) error    { return ErrAgentLedgerImmutable }
func (*BonusSourceAllocation) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*BonusSourceAllocation) BeforeUpdate(tx *gorm.DB) error {
	if tx.Statement.Changed("UserID", "LotID", "LedgerID", "ReferenceType", "ReferenceID", "Amount", "CreatedAt") {
		return ErrAgentLedgerImmutable
	}
	return nil
}

type BonusSourceAnomaly struct {
	ID        int64  `json:"id" gorm:"primaryKey"`
	UserID    int    `json:"user_id" gorm:"index"`
	Key       string `json:"-" gorm:"type:varchar(96);uniqueIndex"`
	Code      string `json:"code" gorm:"type:varchar(48)"`
	Detail    string `json:"detail" gorm:"type:varchar(255)"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

func BonusSourceModels() []any {
	return []any{&BonusSourceLot{}, &BonusSourceState{}, &BonusSourceAccount{}, &BonusSourceAllocation{}, &BonusSourceRestoration{}, &BonusSourceRemoval{}, &BonusSourceAnomaly{}, &BonusSourceReconciliationCursor{}}
}

// Internal-only terms are never bindable from a wallet HTTP request.
type bonusSourceGrant struct {
	CampaignID, ClaimID, ExpiresAt int64
	Revocable                      bool
}

func validateBonusWallet(user *User) error {
	total := 0
	for _, n := range []int{user.QuotaPaid, user.QuotaBonus, user.QuotaReward} {
		if n < 0 || n > common.MaxWalletQuota-total {
			return ErrBonusSourceInvariant
		}
		total += n
	}
	if total != user.Quota {
		return ErrBonusSourceInvariant
	}
	return nil
}
func ensureBonusSourceAccountTx(tx *gorm.DB, user *User) error {
	if err := validateBonusWallet(user); err != nil {
		return err
	}
	var account BonusSourceAccount
	err := tx.First(&account, user.Id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var count int64
		if err := tx.Model(&BonusSourceLot{}).Where("user_id = ?", user.Id).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrBonusSourceInvariant
		}
		var last BalanceLedger
		if err := tx.Where("user_id = ?", user.Id).Order("id DESC").Limit(1).Find(&last).Error; err != nil {
			return err
		}
		account = BonusSourceAccount{UserID: user.Id, CutoverLedgerID: last.Id}
		if err := tx.Create(&account).Error; err != nil {
			return err
		}
		if user.QuotaBonus > 0 {
			lot := BonusSourceLot{UserID: user.Id, SourceKey: fmt.Sprintf("legacy-bonus:%d", user.Id), Kind: "legacy_nonexpiring", GrantQuota: user.QuotaBonus, RefundTerms: "restore_original_expiry"}
			if err := tx.Create(&lot).Error; err != nil {
				return err
			}
			if err := tx.Create(&BonusSourceState{LotID: lot.ID, UserID: user.Id, Available: user.QuotaBonus}).Error; err != nil {
				return err
			}
		}
	} else if err != nil {
		return err
	}
	var states []BonusSourceState
	if err := tx.Where("user_id = ?", user.Id).Find(&states).Error; err != nil {
		return err
	}
	total := 0
	for _, s := range states {
		var lot BonusSourceLot
		if err := tx.First(&lot, s.LotID).Error; err != nil {
			return ErrBonusSourceInvariant
		}
		if lot.UserID != user.Id || s.Available < 0 || s.Outstanding < 0 || s.Expired < 0 || s.Revoked < 0 || s.LegacyRestored < 0 || lot.GrantQuota < 0 || lot.GrantQuota > common.MaxWalletQuota || lot.ExpiresAt < 0 || s.RevocationActive && !lot.Revocable || s.Available > lot.GrantQuota || s.Outstanding > lot.GrantQuota-s.Available || s.Expired > lot.GrantQuota-s.Available-s.Outstanding || s.Revoked > lot.GrantQuota-s.Available-s.Outstanding-s.Expired || s.LegacyRestored != lot.GrantQuota-s.Available-s.Outstanding-s.Expired-s.Revoked || s.Available > common.MaxWalletQuota-total {
			return ErrBonusSourceInvariant
		}
		total += s.Available
	}
	if total != user.QuotaBonus {
		return ErrBonusSourceInvariant
	}
	var lots int64
	if err := tx.Model(&BonusSourceLot{}).Where("user_id = ?", user.Id).Count(&lots).Error; err != nil {
		return err
	}
	if lots != int64(len(states)) {
		return ErrBonusSourceInvariant
	}
	return nil
}

func bonusSourceRefundReason(entry LedgerEntry) bool {
	return entry.Reason == LedgerReasonBillingRefund || entry.Reason == LedgerReasonTaskRefund || entry.Reason == LedgerReasonBillingSettle
}

// Historical refunds get explicitly non-expiring provenance only for original
// pre-cutover debit rows. Resolve the complete persisted task/relay reference
// family BEFORE ordering the ledger: task refunds and relay pre-debits are one
// history, not separate balances. No post-cutover gap is treated as legacy.
func importLegacyBonusDebitsTx(tx *gorm.DB, userID int, refs [][2]string) ([][2]string, error) {
	var account BonusSourceAccount
	if err := tx.First(&account, userID).Error; err != nil {
		return nil, err
	}
	if account.CutoverLedgerID == 0 {
		return refs, nil
	}
	initialScope := tx.Where("reference_type = ? AND reference_id = ?", refs[0][0], refs[0][1])
	for _, ref := range refs[1:] {
		initialScope = initialScope.Or("reference_type = ? AND reference_id = ?", ref[0], ref[1])
	}
	var historicalDebits int64
	if err := tx.Model(&BalanceLedger{}).Where("user_id = ? AND account = ? AND id <= ? AND delta < 0", userID, AccountBonus, account.CutoverLedgerID).Where(initialScope).Count(&historicalDebits).Error; err != nil {
		return nil, err
	}
	if historicalDebits == 0 {
		// A fully source-tracked new receipt has no dependency on an unrelated
		// broken old task. Missing NEW allocations still fail in the caller.
		return refs, nil
	}
	// Only historical task references need reverse resolution. This migration
	// path is not used by new accounts and never scans task credentials/payloads.
	var historicalRefs []BalanceLedger
	if err := tx.Model(&BalanceLedger{}).Select("DISTINCT reference_type, reference_id, request_id").Where("user_id = ? AND account = ? AND id <= ? AND reference_type IN ?", userID, AccountBonus, account.CutoverLedgerID, []string{"task", "mj_task"}).Find(&historicalRefs).Error; err != nil {
		return nil, err
	}
	families := make([][][2]string, 0, len(historicalRefs))
	for _, row := range historicalRefs {
		family, err := bonusSourceRefundRefsTx(tx, LedgerEntry{UserId: userID, ReferenceType: row.ReferenceType, ReferenceId: row.ReferenceId, RequestId: row.RequestId})
		if err != nil {
			return nil, err
		}
		families = append(families, family)
	}
	seen := map[[2]string]bool{}
	for _, ref := range refs {
		seen[ref] = true
	}
	for changed := true; changed; {
		changed = false
		for _, family := range families {
			linked := false
			for _, ref := range family {
				linked = linked || seen[ref]
			}
			if linked {
				for _, ref := range family {
					if !seen[ref] {
						seen[ref] = true
						refs = append(refs, ref)
						changed = true
					}
				}
			}
		}
	}
	scope := tx.Where("reference_type = ? AND reference_id = ?", refs[0][0], refs[0][1])
	for _, ref := range refs[1:] {
		scope = scope.Or("reference_type = ? AND reference_id = ?", ref[0], ref[1])
	}
	var rows []BalanceLedger
	if err := tx.Where("user_id = ? AND account = ? AND id <= ?", userID, AccountBonus, account.CutoverLedgerID).Where(scope).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	type historicalDebit struct {
		row          BalanceLedger
		amount       int
		refunded     int
		restorations map[int]int
	}
	debits := []historicalDebit{}
	for _, row := range rows {
		if row.Delta == 0 || row.Delta > 0 && !bonusSourceRefundReason(LedgerEntry{Reason: row.Reason}) {
			continue
		}
		op, details, err := loadLedgerOperation(tx, row.IdempotencyKey)
		if err != nil || op == nil || op.UserId != userID || op.Id != row.OperationId || len(details) == 0 || row.Delta < -common.MaxWalletQuota || row.Delta > common.MaxWalletQuota || row.BalanceBefore < 0 || row.BalanceAfter < 0 || row.BalanceAfter-row.BalanceBefore != row.Delta {
			return nil, ErrBonusSourceInvariant
		}
		if row.Delta < 0 {
			if op.Operation != string(ledgerOpDebit) {
				return nil, ErrBonusSourceInvariant
			}
			if row.ReferenceType == "relay" {
				independent, err := verifiedIndependentBillingDebitTx(tx, &BillingFinalization{UserID: userID, RequestID: row.ReferenceId}, row)
				if err != nil {
					return nil, err
				}
				if independent {
					continue
				}
			}
			debits = append(debits, historicalDebit{row: row, amount: -row.Delta, restorations: map[int]int{}})
			continue
		}
		if op.Operation != string(ledgerOpCredit) {
			return nil, ErrBonusSourceInvariant
		}
		remaining := row.Delta
		for i := range debits {
			d := &debits[i]
			n := min(remaining, d.amount-d.refunded)
			if n > 0 {
				d.refunded += n
				d.restorations[row.Id] = n
				remaining -= n
			}
		}
		if remaining != 0 {
			return nil, ErrBonusSourceInvariant
		}
	}
	for _, d := range debits {
		var allocations []BonusSourceAllocation
		if err := tx.Where("ledger_id = ?", d.row.Id).Find(&allocations).Error; err != nil {
			return nil, err
		}
		if len(allocations) > 1 {
			return nil, ErrBonusSourceInvariant
		}
		if len(allocations) == 1 {
			a := allocations[0]
			var lot BonusSourceLot
			var state BonusSourceState
			if err := tx.First(&lot, a.LotID).Error; err != nil {
				return nil, err
			}
			if err := tx.First(&state, a.LotID).Error; err != nil {
				return nil, err
			}
			if a.UserID != userID || a.Amount != d.amount || a.Refunded < d.refunded || a.Refunded > a.Amount || a.ReferenceType != d.row.ReferenceType || a.ReferenceID != d.row.ReferenceId || lot.SourceKey != fmt.Sprintf("legacy-debit:%d", d.row.Id) || lot.Kind != "legacy_debit" || lot.ExpiresAt != 0 || lot.GrantQuota != d.amount || state.LegacyRestored != d.refunded {
				return nil, ErrBonusSourceInvariant
			}
			var restored []BonusSourceRestoration
			if err := tx.Where("allocation_id = ? AND ledger_id <= ?", a.ID, account.CutoverLedgerID).Find(&restored).Error; err != nil {
				return nil, err
			}
			if len(restored) != len(d.restorations) {
				return nil, ErrBonusSourceInvariant
			}
			for _, r := range restored {
				if d.restorations[r.LedgerID] != r.Quota {
					return nil, ErrBonusSourceInvariant
				}
			}
			continue
		}
		lot := BonusSourceLot{UserID: userID, SourceKey: fmt.Sprintf("legacy-debit:%d", d.row.Id), Kind: "legacy_debit", GrantQuota: d.amount, LedgerOperationID: d.row.OperationId, RefundTerms: "restore_original_expiry"}
		if err := tx.Create(&lot).Error; err != nil {
			return nil, err
		}
		// Historical refunds are already present in the baseline available lot.
		// Record their original mappings, but never credit their amount again.
		if err := tx.Create(&BonusSourceState{LotID: lot.ID, UserID: userID, Outstanding: d.amount - d.refunded, LegacyRestored: d.refunded}).Error; err != nil {
			return nil, err
		}
		a := BonusSourceAllocation{UserID: userID, LotID: lot.ID, LedgerID: d.row.Id, ReferenceType: d.row.ReferenceType, ReferenceID: d.row.ReferenceId, Amount: d.amount, Refunded: d.refunded}
		if err := tx.Create(&a).Error; err != nil {
			return nil, err
		}
		for ledgerID, quota := range d.restorations {
			if err := tx.Create(&BonusSourceRestoration{AllocationID: a.ID, LedgerID: ledgerID, Quota: quota}).Error; err != nil {
				return nil, err
			}
		}
	}
	return refs, nil
}

func bonusSourceRefundRefsTx(tx *gorm.DB, entry LedgerEntry) ([][2]string, error) {
	refs := [][2]string{{entry.ReferenceType, entry.ReferenceId}}
	if entry.ReferenceType == "task" {
		var tasks []Task
		if err := tx.Where("user_id = ? AND task_id = ?", entry.UserId, entry.ReferenceId).Limit(2).Find(&tasks).Error; err != nil {
			return nil, err
		}
		if len(tasks) != 1 {
			return nil, ErrBonusSourceInvariant
		}
		original := tasks[0].PrivateData.EffectiveRequestID()
		if original != "" {
			refs = append(refs, [2]string{"relay", original})
		}
	}
	if entry.ReferenceType == "mj_task" {
		var rows []Midjourney
		query := tx.Where("user_id = ?", entry.UserId)
		if entry.RequestId != "" {
			query = query.Where("mj_id = ?", entry.ReferenceId)
		} else {
			id, err := strconv.Atoi(entry.ReferenceId)
			if err != nil || id <= 0 {
				return nil, ErrBonusSourceInvariant
			}
			query = query.Where("id = ?", id)
		}
		if err := query.Limit(2).Find(&rows).Error; err != nil {
			return nil, err
		}
		if len(rows) != 1 || rows[0].BillingRequestID == "" {
			return nil, ErrBonusSourceInvariant
		}
		// Legacy numeric IDs and later public IDs share the ledger reference
		// column. A collision must not join two unrelated financial histories.
		aliases := tx.Where("mj_id = ?", strconv.Itoa(rows[0].Id))
		if rows[0].MjId != "" {
			aliases = aliases.Or("mj_id = ?", rows[0].MjId)
		}
		if publicID, err := strconv.Atoi(rows[0].MjId); err == nil && publicID > 0 {
			aliases = aliases.Or("id = ?", publicID)
		}
		var collisions int64
		if err := tx.Model(&Midjourney{}).Where("user_id = ? AND id <> ?", entry.UserId, rows[0].Id).Where(aliases).Count(&collisions).Error; err != nil {
			return nil, err
		}
		if collisions != 0 {
			return nil, ErrBonusSourceInvariant
		}
		refs = append(refs, [2]string{"relay", rows[0].BillingRequestID}, [2]string{"mj_task", strconv.Itoa(rows[0].Id)})
		if rows[0].MjId != "" {
			refs = append(refs, [2]string{"mj_task", rows[0].MjId})
		}
	}
	if entry.ReferenceType == "" || entry.ReferenceId == "" {
		return nil, ErrMissingWalletDebitReceipt
	}
	return refs, nil
}

func applyBonusSourceLedgerTx(tx *gorm.DB, user *User, entry LedgerEntry, op ledgerOpKind, row BalanceLedger) error {
	if row.Account != string(AccountBonus) || row.Delta == 0 {
		return nil
	}
	if op == ledgerOpTransfer && row.Delta < 0 {
		return fmt.Errorf("%w: noncash bonus cannot change wallet class", ErrLedgerInvalidEntry)
	}
	if row.Delta > 0 {
		if bonusSourceRefundReason(entry) && op == ledgerOpCredit {
			refs, err := bonusSourceRefundRefsTx(tx, entry)
			if err != nil {
				return err
			}
			refs, err = importLegacyBonusDebitsTx(tx, user.Id, refs)
			if err != nil {
				return err
			}
			var allocations []BonusSourceAllocation
			query := tx.Where("user_id = ? AND refunded < amount", user.Id)
			scope := tx.Where("reference_type = ? AND reference_id = ?", refs[0][0], refs[0][1])
			for _, ref := range refs[1:] {
				scope = scope.Or("reference_type = ? AND reference_id = ?", ref[0], ref[1])
			}
			if err := query.Where(scope).Order("id").Find(&allocations).Error; err != nil {
				return err
			}
			remaining := row.Delta
			for _, a := range allocations {
				var original BalanceLedger
				if err := tx.First(&original, a.LedgerID).Error; err != nil {
					return ErrBonusSourceInvariant
				}
				if original.UserId != user.Id || original.Account != string(AccountBonus) || original.Delta >= 0 || a.Amount > -original.Delta {
					return ErrBonusSourceInvariant
				}
				if original.ReferenceType == "relay" {
					independent, err := verifiedIndependentBillingDebitTx(tx, &BillingFinalization{UserID: user.Id, RequestID: original.ReferenceId}, original)
					if err != nil {
						return err
					}
					if independent {
						continue
					}
				}
				n := min(remaining, a.Amount-a.Refunded)
				if n <= 0 {
					continue
				}
				var lot BonusSourceLot
				var state BonusSourceState
				if err := tx.First(&lot, a.LotID).Error; err != nil {
					return err
				}
				if err := tx.First(&state, a.LotID).Error; err != nil {
					return err
				}
				if lot.UserID != user.Id || state.Outstanding < n || lot.RefundTerms != "restore_original_expiry" {
					return ErrBonusSourceInvariant
				}
				if err := tx.Model(&a).Update("refunded", a.Refunded+n).Error; err != nil {
					return err
				}
				if err := tx.Model(&state).Updates(map[string]any{"available": state.Available + n, "outstanding": state.Outstanding - n}).Error; err != nil {
					return err
				}
				if err := tx.Create(&BonusSourceRestoration{AllocationID: a.ID, LedgerID: row.Id, Quota: n}).Error; err != nil {
					return err
				}
				remaining -= n
			}
			if remaining != 0 {
				return ErrMissingWalletDebitReceipt
			}
			return nil
		}
		lot := BonusSourceLot{UserID: user.Id, SourceKey: fmt.Sprintf("ledger:%d", row.Id), Kind: entry.Reason, GrantQuota: row.Delta, LedgerOperationID: row.OperationId, RefundTerms: "restore_original_expiry"}
		if len(lot.Kind) > 32 {
			lot.Kind = "wallet_bonus_credit"
		}
		if g := entry.bonusGrant; g != nil {
			lot.Kind = "campaign"
			lot.CampaignID = g.CampaignID
			lot.ClaimID = g.ClaimID
			lot.ExpiresAt = g.ExpiresAt
			lot.Revocable = g.Revocable
		}
		if err := tx.Create(&lot).Error; err != nil {
			return err
		}
		return tx.Create(&BonusSourceState{LotID: lot.ID, UserID: user.Id, Available: row.Delta}).Error
	}
	remaining := -row.Delta
	var lots []BonusSourceLot
	query := tx.Where("user_id = ?", user.Id)
	if entry.bonusExpireLot > 0 {
		query = query.Where("id = ?", entry.bonusExpireLot)
	}
	if err := query.Order("CASE WHEN expires_at = 0 THEN 1 ELSE 0 END, expires_at, id").Find(&lots).Error; err != nil {
		return err
	}
	for _, lot := range lots {
		var state BonusSourceState
		if err := tx.First(&state, lot.ID).Error; err != nil {
			return err
		}
		n := min(remaining, state.Available)
		if n == 0 {
			continue
		}
		updates := map[string]any{"available": state.Available - n}
		if entry.bonusExpireLot > 0 || op == ledgerOpOverride {
			if entry.Reason == "campaign_revoke" || op == ledgerOpOverride {
				updates["revoked"] = state.Revoked + n
			} else {
				updates["expired"] = state.Expired + n
			}
			if err := tx.Create(&BonusSourceRemoval{UserID: user.Id, LotID: lot.ID, LedgerID: row.Id, Kind: entry.Reason, Quota: n}).Error; err != nil {
				return err
			}
		} else {
			updates["outstanding"] = state.Outstanding + n
			a := BonusSourceAllocation{UserID: user.Id, LotID: lot.ID, LedgerID: row.Id, ReferenceType: row.ReferenceType, ReferenceID: row.ReferenceId, Amount: n}
			if err := tx.Create(&a).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&state).Updates(updates).Error; err != nil {
			return err
		}
		remaining -= n
	}
	if remaining != 0 {
		return ErrBonusSourceInvariant
	}
	return nil
}

func expireBonusSourceLotTx(tx *gorm.DB, user *User, lot BonusSourceLot, now int64) (bool, error) {
	var state BonusSourceState
	if err := tx.First(&state, lot.ID).Error; err != nil {
		return false, err
	}
	if state.Available == 0 || !state.RevocationActive && (lot.ExpiresAt == 0 || lot.ExpiresAt > now) {
		return false, nil
	}
	entry := LedgerEntry{UserId: user.Id, Account: AccountBonus, Delta: state.Available, Reason: "campaign_expiry", ReferenceType: "bonus_source", ReferenceId: strconv.FormatInt(lot.ID, 10), IdempotencyKey: fmt.Sprintf("bonus-expiry:%d:%d", lot.ID, state.Expired), bonusExpireLot: lot.ID}
	if state.RevocationActive {
		entry.Reason = "campaign_revoke"
		entry.IdempotencyKey = fmt.Sprintf("bonus-revocation:%d:%d", lot.ID, state.Revoked)
	}
	result, err := DebitQuotaTx(tx, entry, AccountBonus)
	if err != nil {
		return false, err
	}
	user.QuotaBonus -= state.Available
	user.Quota = result.TotalAfter
	return true, nil
}

func expireBonusSourcesTx(tx *gorm.DB, user *User, now int64) error {
	var lots []BonusSourceLot
	if err := tx.Where("user_id = ? AND ((expires_at > 0 AND expires_at <= ?) OR id IN (?))", user.Id, now, tx.Model(&BonusSourceState{}).Select("lot_id").Where("user_id = ? AND revocation_active = ?", user.Id, true)).Order("id").Find(&lots).Error; err != nil {
		return err
	}
	for _, lot := range lots {
		if _, err := expireBonusSourceLotTx(tx, user, lot, now); err != nil {
			return err
		}
	}
	return nil
}

// Full receipt reconciliation is off the hot wallet path: aggregate in SQL
// rather than loading every historical model request on every paid-only debit.
func auditBonusSourceAllocationsTx(tx *gorm.DB, userID int) error {
	var invalid int64
	if err := tx.Model(&BonusSourceAllocation{}).Where("user_id = ? AND (amount <= 0 OR refunded < 0 OR refunded > amount OR ledger_id NOT IN (?))", userID, tx.Model(&BalanceLedger{}).Select("id").Where("user_id = ? AND account = ? AND delta < 0", userID, AccountBonus)).Count(&invalid).Error; err != nil {
		return err
	}
	if invalid != 0 {
		return ErrBonusSourceInvariant
	}
	var sums []struct {
		LotID       int64
		Outstanding int
	}
	if err := tx.Model(&BonusSourceAllocation{}).Select("lot_id, SUM(amount - refunded) AS outstanding").Where("user_id = ?", userID).Group("lot_id").Scan(&sums).Error; err != nil {
		return err
	}
	actual := map[int64]int{}
	for _, sum := range sums {
		if sum.Outstanding < 0 || sum.Outstanding > common.MaxWalletQuota {
			return ErrBonusSourceInvariant
		}
		actual[sum.LotID] = sum.Outstanding
	}
	var states []BonusSourceState
	if err := tx.Where("user_id = ?", userID).Find(&states).Error; err != nil {
		return err
	}
	for _, state := range states {
		if actual[state.LotID] != state.Outstanding {
			return ErrBonusSourceInvariant
		}
		delete(actual, state.LotID)
	}
	if len(actual) > 0 {
		return ErrBonusSourceInvariant
	}
	return nil
}

// ReconcileBonusSources initializes legacy provenance without changing money.
// All anomalies are committed separately from a rolled-back account operation.
func ReconcileBonusSources(ctx context.Context, afterUserID, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, ErrLedgerInvalidEntry
	}
	var users []User
	if err := DB.WithContext(ctx).Unscoped().Where("id > ?", afterUserID).Order("id").Limit(limit).Find(&users).Error; err != nil {
		return 0, err
	}
	last := afterUserID
	for _, u := range users {
		last = u.Id
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var locked User
			if err := lockForUpdate(tx.Unscoped()).First(&locked, u.Id).Error; err != nil {
				return err
			}
			if err := ensureBonusSourceAccountTx(tx, &locked); err != nil {
				return err
			}
			return auditBonusSourceAllocationsTx(tx, locked.Id)
		})
		if err != nil {
			if !errors.Is(err, ErrBonusSourceInvariant) {
				return last, err
			}
			anomaly := BonusSourceAnomaly{UserID: u.Id, Key: fmt.Sprintf("wallet:%d", u.Id), Code: "wallet_source_mismatch", Detail: "Source or three-wallet balance mismatch; no balance changed"}
			if err := DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&anomaly).Error; err != nil {
				return last, err
			}
		}
	}
	// Retained sources survive deleted accounts; surface orphans, never transfer.
	var orphan []BonusSourceLot
	if err := DB.WithContext(ctx).Where("user_id NOT IN (?)", DB.Unscoped().Model(&User{}).Select("id")).Limit(limit).Find(&orphan).Error; err != nil {
		return last, err
	}
	for _, lot := range orphan {
		a := BonusSourceAnomaly{UserID: lot.UserID, Key: fmt.Sprintf("orphan:%d", lot.ID), Code: "orphan_source", Detail: "Source owner missing; no balance changed"}
		if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&a).Error; err != nil {
			return last, err
		}
	}
	return last, nil
}
