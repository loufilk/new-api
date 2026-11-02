package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type supportTicketCreateRequest struct {
	Subject      string  `json:"subject"`
	Message      string  `json:"message"`
	Category     string  `json:"category"`
	ContactEmail *string `json:"contact_email"`
	RequestID    string  `json:"request_id"`
}

type supportTicketReplyRequest struct {
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type supportTicketStatusRequest struct {
	Status string `json:"status"`
}

func supportTicketID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidId)
		return 0, false
	}
	return id, true
}

func supportTicketUnreadOnly(c *gin.Context) (bool, bool) {
	raw := c.Query("unread")
	if raw == "" {
		return false, true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return false, false
	}
	return value, true
}

func supportTicketError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrSupportTicketSubmissionConflict):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This request was already submitted with different content"), gin.H{"code": "SUPPORT_TICKET_REQUEST_CONFLICT"})
	case errors.Is(err, service.ErrSupportTicketInvalidInput), errors.Is(err, service.ErrSupportTicketInvalidStatus):
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
	case errors.Is(err, service.ErrSupportTicketNotFound):
		common.ApiErrorI18n(c, i18n.MsgNotFound)
	case errors.Is(err, service.ErrSupportTicketResolved):
		common.ApiErrorI18n(c, i18n.MsgSupportTicketResolved)
	default:
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
	}
}

func ListSupportTickets(c *gin.Context) {
	listSupportTickets(c, false)
}

func ListAdminSupportTickets(c *gin.Context) {
	listSupportTickets(c, true)
}

func listSupportTickets(c *gin.Context, adminView bool) {
	unreadOnly, ok := supportTicketUnreadOnly(c)
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	result, err := service.ListSupportTickets(c.GetInt("id"), adminView, c.Query("keyword"), c.Query("status"), unreadOnly, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		supportTicketError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":         result.Items,
		"total":         result.Total,
		"page":          page.GetPage(),
		"page_size":     page.GetPageSize(),
		"status_counts": result.StatusCounts,
	})
}

func CreateSupportTicket(c *gin.Context) {
	request, attachments, err := readSupportTicketRequest(c, true)
	if err != nil {
		writeSupportTicketUploadError(c, err)
		return
	}
	detail, err := service.CreateSupportTicketWithOptions(c.GetInt("id"), service.SupportTicketCreateInput{Subject: request.Subject, Message: request.Message, Category: request.Category, ContactEmail: request.ContactEmail, RequestID: request.RequestID, Attachments: attachments})
	if err != nil {
		supportTicketError(c, err)
		return
	}
	common.ApiSuccess(c, detail)
}

func GetSupportTicket(c *gin.Context) {
	getSupportTicket(c, false)
}

func GetAdminSupportTicket(c *gin.Context) {
	getSupportTicket(c, true)
}

func getSupportTicket(c *gin.Context, adminView bool) {
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	detail, err := service.GetSupportTicket(c.GetInt("id"), adminView, id)
	if err != nil {
		supportTicketError(c, err)
		return
	}
	common.ApiSuccess(c, detail)
}

func ReplySupportTicket(c *gin.Context) {
	replySupportTicket(c, false)
}

func ReplyAdminSupportTicket(c *gin.Context) {
	replySupportTicket(c, true)
}

func replySupportTicket(c *gin.Context, adminView bool) {
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	request, attachments, err := readSupportTicketRequest(c, false)
	if err != nil {
		writeSupportTicketUploadError(c, err)
		return
	}
	ticket, err := service.ReplySupportTicketWithAttachments(c.GetInt("id"), adminView, id, request.Message, request.RequestID, attachments)
	if err != nil {
		supportTicketError(c, err)
		return
	}
	common.ApiSuccess(c, ticket)
}

func UpdateAdminSupportTicketStatus(c *gin.Context) {
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	var request supportTicketStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ticket, err := service.SetSupportTicketStatus(id, request.Status)
	if err != nil {
		supportTicketError(c, err)
		return
	}
	common.ApiSuccess(c, ticket)
}

func MarkSupportTicketRead(c *gin.Context) {
	markSupportTicketRead(c, false)
}

func MarkAdminSupportTicketRead(c *gin.Context) {
	markSupportTicketRead(c, true)
}

func markSupportTicketRead(c *gin.Context, adminView bool) {
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	if err := service.MarkSupportTicketRead(c.GetInt("id"), adminView, id); err != nil {
		supportTicketError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// Stream bounded multipart into memory; never create pre-ticket uploads or
// temporary public files. JSON remains available to existing text-only clients.
func readSupportTicketRequest(c *gin.Context, create bool) (supportTicketCreateRequest, []model.SupportTicketAttachment, error) {
	var input supportTicketCreateRequest
	var attachments []model.SupportTicketAttachment
	limit := int64(service.SupportTicketMaxImages*service.SupportTicketImageMaxBytes + 65536)
	if c.ContentType() != "multipart/form-data" {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
		if create {
			err := common.DecodeJsonStrict(c.Request.Body, &input)
			return input, nil, err
		}
		var reply supportTicketReplyRequest
		err := common.DecodeJsonStrict(c.Request.Body, &reply)
		input.Message, input.RequestID = reply.Message, reply.RequestID
		return input, nil, err
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return input, nil, err
	}
	fields := make(map[string]bool)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return input, attachments, nil
		}
		if err != nil {
			return input, nil, err
		}
		name := part.FormName()
		if name == "images" {
			if len(attachments) >= service.SupportTicketMaxImages || part.FileName() == "" {
				part.Close()
				return input, nil, service.ErrSupportTicketInvalidInput
			}
			data, err := io.ReadAll(io.LimitReader(part, service.SupportTicketImageMaxBytes+1))
			part.Close()
			if err != nil {
				return input, nil, err
			}
			attachment, err := service.NormalizeSupportTicketImage(part.FileName(), part.Header.Get("Content-Type"), data)
			if err != nil {
				return input, nil, err
			}
			attachments = append(attachments, attachment)
			continue
		}
		allowed := name == "message" || name == "request_id" || (create && (name == "subject" || name == "category" || name == "contact_email"))
		if !allowed || fields[name] || part.FileName() != "" {
			part.Close()
			return input, nil, service.ErrSupportTicketInvalidInput
		}
		fields[name] = true
		value, err := io.ReadAll(io.LimitReader(part, 40001))
		part.Close()
		if err != nil || len(value) > 40000 {
			return input, nil, service.ErrSupportTicketInvalidInput
		}
		switch name {
		case "subject":
			input.Subject = string(value)
		case "message":
			input.Message = string(value)
		case "category":
			input.Category = string(value)
		case "contact_email":
			contact := string(value)
			input.ContactEmail = &contact
		case "request_id":
			input.RequestID = string(value)
		}
	}
}

func writeSupportTicketUploadError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		status = http.StatusRequestEntityTooLarge
	}
	common.ApiErrorStatus(c, status, common.NewMessage("Invalid parameters"))
}

func GetSupportTicketAttachment(c *gin.Context)      { getSupportTicketAttachment(c, false) }
func GetAdminSupportTicketAttachment(c *gin.Context) { getSupportTicketAttachment(c, true) }

func getSupportTicketAttachment(c *gin.Context, adminView bool) {
	ticketID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	attachmentID, idErr := uuid.Parse(c.Param("attachment_id"))
	if err != nil || ticketID <= 0 || idErr != nil || attachmentID == uuid.Nil {
		c.Status(http.StatusNotFound)
		return
	}
	attachment, err := model.GetSupportTicketAttachment(ticketID, attachmentID.String(), c.GetInt("id"), adminView)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.Status(http.StatusNotFound)
		return
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Database error, please contact the administrator"))
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Header("Content-Disposition", `inline; filename="support-`+attachment.ID+`.png"`)
	c.Data(http.StatusOK, attachment.MediaType, attachment.Data)
}
