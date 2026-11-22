package model

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BonusSourceReconciliationCursor struct {
	ID               int `gorm:"primaryKey;autoIncrement:false"`
	AfterUserID      int
	AfterExpiryLotID int64 `gorm:"not null;default:0"`
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
	var cursor BonusSourceReconciliationCursor
	if err := DB.WithContext(ctx).Where("id = 1").Limit(1).Find(&cursor).Error; err != nil {
		return report, err
	}
	now := bonusSourceNow()
	lots := make([]BonusSourceLot, 0, limit)
	sub := DB.Model(&BonusSourceState{}).Select("lot_id").Where("available > 0")
	query := DB.WithContext(ctx).Where("id IN (?) AND ((expires_at > 0 AND expires_at <= ?) OR id IN (?))", sub, now, DB.Model(&BonusSourceState{}).Select("lot_id").Where("revocation_active = ?", true))
	after := cursor.AfterExpiryLotID
	if err := query.Session(&gorm.Session{}).Where("id > ?", after).Order("id").Limit(limit).Find(&lots).Error; err != nil {
		return report, err
	}
	if len(lots) < limit && after > 0 {
		var wrapped []BonusSourceLot
		if err := query.Session(&gorm.Session{}).Where("id <= ?", after).Order("id").Limit(limit - len(lots)).Find(&wrapped).Error; err != nil {
			return report, err
		}
		lots = append(lots, wrapped...)
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
		// Advance the sweep even when this lot requires operator review; it
		// remains eligible for repair and a later pass after wraparound.
		after = lot.ID
		if errors.Is(err, ErrBonusSourceInvariant) || errors.Is(err, gorm.ErrRecordNotFound) {
			a := BonusSourceAnomaly{UserID: lot.UserID, Key: fmt.Sprintf("expiry:%d", lot.ID), Code: "expiry_source_mismatch", Detail: "Expiry refused; source owner or accounting needs review"}
			insert := DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&a)
			if insert.Error != nil {
				return report, insert.Error
			}
			report.Anomalies += int(insert.RowsAffected)
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
	if len(lots) == 0 {
		after = 0
	}
	// Another node may sweep the same safe, idempotent batch. Only advance a
	// cursor read at this position; don't overwrite a concurrent newer scan.
	row := BonusSourceReconciliationCursor{ID: 1}
	if err := DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return report, err
	}
	if err := DB.WithContext(ctx).Model(&row).Where("after_expiry_lot_id = ?", cursor.AfterExpiryLotID).UpdateColumn("after_expiry_lot_id", after).Error; err != nil {
		return report, err
	}
	nextUser, err := ReconcileBonusSources(ctx, cursor.AfterUserID, limit)
	if err != nil {
		return report, err
	}
	report.ReconciledThroughUserID = nextUser
	if nextUser == cursor.AfterUserID {
		nextUser = 0
	}
	if err := DB.WithContext(ctx).Model(&row).Where("after_user_id = ?", cursor.AfterUserID).UpdateColumn("after_user_id", nextUser).Error; err != nil {
		return report, err
	}
	return report, nil
}
