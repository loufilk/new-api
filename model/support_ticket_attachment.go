package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// Images live in the primary store so every node enforces the same ownership.
// Data is never serialized into ticket, message, audit or notification JSON.
type SupportTicketAttachment struct {
	ID        string `json:"id" gorm:"primaryKey;size:36"`
	TicketID  int64  `json:"-" gorm:"not null;index:idx_support_attachment_ticket"`
	MessageID int64  `json:"-" gorm:"not null;index:idx_support_attachment_message"`
	Position  int    `json:"-"`
	MediaType string `json:"media_type" gorm:"size:32;not null"`
	SizeBytes int    `json:"size_bytes" gorm:"not null"`
	Width     int    `json:"width" gorm:"not null"`
	Height    int    `json:"height" gorm:"not null"`
	Data      []byte `json:"-" gorm:"not null"`
}

// A row is created and completed in the same transaction as its message,
// images and outbox. Failed or cancelled writes leave no upload/orphan rows.
type SupportTicketSubmission struct {
	Key         string `json:"-" gorm:"primaryKey;size:64"`
	PayloadHash string `json:"-" gorm:"size:64;not null"`
	TicketID    int64  `json:"-"`
	MessageID   int64  `json:"-"`
}

func lockSupportTicketSubmission(tx *gorm.DB, submission *SupportTicketSubmission) (bool, error) {
	if submission == nil {
		return false, nil
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(submission).Error; err != nil {
		return false, err
	}
	var existing SupportTicketSubmission
	if err := lockForUpdate(tx).Where(SupportTicketSubmission{Key: submission.Key}).First(&existing).Error; err != nil {
		return false, err
	}
	if existing.PayloadHash != submission.PayloadHash {
		return false, ErrSupportTicketSubmissionConflict
	}
	*submission = existing
	return existing.TicketID > 0, nil
}

func completeSupportTicketSubmission(tx *gorm.DB, submission *SupportTicketSubmission, ticketID, messageID int64) error {
	if submission == nil {
		return nil
	}
	return tx.Model(submission).Updates(map[string]any{"ticket_id": ticketID, "message_id": messageID}).Error
}

func persistSupportTicketAttachments(tx *gorm.DB, ticketID, messageID int64, attachments []SupportTicketAttachment) error {
	if len(attachments) == 0 {
		return nil
	}
	for i := range attachments {
		attachments[i].TicketID, attachments[i].MessageID, attachments[i].Position = ticketID, messageID, i
	}
	// Database errors must not log screenshots or original image bytes.
	return tx.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Create(&attachments).Error
}

func GetSupportTicketAttachment(ticketID int64, attachmentID string, userID int, adminView bool) (*SupportTicketAttachment, error) {
	var attachment SupportTicketAttachment
	query := DB.Table("support_ticket_attachments AS attachment").Select("attachment.*").Joins("JOIN support_tickets AS ticket ON ticket.id = attachment.ticket_id").Where("attachment.id = ? AND attachment.ticket_id = ?", attachmentID, ticketID)
	if !adminView {
		query = query.Where("ticket.user_id = ?", userID)
	}
	if err := query.First(&attachment).Error; err != nil {
		return nil, err
	}
	return &attachment, nil
}
