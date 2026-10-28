package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useSupportTicketTestDB(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.SupportTicket{}, &model.SupportTicketMessage{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		require.NoError(t, sqlDB.Close())
	})
}

func TestSupportTicketConversationTransitionsAndOwnership(t *testing.T) {
	useSupportTicketTestDB(t)

	created, err := CreateSupportTicket(11, "Cannot use image API", "Please help")
	require.NoError(t, err)
	require.NotNil(t, created.Ticket)
	assert.Equal(t, model.SupportTicketStatusProcessing, created.Ticket.Status)
	assert.True(t, created.Ticket.AdminUnread)
	assert.False(t, created.Ticket.UserUnread)
	require.Len(t, created.Messages, 1)
	assert.Equal(t, model.SupportTicketAuthorUser, created.Messages[0].AuthorRole)

	_, err = GetSupportTicket(12, false, created.Ticket.ID)
	require.ErrorIs(t, err, ErrSupportTicketNotFound)

	userList, err := ListSupportTickets(11, false, "image", "", false, 0, 20)
	require.NoError(t, err)
	require.Len(t, userList.Items, 1)
	assert.Equal(t, int64(1), userList.StatusCounts.Processing)

	ticket, err := ReplySupportTicket(99, true, created.Ticket.ID, "Please send the request ID")
	require.NoError(t, err)
	assert.Equal(t, model.SupportTicketStatusAwaitingUser, ticket.Status)
	assert.True(t, ticket.UserUnread)
	assert.False(t, ticket.AdminUnread)

	unread, err := ListSupportTickets(11, false, "", "", true, 0, 20)
	require.NoError(t, err)
	require.Len(t, unread.Items, 1)
	require.NoError(t, MarkSupportTicketRead(11, false, created.Ticket.ID))
	require.NoError(t, MarkSupportTicketRead(11, false, created.Ticket.ID), "mark-read must be idempotent across database affected-row semantics")
	unread, err = ListSupportTickets(11, false, "", "", true, 0, 20)
	require.NoError(t, err)
	assert.Empty(t, unread.Items)
	require.ErrorIs(t, MarkSupportTicketRead(12, false, created.Ticket.ID), ErrSupportTicketNotFound)

	ticket, err = ReplySupportTicket(11, false, created.Ticket.ID, "Request ID is req_123")
	require.NoError(t, err)
	assert.Equal(t, model.SupportTicketStatusProcessing, ticket.Status)
	assert.True(t, ticket.AdminUnread)

	ticket, err = SetSupportTicketStatus(created.Ticket.ID, model.SupportTicketStatusResolved)
	require.NoError(t, err)
	assert.Equal(t, model.SupportTicketStatusResolved, ticket.Status)
	assert.True(t, ticket.UserUnread)

	_, err = ReplySupportTicket(11, false, created.Ticket.ID, "One more thing")
	require.ErrorIs(t, err, ErrSupportTicketResolved)

	ticket, err = ReplySupportTicket(99, true, created.Ticket.ID, "Reopening with one more question")
	require.NoError(t, err)
	assert.Equal(t, model.SupportTicketStatusAwaitingUser, ticket.Status)

	detail, err := GetSupportTicket(11, false, created.Ticket.ID)
	require.NoError(t, err)
	require.Len(t, detail.Messages, 4)
	assert.Equal(t, []string{
		model.SupportTicketAuthorUser,
		model.SupportTicketAuthorAdmin,
		model.SupportTicketAuthorUser,
		model.SupportTicketAuthorAdmin,
	}, []string{
		detail.Messages[0].AuthorRole,
		detail.Messages[1].AuthorRole,
		detail.Messages[2].AuthorRole,
		detail.Messages[3].AuthorRole,
	})
}

func TestSupportTicketValidationRejectsInvalidInput(t *testing.T) {
	useSupportTicketTestDB(t)

	_, err := CreateSupportTicket(11, "", "message")
	require.ErrorIs(t, err, ErrSupportTicketInvalidInput)
	_, err = CreateSupportTicket(11, strings.Repeat("a", SupportTicketSubjectMaxRunes+1), "message")
	require.ErrorIs(t, err, ErrSupportTicketInvalidInput)
	_, err = CreateSupportTicket(11, "subject", strings.Repeat("a", SupportTicketMessageMaxRunes+1))
	require.ErrorIs(t, err, ErrSupportTicketInvalidInput)
	_, err = ListSupportTickets(11, false, "", "unknown", false, 0, 20)
	require.ErrorIs(t, err, ErrSupportTicketInvalidStatus)
	_, err = SetSupportTicketStatus(1, "unknown")
	require.ErrorIs(t, err, ErrSupportTicketInvalidStatus)
}
