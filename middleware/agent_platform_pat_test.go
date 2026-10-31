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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Use real persisted credentials, not a forged authentication context. The
// router contract test separately checks that every registered agent route has
// exactly one declaration, so deleting a rule cannot silently reduce coverage.
func TestAgentPlatformRejectsScopedAndLegacyPATPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_AGENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", parsed.Hostname())
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserAccessToken{}, &model.AuditLog{}, &model.Option{}))
	oldDB, oldLogDB, oldRedis := model.DB, model.LOG_DB, common.RedisEnabled
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB, common.RedisEnabled = db, db, false
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled = oldDB, oldLogDB, oldRedis
		common.SetDatabaseTypes(oldMainType, oldLogType)
	})
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	gin.SetMode(gin.TestMode)
	legacy := "synthetic-agent-legacy-" + common.GetRandomString(8)
	user := createMiddlewarePATUser(t, "pat-"+common.GetRandomString(8), legacy)
	require.NoError(t, db.Model(user).Update("role", common.RoleRootUser).Error)
	scoped, _ := createMiddlewareScopedToken(t, user.Id, time.Now().Unix()+3600, "profile:read")

	// Both tokens must first succeed on an ordinary scoped endpoint. Otherwise
	// authentication failure could disguise a broken session-only boundary.
	profileRouter := gin.New()
	profileRouter.GET("/api/user/self", UserAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, credential := range []struct{ name, token string }{{"scoped", scoped}, {"legacy", legacy}} {
		require.Equal(t, http.StatusNoContent, middlewareBearerRequest(profileRouter, "/api/user/self", credential.token).Code, credential.name)
	}

	testedRoutes := 0
	for _, key := range AccessTokenRouteRuleKeys() {
		method, path, ok := strings.Cut(key, " ")
		if !ok || !strings.HasPrefix(path, "/api/agent-platform/") {
			continue
		}
		testedRoutes++
		for _, credential := range []struct{ name, token string }{{"scoped", scoped}, {"legacy", legacy}} {
			t.Run(key+"/"+credential.name, func(t *testing.T) {
				called := false
				router := gin.New()
				router.Handle(method, path, UserAuth(), AgentBrowserSessionRequired(), func(c *gin.Context) {
					called = true
					c.Status(http.StatusNoContent)
				})
				segments := strings.Split(path, "/")
				for i, segment := range segments {
					if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
						segments[i] = "1"
					}
				}
				request := httptest.NewRequest(method, strings.Join(segments, "/"), strings.NewReader("{}"))
				request.Header.Set("Authorization", "Bearer "+credential.token)
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
				assert.Contains(t, response.Body.String(), `"code":"AUTH_SESSION_REQUIRED"`, "the declared route rule, not a later guard, must reject the PAT")
				assert.False(t, called, "a PAT must never reach an agent financial handler")
			})
		}
	}
	require.Positive(t, testedRoutes, "the test must exercise registered agent route declarations")
	t.Logf("Verified %d agent-platform routes with both valid scoped and legacy PATs", testedRoutes)
}
