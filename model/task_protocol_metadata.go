package model

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// MarkResponsesBackground merges the protocol flag into the current task row.
// The submit snapshot may predate a terminal observation or settlement, so none
// of its other fields may be written back. The caller's response snapshot stays
// unchanged; only the task row is locked, in the financial lifecycle's order.
func (t *Task) MarkResponsesBackground(ctx context.Context) error {
	if ctx == nil || t == nil || t.ID <= 0 || t.UserId <= 0 || t.TaskID == "" || t.Platform == "" {
		return errors.New("invalid task background identity")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stored, err := LoadTaskFinancialTx(tx, t.ID, t.UserId)
		if err != nil {
			return err
		}
		if stored.TaskID != t.TaskID || stored.Platform != t.Platform {
			return errors.New("task background identity changed")
		}
		if stored.PrivateData.ResponsesBackground {
			return nil
		}
		stored.PrivateData.ResponsesBackground = true
		result := tx.Model(stored).Update("private_data", stored.PrivateData)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("task background update affected %d rows", result.RowsAffected)
		}
		return nil
	})
}
