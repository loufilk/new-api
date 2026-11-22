package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// MarkTaskPollingReview preserves money and a discoverable nonterminal task
// when local metadata cannot establish the upstream delivery outcome.
func MarkTaskPollingReview(ctx context.Context, task *Task, reason string) error {
	if task == nil || reason == "" {
		return errors.New("invalid task review identity")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stored, err := LoadTaskFinancialTx(tx, task.ID, task.UserId)
		if err != nil {
			return err
		}
		if stored.Status == TaskStatusSuccess || stored.Status == TaskStatusFailure || stored.HasLegacyBillingIntent() || stored.SettlementPending {
			return nil
		}
		stored.Status = TaskStatusUnknown
		stored.FailReason = reason
		stored.PrivateData.FinancialReviewReason = reason
		stored.PrivateData.PollFailures = max(stored.PrivateData.PollFailures, task.PrivateData.PollFailures)
		if stored.Progress == "100%" {
			stored.Progress = "0%"
		}
		if err := tx.Model(stored).Select("status", "progress", "fail_reason", "private_data").Updates(stored).Error; err != nil {
			return err
		}
		*task = *stored
		return nil
	})
}

// CommitTaskTerminalFinancial persists a new-v2 financial intent with the
// observed terminal transition. A stale poll only reads the winning row; it
// cannot overwrite SUCCESS or overwrite a newer billing receipt/private state.
func CommitTaskTerminalFinancial(ctx context.Context, task *Task, from TaskStatus, intent *TaskFinancialIntent) (bool, error) {
	if task == nil || intent == nil || (task.Status != TaskStatusSuccess && task.Status != TaskStatusFailure) {
		return false, errors.New("invalid task terminal intent")
	}
	won := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stored, err := LoadTaskFinancialTx(tx, task.ID, task.UserId)
		if err != nil {
			return err
		}
		if stored.Status != from || stored.Status == TaskStatusSuccess || stored.Status == TaskStatusFailure || stored.SettlementPending || stored.HasLegacyBillingIntent() {
			*task = *stored
			return nil
		}
		legacy := stored.PrivateData.FinancialVersion != 2 || !stored.PrivateData.SubmissionAccounted
		if !legacy && intent.PreviousQuota != stored.Quota {
			return ErrTaskFinancialReview
		}
		stored.Status, stored.Progress = task.Status, task.Progress
		stored.StartTime, stored.FinishTime, stored.FailReason = task.StartTime, task.FinishTime, task.FailReason
		stored.Data = task.Data
		stored.PrivateData.ResultURL = task.PrivateData.ResultURL
		stored.PrivateData.PluginState = task.PrivateData.PluginState
		stored.PrivateData.PollFailures = task.PrivateData.PollFailures
		stored.PrivateData.BillingContext = task.PrivateData.BillingContext
		if legacy {
			// Deployment must not strand an already submitted legacy delivery.
			// Save its observed result, but never infer missing financial identity
			// from today's token or alter its historical money/usage/receipt.
			stored.PrivateData.FinancialReviewReason = "Legacy task billing identity is unavailable; financial review required"
		} else {
			stored.PrivateData.FinancialReviewReason = ""
			stored.PrivateData.FinancialIntent = intent
			stored.SettlementPending, stored.SettlementAttemptedAt = true, common.GetTimestamp()
		}
		if err := tx.Model(stored).Select("status", "progress", "start_time", "finish_time", "fail_reason", "data", "private_data", "settlement_pending", "settlement_attempted_at").Updates(stored).Error; err != nil {
			return err
		}
		*task, won = *stored, true
		return nil
	})
	return won && err == nil, err
}

// GetCurrentTaskFinancialIntents never executes historical pending records.
// Keyset paging prevents an old pending page from starving new-v2 work without
// requiring database-specific JSON predicates or altering the existing schema.
func GetCurrentTaskFinancialIntents(ctx context.Context, limit int) ([]Task, error) {
	rows, _, err := GetCurrentTaskFinancialIntentsAfter(ctx, limit, 0)
	return rows, err
}

// One bounded keyset page, including skipped historical evidence. The caller
// persists next independently of money so poison rows cannot monopolize scans.
func GetCurrentTaskFinancialIntentsAfter(ctx context.Context, limit int, after int64) ([]Task, int64, error) {
	if limit <= 0 || limit > 100 || after < 0 {
		return nil, after, errors.New("invalid task financial scan")
	}
	var page []Task
	if err := DB.WithContext(ctx).Where("settlement_pending = ? AND id > ?", true, after).Order("id").Limit(100).Find(&page).Error; err != nil {
		return nil, after, err
	}
	var out []Task
	next := after
	for _, task := range page {
		next = task.ID
		if task.PrivateData.FinancialVersion == 2 && task.PrivateData.FinancialIntent != nil && task.PrivateData.FinancialReviewReason == "" && !task.HasLegacyBillingIntent() {
			out = append(out, task)
			if len(out) == limit {
				break
			}
		}
	}
	return out, next, nil
}

func HasPendingTaskFinancialEvidence(ctx context.Context) (bool, error) {
	var rows []Task
	err := DB.WithContext(ctx).Select("id").Where("settlement_pending = ?", true).Limit(1).Find(&rows).Error
	return len(rows) != 0, err
}

func GetCurrentMidjourneyRefunds(ctx context.Context, limit int) ([]Midjourney, error) {
	return GetCurrentMidjourneyRefundsAfter(ctx, limit, 0)
}

func GetCurrentMidjourneyRefundsAfter(ctx context.Context, limit int, after int64) ([]Midjourney, error) {
	if limit <= 0 || limit > 100 || after < 0 {
		return nil, errors.New("invalid Midjourney financial scan limit")
	}
	var rows []Midjourney
	err := DB.WithContext(ctx).Where("status = ? AND quota > 0 AND billing_accounted = ? AND token_fingerprint <> '' AND billing_request_id <> '' AND id > ?", "FAILURE", true, after).Order("id").Limit(limit).Find(&rows).Error
	return rows, err
}
