package model

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

const (
	billingRefundRetryable = "retryable"
	billingRefundReview    = "review_required"
)

type billingRefundIssue struct {
	code    string
	message string
}

func (e *billingRefundIssue) Error() string { return e.message }

// Called only at locally known invariant checks, never on arbitrary error text.
func billingRefundReviewError(code, message string) error {
	return &billingRefundIssue{code: code, message: message}
}

// BillingRefundErrorCode is the retained sanitized classifier used by normal
// settlement/cache logs. It does not schedule retries or authorize refunds.
func BillingRefundErrorCode(err error) (code, class string) {
	if err == nil {
		return "", ""
	}
	var issue *billingRefundIssue
	if errors.As(err, &issue) {
		return issue.code, billingRefundReview
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded", billingRefundRetryable
	case errors.Is(err, context.Canceled):
		return "canceled", billingRefundRetryable
	case errors.Is(err, gorm.ErrRecordNotFound):
		return "missing_record", billingRefundReview
	case errors.Is(err, ErrBillingFinalized):
		return "terminal_owner_conflict", billingRefundReview
	case errors.Is(err, ErrLedgerIdempotencyConflict), errors.Is(err, ErrLedgerInvalidEntry):
		return "ledger_evidence", billingRefundReview
	default:
		return "recovery_error", billingRefundRetryable
	}
}
