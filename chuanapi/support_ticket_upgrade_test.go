package chuanapi_test

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/chuanapi"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// This opt-in test consumes a disposable database initialized by the released
// application, not by the current model structs. Set SQL_DSN, LOG_SQL_DSN and,
// for SQLite, SQLITE_PATH. Run in a separate go test process because the
// production extension registry is write-once.
func TestSupportTicketUpgradeFromReleasedDatabase(t *testing.T) {
	if os.Getenv("TEST_SUPPORT_UPGRADE") != "1" {
		t.Skip("set TEST_SUPPORT_UPGRADE=1 with a released database fixture")
	}
	require.NotEmpty(t, os.Getenv("SQL_DSN"))
	previousMaster, previousRedis := common.IsMasterNode, common.RedisEnabled
	previousSQLite := common.SQLitePath
	common.IsMasterNode, common.RedisEnabled = true, false
	if path := os.Getenv("SQLITE_PATH"); path != "" {
		common.SQLitePath = path
	}
	t.Cleanup(func() {
		common.IsMasterNode, common.RedisEnabled = previousMaster, previousRedis
		common.SQLitePath = previousSQLite
	})

	registry := extension.NewRegistry()
	require.NoError(t, chuanapi.Register(registry))
	require.NoError(t, extension.SetDefault(registry))
	var ticketID int64
	var ownerID, adminID, tokenID int

	for restart := 0; restart < 2; restart++ {
		require.NoError(t, model.InitDB(), "released main database upgrade/restart %d", restart)
		require.NoError(t, model.InitLogDB(), "released log database upgrade/restart %d", restart)
		mainSQL, err := model.DB.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, mainSQL.Close()) })
		if model.LOG_DB != model.DB {
			logSQL, logErr := model.LOG_DB.DB()
			require.NoError(t, logErr)
			t.Cleanup(func() { require.NoError(t, logSQL.Close()) })
		}

		var marker model.Option
		require.NoError(t, model.DB.Where(&model.Option{Key: "SupportUpgradeFixture"}).First(&marker).Error)
		assert.Equal(t, "v1.0.0-rc.37", marker.Value)
		var owner, admin model.User
		require.NoError(t, model.DB.Where("username = ?", "support-upgrade").First(&owner).Error)
		require.NoError(t, model.DB.Where("username = ?", "support-admin").First(&admin).Error)
		assert.Equal(t, 424242, owner.Quota)
		assert.Equal(t, 424242, owner.QuotaPaid, "existing quota must be preserved by the existing wallet backfill")
		assert.Equal(t, 123, owner.UsedQuota)
		assert.Equal(t, "support@example.invalid", owner.Email)
		assert.Equal(t, "support-upgrade", owner.AffCode)
		assert.Equal(t, common.RoleCommonUser, owner.Role)
		assert.Equal(t, common.UserStatusEnabled, owner.Status)
		var token model.Token
		require.NoError(t, model.DB.Where("name = ?", "support-upgrade-token").First(&token).Error)
		assert.Equal(t, owner.Id, token.UserId)
		assert.Equal(t, strings.Repeat("b", 48), token.Key)
		assert.Equal(t, 24680, token.RemainQuota)
		assert.Equal(t, int64(-1), token.ExpiredTime)
		assert.False(t, token.UnlimitedQuota)
		var log model.Log
		require.NoError(t, model.LOG_DB.Where("content = ?", "support-upgrade-log").First(&log).Error)
		assert.Equal(t, owner.Id, log.UserId)
		assert.Equal(t, token.Id, log.TokenId)
		assert.Equal(t, 123, log.Quota)

		if restart == 0 {
			ownerID, adminID, tokenID = owner.Id, admin.Id, token.Id
			created, createErr := service.CreateSupportTicket(owner.Id, "Upgrade 工单 😀", "Original message 中文 😀")
			require.NoError(t, createErr)
			ticketID = created.Ticket.ID
			_, err = service.ReplySupportTicket(admin.Id, true, ticketID, "Support reply")
			require.NoError(t, err)
			require.NoError(t, service.MarkSupportTicketRead(owner.Id, false, ticketID))
			require.NoError(t, service.MarkSupportTicketRead(owner.Id, false, ticketID))
			_, err = service.ReplySupportTicket(owner.Id, false, ticketID, "User follow-up")
			require.NoError(t, err)
			_, err = service.SetSupportTicketStatus(ticketID, model.SupportTicketStatusResolved)
			require.NoError(t, err)
		}
		assert.Equal(t, ownerID, owner.Id)
		assert.Equal(t, adminID, admin.Id)
		assert.Equal(t, tokenID, token.Id)
		detail, err := service.GetSupportTicket(owner.Id, false, ticketID)
		require.NoError(t, err)
		assert.Equal(t, model.SupportTicketStatusResolved, detail.Ticket.Status)
		assert.True(t, detail.Ticket.UserUnread)
		assert.False(t, detail.Ticket.AdminUnread)
		assert.Equal(t, "Upgrade 工单 😀", detail.Ticket.Subject)
		require.Len(t, detail.Messages, 3)
		assert.Equal(t, "Original message 中文 😀", detail.Messages[0].Content)
		_, err = service.ReplySupportTicket(owner.Id, false, ticketID, "Rejected after resolution")
		require.ErrorIs(t, err, service.ErrSupportTicketResolved)

		for _, index := range []string{"idx_support_ticket_user_status", "idx_support_ticket_status", "idx_support_ticket_admin_unread", "idx_support_ticket_last_message"} {
			assert.True(t, model.DB.Migrator().HasIndex(&model.SupportTicket{}, index))
		}
		assert.True(t, model.DB.Migrator().HasIndex(&model.SupportTicketMessage{}, "idx_support_ticket_message_ticket"))
		assert.True(t, model.DB.Migrator().HasIndex(&model.SupportTicketMessage{}, "idx_support_ticket_message_created"))
		quiet := model.DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
		duplicateUser := model.User{Username: owner.Username, Password: "fixture-hash", AffCode: "different-aff-code"}
		require.Error(t, quiet.Create(&duplicateUser).Error, "user uniqueness must survive upgrade and restart")
		duplicateToken := model.Token{UserId: owner.Id, Name: "duplicate-key", Key: token.Key}
		require.Error(t, quiet.Create(&duplicateToken).Error, "token-key uniqueness must survive upgrade and restart")
		t.Logf("released fixture upgrade/restart %d preserved identities, balances, token uniqueness, logs, support history and indexes (%s)", restart+1, model.DB.Dialector.Name())

		if model.LOG_DB != model.DB {
			logSQL, logErr := model.LOG_DB.DB()
			require.NoError(t, logErr)
			require.NoError(t, logSQL.Close())
		}
		require.NoError(t, mainSQL.Close())
	}
}
