package model

import (
	"cmp"
	"context"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelFingerprintHistoryLimit = 60
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
		if err := pruneModelFingerprintReports(tx, now, "", ""); err != nil {
			return err
		}
		return anonymizeExpiredModelTestRuns(tx, now)
	})
}

func modelFingerprintHistoryQuery(tx *gorm.DB, site bool) *gorm.DB {
	if site {
		return tx.Model(&ModelFingerprintSiteRun{}).Where("history_pruned_at = ? AND status IN ?", 0, []string{"succeeded", "failed", "unknown"})
	}
	return tx.Model(&ModelTestRun{}).Where("history_pruned_at = ? AND source = ? AND benchmark_id = ? AND status IN ?", 0, "user", "local-fingerprint-v1", fingerprintManualTerminalStatuses)
}

type modelFingerprintReportRef struct {
	ID        string
	CreatedAt int64
	Site      bool `gorm:"-"`
}

// One window spans automatic and manual observations, including unsuccessful
// tests. Fetch only bounded metadata from each source before merging the order.
func latestModelFingerprintReportRefs(tx *gorm.DB, group, modelName string) ([]modelFingerprintReportRef, error) {
	refs := make([]modelFingerprintReportRef, 0, 2*ModelFingerprintHistoryLimit)
	for _, site := range []bool{false, true} {
		var rows []modelFingerprintReportRef
		if err := modelFingerprintHistoryQuery(tx, site).Where(commonGroupCol+" = ? AND model = ?", group, modelName).
			Select("id", "created_at").Order("created_at DESC, id DESC").Limit(ModelFingerprintHistoryLimit).Find(&rows).Error; err != nil {
			return nil, err
		}
		for i := range rows {
			rows[i].Site = site
		}
		refs = append(refs, rows...)
	}
	slices.SortFunc(refs, func(a, b modelFingerprintReportRef) int {
		if order := cmp.Compare(b.CreatedAt, a.CreatedAt); order != 0 {
			return order
		}
		if order := cmp.Compare(b.ID, a.ID); order != 0 {
			return order
		}
		if a.Site == b.Site {
			return 0
		}
		if a.Site {
			return -1
		}
		return 1
	})
	return refs[:min(len(refs), ModelFingerprintHistoryLimit)], nil
}

// Rows keep their one-use confirmation, request id, schedule window and audit
// metadata. The actual large report is cleared and can never reappear after
// newer results are removed. No wallet, token or receipt is edited.
func pruneModelFingerprintReports(tx *gorm.DB, now int64, onlyGroup, onlyModel string) error {
	type target struct{ Group, Model string }
	pairs := make(map[target]struct{})
	if onlyGroup != "" && onlyModel != "" {
		pairs[target{Group: onlyGroup, Model: onlyModel}] = struct{}{}
	} else {
		for _, site := range []bool{false, true} {
			var rows []target
			if err := modelFingerprintHistoryQuery(tx, site).Select(commonGroupCol, "model").Distinct().Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				pairs[row] = struct{}{}
			}
		}
	}
	for pair := range pairs {
		refs, err := latestModelFingerprintReportRefs(tx, pair.Group, pair.Model)
		if err != nil {
			return err
		}
		if len(refs) < ModelFingerprintHistoryLimit {
			continue
		}
		for _, site := range []bool{false, true} {
			keep := make([]string, 0, len(refs))
			for _, ref := range refs {
				if ref.Site == site {
					keep = append(keep, ref.ID)
				}
			}
			query := modelFingerprintHistoryQuery(tx, site).Where(commonGroupCol+" = ? AND model = ?", pair.Group, pair.Model)
			if len(keep) > 0 {
				query = query.Where("id NOT IN ?", keep)
			}
			values := map[string]any{"fingerprint_result": nil, "history_pruned_at": now}
			if site {
				values["fingerprint_result"] = ""
			} else {
				values["artwork_html"] = ""
			}
			if err := query.Updates(values).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// ListModelFingerprintCurrentRuns returns the same bounded window that retention
// keeps. A disabled or removed target is not an alternate public history view.
func ListModelFingerprintCurrentRuns(ctx context.Context, targets []ModelFingerprintSiteTarget) ([]ModelFingerprintSitePublicRun, []ModelTestPublicRun, error) {
	siteResults := make([]ModelFingerprintSitePublicRun, 0)
	manualResults := make([]ModelTestPublicRun, 0)
	tx := DB.WithContext(ctx)
	for _, target := range targets {
		if !target.Enabled {
			continue
		}
		refs, err := latestModelFingerprintReportRefs(tx, target.Group, target.Model)
		if err != nil {
			return nil, nil, err
		}
		siteIDs, manualIDs := make([]string, 0, len(refs)), make([]string, 0, len(refs))
		for _, ref := range refs {
			if ref.Site {
				siteIDs = append(siteIDs, ref.ID)
			} else {
				manualIDs = append(manualIDs, ref.ID)
			}
		}
		if len(siteIDs) > 0 {
			var runs []ModelFingerprintSiteRun
			if err := modelFingerprintHistoryQuery(tx, true).Where("id IN ?", siteIDs).Order("created_at DESC, id DESC").Find(&runs).Error; err != nil {
				return nil, nil, err
			}
			for _, run := range runs {
				siteResults = append(siteResults, run.Public())
			}
		}
		if len(manualIDs) > 0 {
			var runs []ModelTestRun
			if err := modelFingerprintHistoryQuery(tx, false).Where("id IN ?", manualIDs).
				Select("id", "model", "group", "benchmark_id", "status", "assessment", "fingerprint_result", "created_at", "completed_at").Order("created_at DESC, id DESC").Find(&runs).Error; err != nil {
				return nil, nil, err
			}
			for _, run := range runs {
				manualResults = append(manualResults, run.PublicFingerprint())
			}
		}
	}
	return siteResults, manualResults, nil
}
