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
	contact := ""
	return CreateSupportTicketWithOptions(userID, SupportTicketCreateInput{Subject: subject, Message: content, ContactEmail: &contact})
}

func CreateSupportTicketWithOptions(userID int, input SupportTicketCreateInput) (*SupportTicketDetail, error) {
	subject, content := input.Subject, input.Message
	subject = strings.TrimSpace(subject)
	content = strings.TrimSpace(content)
	if userID <= 0 || subject == "" || content == "" || utf8.RuneCountInString(subject) > SupportTicketSubjectMaxRunes || utf8.RuneCountInString(content) > SupportTicketMessageMaxRunes {
		return nil, ErrSupportTicketInvalidInput
	}
	category := strings.TrimSpace(input.Category)
	if category == "" {
		category = "other"
	}
	if !ValidSupportTicketCategory(category) || len(input.Attachments) > SupportTicketMaxImages {
		return nil, ErrSupportTicketInvalidInput
	}
	contact := ""
	if input.ContactEmail == nil {
		var owner model.User
		if err := model.DB.Select("id", "email").Where("id = ?", userID).First(&owner).Error; err != nil {
			return nil, err
		}
		// Imported legacy addresses are not trusted contacts or a reason to
		// prevent support access. Explicit entries, however, must be valid.
		contact, _ = model.ValidSupportEmailAddress(owner.Email)
	} else if strings.TrimSpace(*input.ContactEmail) != "" {
		var valid bool
		contact, valid = model.ValidSupportEmailAddress(*input.ContactEmail)
		if !valid {
			return nil, ErrSupportTicketInvalidInput
		}
	}
	submission, err := supportTicketSubmission(input.RequestID, userID, 0, subject, content, category, contact, input.Attachments)
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	ticket := &model.SupportTicket{
		UserID:        userID,
		Subject:       subject,
		Category:      category,
		ContactEmail:  contact,
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
	if err := model.CreateSupportTicketWithAttachments(ticket, message, input.Attachments, submission); err != nil {
		return nil, err
	}
	return GetSupportTicket(userID, false, ticket.ID)
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
	return ReplySupportTicketWithAttachments(userID, adminView, ticketID, content, "", nil)
}

func ReplySupportTicketWithAttachments(userID int, adminView bool, ticketID int64, content, requestID string, attachments []model.SupportTicketAttachment) (*model.SupportTicket, error) {
	content = strings.TrimSpace(content)
	if ticketID <= 0 || userID <= 0 || content == "" || utf8.RuneCountInString(content) > SupportTicketMessageMaxRunes || len(attachments) > SupportTicketMaxImages {
		return nil, ErrSupportTicketInvalidInput
	}
	authorRole := model.SupportTicketAuthorUser
	nextStatus := model.SupportTicketStatusProcessing
	if adminView {
		authorRole = model.SupportTicketAuthorAdmin
		nextStatus = model.SupportTicketStatusAwaitingUser
	}
	submission, err := supportTicketSubmission(requestID, userID, ticketID, authorRole, content, "", "", attachments)
	if err != nil {
		return nil, err
	}
	ticket, err := model.AppendSupportTicketMessageWithAttachments(ticketID, userID, adminView, content, authorRole, nextStatus, common.GetTimestamp(), attachments, submission)
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
