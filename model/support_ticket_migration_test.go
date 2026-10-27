package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifySupportTicketSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	previousDB, previousType := DB, common.MainDatabaseType()
	DB = db
	switch db.Dialector.Name() {
	case "mysql":
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	case "postgres":
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	case "sqlite":
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	default:
		t.Fatalf("unsupported support database %q", db.Dialector.Name())
	}
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
	})
	versionSQL := "SELECT VERSION()"
	if db.Dialector.Name() == "sqlite" {
		versionSQL = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
	t.Logf("support database: %s %s", db.Dialector.Name(), version)

	require.NoError(t, db.AutoMigrate(&SupportTicket{}, &SupportTicketMessage{}))
	ticket := &SupportTicket{
		UserID: 107, Subject: "Support migration 工单 😀", Status: SupportTicketStatusProcessing,
		UserUnread: false, AdminUnread: true, LastMessageAt: 1_700_000_000,
		CreatedAt: 1_700_000_000, UpdatedAt: 1_700_000_000,
	}
	message := &SupportTicketMessage{
		AuthorUserID: ticket.UserID, AuthorRole: SupportTicketAuthorUser,
		Content: "Preserve 中文 and emoji 😀 across restarts", CreatedAt: ticket.CreatedAt,
	}
	require.NoError(t, CreateSupportTicket(ticket, message))
	t.Cleanup(func() {
		require.NoError(t, db.Where("ticket_id = ?", ticket.ID).Delete(&SupportTicketMessage{}).Error)
		require.NoError(t, db.Where("id = ?", ticket.ID).Delete(&SupportTicket{}).Error)
	})

	require.NoError(t, db.AutoMigrate(&SupportTicket{}, &SupportTicketMessage{}), "support schema migration must be idempotent")
	assert.True(t, db.Migrator().HasTable(&SupportTicket{}))
	assert.True(t, db.Migrator().HasTable(&SupportTicketMessage{}))
	for _, index := range []string{"idx_support_ticket_user_status", "idx_support_ticket_status", "idx_support_ticket_admin_unread", "idx_support_ticket_last_message"} {
		assert.True(t, db.Migrator().HasIndex(&SupportTicket{}, index), "ticket index %s must survive restart", index)
	}
	for _, index := range []string{"idx_support_ticket_message_ticket", "idx_support_ticket_message_created"} {
		assert.True(t, db.Migrator().HasIndex(&SupportTicketMessage{}, index), "message index %s must survive restart", index)
	}
	stored, messages, err := GetSupportTicket(ticket.ID, ticket.UserID, false)
	require.NoError(t, err)
	assert.Equal(t, ticket, stored)
	require.Len(t, messages, 1)
	assert.Equal(t, *message, messages[0])

	updated, err := AppendSupportTicketMessage(ticket.ID, 108, true, "Support reply 回复 😀", SupportTicketAuthorAdmin, SupportTicketStatusAwaitingUser, ticket.LastMessageAt+1)
	require.NoError(t, err)
	assert.True(t, updated.UserUnread)
	assert.False(t, updated.AdminUnread)
	require.NoError(t, MarkSupportTicketRead(ticket.ID, ticket.UserID, false))
	require.NoError(t, MarkSupportTicketRead(ticket.ID, ticket.UserID, false), "MySQL zero affected rows must still be an idempotent success")
	require.ErrorIs(t, MarkSupportTicketRead(ticket.ID, ticket.UserID+1, false), gorm.ErrRecordNotFound)
	unread, total, counts, err := SearchSupportTickets(SupportTicketFilter{UserID: ticket.UserID, Keyword: ticket.Subject, UnreadOnly: true}, 0, 20)
	require.NoError(t, err)
	assert.Empty(t, unread)
	assert.Zero(t, total)
	assert.GreaterOrEqual(t, counts.AwaitingUser, int64(1))

	updated, err = UpdateSupportTicketStatus(ticket.ID, SupportTicketStatusResolved, ticket.LastMessageAt+2)
	require.NoError(t, err)
	assert.Equal(t, SupportTicketStatusResolved, updated.Status)
	_, err = AppendSupportTicketMessage(ticket.ID, ticket.UserID, false, "Late reply", SupportTicketAuthorUser, SupportTicketStatusProcessing, ticket.LastMessageAt+3)
	require.ErrorIs(t, err, ErrSupportTicketResolved)
	_, messages, err = GetSupportTicket(ticket.ID, ticket.UserID, false)
	require.NoError(t, err)
	assert.Len(t, messages, 2, "rejected replies must not persist a message")
}

func TestSupportTicketSchemaSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "support.db")), &gorm.Config{})
	require.NoError(t, err)
	verifySupportTicketSchema(t, db)
}

func TestSupportTicketSchemaConfiguredDatabases(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		dialector func(string) gorm.Dialector
	}{
		{name: "mysql", env: "TEST_MYSQL_DSN", dialector: func(dsn string) gorm.Dialector { return mysql.Open(dsn) }},
		{name: "postgres", env: "TEST_POSTGRES_DSN", dialector: func(dsn string) gorm.Dialector {
			return postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.env))
			if dsn == "" {
				if os.Getenv("TEST_DATABASE_COMPAT") == "1" && os.Getenv("TEST_DATABASE_DIALECT") == test.name {
					require.NotEmpty(t, dsn, "%s is required by the selected database matrix job", test.env)
				}
				t.Skip(test.env + " is not configured")
			}
			db, err := gorm.Open(test.dialector(dsn), &gorm.Config{})
			require.NoError(t, err)
			verifySupportTicketSchema(t, db)
		})
	}
}
