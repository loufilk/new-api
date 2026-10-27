package monitor

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// The dedicated database must belong to a disposable PostgreSQL instance.
// Never point this fixture at an existing application database.
func TestMonitorSettingsPostgresPersistenceAndAuthorization(t *testing.T) {
	dsn := os.Getenv("MONITOR_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an isolated PostgreSQL fixture via MONITOR_TEST_POSTGRES_DSN")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	var database string
	require.NoError(t, db.Raw("SELECT current_database()").Scan(&database).Error)
	require.Equal(t, "monitor_settings_test", database, "refusing an unrelated database")
	require.False(t, db.Migrator().HasTable(&model.Option{}), "fixture must start empty")
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.SetDatabaseTypes(previousMain, previousLog)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.UserAccessToken{}, &model.AuditLog{}))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	s := New(false)
	s.Register(engine.Group("/api/model-monitor"))
	s.RegisterSettings(engine.Group("/api/option", middleware.RootAuth()))
	request := func(method, path, token string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		return response
	}
	config := Config{Enabled: true, Title: "页面标题", Description: "Public reference", Groups: map[string]string{"route-" + strings.Repeat("a", 32): "自定义分组"}, HiddenGroups: map[string]bool{"route-" + strings.Repeat("a", 32): true}}
	body, err := common.Marshal(config)
	require.NoError(t, err)
	for _, path := range []string{"/api/option/model-monitor", "/api/option/model-monitor/targets"} {
		assert.Equal(t, http.StatusUnauthorized, request(http.MethodGet, path, "", nil).Code)
	}
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodPut, "/api/option/model-monitor", "", body).Code)
	var rootToken string
	var rootUser model.User
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		user := model.User{Username: strings.Repeat("u", role/10+1), Password: "unused-test-hash", Role: role, Status: common.UserStatusEnabled, Group: "default", AffCode: strings.Repeat("a", role/10+1)}
		require.NoError(t, db.Create(&user).Error)
		bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "monitor-test")
		require.NoError(t, err)
		if role == common.RoleRootUser {
			rootToken = bundle.AccessToken
			rootUser = user
			continue
		}
		assert.Equal(t, http.StatusForbidden, request(http.MethodGet, "/api/option/model-monitor", bundle.AccessToken, nil).Code)
		assert.Equal(t, http.StatusForbidden, request(http.MethodPut, "/api/option/model-monitor", bundle.AccessToken, body).Code)
	}
	// Browser cookies alone never authenticate these bearer-token endpoints.
	cookieReq := httptest.NewRequest(http.MethodPut, "/api/option/model-monitor", bytes.NewReader(body))
	cookieReq.Header.Set("Cookie", service.RefreshCookieName+"=untrusted-cookie")
	cookieReq.Header.Set("Origin", "https://untrusted.example")
	cookieResponse := httptest.NewRecorder()
	engine.ServeHTTP(cookieResponse, cookieReq)
	assert.Equal(t, http.StatusUnauthorized, cookieResponse.Code)
	for _, scope := range []string{"option:read", "option:write"} {
		raw := model.AccessTokenPrefix + scope + "-monitor-fixture"
		token := model.UserAccessToken{UserId: rootUser.Id, Name: "monitor-fixture", TokenHash: model.AccessTokenFingerprint(raw), ExpiresAt: common.GetTimestamp() + 3600}
		require.NoError(t, token.SetScopes([]string{scope}))
		require.NoError(t, db.Create(&token).Error)
		response := request(http.MethodPut, "/api/option/model-monitor", raw, body)
		if scope == "option:read" {
			assert.Equal(t, http.StatusForbidden, response.Code)
		} else {
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		}
	}
	response := request(http.MethodPut, "/api/option/model-monitor", rootToken, body)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var persisted model.Option
	require.NoError(t, db.Where(&model.Option{Key: settingsOption}).First(&persisted).Error)
	assert.JSONEq(t, string(body), persisted.Value)
	// Simulate application startup loading the existing options table.
	model.InitOptionMap()
	loaded, err := readConfig()
	require.NoError(t, err)
	assert.Equal(t, config, loaded)
	model.InitOptionMap()
	loaded, err = readConfig()
	require.NoError(t, err)
	assert.Equal(t, config, loaded)
	for _, invalid := range []string{`{}`, `{"enabled":true,"title":"x","description":"x","groups":{"route-1":"bad id"}}`, strings.Repeat("x", (64<<10)+1)} {
		assert.Equal(t, http.StatusBadRequest, request(http.MethodPut, "/api/option/model-monitor", rootToken, []byte(invalid)).Code)
	}
	loaded, err = readConfig()
	require.NoError(t, err)
	assert.Equal(t, config, loaded)
	var writers sync.WaitGroup
	for _, title := range []string{"first", "second"} {
		writers.Go(func() {
			changed := config
			changed.Title = title
			encoded, err := common.Marshal(changed)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, request(http.MethodPut, "/api/option/model-monitor", rootToken, encoded).Code)
		})
	}
	writers.Wait()
	require.NoError(t, db.Where(&model.Option{Key: settingsOption}).First(&persisted).Error)
	loaded, err = readConfig()
	require.NoError(t, err)
	encoded, err := common.Marshal(loaded)
	require.NoError(t, err)
	assert.JSONEq(t, persisted.Value, string(encoded))
	// A failed database write must not publish unsaved configuration in memory.
	require.NoError(t, db.Migrator().DropTable(&model.Option{}))
	assert.Equal(t, http.StatusInternalServerError, request(http.MethodPut, "/api/option/model-monitor", rootToken, body).Code)
	afterFailure, err := readConfig()
	require.NoError(t, err)
	assert.Equal(t, loaded, afterFailure)
	// Keep a disposable persistent copy for a second process to verify later.
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, db.Create(&persisted).Error)
}
