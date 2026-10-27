package model

import (
	"context"
	"database/sql/driver"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"gorm.io/gorm"
)

// RequestBillingSettlement freezes the caller's already calculated actual
// charge under the existing owner. It never recalculates prices, releases holds
// or authorizes a background debit. Process death leaves an enumerable review
// item; recovery must retain the original funding policy and request identity.
func RequestBillingSettlement(ctx context.Context, receipt BillingFinalization, actual int) error {
	if actual < 0 || actual > common.MaxQuota {
		return errors.New("invalid billing actual quota")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		switch r.State {
		case BillingSettled, BillingSettlementReview:
			if r.ActualQuota != actual {
				return billingRefundReviewError("actual_mismatch", "billing settlement replay amount mismatch")
			}
			return nil
		case BillingHeld:
			return tx.Model(r).Updates(map[string]any{
				"state": BillingSettlementReview, "actual_quota": actual,
				"last_error_code": "settlement_unconfirmed", "last_error_class": billingRefundReview,
				"next_retry_at": 0, "escalated_at": common.GetTimestamp(),
			}).Error
		case "building":
			return billingRefundReviewError("owner_missing", "settlement requires an atomically reserved request owner")
		default:
			return ErrBillingFinalized
		}
	})
}

// BillingSettlementFailureCode stores only bounded classifications, never raw
// provider/SQL errors or credentials. Unknown errors require review, not retries.
func BillingSettlementFailureCode(failure error) (string, string) {
	switch {
	case errors.Is(failure, moneyport.ErrWalletInsufficientBalance), errors.Is(failure, moneyport.ErrBonusFundingNotSupported), errors.Is(failure, ErrLedgerInsufficientBalance):
		return "insufficient_balance", billingRefundReview
	case IsTransientBillingSettlementError(failure):
		return "transient_database", billingRefundRetryable
	default:
		code, _ := BillingRefundErrorCode(failure)
		return code, billingRefundReview
	}
}

// IsTransientBillingSettlementError deliberately uses typed driver evidence,
// not arbitrary text or the refund classifier's broad fallback. The caller
// still bounds both attempts and the total SQL/cache operation budget.
func IsTransientBillingSettlementError(err error) bool {
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		switch sqlState.SQLState() {
		case "40001", "40P01", "55P03", "08000", "08003", "08006":
			return true
		}
	}
	return false
}

// RecordBillingSettlementFailure cannot create authority after a failed intent
// write, change the frozen amount or reopen a committed settlement. A failure
// here must also be logged: an unavailable database is not durable evidence.
func RecordBillingSettlementFailure(ctx context.Context, receipt BillingFinalization, actual int, failure error) error {
	if failure == nil {
		return nil
	}
	code, class := BillingSettlementFailureCode(failure)
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", receipt.RequestID).First(&r).Error; err != nil {
			return err
		}
		if !sameBillingIdentity(r, receipt) {
			return billingRefundReviewError("identity_mismatch", "billing settlement failure receipt mismatch")
		}
		if r.State == BillingSettled && r.ActualQuota == actual {
			return nil // The supposedly failed COMMIT succeeded.
		}
		if r.State != BillingSettlementReview || r.ActualQuota != actual {
			return billingRefundReviewError("settlement_intent_unconfirmed", "billing settlement intent is not confirmed")
		}
		now := common.GetTimestamp()
		updates := map[string]any{
			"attempts": r.Attempts + 1, "last_error_code": code, "last_error_class": class,
			"last_attempt_at": now, "next_retry_at": 0, "escalated_at": now,
		}
		if r.FirstFailedAt == 0 {
			updates["first_failed_at"], updates["first_error_code"], updates["first_error_class"] = now, code, class
		}
		return tx.Model(&r).Updates(updates).Error
	})
}
