package controller_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/router"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func probeDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestHealthAndReadinessRoutes(t *testing.T) {
	savedDB, savedLog, savedRedis := model.DB, model.LOG_DB, common.RDB
	savedEnabled, savedLimit, savedMode := common.RedisEnabled, common.GlobalApiRateLimitEnable, gin.Mode()
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RDB = savedDB, savedLog, savedRedis
		common.RedisEnabled, common.GlobalApiRateLimitEnable = savedEnabled, savedLimit
		gin.SetMode(savedMode)
	})
	gin.SetMode(gin.TestMode)
	common.GlobalApiRateLimitEnable = true
	for _, tc := range []struct {
		name   string
		path   string
		setup  func(*testing.T)
		status int
	}{
		{"liveness survives unavailable Redis limiter and databases", "/api/healthz", func(t *testing.T) {
			model.DB, model.LOG_DB, common.RDB = nil, nil, nil
			common.RedisEnabled = true
		}, http.StatusOK},
		{"uninitialized database is not ready", "/api/readyz", func(t *testing.T) { model.DB = nil }, http.StatusServiceUnavailable},
		{"healthy colocated database with Redis disabled is ready", "/api/readyz", func(t *testing.T) {}, http.StatusOK},
		{"healthy separate log database is ready", "/api/readyz", func(t *testing.T) { model.LOG_DB = probeDatabase(t) }, http.StatusOK},
		{"closed primary database is not ready", "/api/readyz", func(t *testing.T) {
			db, err := model.DB.DB()
			require.NoError(t, err)
			require.NoError(t, db.Close())
		}, http.StatusServiceUnavailable},
		{"closed separate log database is not ready", "/api/readyz", func(t *testing.T) {
			model.LOG_DB = probeDatabase(t)
			db, err := model.LOG_DB.DB()
			require.NoError(t, err)
			require.NoError(t, db.Close())
		}, http.StatusServiceUnavailable},
		{"enabled uninitialized Redis is not ready", "/api/readyz", func(t *testing.T) { common.RedisEnabled = true }, http.StatusServiceUnavailable},
		{"healthy Redis is ready", "/api/readyz", func(t *testing.T) {
			server := miniredis.RunT(t)
			common.RedisEnabled = true
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			common.RDB = client
			t.Cleanup(func() { _ = client.Close() })
		}, http.StatusOK},
		{"Redis error makes instance not ready", "/api/readyz", func(t *testing.T) {
			common.RedisEnabled = true
			common.RDB = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
			require.NoError(t, common.RDB.Close())
		}, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model.DB = probeDatabase(t)
			model.LOG_DB = model.DB
			common.RedisEnabled, common.RDB = false, nil
			tc.setup(t)
			engine := gin.New()
			router.SetApiRouter(engine)
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, httptest.NewRequest(method, tc.path, nil))
				assert.Equal(t, tc.status, response.Code)
				assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
				if method == http.MethodGet {
					var actual struct{ Success bool }
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &actual))
					assert.Equal(t, tc.status == http.StatusOK, actual.Success)
					assert.NotContains(t, response.Body.String(), "database")
				}
			}
		})
	}
}
