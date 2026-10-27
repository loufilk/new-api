package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// CommitBillingTask transfers the request owner to its durable task. No poller
// can observe a task with pre-settlement wallet shares: insert, source delta,
// original token, initial usage and ownership are one main-DB transaction.
func CommitBillingTask(ctx context.Context, receipt BillingFinalization, actual int, task *Task, omits []string, apply func(*gorm.DB, *Task) error) (bool, error) {
	if task == nil || task.TaskID == "" || task.UserId != receipt.UserID || actual < 0 || actual > common.MaxQuota || apply == nil {
		return false, errors.New("invalid billing task handoff")
	}
	applied := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		if r.State == BillingTaskOwned {
			if r.TaskPublicID != task.TaskID || r.ActualQuota != actual || r.TaskID <= 0 {
				return billingRefundReviewError("identity_mismatch", "task handoff replay identity mismatch")
			}
			var stored Task
			if err := lockForUpdate(tx).First(&stored, r.TaskID).Error; err != nil {
				return err
			}
			if !billingTaskIdentityMatches(r, &stored) || stored.PrivateData.TokenFingerprint != r.TokenFingerprint || stored.PrivateData.FinancialVersion != 2 || !stored.PrivateData.SubmissionAccounted {
				return billingRefundReviewError("source_evidence", "task handoff completion evidence mismatch")
			}
			*task = stored
			return nil
		}
		if r.State == "building" {
			// Free submissions intentionally have no funding session. They may
			// create a zero-cost owner, but may not import any historical hold.
			if actual != 0 || task.Quota != 0 || r.Source != "wallet" || r.PreConsumed != 0 || r.SourceHeld != 0 || r.TokenHeld != 0 || r.Paid != 0 || r.Bonus != 0 || r.Reward != 0 {
				return billingRefundReviewError("owner_missing", "funded task handoff requires an atomically reserved request owner")
			}
		} else if r.State != BillingHeld {
			return ErrBillingFinalized
		}
		if err := validateBillingSourceTx(tx, r); err != nil {
			return err
		}
		if err := apply(tx, task); err != nil {
			return err
		}
		task.PrivateData.FinancialVersion = 2
		task.PrivateData.TokenFingerprint = r.TokenFingerprint
		task.PrivateData.SubmissionAccounted = true
		if r.Source == "subscription" {
			task.PrivateData.SubscriptionReservedAmount = int64(r.SourceHeld)
		}
		if !billingTaskIdentityMatches(r, task) {
			return billingRefundReviewError("identity_mismatch", "task handoff destination identity mismatch")
		}
		if err := tx.Omit(omits...).Create(task).Error; err != nil {
			return err
		}
		if err := ApplyTaskUsageDeltaTx(tx, task.UserId, task.ChannelId, actual, 1); err != nil {
			return err
		}
		if err := tx.Model(r).Updates(map[string]any{"state": BillingTaskOwned, "actual_quota": actual, "task_id": task.ID, "task_public_id": task.TaskID}).Error; err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied && err == nil, err
}
