package router

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnterpriseLaneStatusRootOnlyAndReadOnly(t *testing.T) {
	previousDB, previousLogDB, previousRedis, previousAPI := model.DB, model.LOG_DB, common.RedisEnabled, common.GlobalApiRateLimitEnable
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.GlobalApiRateLimitEnable = false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.GlobalApiRateLimitEnable = previousRedis, previousAPI
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserAccessToken{}, &model.AuditLog{}))
	tokens := map[string]string{}
	for label, role := range map[string]int{"customer": common.RoleCommonUser, "admin": common.RoleAdminUser, "root": common.RoleRootUser, "root-wrong-scope": common.RoleRootUser} {
		u := model.User{Username: "lane-" + label, Password: "synthetic", Role: role, Status: common.UserStatusEnabled, AffCode: "aff-" + label}
		require.NoError(t, db.Create(&u).Error)
		token := model.AccessTokenPrefix + "lane-status-test-" + label
		scope := "ops:read"
		if label == "root-wrong-scope" {
			scope = "option:read"
		}
		seedRouteScopedToken(t, db, u.Id, token, scope)
		tokens[label] = token
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	invoke := func(method, label string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/performance/lane_status", nil)
		if token := tokens[label]; token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		result := httptest.NewRecorder()
		engine.ServeHTTP(result, req)
		return result
	}
	for label, status := range map[string]int{"": 401, "customer": 403, "admin": 403, "root-wrong-scope": 403, "root": 200} {
		got := invoke(http.MethodGet, label)
		require.Equal(t, status, got.Code, "%s %s", label, got.Body.String())
		require.Contains(t, got.Header().Get("Cache-Control"), "no-store")
		if status == 200 {
			require.JSONEq(t, fmt.Sprintf(`{"success":true,"message":"","data":{"build_profile":%q,"gateway_enabled":false,"runtime_capable":false,"observation_scope":"current_instance"}}`, middleware.EnterpriseLaneBuildProfile()), got.Body.String())
		} else {
			require.NotContains(t, got.Body.String(), "build_profile")
		}
	}
	require.Equal(t, http.StatusNotFound, invoke(http.MethodPost, "root").Code)
}
