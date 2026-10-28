package router

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuthSessionReadOnlyBrowserIdentity(t *testing.T) {
	previousDB, previousRedis, previousSecret := model.DB, common.RedisEnabled, common.SessionSecret
	previousSecure, previousTrusted := common.SessionCookieSecure, common.SessionCookieTrustedURLs
	previousLimiter := common.GlobalApiRateLimitEnable
	previousDatabaseType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	model.DB, common.RedisEnabled, common.SessionSecret = db, false, "session-probe-test-secret"
	common.SessionCookieSecure = true
	common.SessionCookieTrustedURLs = []string{"https://marketing.example"}
	common.GlobalApiRateLimitEnable = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, common.RedisEnabled, common.SessionSecret = previousDB, previousRedis, previousSecret
		common.SessionCookieSecure, common.SessionCookieTrustedURLs = previousSecure, previousTrusted
		common.GlobalApiRateLimitEnable = previousLimiter
		common.SetMainDatabaseType(previousDatabaseType)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	pat := "identity-probe-pat"
	user := &model.User{
		Username: "browser-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
		Email: "private@example.com", Quota: 123, AccessToken: &pat,
	}
	require.NoError(t, db.Create(user).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "probe-browser")
	require.NoError(t, err)
	session, err := model.GetUserSessionBySID(bundle.Session.SID)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	applyConsoleRoutes(t, SetApiRouter(engine))
	const endpoint = "https://console.example/api/user/auth/session"
	tests := []struct {
		name       string
		cookie     string
		bearer     string
		origin     string
		referer    string
		sessionSet map[string]any
		userSet    map[string]any
		wantStatus int
		wantCode   string
	}{
		{name: "same origin cookie", cookie: bundle.RefreshToken, referer: "https://console.example/", wantStatus: http.StatusOK},
		{name: "trusted origin cookie", cookie: bundle.RefreshToken, origin: "https://marketing.example", wantStatus: http.StatusOK},
		{name: "no cookie", referer: "https://console.example/", wantStatus: http.StatusUnauthorized, wantCode: "AUTH_UNAUTHORIZED"},
		{name: "bearer is not a cookie", bearer: bundle.AccessToken, referer: "https://console.example/", wantStatus: http.StatusUnauthorized, wantCode: "AUTH_UNAUTHORIZED"},
		{name: "PAT is not a cookie", bearer: pat, referer: "https://console.example/", wantStatus: http.StatusUnauthorized, wantCode: "AUTH_UNAUTHORIZED"},
		{name: "malformed cookie", cookie: "invalid", referer: "https://console.example/", wantStatus: http.StatusUnauthorized, wantCode: "AUTH_UNAUTHORIZED"},
		{name: "wrong refresh secret", cookie: bundle.Session.SID + ".wrong-secret", referer: "https://console.example/", wantStatus: http.StatusUnauthorized, wantCode: "AUTH_UNAUTHORIZED"},
		{name: "expired session", cookie: bundle.RefreshToken, referer: "https://console.example/", sessionSet: map[string]any{"expires_at": time.Now().Add(-time.Minute).Unix()}, wantStatus: http.StatusUnauthorized, wantCode: "AUTH_SESSION_REVOKED"},
		{name: "revoked session", cookie: bundle.RefreshToken, referer: "https://console.example/", sessionSet: map[string]any{"status": model.UserSessionStatusRevoked}, wantStatus: http.StatusUnauthorized, wantCode: "AUTH_SESSION_REVOKED"},
		{name: "disabled user", cookie: bundle.RefreshToken, referer: "https://console.example/", userSet: map[string]any{"status": common.UserStatusDisabled}, wantStatus: http.StatusUnauthorized, wantCode: "AUTH_SESSION_REVOKED"},
		{name: "security version changed", cookie: bundle.RefreshToken, referer: "https://console.example/", userSet: map[string]any{"auth_version": 2}, wantStatus: http.StatusUnauthorized, wantCode: "AUTH_SESSION_REVOKED"},
		{name: "foreign origin", cookie: bundle.RefreshToken, origin: "https://untrusted.example", wantStatus: http.StatusForbidden, wantCode: "AUTH_ORIGIN_FORBIDDEN"},
		{name: "missing browser origin", cookie: bundle.RefreshToken, wantStatus: http.StatusForbidden, wantCode: "AUTH_ORIGIN_FORBIDDEN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, db.Save(user).Error)
			require.NoError(t, db.Save(session).Error)
			if test.sessionSet != nil {
				require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", session.SID).Updates(test.sessionSet).Error)
			}
			if test.userSet != nil {
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Updates(test.userSet).Error)
			}
			before, err := model.GetUserSessionBySID(session.SID)
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodGet, endpoint, nil)
			request.Header.Set("Origin", test.origin)
			if test.origin == "" {
				request.Header.Del("Origin")
			}
			if test.referer != "" {
				request.Header.Set("Referer", test.referer)
			}
			if test.bearer != "" {
				request.Header.Set("Authorization", "Bearer "+test.bearer)
			}
			if test.cookie != "" {
				// Exercise the real refresh-cookie attributes and browser path matching.
				cookieResponse := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(cookieResponse)
				service.WriteRefreshCookie(c, test.cookie)
				jar, err := cookiejar.New(nil)
				require.NoError(t, err)
				jar.SetCookies(request.URL, cookieResponse.Result().Cookies())
				cookies := jar.Cookies(request.URL)
				for _, cookie := range cookies {
					request.AddCookie(cookie)
				}
				refreshCookie, err := request.Cookie(service.RefreshCookieName)
				require.NoError(t, err, "the browser must send the existing refresh cookie to this path")
				require.Equal(t, test.cookie, refreshCookie.Value)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			assert.Equal(t, test.wantStatus, response.Code)
			assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
			assert.Empty(t, response.Header().Values("Set-Cookie"), "a probe must not rotate or clear browser credentials")
			var body map[string]any
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
			if test.wantStatus == http.StatusOK {
				assert.Equal(t, map[string]any{"success": true, "data": map[string]any{"user": map[string]any{"id": float64(user.Id), "username": user.Username}}}, body)
			} else {
				assert.Equal(t, false, body["success"])
				assert.Equal(t, test.wantCode, body["code"])
				assert.NotContains(t, body, "data")
			}
			after, err := model.GetUserSessionBySID(session.SID)
			require.NoError(t, err)
			assert.Equal(t, before, after, "the probe must not renew lifetime, touch activity, rotate, or revoke the session")
		})
	}

	t.Run("backend failure is not cached", func(t *testing.T) {
		require.NoError(t, db.Save(user).Error)
		require.NoError(t, db.Save(session).Error)
		const callback = "test:session-probe-user-failure"
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "users" {
				tx.AddError(errors.New("forced user lookup failure"))
			}
		}))
		t.Cleanup(func() { db.Callback().Query().Remove(callback) })
		request := httptest.NewRequest(http.MethodGet, endpoint, nil)
		request.Header.Set("Referer", "https://console.example/")
		request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: bundle.RefreshToken})
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.Contains(t, response.Body.String(), "AUTH_INTERNAL_ERROR")
		assert.NotContains(t, response.Body.String(), "forced user lookup failure")
		assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
		assert.Empty(t, response.Header().Values("Set-Cookie"))
	})
}

func TestAuthSessionRateLimitIsNotCached(t *testing.T) {
	previousEnable, previousNum, previousDuration := common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
	previousRedis, previousSecure, previousRDB := common.RedisEnabled, common.SessionCookieSecure, common.RDB
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = true, 1, 60
	common.RedisEnabled, common.SessionCookieSecure, common.RDB = true, false, redisClient
	t.Cleanup(func() {
		common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = previousEnable, previousNum, previousDuration
		common.RedisEnabled, common.SessionCookieSecure, common.RDB = previousRedis, previousSecure, previousRDB
		require.NoError(t, redisClient.Close())
	})
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	applyConsoleRoutes(t, SetApiRouter(engine))
	for _, wantStatus := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodGet, "/api/user/auth/session", nil)
		request.RemoteAddr = "192.0.2.207:12345"
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		assert.Equal(t, wantStatus, response.Code)
		assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	}
	common.RDB = nil
	request := httptest.NewRequest(http.MethodGet, "/api/user/auth/session", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
}
