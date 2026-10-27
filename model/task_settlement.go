package model

import (
	"context"
)

// GetPendingTaskSettlements is a historical SELECT-only inspection helper.
// No current production worker executes or changes these saved intents.
func GetPendingTaskSettlements(ctx context.Context, limit int) ([]*Task, error) {
	var tasks []*Task
	err := DB.WithContext(ctx).Where("settlement_pending = ?", true).Order("COALESCE(settlement_attempted_at, 0), id").Limit(limit).Find(&tasks).Error
	return tasks, err
}
