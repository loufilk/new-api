package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// anonymizeExpiredModelTestRuns keeps permanent replay tombstones, never deletes
// billing receipts, and never mistakes a successful model response for settlement.
// The caller owns the transaction. Unresolved audit rows are revisited on later runs.
func anonymizeExpiredModelTestRuns(tx *gorm.DB, now int64) error {
	lastID := ""
	for {
		var runs []ModelTestRun
		err := lockForUpdate(tx).Where("id > ? AND source = ? AND created_at < ? AND status <> ? AND benchmark_id = ? AND user_id > 0", lastID, "user", now-ModelTestTransientHistorySeconds, "running", "local-fingerprint-v1").Order("id").Limit(200).Find(&runs).Error
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			return nil
		}
		for _, run := range runs {
			lastID = run.ID
			// The row remains the single shared report; only personal associations expire.
			updates := map[string]any{}
			safe, err := modelTestAuditSettled(tx, run)
			if err != nil {
				return err
			}
			if !safe {
				continue
			}
			for _, field := range []string{"user_id", "token_id", "channel_id", "channel_key_index", "max_output_tokens", "expires_at"} {
				updates[field] = 0
			}
			for _, field := range []string{"samples_json", "request_id", "error_code"} {
				updates[field] = ""
			}
			updates["estimated_quota"] = nil
			updates["cancel_requested"] = false
			if err := tx.Model(&ModelTestRun{}).Where("id = ? AND status <> ?", run.ID, "running").Updates(updates).Error; err != nil {
				return err
			}
		}
	}
}

// A missing receipt is unknown, not free or refunded. Consume logs alone cannot
// prove the token/wallet finalization completed atomically.
func modelTestAuditSettled(tx *gorm.DB, run ModelTestRun) (bool, error) {
	if run.Status == "unknown" {
		return false, nil // A final charge alone does not resolve an uncertain upstream outcome.
	}
	var samples []struct {
		RequestID string `json:"request_id"`
	}
	if run.SamplesJSON != "" && common.UnmarshalJsonStr(run.SamplesJSON, &samples) != nil {
		return false, nil
	}
	requests := make(map[string]bool)
	for _, sample := range samples {
		if sample.RequestID == "" {
			return false, nil
		}
		requests[sample.RequestID] = true
	}
	if len(samples) == 0 {
		// Fingerprint dispatch always persists its sample intent first. Legacy
		// playground invocations did not use this journal and require a receipt.
		if run.BenchmarkID == "local-fingerprint-v1" && run.ChannelID == 0 && run.Status != "completed" && run.Status != "unknown" {
			return true, nil
		}
		if run.RequestID == "" {
			return false, nil
		}
		requests[run.RequestID] = true
	}
	for requestID := range requests {
		var receipt BillingFinalization
		err := tx.Where("request_id = ?", requestID).First(&receipt).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if receipt.UserID != run.UserID || receipt.TokenID != run.TokenID || (receipt.State != BillingSettled && receipt.State != BillingRefunded) {
			return false, nil
		}
	}
	return true, nil
}
