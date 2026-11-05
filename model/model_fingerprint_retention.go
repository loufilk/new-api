package model

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelFingerprintScoredHistoryLimit = 60
const ModelFingerprintOtherHistoryLimit = 5
const ModelTestTransientHistorySeconds = int64(24 * time.Hour / time.Second)

var fingerprintManualTerminalStatuses = []string{"completed", "failed", "unknown", "cancelled", "intercepted", "timeout"}

// Share the admission gate so completion, pruning and request-id reuse cannot
// race. Configuration writers already take this gate before the site config.
func lockModelFingerprintRetention(tx *gorm.DB) error {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ModelTestGate{ID: 1}).Error; err != nil {
		return err
	}
	var gate ModelTestGate
	return lockForUpdate(tx).First(&gate, 1).Error
}

// PruneModelFingerprintHistory erases expired display payloads, not billing or
// replay identities. It also removes personal associations only after durable
// settlement proof and the transient reconciliation window have elapsed.
func PruneModelFingerprintHistory(ctx context.Context, now int64) error {
	if now <= 0 {
		now = time.Now().Unix()
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockModelFingerprintRetention(tx); err != nil {
			return err
		}
		if err := pruneModelFingerprintReports(tx, now, false, "", ""); err != nil {
			return err
		}
		if err := pruneModelFingerprintReports(tx, now, true, "", ""); err != nil {
			return err
		}
		return anonymizeExpiredModelTestRuns(tx, now)
	})
}

// Rows keep their one-use confirmation, request id, schedule window and audit
// metadata. The actual large report is cleared and can never reappear in public
// history after newer results are removed. No wallet, token or receipt is edited.
func pruneModelFingerprintReports(tx *gorm.DB, now int64, site bool, onlyGroup, onlyModel string) error {
	var entity any = &ModelTestRun{}
	status := "completed"
	if site {
		entity, status = &ModelFingerprintSiteRun{}, "succeeded"
	}
	base := tx.Model(entity).Where("history_pruned_at = ?", 0)
	if site {
		base = base.Where("status IN ?", []string{"succeeded", "failed", "unknown"})
	} else {
		base = base.Where("source = ? AND benchmark_id = ? AND status IN ?", "user", "local-fingerprint-v1", fingerprintManualTerminalStatuses)
	}
	if onlyGroup != "" && onlyModel != "" {
		base = base.Where(commonGroupCol+" = ? AND model = ?", onlyGroup, onlyModel)
	}
	var pairs []struct{ Group, Model string }
	if err := base.Session(&gorm.Session{}).Select(commonGroupCol, "model").Distinct().Find(&pairs).Error; err != nil {
		return err
	}
	for _, pair := range pairs {
		for _, scored := range []bool{true, false} {
			query := base.Session(&gorm.Session{}).Where(commonGroupCol+" = ? AND model = ?", pair.Group, pair.Model)
			limit := ModelFingerprintOtherHistoryLimit
			if scored {
				query = query.Where("status = ? AND assessment = ?", status, "scored")
				limit = ModelFingerprintScoredHistoryLimit
			} else {
				query = query.Where("NOT (status = ? AND assessment = ?) OR assessment IS NULL", status, "scored")
			}
			var keep []string
			if err := query.Session(&gorm.Session{}).Order("created_at DESC, id DESC").Limit(limit).Pluck("id", &keep).Error; err != nil {
				return err
			}
			if len(keep) < limit {
				continue
			}
			values := map[string]any{"fingerprint_result": nil, "history_pruned_at": now}
			if site {
				values["fingerprint_result"] = ""
			} else {
				values["artwork_html"] = ""
			}
			if err := query.Session(&gorm.Session{}).Where("id NOT IN ?", keep).Updates(values).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
