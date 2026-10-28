package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// ClaimMidjourneyBillingOwnerTx binds the globally unique request owner to one
// exact MJ row and original charge. The caller locks the MJ row first and must
// commit its source, token, usage and BillingAccounted marker in this same Tx.
// Zero held means this legacy submission path did not reserve funds in advance;
// the task-owned record is not an executable refund receipt.
func ClaimMidjourneyBillingOwnerTx(tx *gorm.DB, task *Midjourney, allowNew bool, actualQuota int) (bool, error) {
	if tx == nil || task == nil || task.Id <= 0 || !task.HasCurrentBillingIdentity() || actualQuota != 0 && actualQuota != task.BillingOriginalQuota {
		return false, ErrTaskFinancialReview
	}
	fingerprint := task.TokenFingerprint
	if task.TokenId == 0 {
		fingerprint = "" // the MJ sentinel is a version marker, not authorization
	}
	identity := BillingFinalization{
		RequestID: task.BillingRequestID, UserID: task.UserId, Source: "wallet",
		TokenID: task.TokenId, TokenFingerprint: fingerprint,
	}
	// Current MJ submissions already hold source and original-token funds. Their
	// terminal owner retains that receipt; legacy MJ owners have zero holds.
	var existing BillingFinalization
	err := lockForUpdate(tx).Where("request_id = ?", identity.RequestID).First(&existing).Error
	var owner *BillingFinalization
	if err == nil {
		if err := existing.validate(); err != nil {
			return false, err
		}
		if !sameBillingAuthorization(existing, identity) {
			return false, ErrTaskFinancialReview
		}
		owner = &existing
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		owner, err = claimBillingFinalizationTx(tx, identity)
		if err != nil {
			return false, err
		}
	} else {
		return false, err
	}
	publicID := midjourneyBillingPublicID(task)
	if owner.State != "building" {
		if owner.State != BillingTaskOwned || owner.TaskID != int64(task.Id) || owner.TaskPublicID != publicID || owner.ActualQuota != actualQuota {
			return false, ErrBillingFinalized
		}
		return true, nil
	}
	if !allowNew || task.BillingAccounted {
		return false, fmt.Errorf("%w: Midjourney accounting has no request owner", ErrTaskFinancialReview)
	}
	// No MJ pre-reservation exists. Any prior relay ledger for this request is
	// ambiguous source-only history, including a different amount/key. Never
	// promote it to proof that token/usage accounting completed.
	var user User
	if err := lockForUpdate(tx).First(&user, task.UserId).Error; err != nil {
		return false, err
	}
	var previous BalanceLedger
	err = lockForUpdate(tx).Where("request_id = ? OR (reference_type = ? AND reference_id = ?)", task.BillingRequestID, "relay", task.BillingRequestID).First(&previous).Error
	if err == nil {
		return false, fmt.Errorf("%w: prior Midjourney source-only request ledger", ErrTaskFinancialReview)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	result := tx.Model(owner).Updates(map[string]any{
		"state": BillingTaskOwned, "task_id": task.Id, "task_public_id": publicID,
		"actual_quota": actualQuota,
	})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected != 1 {
		return false, ErrTaskFinancialReview
	}
	return false, nil
}
