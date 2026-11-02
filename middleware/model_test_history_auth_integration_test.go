package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestModelTestHistoryAuthPostgresRetainsSessionSecurityWithoutGroupPolicy(t *testing.T) {
	require.Equal(t, "1", os.Getenv("NO_AGENT_FIXTURE_OWNED"))
	parsed, err := url.Parse(os.Getenv("TEST_POSTGRES_DSN"))
	require.NoError(t, err)
	require.Equal(t, "postgres", parsed.Scheme)
	require.Equal(t, "127.0.0.1", parsed.Hostname())
	admin, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	schema := "joint_history_auth_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, adminSQL.Close())
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	var version string
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}, &model.Option{}))
	oldDB, oldLog, oldRedis, oldMemory, oldSecret := model.DB, model.LOG_DB, common.RedisEnabled, common.MemoryCacheEnabled, common.SessionSecret
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.SessionSecret = "synthetic-joint-history-session-secret"
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled, common.MemoryCacheEnabled, common.SessionSecret = oldDB, oldLog, oldRedis, oldMemory, oldSecret
		common.SetDatabaseTypes(oldMain, oldLogType)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, i18n.Init())
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	legacy := "synthetic-joint-legacy-pat"
	user := &model.User{Username: "joint-history-owner", AffCode: "joint-history-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, Group: "old", AccessToken: &legacy}
	require.NoError(t, db.Create(user).Error)
	now := time.Now().Unix()
	session := &model.UserSession{SID: "synthetic-joint-history-session", UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "synthetic-joint-refresh-hash", LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600}
	require.NoError(t, model.CreateUserSession(session))
	identity := service.AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
	access, _, err := service.IssueAccessToken(identity)
	require.NoError(t, err)
	scoped, _ := createMiddlewareScopedToken(t, user.Id, 0, "profile:read")
	require.NoError(t, db.Create(&model.Option{Key: "GroupConfigVersion", Value: "malformed-primary-version"}).Error)
	routes := []struct{ method, pattern, path string }{
		{http.MethodGet, "/api/user/model-tests", "/api/user/model-tests"},
		{http.MethodGet, "/api/user/model-tests/runs/:id", "/api/user/model-tests/runs/synthetic-owned-run"},
		{http.MethodPost, "/api/user/model-tests/runs/:id/cancel", "/api/user/model-tests/runs/synthetic-owned-run/cancel"},
	}
	engine := gin.New()
	engine.GET("/api/user/self", UserAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, route := range routes {
		engine.Handle(route.method, route.pattern, ModelTestHistoryAuth(), func(c *gin.Context) {
			assert.Equal(t, user.Id, c.GetInt("id"))
			_, groupGuardPresent := c.Get(model.GroupConfigVersionContextKey)
			assert.False(t, groupGuardPresent)
			c.Status(http.StatusOK)
		})
	}
	perform := func(method, path, bearer string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, nil)
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response
	}
	guarded := perform(http.MethodGet, "/api/user/self", access)
	require.Equal(t, http.StatusServiceUnavailable, guarded.Code, guarded.Body.String())
	for _, route := range routes {
		for _, credential := range []struct {
			name, token string
			status      int
			code        string
		}{
			{"active-session", access, http.StatusOK, ""},
			{"missing", "", http.StatusUnauthorized, ""},
			{"tampered", tamperDashboardToken(access), http.StatusUnauthorized, ""},
			{"expired", issueExpiredDashboardAccessToken(t, identity), http.StatusUnauthorized, ""},
			{"scoped-pat", scoped, http.StatusForbidden, "AUTH_SESSION_REQUIRED"},
			{"legacy-pat", legacy, http.StatusForbidden, "AUTH_SESSION_REQUIRED"},
		} {
			t.Run(route.method+route.pattern+"/"+credential.name, func(t *testing.T) {
				response := perform(route.method, route.path, credential.token)
				assert.Equal(t, credential.status, response.Code, response.Body.String())
				if credential.code != "" {
					assert.Contains(t, response.Body.String(), credential.code)
				}
			})
		}
	}
	require.NoError(t, db.Model(user).Update("status", common.UserStatusDisabled).Error)
	for _, route := range routes {
		response := perform(route.method, route.path, access)
		assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	}
	require.NoError(t, db.Model(user).Update("status", common.UserStatusEnabled).Error)
	for _, route := range routes {
		response := perform(route.method, route.path, access)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	revoked, err := model.RevokeUserSession(user.Id, session.SID, "synthetic-joint-revocation")
	require.NoError(t, err)
	require.True(t, revoked)
	for _, route := range routes {
		response := perform(route.method, route.path, access)
		assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	}
}
