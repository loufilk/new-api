package model

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BonusSourceReconciliationCursor struct {
	ID          int `gorm:"primaryKey;autoIncrement:false"`
	AfterUserID int
}
type BonusSourceMaintenanceReport struct {
	ExpiredLots             int `json:"expired_lots"`
	ReconciledThroughUserID int `json:"reconciled_through_user_id"`
	Anomalies               int `json:"anomalies"`
}

// RunBonusSourceMaintenance expires only AVAILABLE source quota and keeps
// ordinary wallet provenance reconciled. Outstanding debit holds remain intact.
func RunBonusSourceMaintenance(ctx context.Context, limit int) (BonusSourceMaintenanceReport, error) {
	var report BonusSourceMaintenanceReport
	if limit < 1 || limit > 100 {
		return report, ErrLedgerInvalidEntry
	}
	now := bonusSourceNow()
	var lots []BonusSourceLot
	sub := DB.Model(&BonusSourceState{}).Select("lot_id").Where("available > 0")
	if err := DB.WithContext(ctx).Where("id IN (?) AND ((expires_at > 0 AND expires_at <= ?) OR id IN (?))", sub, now, DB.Model(&BonusSourceState{}).Select("lot_id").Where("revocation_active = ?", true)).Order("id").Limit(limit).Find(&lots).Error; err != nil {
		return report, err
	}
	for _, lot := range lots {
		changed := false
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var user User
			if err := lockForUpdate(tx).First(&user, lot.UserID).Error; err != nil {
				return err
			}
			if err := ensureBonusSourceAccountTx(tx, &user); err != nil {
				return err
			}
			var err error
			changed, err = expireBonusSourceLotTx(tx, &user, lot, now)
			return err
		})
		if errors.Is(err, ErrBonusSourceInvariant) || errors.Is(err, gorm.ErrRecordNotFound) {
			a := BonusSourceAnomaly{UserID: lot.UserID, Key: fmt.Sprintf("expiry:%d", lot.ID), Code: "expiry_source_mismatch", Detail: "Expiry refused; source owner or accounting needs review"}
			if e := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&a).Error; e != nil {
				return report, e
			}
			report.Anomalies++
			continue
		}
		if err != nil {
			return report, err
		}
		if changed {
			report.ExpiredLots++
		}
		RefreshUserQuotaCache(lot.UserID, 0)
	}
	var cursor BonusSourceReconciliationCursor
	if err := DB.Where("id = 1").Limit(1).Find(&cursor).Error; err != nil {
		return report, err
	}
	after, err := ReconcileBonusSources(ctx, cursor.AfterUserID, limit)
	if err != nil {
		return report, err
	}
	report.ReconciledThroughUserID = after
	if after == cursor.AfterUserID {
		after = 0
	}
	row := BonusSourceReconciliationCursor{ID: 1, AfterUserID: after}
	if err := DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"after_user_id"})}).Create(&row).Error; err != nil {
		return report, err
	}
	return report, nil
}
