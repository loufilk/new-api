package controller

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type supportTicketCreateRequest struct {
	Subject string `json:"subject"`
	Message string `json:"message"`
}

type supportTicketReplyRequest struct {
	Message string `json:"message"`
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
	var request supportTicketCreateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	detail, err := service.CreateSupportTicket(c.GetInt("id"), request.Subject, request.Message)
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
	var request supportTicketReplyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ticket, err := service.ReplySupportTicket(c.GetInt("id"), adminView, id, request.Message)
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
