package channel_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupModelTestSwitchPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires owned PostgreSQL 18.6 TEST_POSTGRES_DSN")
	}
	require.Equal(t, "1", os.Getenv("NO_AGENT_FIXTURE_OWNED"))
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	var version string
	require.NoError(t, admin.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	schema := "model_test_switch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.ModelFingerprintSiteConfig{}))
	require.NoError(t, db.Create(&model.Option{Key: model.ModelTestEnabledKey, Value: "true"}).Error)
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		conn, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		conn, err = admin.DB()
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})
	return db
}

func TestModelTestSwitchPostgresFinalDispatchFailsClosedWithoutBlockingOrdinaryRelay(t *testing.T) {
	db := setupModelTestSwitchPostgres(t)
	service.InitHttpClient()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer upstream.Close()
	for _, state := range []string{"disabled", "missing", "settings-unavailable"} {
		t.Run(state, func(t *testing.T) {
			switch state {
			case "disabled":
				require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
			case "missing":
				require.NoError(t, db.Where("key = ?", model.ModelTestEnabledKey).Delete(&model.Option{}).Error)
			case "settings-unavailable":
				require.NoError(t, db.Migrator().RenameTable("options", "unavailable_options"))
				defer func() { require.NoError(t, db.Migrator().RenameTable("unavailable_options", "options")) }()
			}
			before := calls.Load()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
			common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, true)
			common.SetContextKey(c, constant.ContextKeyModelTestOutputLimit, 2048)
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, upstream.URL, bytes.NewBufferString(`{"max_tokens":2048}`))
			require.NoError(t, err)
			_, err = channel.DoRequest(c, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
			require.Error(t, err)
			assert.Equal(t, before, calls.Load())
			assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
			code := "model_tests_disabled"
			if state == "settings-unavailable" {
				code = "model_test_settings_unavailable"
			}
			assert.Equal(t, code, c.GetString("model_test_error_code"))
			ordinary, _ := gin.CreateTestContext(httptest.NewRecorder())
			ordinary.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req, err = http.NewRequest(http.MethodPost, upstream.URL, bytes.NewBufferString(`{"max_tokens":2048}`))
			require.NoError(t, err)
			response, err := channel.DoRequest(ordinary, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			assert.Equal(t, before+1, calls.Load(), "ordinary calls never depend on this option")
		})
	}
}
