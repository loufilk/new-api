package model

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This is a focused log-projection fixture, not an engine compatibility gate.
func TestLogPrivacyUserTokenAndExportReadBoundaries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "logs.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	oldLogDB, oldMaxRecentItems := LOG_DB, common.MaxRecentItems
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		LOG_DB, common.MaxRecentItems = oldLogDB, oldMaxRecentItems
		common.SetDatabaseTypes(oldMain, oldLogType)
		assert.NoError(t, pool.Close())
	})
	LOG_DB, common.MaxRecentItems = db, 20
	common.SetDatabaseTypes(oldMain, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&Log{}))
	const raw = "private upstream https://provider.invalid/?key=synthetic-secret model private-model"
	const diagnostics = `{"status_code":502,"error_type":"bad_response_status_code","error_code":"private-code","model_price":1.25,"admin_info":{"diagnostic":"private-admin"},"root_info":{"diagnostic":"private-root"}}`
	rows := []Log{
		{UserId: 1, TokenId: 11, Type: LogTypeError, Content: raw, ChannelId: 909, UpstreamRequestId: "private-upstream", RequestId: "local-error", Other: diagnostics},
		{UserId: 1, TokenId: 11, Type: LogTypeConsume, Content: "wallet charged 100", ChannelId: 909, UpstreamRequestId: "private-upstream", RequestId: "local-consume", Quota: 100, Other: diagnostics},
		{UserId: 1, TokenId: 11, Type: LogTypeTopup, Content: "wallet credited 200", ChannelId: 909, UpstreamRequestId: "private-upstream", RequestId: "local-topup", Quota: 200},
		{UserId: 1, TokenId: 11, Type: LogTypeError, Content: raw, ChannelId: 909, UpstreamRequestId: "private-upstream", RequestId: "local-malformed", Other: "{malformed"},
		{UserId: 2, TokenId: 22, Type: LogTypeError, Content: "other-user-private", RequestId: "other-user"},
	}
	require.NoError(t, db.Create(&rows).Error)
	var before []Log
	require.NoError(t, db.Order("id").Find(&before).Error)
	check := func(t *testing.T, logs []*Log, firstID int) {
		t.Helper()
		require.Len(t, logs, 4)
		for i, log := range logs {
			assert.Equal(t, firstID+i, log.Id)
			assert.Equal(t, 1, log.UserId)
			assert.Equal(t, 11, log.TokenId)
			assert.Zero(t, log.ChannelId)
			assert.Empty(t, log.ChannelName)
			assert.Empty(t, log.UpstreamRequestId)
			assert.NotEmpty(t, log.RequestId, "local correlation remains available")
			assert.NotContains(t, log.Other, "private-")
			switch log.RequestId {
			case "local-error":
				assert.Equal(t, "upstream request failed", log.Content)
				assert.Contains(t, log.Other, `"model_price":1.25`)
			case "local-malformed":
				assert.Equal(t, "request failed", log.Content)
				assert.Equal(t, "{}", log.Other)
			case "local-consume":
				assert.Equal(t, "wallet charged 100", log.Content)
				assert.Equal(t, 100, log.Quota)
			case "local-topup":
				assert.Equal(t, "wallet credited 200", log.Content)
				assert.Equal(t, 200, log.Quota)
			default:
				t.Fatalf("foreign or missing request identity: %q", log.RequestId)
			}
		}
	}
	t.Run("user-list", func(t *testing.T) {
		logs, total, err := GetUserLogs(1, LogFilter{}, 0, 20)
		require.NoError(t, err)
		assert.Equal(t, int64(4), total)
		check(t, logs, 1)
		page, total, err := GetUserLogs(1, LogFilter{}, 1, 2)
		require.NoError(t, err)
		require.Len(t, page, 2)
		assert.Equal(t, int64(4), total)
		assert.Equal(t, 2, page[0].Id)
		assert.Equal(t, 3, page[1].Id)
		assert.Equal(t, "local-topup", page[0].RequestId)
		assert.Equal(t, "local-consume", page[1].RequestId)
	})
	t.Run("token-list", func(t *testing.T) {
		logs, err := GetLogByTokenId(11)
		require.NoError(t, err)
		check(t, logs, 1)
	})
	t.Run("user-export", func(t *testing.T) {
		var logs []*Log
		err := StreamUserLogs(context.Background(), 1, LogFilter{}, func(log *Log) error {
			copy := *log
			logs = append(logs, &copy)
			return nil
		})
		require.NoError(t, err)
		check(t, logs, 1)
		assert.Equal(t, "local-malformed", logs[0].RequestId, "descending source order is preserved")
	})
	t.Run("privileged-views-preserved", func(t *testing.T) {
		for _, role := range []string{"admin", "root"} {
			t.Run(role, func(t *testing.T) {
				log := before[0]
				log.ChannelName = "resolved-private-channel"
				if role == "admin" {
					FormatAdminLogs([]*Log{&log})
					assert.NotContains(t, log.Other, "private-root")
				} else {
					FormatRootLogs([]*Log{&log})
					assert.Contains(t, log.Other, "private-root")
				}
				assert.Equal(t, raw, log.Content)
				assert.Equal(t, 909, log.ChannelId)
				assert.Equal(t, "resolved-private-channel", log.ChannelName)
				assert.Equal(t, "private-upstream", log.UpstreamRequestId)
				assert.Contains(t, log.Other, "private-admin")
			})
		}
	})
	var after []Log
	require.NoError(t, db.Order("id").Find(&after).Error)
	assert.Equal(t, before, after, "read projections never rewrite stored diagnostics")
}
