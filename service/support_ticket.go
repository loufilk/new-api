package service

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const (
	SupportTicketSubjectMaxRunes = 200
	SupportTicketMessageMaxRunes = 10000
)

var (
	ErrSupportTicketInvalidInput  = errors.New("invalid support ticket input")
	ErrSupportTicketInvalidStatus = errors.New("invalid support ticket status")
	ErrSupportTicketResolved      = errors.New("support ticket is resolved")
	ErrSupportTicketNotFound      = errors.New("support ticket not found")
)

type SupportTicketListResult struct {
	Items        []model.SupportTicket           `json:"items"`
	Total        int64                           `json:"total"`
	StatusCounts model.SupportTicketStatusCounts `json:"status_counts"`
}

type SupportTicketDetail struct {
	Ticket   *model.SupportTicket         `json:"ticket"`
	Messages []model.SupportTicketMessage `json:"messages"`
}

func ValidSupportTicketStatus(status string) bool {
	switch status {
	case "", model.SupportTicketStatusProcessing, model.SupportTicketStatusAwaitingUser, model.SupportTicketStatusResolved:
		return true
	default:
		return false
	}
}

func ListSupportTickets(userID int, adminView bool, keyword, status string, unreadOnly bool, offset, limit int) (*SupportTicketListResult, error) {
	status = strings.TrimSpace(status)
	if !ValidSupportTicketStatus(status) {
		return nil, ErrSupportTicketInvalidStatus
	}
	filter := model.SupportTicketFilter{
		Keyword:    strings.TrimSpace(keyword),
		Status:     status,
		UnreadOnly: unreadOnly,
		AdminView:  adminView,
	}
	if !adminView {
		filter.UserID = userID
	}
	items, total, counts, err := model.SearchSupportTickets(filter, offset, limit)
	if err != nil {
		return nil, err
	}
	return &SupportTicketListResult{Items: items, Total: total, StatusCounts: counts}, nil
}

func CreateSupportTicket(userID int, subject, content string) (*SupportTicketDetail, error) {
	subject = strings.TrimSpace(subject)
	content = strings.TrimSpace(content)
	if userID <= 0 || subject == "" || content == "" || utf8.RuneCountInString(subject) > SupportTicketSubjectMaxRunes || utf8.RuneCountInString(content) > SupportTicketMessageMaxRunes {
		return nil, ErrSupportTicketInvalidInput
	}
	now := common.GetTimestamp()
	ticket := &model.SupportTicket{
		UserID:        userID,
		Subject:       subject,
		Status:        model.SupportTicketStatusProcessing,
		AdminUnread:   true,
		UserUnread:    false,
		LastMessageAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	message := &model.SupportTicketMessage{
		AuthorUserID: userID,
		AuthorRole:   model.SupportTicketAuthorUser,
		Content:      content,
		CreatedAt:    now,
	}
	if err := model.CreateSupportTicket(ticket, message); err != nil {
		return nil, err
	}
	return &SupportTicketDetail{Ticket: ticket, Messages: []model.SupportTicketMessage{*message}}, nil
}

func GetSupportTicket(userID int, adminView bool, ticketID int64) (*SupportTicketDetail, error) {
	if ticketID <= 0 || (!adminView && userID <= 0) {
		return nil, ErrSupportTicketInvalidInput
	}
	ticket, messages, err := model.GetSupportTicket(ticketID, userID, adminView)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSupportTicketNotFound
	}
	if err != nil {
		return nil, err
	}
	return &SupportTicketDetail{Ticket: ticket, Messages: messages}, nil
}

func ReplySupportTicket(userID int, adminView bool, ticketID int64, content string) (*model.SupportTicket, error) {
	content = strings.TrimSpace(content)
	if ticketID <= 0 || userID <= 0 || content == "" || utf8.RuneCountInString(content) > SupportTicketMessageMaxRunes {
		return nil, ErrSupportTicketInvalidInput
	}
	authorRole := model.SupportTicketAuthorUser
	nextStatus := model.SupportTicketStatusProcessing
	if adminView {
		authorRole = model.SupportTicketAuthorAdmin
		nextStatus = model.SupportTicketStatusAwaitingUser
	}
	ticket, err := model.AppendSupportTicketMessage(ticketID, userID, adminView, content, authorRole, nextStatus, common.GetTimestamp())
	if errors.Is(err, model.ErrSupportTicketResolved) {
		return nil, ErrSupportTicketResolved
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSupportTicketNotFound
	}
	return ticket, err
}

func SetSupportTicketStatus(ticketID int64, status string) (*model.SupportTicket, error) {
	status = strings.TrimSpace(status)
	if ticketID <= 0 || status == "" || !ValidSupportTicketStatus(status) {
		return nil, ErrSupportTicketInvalidStatus
	}
	ticket, err := model.UpdateSupportTicketStatus(ticketID, status, common.GetTimestamp())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSupportTicketNotFound
	}
	return ticket, err
}

func MarkSupportTicketRead(userID int, adminView bool, ticketID int64) error {
	if ticketID <= 0 || (!adminView && userID <= 0) {
		return ErrSupportTicketInvalidInput
	}
	err := model.MarkSupportTicketRead(ticketID, userID, adminView)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrSupportTicketNotFound
	}
	return err
}
