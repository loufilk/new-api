package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestGroupModelDistributorPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an isolated TEST_POSTGRES_DSN")
	}
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
	oldAuto := setting.AutoGroups2JsonString()
	oldUsable := setting.UserUsableGroups2JSONString()
	oldRatios := ratio_setting.GroupRatio2JSONString()
	model.DB, common.MemoryCacheEnabled = tx, true
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled = oldDB, oldMemory
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(oldAuto))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios))
		require.NoError(t, tx.Rollback().Error)
		require.NoError(t, sqlDB.Close())
		if oldMemory && oldDB != nil && oldDB.Migrator().HasTable(&model.Channel{}) && oldDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
	})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["selected","fallback"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"selected":"Selected","fallback":"Fallback"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"selected":1,"fallback":1}`))
	require.NoError(t, tx.Exec("SET LOCAL search_path = pg_temp").Error)
	require.NoError(t, tx.Exec(`CREATE TEMPORARY TABLE abilities (
		"group" varchar(64) NOT NULL, model varchar(255) NOT NULL, channel_id integer NOT NULL,
		enabled boolean, priority bigint, weight bigint, tag varchar(255),
		PRIMARY KEY ("group", model, channel_id)
	) ON COMMIT DROP`).Error)
	require.NoError(t, tx.Create(&[]model.Ability{
		{Group: "selected", Model: "disabled-model", ChannelId: 1, Enabled: false},
		{Group: "fallback", Model: "fallback-model", ChannelId: 2, Enabled: false},
		{Group: "selected", Model: "qwen3-max", ChannelId: 3, Enabled: false},
	}).Error)

	// Explicitly publish an empty routing snapshot: all configured abilities
	// above are disabled, but the error probe must still see their configuration.
	require.NoError(t, tx.Exec(`CREATE TEMPORARY TABLE channels (
		id integer, type integer, models text, model_mapping text, status integer
	) ON COMMIT DROP`).Error)
	require.NoError(t, model.RefreshChannelCache(context.Background()))

	for _, tc := range []struct {
		name, group, model, path string
		scope                    []string
		status                   int
		code                     string
	}{
		{name: "unsupported group and model", group: "selected", model: "missing-model", path: "/v1/chat/completions", status: http.StatusNotFound, code: "model_not_available_in_group"},
		{name: "anthropic unsupported model", group: "selected", model: "missing-model", path: "/v1/messages", status: http.StatusNotFound, code: "model_not_available_in_group"},
		{name: "disabled is unavailable", group: "selected", model: "disabled-model", path: "/v1/chat/completions", status: http.StatusServiceUnavailable, code: "model_not_found"},
		{name: "modifier is configured", group: "selected", model: "qwen3-max@thinking:on", path: "/v1/chat/completions", status: http.StatusServiceUnavailable, code: "model_not_found"},
		{name: "auto fallback is configured", group: "auto", model: "fallback-model", path: "/v1/chat/completions", status: http.StatusServiceUnavailable, code: "model_not_found"},
		{name: "scoped auto excludes global fallback", group: "auto", model: "fallback-model", scope: []string{"selected"}, path: "/v1/chat/completions", status: http.StatusNotFound, code: "model_not_available_in_group"},
		{name: "empty scoped auto stays unavailable", group: "auto", model: "missing-model", scope: []string{}, path: "/v1/chat/completions", status: http.StatusServiceUnavailable, code: "model_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.POST(tc.path, RequestId(), func(c *gin.Context) {
				common.SetContextKey(c, constant.ContextKeyUsingGroup, tc.group)
				common.SetContextKey(c, constant.ContextKeyUserGroup, "selected")
				if tc.scope != nil {
					common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, tc.scope)
				}
			}, Distribute(), func(c *gin.Context) {
				t.Error("an unavailable route must not reach upstream or billing")
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"model":"`+tc.model+`"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept-Language", "en")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			var response struct {
				Type  string `json:"type"`
				Error struct {
					Code    string `json:"code"`
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, tc.code, response.Error.Code)
			if tc.status == http.StatusNotFound {
				assert.Contains(t, response.Error.Message, "/v1/models")
				assert.NotContains(t, response.Error.Message, tc.model)
				assert.NotContains(t, response.Error.Message, "fallback")
				if tc.path == "/v1/messages" {
					assert.Equal(t, "error", response.Type)
					assert.Equal(t, "not_found_error", response.Error.Type)
				}
			}
		})
	}
}
