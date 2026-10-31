package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAgentDashboardAuthorizationPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_AGENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", parsed.Hostname())
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	var version string
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Token{}, &model.AuditLog{}, &model.Option{}, &model.AgentProfile{}))
	oldDB, oldLog, oldRedis, oldSecret := model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret = db, db, false, "synthetic-agent-session-test"
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret = oldDB, oldLog, oldRedis, oldSecret
		common.SetDatabaseTypes(oldMain, oldLogType)
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	gin.SetMode(gin.TestMode)
	user := model.User{Username: "agent-auth-" + common.GetRandomString(10), AffCode: common.GetRandomString(16), Role: common.RoleAgentUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.AgentProfile{UserID: user.Id, Status: model.AgentProfileActive, Version: 1}).Error)
	now := time.Now().Unix()
	session := model.UserSession{SID: "agent-" + common.GetRandomString(16), UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "synthetic", LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600}
	require.NoError(t, model.CreateUserSession(&session))
	token, _, err := service.IssueAccessToken(service.AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1})
	require.NoError(t, err)
	routes := []struct {
		method, path, url string
		middleware        gin.HandlerFunc
		status            int
	}{
		{"GET", "/api/user/self", "/api/user/self", UserAuth(), 200},
		{"GET", "/api/user/aff", "/api/user/aff", UserAuth(), 200},
		{"GET", "/api/redemption/", "/api/redemption/", AgentRedemptionAuth(), 200},
		{"GET", "/api/redemption/:id", "/api/redemption/123", AgentRedemptionAuth(), 200},
		{"POST", "/api/redemption/", "/api/redemption/", AgentRedemptionAuth(), 200},
		{"GET", "/api/agent-redemption/accounts", "/api/agent-redemption/accounts", AgentRedemptionAuth(), 200},
		{"GET", "/api/agent-redemption/accounts/:id/ledger", "/api/agent-redemption/accounts/123/ledger", AgentRedemptionAuth(), 200},
		{"POST", "/api/user/topup", "/api/user/topup", UserAuth(), 200},
		{"POST", "/api/user/access_tokens", "/api/user/access_tokens", UserAuth(), 200},
		{"GET", "/api/channel/", "/api/channel/", AdminAuth(), 403},
		{"POST", "/api/user/manage", "/api/user/manage", AdminAuth(), 403},
		{"POST", "/api/agent-redemption/allocations", "/api/agent-redemption/allocations", AdminAuth(), 403},
		{"PUT", "/api/agent-redemption/invitation-policy", "/api/agent-redemption/invitation-policy", RootAuth(), 403},
		{"GET", "/optional", "/optional", TryUserAuth(), 200},
		{"POST", "/v1/test", "/v1/test", TokenOrUserAuth(), 200},
	}
	for _, tc := range routes {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := gin.New()
			r.Handle(tc.method, tc.path, tc.middleware, func(c *gin.Context) { c.Status(200) })
			request := httptest.NewRequest(tc.method, tc.url, strings.NewReader("{}"))
			request.Header.Set("Authorization", "Bearer "+token)
			recorder := httptest.NewRecorder()
			r.ServeHTTP(recorder, request)
			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
	// A committed demotion/disable must invalidate even an otherwise unexpired JWT.
	_, err = model.BumpUserAuthVersion(user.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&user).Update("status", common.UserStatusDisabled).Error)
	r := gin.New()
	r.GET("/api/redemption/", AgentRedemptionAuth(), func(c *gin.Context) { c.Status(200) })
	request := httptest.NewRequest("GET", "/api/redemption/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	assert.NotEqual(t, 200, recorder.Code)
}

func TestAgentBrowserBoundaryRejectsPATAndCSRF(t *testing.T) {
	oldSecure, oldTrusted := common.SessionCookieSecure, common.SessionCookieTrustedURLs
	common.SessionCookieSecure = false
	common.SessionCookieTrustedURLs = nil
	t.Cleanup(func() { common.SessionCookieSecure, common.SessionCookieTrustedURLs = oldSecure, oldTrusted })
	for _, tc := range []struct {
		name, session, authorization, contentType, origin, fetch string
		pat                                                      bool
		status                                                   int
	}{
		{name: "browser JSON", session: "browser", authorization: "Bearer browser-token", contentType: "application/json", status: http.StatusNoContent},
		{name: "same-origin JSON", session: "browser", authorization: "Bearer browser-token", contentType: "application/json", origin: "https://panel.example.test", status: http.StatusNoContent},
		{name: "cookie only", session: "browser", contentType: "application/json", status: http.StatusForbidden},
		{name: "PAT identity", session: "pat:12", authorization: "Bearer scoped-pat", contentType: "application/json", status: http.StatusForbidden},
		{name: "PAT context", session: "browser", authorization: "Bearer scoped-pat", contentType: "application/json", pat: true, status: http.StatusForbidden},
		{name: "model key has no browser", authorization: "Bearer sk-model-key", contentType: "application/json", status: http.StatusForbidden},
		{name: "simple form content", session: "browser", authorization: "Bearer browser-token", contentType: "application/x-www-form-urlencoded", status: http.StatusUnsupportedMediaType},
		{name: "cross origin insecure-cookie mode", session: "browser", authorization: "Bearer browser-token", contentType: "application/json", origin: "https://evil.example.test", status: http.StatusForbidden},
		{name: "cross-site metadata", session: "browser", authorization: "Bearer browser-token", contentType: "application/json", fetch: "cross-site", status: http.StatusForbidden},
		{name: "null origin", session: "browser", authorization: "Bearer browser-token", contentType: "application/json", origin: "null", status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("id", 1)
				c.Set("session_id", tc.session)
				c.Set("auth_version", int64(1))
				c.Set("session_version", int64(1))
				c.Set("use_access_token", tc.pat)
				c.Next()
			})
			router.POST("/fund", AgentBrowserSessionRequired(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			request := httptest.NewRequest(http.MethodPost, "https://panel.example.test/fund", strings.NewReader("{}"))
			request.Header.Set("Authorization", tc.authorization)
			request.Header.Set("Content-Type", tc.contentType)
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			request.Header.Set("Sec-Fetch-Site", tc.fetch)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, tc.status, response.Code, response.Body.String())
		})
	}
}
