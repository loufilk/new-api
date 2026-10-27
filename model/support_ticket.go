package model

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

const (
	SupportTicketStatusProcessing   = "processing"
	SupportTicketStatusAwaitingUser = "awaiting_user"
	SupportTicketStatusResolved     = "resolved"
)

const (
	SupportTicketAuthorUser  = "user"
	SupportTicketAuthorAdmin = "admin"
)

var ErrSupportTicketResolved = errors.New("support ticket is resolved")

type SupportTicket struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	UserID        int    `json:"user_id" gorm:"not null;index:idx_support_ticket_user_status,priority:1"`
	Subject       string `json:"subject" gorm:"size:200;not null"`
	Status        string `json:"status" gorm:"size:32;not null;index:idx_support_ticket_user_status,priority:2;index:idx_support_ticket_status"`
	UserUnread    bool   `json:"user_unread" gorm:"not null"`
	AdminUnread   bool   `json:"admin_unread" gorm:"not null;index:idx_support_ticket_admin_unread"`
	LastMessageAt int64  `json:"last_message_at" gorm:"not null;index:idx_support_ticket_last_message"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt     int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

type SupportTicketMessage struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	TicketID     int64  `json:"ticket_id" gorm:"not null;index:idx_support_ticket_message_ticket"`
	AuthorUserID int    `json:"-" gorm:"not null"`
	AuthorRole   string `json:"author_role" gorm:"size:16;not null"`
	Content      string `json:"content" gorm:"type:text;not null"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime;index:idx_support_ticket_message_created"`
}

type SupportTicketFilter struct {
	UserID     int
	Keyword    string
	Status     string
	UnreadOnly bool
	AdminView  bool
}

type SupportTicketStatusCounts struct {
	Processing   int64 `json:"processing"`
	AwaitingUser int64 `json:"awaiting_user"`
	Resolved     int64 `json:"resolved"`
}

func supportTicketScope(filter SupportTicketFilter) *gorm.DB {
	query := DB.Model(&SupportTicket{})
	if filter.UserID > 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	return query
}

func SearchSupportTickets(filter SupportTicketFilter, offset, limit int) ([]SupportTicket, int64, SupportTicketStatusCounts, error) {
	var counts SupportTicketStatusCounts
	var countRows []struct {
		Status string
		Count  int64
	}
	if err := supportTicketScope(filter).Select("status, COUNT(*) AS count").Group("status").Scan(&countRows).Error; err != nil {
		return nil, 0, counts, err
	}
	for _, row := range countRows {
		switch row.Status {
		case SupportTicketStatusProcessing:
			counts.Processing = row.Count
		case SupportTicketStatusAwaitingUser:
			counts.AwaitingUser = row.Count
		case SupportTicketStatusResolved:
			counts.Resolved = row.Count
		}
	}

	query := supportTicketScope(filter)
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		query = query.Where("subject LIKE ?", "%"+keyword+"%")
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.UnreadOnly {
		if filter.AdminView {
			query = query.Where("admin_unread = ?", true)
		} else {
			query = query.Where("user_unread = ?", true)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, counts, err
	}
	var tickets []SupportTicket
	if err := query.Order("last_message_at DESC").Order("id DESC").Offset(offset).Limit(limit).Find(&tickets).Error; err != nil {
		return nil, 0, counts, err
	}
	return tickets, total, counts, nil
}

func CreateSupportTicket(ticket *SupportTicket, message *SupportTicketMessage) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(ticket).Error; err != nil {
			return err
		}
		message.TicketID = ticket.ID
		return tx.Create(message).Error
	})
}

func GetSupportTicket(ticketID int64, userID int, adminView bool) (*SupportTicket, []SupportTicketMessage, error) {
	var ticket SupportTicket
	query := DB.Where("id = ?", ticketID)
	if !adminView {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.First(&ticket).Error; err != nil {
		return nil, nil, err
	}
	var messages []SupportTicketMessage
	if err := DB.Where("ticket_id = ?", ticket.ID).Order("id ASC").Find(&messages).Error; err != nil {
		return nil, nil, err
	}
	return &ticket, messages, nil
}

func AppendSupportTicketMessage(ticketID int64, userID int, adminView bool, content, authorRole, nextStatus string, now int64) (*SupportTicket, error) {
	var updated SupportTicket
	err := DB.Transaction(func(tx *gorm.DB) error {
		query := lockForUpdate(tx).Where("id = ?", ticketID)
		if !adminView {
			query = query.Where("user_id = ?", userID)
		}
		if err := query.First(&updated).Error; err != nil {
			return err
		}
		if !adminView && updated.Status == SupportTicketStatusResolved {
			return ErrSupportTicketResolved
		}
		message := SupportTicketMessage{
			TicketID:     updated.ID,
			AuthorUserID: userID,
			AuthorRole:   authorRole,
			Content:      content,
			CreatedAt:    now,
		}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		updates := map[string]any{
			"status":          nextStatus,
			"last_message_at": now,
			"updated_at":      now,
		}
		if adminView {
			updates["user_unread"] = true
			updates["admin_unread"] = false
		} else {
			updates["user_unread"] = false
			updates["admin_unread"] = true
		}
		if err := tx.Model(&SupportTicket{}).Where("id = ?", updated.ID).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", updated.ID).First(&updated).Error
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func UpdateSupportTicketStatus(ticketID int64, status string, now int64) (*SupportTicket, error) {
	var ticket SupportTicket
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ?", ticketID).First(&ticket).Error; err != nil {
			return err
		}
		updates := map[string]any{"status": status, "updated_at": now}
		if status == SupportTicketStatusResolved {
			updates["user_unread"] = true
			updates["admin_unread"] = false
		}
		if err := tx.Model(&SupportTicket{}).Where("id = ?", ticket.ID).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", ticket.ID).First(&ticket).Error
	})
	if err != nil {
		return nil, err
	}
	return &ticket, nil
}

func MarkSupportTicketRead(ticketID int64, userID int, adminView bool) error {
	query := DB.Model(&SupportTicket{}).Where("id = ?", ticketID)
	column := "admin_unread"
	if !adminView {
		query = query.Where("user_id = ?", userID)
		column = "user_unread"
	}
	result := query.Update(column, false)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	// MySQL can report zero affected rows when the value is already false.
	// Keep the read endpoint idempotent without confusing that case with a
	// missing/foreign ticket.
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
