package model

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// Classification remains used by normal settlement/cache logs. This pure test
// has no retry schedule, persisted intent, worker or database side effects.
func TestBillingErrorClassificationExcludesSecretDetails(t *testing.T) {
	const secret = "synthetic-do-not-store-password"
	for _, tc := range []struct {
		name, code, class string
		err               error
	}{
		{"nil", "", "", nil},
		{"unknown_secret", "recovery_error", billingRefundRetryable, errors.New("SQL token=" + secret + " identity_mismatch")},
		{"wrapped_unknown_secret", "recovery_error", billingRefundRetryable, fmt.Errorf("credential=%s: %w", secret, errors.New("arbitrary driver failure"))},
		{"timeout", "deadline_exceeded", billingRefundRetryable, fmt.Errorf("credential=%s: %w", secret, context.DeadlineExceeded)},
		{"cancel", "canceled", billingRefundRetryable, fmt.Errorf("credential=%s: %w", secret, context.Canceled)},
		{"identity", "identity_mismatch", billingRefundReview, billingRefundReviewError("identity_mismatch", "SQL token="+secret)},
		{"missing", "missing_record", billingRefundReview, fmt.Errorf("credential=%s: %w", secret, gorm.ErrRecordNotFound)},
		{"terminal_owner", "terminal_owner_conflict", billingRefundReview, fmt.Errorf("credential=%s: %w", secret, ErrBillingFinalized)},
		{"ledger_conflict", "ledger_evidence", billingRefundReview, fmt.Errorf("credential=%s: %w", secret, ErrLedgerIdempotencyConflict)},
		{"ledger_invalid", "ledger_evidence", billingRefundReview, fmt.Errorf("credential=%s: %w", secret, ErrLedgerInvalidEntry)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, class := BillingRefundErrorCode(tc.err)
			assert.Equal(t, tc.code, code)
			assert.Equal(t, tc.class, class)
			assert.NotContains(t, code+class, secret)
			assert.NotContains(t, code+class, "SQL")
			assert.NotContains(t, code+class, "credential=")
		})
	}
}
