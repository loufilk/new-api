package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentSimpleReminderReceipt struct {
	ID         int64 `json:"id" gorm:"primaryKey"`
	ReminderID int64 `json:"reminder_id" gorm:"not null;uniqueIndex:idx_simple_reminder_receipt,priority:1"`
	UserID     int   `json:"user_id" gorm:"not null;uniqueIndex:idx_simple_reminder_receipt,priority:2"`
	CreatedAt  int64 `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentSimpleReminderReceipt) BeforeUpdate(*gorm.DB) error { return ErrAgentSimpleImmutable }
func (*AgentSimpleReminderReceipt) BeforeDelete(*gorm.DB) error { return ErrAgentSimpleImmutable }

func agentSimpleReminderQuery(tx *gorm.DB, actor User) *gorm.DB {
	query := tx.Model(&AgentSimpleReminder{})
	if actor.Role == common.RoleRootUser {
		return query.Where("audience = ?", "root")
	}
	return query.Where("audience = ? AND agent_user_id = ?", "agent", actor.Id)
}

// ListUnreadAgentSimpleReminders exposes today's actionable reminders. Historical
// reminders remain available in history, rather than repeatedly surfacing offline days.
func ListUnreadAgentSimpleReminders(identity AuthSessionIdentity) ([]AgentSimpleReminder, error) {
	rows := make([]AgentSimpleReminder, 0)
	err := DB.Transaction(func(tx *gorm.DB) error {
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil || actor.Role == common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		day := time.Now().In(agentSimpleLocation).Format("2006-01-02")
		return agentSimpleReminderQuery(tx, actor).
			Where("day = ?", day).
			Where("NOT EXISTS (SELECT 1 FROM agent_simple_reminder_receipts receipt WHERE receipt.reminder_id = agent_simple_reminders.id AND receipt.user_id = ?)", actor.Id).
			Where("EXISTS (SELECT 1 FROM agent_simple_bills bill WHERE bill.agent_user_id = agent_simple_reminders.agent_user_id AND bill.due_at <= ? AND bill.paid_quota < bill.quota)", common.GetTimestamp()).
			Order("id ASC").Limit(50).Find(&rows).Error
	})
	return rows, err
}

func AcknowledgeAgentSimpleReminders(identity AuthSessionIdentity, ids []int64) error {
	if len(ids) == 0 || len(ids) > 50 {
		return ErrAgentSimpleInvalid
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return ErrAgentSimpleInvalid
		}
		seen[id] = true
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil || actor.Role == common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		var reminders []AgentSimpleReminder
		if err := agentSimpleReminderQuery(tx, actor).Where("id IN ?", ids).Find(&reminders).Error; err != nil {
			return err
		}
		if len(reminders) != len(ids) {
			return ErrAgentSimplePermission
		}
		for _, reminder := range reminders {
			receipt := AgentSimpleReminderReceipt{ReminderID: reminder.ID, UserID: actor.Id}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
