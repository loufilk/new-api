package model

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// CommitMidjourneyBilling transfers an existing full wallet/token hold to one
// accepted MJ task. The receipt, usage and task insertion share one transaction;
// an unknown COMMIT is never permission to submit upstream or debit again.
func CommitMidjourneyBilling(ctx context.Context, receipt BillingFinalization, task *Midjourney) (bool, error) {
	if task == nil || !task.HasCurrentBillingIdentity() || task.UserId != receipt.UserID || task.BillingRequestID != receipt.RequestID || receipt.Source != "wallet" || task.Quota != task.BillingOriginalQuota || task.Quota != receipt.PreConsumed || task.Quota != receipt.SourceHeld || task.MjId == "" && task.Status != "SUCCESS" {
		return false, ErrTaskFinancialReview
	}
	fingerprint := task.TokenFingerprint
	if task.TokenId == 0 {
		fingerprint = ""
	}
	if task.TokenId != receipt.TokenID || fingerprint != receipt.TokenFingerprint || receipt.TokenID > 0 && receipt.TokenHeld != task.Quota {
		return false, ErrTaskFinancialReview
	}
	applied := false
	destination := *task
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		if owner.State == BillingTaskOwned {
			// Do not lock the task after its owner: refund observers lock the
			// task first. This replay is read-only and makes no money decision.
			var stored Midjourney
			if err := tx.First(&stored, owner.TaskID).Error; err != nil {
				return err
			}
			if owner.TaskPublicID != midjourneyBillingPublicID(&stored) || owner.ActualQuota != task.Quota || stored.MjId != task.MjId || stored.UserId != task.UserId || stored.BillingRequestID != task.BillingRequestID || stored.BillingOriginalQuota != task.Quota || stored.TokenId != task.TokenId || stored.TokenFingerprint != task.TokenFingerprint || !stored.BillingAccounted {
				return ErrTaskFinancialReview
			}
			*task = stored
			return nil
		}
		if owner.State != BillingHeld {
			return ErrBillingFinalized
		}
		if task.Id != 0 || task.BillingAccounted {
			return errors.New("Midjourney handoff requires a new task")
		}
		if err := validateBillingSourceTx(tx, owner); err != nil {
			return err
		}
		destination.WalletDebit = append([]LedgerShare{}, receipt.Shares()...)
		destination.BillingAccounted = true
		if err := ValidateTaskWalletReceipt(destination.WalletDebit, destination.Quota); err != nil {
			return err
		}
		if err := tx.Create(&destination).Error; err != nil {
			return err
		}
		if err := ApplyTaskUsageDeltaTx(tx, destination.UserId, destination.GetBillingChannelId(), destination.Quota, 1); err != nil {
			return err
		}
		if err := tx.Model(owner).Updates(map[string]any{"state": BillingTaskOwned, "actual_quota": destination.Quota, "task_id": destination.Id, "task_public_id": midjourneyBillingPublicID(&destination)}).Error; err != nil {
			return err
		}
		applied = true
		return nil
	})
	if applied && err == nil {
		*task = destination
	}
	return applied && err == nil, err
}

func midjourneyBillingPublicID(task *Midjourney) string {
	return fmt.Sprintf("midjourney:%d:%d:%s", task.Id, task.BillingOriginalQuota, BillingTokenFingerprint(task.MjId))
}
