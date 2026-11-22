package model

import (
	"context"
	"gorm.io/gorm"
)

const MidjourneyTimeoutReviewReason = "Upstream task timed out; delivery requires operator review"

// MarkMidjourneyPollingReview cannot turn missing local metadata into proof of
// upstream failure, and cannot overwrite a concurrent terminal success.
func MarkMidjourneyPollingReview(ctx context.Context, ids []int, reason string) error {
	for _, id := range ids {
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var task Midjourney
			if err := lockForUpdate(tx).First(&task, id).Error; err != nil {
				return err
			}
			if task.Status == "SUCCESS" || task.Status == "FAILURE" || task.Progress == "100%" {
				return nil
			}
			return tx.Model(&task).Updates(map[string]any{"status": "UNKNOWN", "fail_reason": reason}).Error
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// RequiresFinancialReview excludes ambiguous local-metadata failures from
// every automatic financial path, including a later ordinary poll timeout.
func (task *Midjourney) RequiresFinancialReview() bool {
	return task != nil && task.Status == "UNKNOWN" && (task.FailReason == "Missing upstream task identity; delivery requires operator review" || task.FailReason == "Missing channel metadata; upstream delivery requires operator review" || task.FailReason == "Ambiguous upstream task identity; delivery requires operator review" || task.FailReason == MidjourneyTimeoutReviewReason)
}

// GetUniqueMidjourneyNotificationTarget refuses ambiguous historical provider
// IDs instead of selecting another user's/channel's task for a financial action.
func GetUniqueMidjourneyNotificationTarget(ctx context.Context, upstreamID string) (*Midjourney, error) {
	if upstreamID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var tasks []Midjourney
	if err := DB.WithContext(ctx).Where("mj_id = ?", upstreamID).Limit(2).Find(&tasks).Error; err != nil {
		return nil, err
	}
	if len(tasks) != 1 {
		return nil, gorm.ErrRecordNotFound
	}
	return &tasks[0], nil
}
