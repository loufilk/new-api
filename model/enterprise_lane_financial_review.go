//go:build enterprise_lane_lab

package model

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	EnterpriseLaneFinancialReviewMaxPageSize = 128
	EnterpriseLaneFinancialReviewMaxReceipts = 4096
	enterpriseLaneFinancialReviewTimeout     = 5 * time.Second
	enterpriseLaneReviewMarkerSource         = "enterprise_lane"
	enterpriseLaneReviewMarkerState          = "lane_owner_v1"
)

var (
	ErrEnterpriseLaneReviewScope         = errors.New("invalid isolated lane financial review scope")
	ErrEnterpriseLaneReviewUnavailable   = errors.New("isolated lane financial review schema or query unavailable")
	ErrEnterpriseLaneReviewMissing       = errors.New("isolated lane financial review evidence is missing")
	ErrEnterpriseLaneReviewInconsistent  = errors.New("isolated lane financial review evidence is inconsistent")
	ErrEnterpriseLaneReviewOwnerConflict = errors.New("isolated lane financial review found conflicting financial ownership")
	ErrEnterpriseLaneReviewLimit         = errors.New("isolated lane financial review exceeded its receipt limit")
)

// EnterpriseLaneFinancialPartition reports quota units in one original grant.
// Paid and finite-token units are two enforceable counters for the SAME spend;
// they must never be added to estimate customer charges.
type EnterpriseLaneFinancialPartition struct {
	Original    int64 `json:"original"`
	Available   int64 `json:"available"`
	Outstanding int64 `json:"outstanding"`
	Spent       int64 `json:"spent"`
	Returned    int64 `json:"returned"`
}

// EnterpriseLaneFinancialState separates historical reservation caps from money
// still held and actuals known for this state. A nil actual means no amount was
// measured, not a zero charge. Observed and over-cap actuals are not settled.
type EnterpriseLaneFinancialState struct {
	Requests     int64  `json:"requests"`
	OriginalCaps int64  `json:"original_caps"`
	Outstanding  int64  `json:"outstanding"`
	KnownActual  *int64 `json:"known_actual,omitempty"`
}

// EnterpriseLaneFinancialReview is a separate lab-only, one-epoch view. It is
// not a refund/dispatch authorization, an installation or drain check, or an
// addition to the ordinary wallet report. It never returns raw identities,
// cursors, request data, fingerprints, private ledger fields, or SQL errors.
//
// Reconciliation proves money-bearing counters against the scanned receipts,
// not historical completeness: deleted refunded or zero-actual rows leave insufficient
// evidence in these counters, and a marker without a receipt cannot be
// mapped to a lane/epoch by today's experimental marker shape. No production
// bridge is enabled, even if all scanned receipts have a matching marker.
type EnterpriseLaneFinancialReview struct {
	State                     string                                  `json:"state"`
	Paid                      EnterpriseLaneFinancialPartition        `json:"paid"`
	FiniteKey                 EnterpriseLaneFinancialPartition        `json:"finite_key"`
	ByState                   map[string]EnterpriseLaneFinancialState `json:"by_state"`
	OpenRequests              int64                                   `json:"open_requests"`
	ReceiptCount              int64                                   `json:"receipt_count"`
	MeasuredProjectionPending int64                                   `json:"measured_projection_pending"`
	CanonicalMarkerMatches    int64                                   `json:"canonical_marker_matches"`
	UnfencedLegacyReceipts    int64                                   `json:"unfenced_legacy_receipts"`
	Scope                     string                                  `json:"scope"`
}

// ReadEnterpriseLaneFinancialReview reconciles a single exact lane generation
// on an explicit, isolated PostgreSQL handle. It owns a read-only, repeatable
// read transaction; a nested handle is rejected rather than weakening that
// guarantee. Keyset pages and the total ceiling bound the selected receipts;
// the deadline also bounds work required to find them. A failure returns no
// partial totals. Callers should also use read-only credentials/snapshots.
func ReadEnterpriseLaneFinancialReview(ctx context.Context, db *gorm.DB, id EnterpriseLaneIdentity, pageSize int) (*EnterpriseLaneFinancialReview, error) {
	if ctx == nil || db == nil || db.Config == nil || db.Dialector == nil || db.Statement == nil || db.DryRun ||
		pageSize < 1 || pageSize > EnterpriseLaneFinancialReviewMaxPageSize {
		return nil, ErrEnterpriseLaneReviewScope
	}
	_, nestedStatement := db.Statement.ConnPool.(gorm.TxCommitter)
	_, nestedConfig := db.Config.ConnPool.(gorm.TxCommitter)
	if nestedStatement || nestedConfig {
		return nil, ErrEnterpriseLaneReviewScope
	}
	if err := enterpriseLaneValidate(db, id); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, enterpriseLaneFinancialReviewTimeout)
	defer cancel()
	reader := db.Session(&gorm.Session{NewDB: true, SkipHooks: true, Logger: logger.Discard, Context: ctx})
	var out *EnterpriseLaneFinancialReview
	err := reader.Transaction(func(tx *gorm.DB) error {
		var lane EnterpriseLane
		if err := tx.Select("lane_key", "lane_id", "epoch", "user_id", "token_id", "token_fingerprint", "paid_budget", "token_budget", "paid_available", "token_available", "held", "spent", "open_requests", "paid_returned", "token_returned", "state").
			Where("lane_key = ?", id.laneKey()).Take(&lane).Error; err != nil {
			return err
		}
		if lane.EnterpriseLaneIdentity != id {
			return ErrEnterpriseLaneReviewScope
		}
		if !lane.valid() {
			return ErrEnterpriseLaneReviewInconsistent
		}
		fund := id.walletEntry(lane.PaidBudget, enterpriseLaneFund)
		fund.DebitOrder = []LedgerAccount{AccountPaid}
		if err := readLaneFinancialTransfer(tx, fund, ledgerOpDebit, -lane.PaidBudget); err != nil {
			return err
		}
		if err := readLaneFinancialTransfer(tx, id.walletEntry(lane.PaidReturned, enterpriseLaneReturn), ledgerOpCredit, lane.PaidReturned); err != nil {
			return err
		}
		report := &EnterpriseLaneFinancialReview{
			State: lane.State, Scope: "lane_epoch_only_bridge_unproven",
			Paid:      EnterpriseLaneFinancialPartition{int64(lane.PaidBudget), int64(lane.PaidAvailable), int64(lane.Held), int64(lane.Spent), int64(lane.PaidReturned)},
			FiniteKey: EnterpriseLaneFinancialPartition{int64(lane.TokenBudget), int64(lane.TokenAvailable), int64(lane.Held), int64(lane.Spent), int64(lane.TokenReturned)},
			ByState:   make(map[string]EnterpriseLaneFinancialState, 7), OpenRequests: int64(lane.OpenRequests),
		}
		var outstanding, openRequests, spent int64
		for cursor := ""; ; {
			// Include one lookahead so the last page is not falsely censored.
			limit := min(pageSize, EnterpriseLaneFinancialReviewMaxReceipts-int(report.ReceiptCount))
			var rows []EnterpriseLaneRequest
			query := tx.Model(&EnterpriseLaneRequest{}).Select("request_id", "lane_id", "epoch", "user_id", "token_id", "token_fingerprint", "state", "held", "actual", "refund_proof", "usage_projection_pending", "usage_channel_id", "usage_prompt_tokens", "usage_completion_tokens", "usage_model", "usage_group", "usage_observed_at").
				Where("lane_id = ? AND epoch = ?", id.LaneID, id.Epoch)
			if cursor != "" {
				query = query.Where("request_id > ?", cursor)
			}
			if err := query.Order("request_id").Limit(limit + 1).Find(&rows).Error; err != nil {
				return err
			}
			for i := 1; i < len(rows); i++ {
				if rows[i-1].RequestID == rows[i].RequestID {
					return ErrEnterpriseLaneReviewInconsistent
				}
			}
			more := len(rows) > limit
			if more && limit == EnterpriseLaneFinancialReviewMaxReceipts-int(report.ReceiptCount) {
				return ErrEnterpriseLaneReviewLimit
			}
			if more {
				rows = rows[:limit]
			}
			marked, err := readLaneFinancialMarkers(tx, rows, id)
			if err != nil {
				return err
			}
			for _, receipt := range rows {
				if receipt.EnterpriseLaneIdentity != id || !enterpriseLaneIDValid(receipt.RequestID) {
					return ErrEnterpriseLaneReviewInconsistent
				}
				held, actualKnown, err := laneFinancialAmounts(receipt)
				if err != nil {
					return err
				}
				bucket := report.ByState[receipt.State]
				bucket.Requests++
				bucket.OriginalCaps += int64(receipt.Held)
				bucket.Outstanding += held
				if actualKnown {
					actual := int64(receipt.Actual)
					if bucket.KnownActual != nil {
						actual += *bucket.KnownActual
					}
					bucket.KnownActual = &actual
				}
				report.ByState[receipt.State] = bucket
				report.ReceiptCount++
				if receipt.UsageProjectionPending {
					report.MeasuredProjectionPending++
				}
				if held > 0 {
					openRequests++
					outstanding += held
				}
				if receipt.State == EnterpriseLaneSettled {
					spent += int64(receipt.Actual)
				}
			}
			report.CanonicalMarkerMatches += int64(marked)
			report.UnfencedLegacyReceipts += int64(len(rows) - marked)
			if !more {
				break
			}
			cursor = rows[len(rows)-1].RequestID
		}
		if outstanding > report.Paid.Outstanding || openRequests > report.OpenRequests || spent > report.Paid.Spent {
			return ErrEnterpriseLaneReviewInconsistent
		}
		if outstanding != report.Paid.Outstanding || openRequests != report.OpenRequests || spent != report.Paid.Spent {
			return ErrEnterpriseLaneReviewMissing
		}
		out = report
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err == nil {
		return out, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, ErrEnterpriseLaneReviewMissing
	case errors.Is(err, ErrEnterpriseLaneReviewScope), errors.Is(err, ErrEnterpriseLaneReviewMissing),
		errors.Is(err, ErrEnterpriseLaneReviewInconsistent), errors.Is(err, ErrEnterpriseLaneReviewOwnerConflict), errors.Is(err, ErrEnterpriseLaneReviewLimit):
		return nil, err
	default:
		return nil, ErrEnterpriseLaneReviewUnavailable // SQL/driver details are not safe to export.
	}
}

func readLaneFinancialTransfer(tx *gorm.DB, entry LedgerEntry, kind ledgerOpKind, delta int) error {
	var operation BalanceLedgerOperation
	err := tx.Select("id", "user_id", "operation", "operation_signature", "total_before", "total_after").
		Where("idempotency_key = ?", entry.IdempotencyKey).Take(&operation).Error
	if delta == 0 {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return ErrEnterpriseLaneReviewInconsistent
	}
	if err != nil {
		return err
	}
	var rows []BalanceLedger
	if err := tx.Model(&BalanceLedger{}).Select("operation_id", "user_id", "account", "delta", "balance_before", "balance_after", "total_before", "total_after", "reason", "reference_type", "reference_id", "idempotency_key").
		Where("operation_id = ?", operation.Id).Limit(2).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrEnterpriseLaneReviewMissing
	}
	row := rows[0]
	if len(rows) != 1 || !verifyReplay(&operation, entry, kind) || row.UserId != entry.UserId ||
		row.Account != string(AccountPaid) || row.Delta != delta || row.BalanceBefore < 0 || row.BalanceBefore > common.MaxWalletQuota ||
		row.BalanceAfter < 0 || row.BalanceAfter > common.MaxWalletQuota || row.BalanceAfter-row.BalanceBefore != delta ||
		row.TotalBefore < row.BalanceBefore || row.TotalBefore > common.MaxWalletQuota || row.TotalAfter < row.BalanceAfter || row.TotalAfter > common.MaxWalletQuota ||
		row.TotalAfter-row.TotalBefore != delta || row.TotalBefore != operation.TotalBefore || row.TotalAfter != operation.TotalAfter ||
		row.Reason != entry.Reason || row.ReferenceType != entry.ReferenceType || row.ReferenceId != entry.ReferenceId || row.IdempotencyKey != entry.IdempotencyKey {
		return ErrEnterpriseLaneReviewInconsistent
	}
	return nil
}

func readLaneFinancialMarkers(tx *gorm.DB, receipts []EnterpriseLaneRequest, id EnterpriseLaneIdentity) (int, error) {
	// Even an empty scope checks that the expected canonical schema exists.
	keys := make([]string, 0, len(receipts))
	for _, receipt := range receipts {
		keys = append(keys, receipt.RequestID)
	}
	var owners []BillingFinalization
	if err := tx.Model(&BillingFinalization{}).Select("request_id", "user_id", "source", "token_id", "token_fingerprint", "source_held", "token_held", "pre_consumed", "paid", "bonus", "reward", "subscription_id", "subscription_period", "state", "actual_quota", "task_id", "task_public_id").
		Where("request_id IN ?", keys).Limit(len(keys) + 1).Find(&owners).Error; err != nil {
		return 0, err
	}
	if len(owners) > len(keys) {
		return 0, ErrEnterpriseLaneReviewInconsistent
	}
	seen := make(map[string]struct{}, len(owners))
	for _, owner := range owners {
		if _, duplicate := seen[owner.RequestID]; duplicate {
			return 0, ErrEnterpriseLaneReviewInconsistent
		}
		seen[owner.RequestID] = struct{}{}
		want := BillingFinalization{RequestID: owner.RequestID, UserID: id.UserID, Source: enterpriseLaneReviewMarkerSource, TokenID: id.TokenID, TokenFingerprint: id.TokenFingerprint}
		if !sameBillingIdentity(owner, want) || owner.State != enterpriseLaneReviewMarkerState || owner.ActualQuota != 0 || owner.TaskID != 0 || owner.TaskPublicID != "" {
			return 0, ErrEnterpriseLaneReviewOwnerConflict
		}
	}
	return len(owners), nil
}

func laneFinancialAmounts(receipt EnterpriseLaneRequest) (outstanding int64, actualKnown bool, err error) {
	if receipt.Held <= 0 || receipt.Held > common.MaxQuota || receipt.Actual < 0 || receipt.Actual > common.MaxQuota ||
		(receipt.Usage != EnterpriseLaneUsage{} && !receipt.Usage.valid()) ||
		(receipt.UsageProjectionPending && (receipt.State != EnterpriseLaneSettled || !receipt.Usage.valid())) {
		return 0, false, ErrEnterpriseLaneReviewInconsistent
	}
	if receipt.RefundProof != "" && receipt.State != EnterpriseLaneRefunded {
		return 0, false, ErrEnterpriseLaneReviewInconsistent
	}
	switch receipt.State {
	case EnterpriseLaneHeld, EnterpriseLaneDispatch, EnterpriseLaneUnknown:
		if receipt.Actual == 0 && receipt.Usage == (EnterpriseLaneUsage{}) {
			return int64(receipt.Held), false, nil
		}
	case EnterpriseLaneObserved:
		if receipt.Actual <= receipt.Held {
			return int64(receipt.Held), true, nil
		}
	case EnterpriseLaneReview:
		if receipt.Actual > receipt.Held {
			return int64(receipt.Held), true, nil
		}
	case EnterpriseLaneSettled:
		if receipt.Actual <= receipt.Held {
			return 0, true, nil
		}
	case EnterpriseLaneRefunded:
		if receipt.Actual == 0 && receipt.Usage == (EnterpriseLaneUsage{}) && receipt.RefundProof == EnterpriseLaneRefundNeverDispatched {
			return 0, false, nil
		}
	}
	return 0, false, ErrEnterpriseLaneReviewInconsistent
}
