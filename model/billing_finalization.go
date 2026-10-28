package model

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BillingRefundPending = "refund_pending"
	BillingRefunded      = "refunded"
	BillingSettled       = "settled"
	BillingTaskOwned     = "task_owned"
	// Frozen actual consumption awaiting atomic settlement or operator review.
	// This is not a refund intent or an automatic background debit queue.
	BillingSettlementReview = "settlement_review_v2"
	// Only these versioned intents are eligible for the new refund executor.
	// Historical refund_pending rows always require operator review.
	BillingRefundReady  = "refund_ready_v2"
	BillingRefundReview = "refund_review_v2"
)

var ErrBillingFinalized = errors.New("request billing already has a different terminal owner")

// BillingFinalization is the unique request financial owner. New versioned
// held/refund-ready receipts are executable only through their explicit state
// transitions. Historical refund_pending rows remain read-only.
// Never delete historical rows while their original financial receipts survive.
type BillingFinalization struct {
	RequestID string `gorm:"type:varchar(64);primaryKey;index:idx_billing_refund_due,priority:3"`
	UserID    int    `gorm:"not null;index"`
	Source    string `gorm:"type:varchar(16);not null"`
	TokenID   int    `gorm:"not null"`
	// VARCHAR preserves the empty no-token identity; PostgreSQL CHAR pads it.
	TokenFingerprint string `json:"-" gorm:"type:varchar(64);not null"`
	// Legacy source and token reservations need not be equal. Never fabricate a
	// token credit from the source total or vice versa.
	SourceHeld         int    `gorm:"type:bigint;not null"`
	TokenHeld          int    `gorm:"type:bigint;not null"`
	PreConsumed        int    `gorm:"type:bigint;not null"`
	Paid               int    `gorm:"type:bigint;not null"`
	Bonus              int    `gorm:"type:bigint;not null"`
	Reward             int    `gorm:"type:bigint;not null"`
	SubscriptionID     int    `gorm:"not null"`
	SubscriptionPeriod int64  `gorm:"type:bigint;not null"`
	State              string `gorm:"type:varchar(24);not null;index:idx_billing_refund_queue,priority:1;index:idx_billing_refund_due,priority:1"`
	ActualQuota        int    `gorm:"type:bigint;not null"`
	TaskID             int64  `gorm:"not null"`
	TaskPublicID       string `gorm:"type:varchar(191);not null"`
	// Operational metadata never changes refund eligibility or money ownership.
	// Historical retry fields remain readable; no current worker schedules them.
	Attempts        int    `gorm:"not null"` // Failed recovery attempts, retained after recovery.
	FirstErrorCode  string `gorm:"type:varchar(48);not null;default:''"`
	FirstErrorClass string `gorm:"type:varchar(24);not null;default:''"`
	LastErrorCode   string `gorm:"type:varchar(48);not null;default:''"`
	LastErrorClass  string `gorm:"type:varchar(24);not null;default:''"`
	FirstFailedAt   int64  `gorm:"not null;default:0"`
	LastAttemptAt   int64  `gorm:"not null;default:0"`
	NextRetryAt     int64  `gorm:"not null;default:0;index:idx_billing_refund_due,priority:2"`
	EscalatedAt     int64  `gorm:"not null;default:0"`
	CreatedAt       int64  `gorm:"autoCreateTime"`
	UpdatedAt       int64  `gorm:"autoUpdateTime;index:idx_billing_refund_queue,priority:2"`
}

func BillingTokenFingerprint(key string) string {
	if key == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

func (r BillingFinalization) Shares() []LedgerShare {
	var shares []LedgerShare
	for _, s := range []LedgerShare{{AccountPaid, r.Paid}, {AccountBonus, r.Bonus}, {AccountReward, r.Reward}} {
		if s.Amount > 0 {
			shares = append(shares, s)
		}
	}
	return shares
}

func (r BillingFinalization) validate() error {
	if r.RequestID == "" || strings.TrimSpace(r.RequestID) != r.RequestID || len(r.RequestID) > 64 || r.UserID <= 0 {
		return billingRefundReviewError("receipt_invalid", "invalid billing finalization identity")
	}
	for _, n := range []int{r.SourceHeld, r.TokenHeld, r.PreConsumed, r.Paid, r.Bonus, r.Reward} {
		if n < 0 || n > common.MaxQuota {
			return billingRefundReviewError("receipt_invalid", "invalid billing held amount")
		}
	}
	if r.TokenID < 0 || r.TokenID == 0 && (r.TokenHeld != 0 || r.TokenFingerprint != "") || r.TokenID > 0 && len(r.TokenFingerprint) != 64 {
		return billingRefundReviewError("receipt_invalid", "invalid original billing token identity")
	}
	switch r.Source {
	case "wallet":
		if r.SourceHeld != r.Paid+r.Bonus+r.Reward || r.SubscriptionID != 0 || r.SubscriptionPeriod != 0 {
			return billingRefundReviewError("receipt_invalid", "wallet reservation shares mismatch")
		}
	case "subscription":
		if r.SubscriptionID <= 0 || r.SubscriptionPeriod <= 0 || r.Paid+r.Bonus+r.Reward != 0 {
			return billingRefundReviewError("receipt_invalid", "invalid subscription reservation identity")
		}
	default:
		return billingRefundReviewError("receipt_invalid", "unsupported durable billing source")
	}
	return nil
}

func sameBillingIdentity(a, b BillingFinalization) bool {
	return a.RequestID == b.RequestID && a.UserID == b.UserID && a.Source == b.Source && a.TokenID == b.TokenID && subtle.ConstantTimeCompare([]byte(a.TokenFingerprint), []byte(b.TokenFingerprint)) == 1 && a.SourceHeld == b.SourceHeld && a.TokenHeld == b.TokenHeld && a.PreConsumed == b.PreConsumed && a.Paid == b.Paid && a.Bonus == b.Bonus && a.Reward == b.Reward && a.SubscriptionID == b.SubscriptionID && a.SubscriptionPeriod == b.SubscriptionPeriod
}

// INSERT ... ON CONFLICT avoids poisoning a PostgreSQL transaction on first-owner
// races. The following current/locking read observes the committed winner on
// MySQL as well; no absent consistent snapshot is established first.
func claimBillingFinalizationTx(tx *gorm.DB, receipt BillingFinalization) (*BillingFinalization, error) {
	if err := receipt.validate(); err != nil {
		return nil, err
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	receipt.State = "b:" + hex.EncodeToString(nonce[:]) // private, never committed
	receipt.ActualQuota, receipt.TaskID, receipt.Attempts = 0, 0, 0
	receipt.TaskPublicID = ""
	receipt.FirstErrorCode, receipt.FirstErrorClass, receipt.LastErrorCode, receipt.LastErrorClass = "", "", "", ""
	receipt.FirstFailedAt, receipt.LastAttemptAt, receipt.NextRetryAt, receipt.EscalatedAt = 0, 0, 0, 0
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&receipt).Error; err != nil {
		return nil, err
	}
	var stored BillingFinalization
	if err := lockForUpdate(tx).Where("request_id = ?", receipt.RequestID).First(&stored).Error; err != nil {
		return nil, err
	}
	if !sameBillingIdentity(stored, receipt) {
		return nil, billingRefundReviewError("identity_mismatch", "billing finalization identity mismatch")
	}
	if stored.State == receipt.State {
		stored.State = "building"
	} else {
		switch stored.State {
		case BillingRefundPending, BillingRefunded, BillingSettled, BillingTaskOwned, BillingRefundReady, BillingRefundReview, BillingHeld, BillingDeliveryUnknown, BillingSettlementReview:
		default:
			return nil, billingRefundReviewError("receipt_invalid", "invalid persisted billing finalization state")
		}
	}
	return &stored, nil
}

// validateBillingSourceTx verifies ORIGINAL held evidence under authoritative
// source locks, never from a current balance. Terminal-owner replays bypass it:
// successful settlement/task processing legitimately changes the source later.
func validateBillingSourceTx(tx *gorm.DB, r *BillingFinalization) error {
	if err := LockFinancialUserTx(tx, r.UserID); err != nil {
		return err
	}
	if r.Source == "subscription" {
		identity, err := GetSubscriptionBillingIdentityTx(tx, r.RequestID, r.UserID, r.SubscriptionID, r.SubscriptionPeriod, int64(r.SourceHeld))
		if err != nil {
			return err
		}
		return ValidateSubscriptionPreConsumeTx(tx, identity, int64(r.SourceHeld))
	}
	if r.Source != "wallet" {
		return billingRefundReviewError("source_evidence", "unsupported billing finalization source")
	}
	var user User
	if err := lockForUpdate(tx).First(&user, r.UserID).Error; err != nil {
		return err
	}
	var rows []BalanceLedger
	if err := lockForUpdate(tx).Where("user_id = ? AND reference_type = ? AND reference_id = ?", r.UserID, "relay", r.RequestID).Order("id").Find(&rows).Error; err != nil {
		return err
	}
	paid, bonus, reward := 0, 0, 0
	checked := map[string]bool{}
	for _, row := range rows {
		if independent, err := verifiedIndependentBillingDebitTx(tx, r, row); err != nil {
			return err
		} else if independent {
			continue
		}
		if row.RequestId != r.RequestID || row.Delta >= 0 || (row.Reason != LedgerReasonBillingPreConsume && row.Reason != LedgerReasonBillingReserve) {
			return billingRefundReviewError("source_evidence", "wallet reservation already adjusted or inconsistent")
		}
		if !checked[row.IdempotencyKey] {
			op, details, err := loadLedgerOperation(lockForUpdate(tx), row.IdempotencyKey)
			if err != nil {
				return err
			}
			if op.UserId != r.UserID || op.Operation != "debit" || op.Id != row.OperationId || len(details) == 0 {
				return billingRefundReviewError("source_evidence", "wallet reservation operation mismatch")
			}
			total := 0
			for _, detail := range details {
				if detail.UserId != r.UserID || detail.ReferenceType != "relay" || detail.ReferenceId != r.RequestID || detail.RequestId != r.RequestID || detail.IdempotencyKey != row.IdempotencyKey || detail.Delta >= 0 || detail.Delta < -common.MaxQuota || detail.Reason != row.Reason || !validAccount(LedgerAccount(detail.Account)) || detail.TotalBefore != op.TotalBefore || detail.TotalAfter != op.TotalAfter {
					return billingRefundReviewError("source_evidence", "wallet reservation operation rows mismatch")
				}
				total -= detail.Delta
				if total > common.MaxQuota {
					return billingRefundReviewError("source_evidence", "wallet reservation amount invalid")
				}
			}
			if op.TotalBefore-op.TotalAfter != total {
				return billingRefundReviewError("source_evidence", "wallet reservation operation amount mismatch")
			}
			expectedKey := "billing:preconsume:" + r.RequestID
			if row.Reason == LedgerReasonBillingReserve {
				prefix := "billing:reserve:" + r.RequestID + ":"
				if !strings.HasPrefix(row.IdempotencyKey, prefix) {
					return billingRefundReviewError("source_evidence", "wallet reserve key mismatch")
				}
				target, err := strconv.ParseInt(strings.TrimPrefix(row.IdempotencyKey, prefix), 10, 64)
				if err != nil || target <= 0 || target > common.MaxQuota {
					return billingRefundReviewError("source_evidence", "invalid wallet reserve target")
				}
				expectedKey = fmt.Sprintf("%s%d", prefix, target)
			}
			if row.IdempotencyKey != expectedKey {
				return billingRefundReviewError("source_evidence", "wallet reservation key mismatch")
			}
			checked[row.IdempotencyKey] = true
		}
		switch LedgerAccount(row.Account) {
		case AccountPaid:
			paid -= row.Delta
		case AccountBonus:
			bonus -= row.Delta
		case AccountReward:
			reward -= row.Delta
		default:
			return billingRefundReviewError("source_evidence", "unknown wallet reservation account")
		}
	}
	if paid != r.Paid || bonus != r.Bonus || reward != r.Reward {
		return billingRefundReviewError("source_evidence", "wallet held evidence mismatch")
	}
	return nil
}

// ApplyBillingTokenDeltaTx restores/debits ONLY the original authorized token.
// Soft deletion does not revive authentication; hard deletion leaves no counter.
func ApplyBillingTokenDeltaTx(tx *gorm.DB, r *BillingFinalization, delta int) error {
	// No token mutation is requested: a replacement cannot receive credit here.
	if delta == 0 {
		return nil
	}
	if r.TokenID == 0 {
		return nil
	}
	var token Token
	err := lockForUpdate(tx.Unscoped()).First(&token, r.TokenID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if token.UserId != r.UserID || subtle.ConstantTimeCompare([]byte(BillingTokenFingerprint(token.Key)), []byte(r.TokenFingerprint)) != 1 {
		return billingRefundReviewError("identity_mismatch", "original billing token identity mismatch")
	}
	return ApplyTokenQuotaDeltaTx(tx.Unscoped(), token.Id, token.Key, delta)
}

// SettleBillingFinalization runs the existing source/token arithmetic under one
// terminal owner. The callback must reject source-only legacy replays: this row
// is the only proof that both sides and the terminal marker committed together.
func SettleBillingFinalization(ctx context.Context, receipt BillingFinalization, actual int, apply func(*gorm.DB) error) (bool, error) {
	if actual < 0 || actual > common.MaxQuota {
		return false, errors.New("invalid billing actual quota")
	}
	// Commit the observed charge before attempting money movement. A rollback,
	// insufficient balance or a process exit must not erase the charge and make
	// the original hold refundable. Replays retain the exact original amount.
	if err := RequestBillingSettlement(ctx, receipt, actual); err != nil {
		return false, err
	}
	applied := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		if r.State == BillingSettled {
			if r.ActualQuota != actual {
				return errors.New("billing settlement replay amount mismatch")
			}
			return nil
		}
		if r.State == "building" {
			// In particular, Settle(0) must not bypass refund admission by
			// reconstructing an unrecorded token hold from a legacy caller.
			return billingRefundReviewError("owner_missing", "settlement requires an atomically reserved request owner")
		}
		if r.State != BillingSettlementReview {
			return ErrBillingFinalized
		}
		if r.ActualQuota != actual {
			return errors.New("billing settlement replay amount mismatch")
		}
		if err = validateBillingSourceTx(tx, r); err != nil {
			return err
		}
		if err = apply(tx); err != nil {
			return err
		}
		applied = true
		return tx.Model(r).Updates(map[string]any{
			"state": BillingSettled, "actual_quota": actual, "next_retry_at": 0,
			"last_error_code": "", "last_error_class": "", "escalated_at": 0,
		}).Error
	})
	return applied && err == nil, err
}

func billingTaskIdentityMatches(r *BillingFinalization, t *Task) bool {
	return t.TaskID != "" && t.UserId == r.UserID && t.PrivateData.EffectiveRequestID() == r.RequestID && t.PrivateData.BillingSource == r.Source && t.PrivateData.TokenId == r.TokenID && t.PrivateData.SubscriptionId == r.SubscriptionID && t.PrivateData.SubscriptionQuotaPeriod == r.SubscriptionPeriod
}

// ProbeBillingFinalization waits for an uncertain COMMIT through the unique
// owner key. An absent owner is proved by inserting then ROLLING BACK a private
// building row; it never creates a reservation journal. This is only used when
// a previous terminal attempt's ACK was uncertain, before allowing more reserve.
func ProbeBillingFinalization(ctx context.Context, receipt BillingFinalization) (*BillingFinalization, error) {
	var stored *BillingFinalization
	absent := errors.New("billing finalization absent")
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		if r.State == "building" {
			return absent
		}
		stored = r
		return nil
	})
	if errors.Is(err, absent) {
		return nil, nil
	}
	return stored, err
}

// Independent fees retain their legacy key/signature and carry explicit new
// provenance. Realtime segments already have a dedicated canonical wss:N key.
// Neither is part of the original hold; never broadly ignore settle rows.
func verifiedIndependentBillingDebitTx(tx *gorm.DB, r *BillingFinalization, row BalanceLedger) (bool, error) {
	if row.Reason != LedgerReasonBillingSettle || row.Delta >= 0 {
		return false, nil
	}
	isFee := func(meta string) bool {
		var value struct {
			IndependentFee string `json:"independent_fee"`
		}
		return common.UnmarshalJsonStr(meta, &value) == nil && value.IndependentFee == "violation_fee"
	}
	fee := isFee(row.Metadata)
	var sequence uint64
	if !fee {
		prefix := "billing:settle:" + r.RequestID + ":wss:"
		if !strings.HasPrefix(row.IdempotencyKey, prefix) {
			return false, nil
		}
		parts := strings.Split(strings.TrimPrefix(row.IdempotencyKey, prefix), ":")
		if len(parts) != 2 {
			return false, billingRefundReviewError("source_evidence", "invalid realtime debit identity")
		}
		var err error
		sequence, err = strconv.ParseUint(parts[0], 10, 64)
		if err != nil || sequence == 0 {
			return false, billingRefundReviewError("source_evidence", "invalid realtime debit sequence")
		}
	}
	op, details, err := loadLedgerOperation(lockForUpdate(tx), row.IdempotencyKey)
	if err != nil {
		return false, err
	}
	if op.UserId != r.UserID || op.Operation != "debit" || op.Id != row.OperationId || len(details) == 0 {
		return false, billingRefundReviewError("source_evidence", "independent billing operation mismatch")
	}
	total := 0
	for _, d := range details {
		validPurpose := fee && isFee(d.Metadata) || !fee && d.Metadata == ""
		if d.UserId != r.UserID || d.RequestId != r.RequestID || d.ReferenceType != "relay" || d.ReferenceId != r.RequestID || d.Reason != LedgerReasonBillingSettle || d.IdempotencyKey != row.IdempotencyKey || d.TotalBefore != op.TotalBefore || d.TotalAfter != op.TotalAfter || d.Delta >= 0 || d.Delta < -common.MaxQuota || !validAccount(LedgerAccount(d.Account)) || !validPurpose {
			return false, billingRefundReviewError("source_evidence", "independent billing rows mismatch")
		}
		total -= d.Delta
		if total > common.MaxQuota {
			return false, billingRefundReviewError("source_evidence", "independent billing amount invalid")
		}
	}
	expectedKey := fmt.Sprintf("billing:settle:%s:%d", r.RequestID, total)
	if !fee {
		expectedKey = fmt.Sprintf("billing:settle:%s:wss:%d:%d", r.RequestID, sequence, total)
	}
	if row.IdempotencyKey != expectedKey || op.TotalBefore-op.TotalAfter != total {
		return false, billingRefundReviewError("source_evidence", "independent billing key mismatch")
	}
	return true, nil
}
