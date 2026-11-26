package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetStatusChatPresetsPreservesHistoricalOptionPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("requires an isolated PostgreSQL TEST_POSTGRES_DSN")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	require.NoError(t, db.AutoMigrate(&model.Option{}))

	oldDB, oldMain, oldLog := model.DB, common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory := common.RedisEnabled, common.MemoryCacheEnabled
	common.OptionMapRWMutex.Lock()
	oldOptionMap, oldChats := common.OptionMap, setting.Chats
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		model.DB = oldDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled = oldRedis, oldMemory
		common.OptionMapRWMutex.Lock()
		common.OptionMap, setting.Chats = oldOptionMap, oldChats
		common.OptionMapRWMutex.Unlock()
	})
	model.DB = db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false

	engine := gin.New()
	engine.GET("/api/status", GetStatus)
	readChatStatus := func() json.RawMessage {
		t.Helper()
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/status", nil))
		require.Equal(t, http.StatusOK, recorder.Code)
		var payload struct {
			Success bool                       `json:"success"`
			Data    map[string]json.RawMessage `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
		require.True(t, payload.Success)
		require.Contains(t, payload.Data, "chats")
		return payload.Data["chats"]
	}

	model.InitOptionMap()
	require.Equal(t, "[]", string(readChatStatus()))
	var count int64
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "Chats").Count(&count).Error)
	require.Zero(t, count, "initialization must not persist a default")

	// Preserve whitespace and escaped syntax byte-for-byte, not merely a
	// reserialized interpretation, across option hydration and /api/status.
	const raw = " [\n  { \"fixture\": \"https:\\/\\/example.invalid\\/connect\" }\n] "
	require.NoError(t, db.Create(&model.Option{Key: "Chats", Value: raw}).Error)
	require.NoError(t, model.LoadOptionsFromDatabase())
	require.JSONEq(t, `[{"fixture":"https://example.invalid/connect"}]`, string(readChatStatus()))
	common.OptionMapRWMutex.RLock()
	cached := common.OptionMap["Chats"]
	common.OptionMapRWMutex.RUnlock()
	require.Equal(t, raw, cached)
	var row model.Option
	require.NoError(t, db.Where("key = ?", "Chats").Take(&row).Error)
	require.Equal(t, raw, row.Value)
}
