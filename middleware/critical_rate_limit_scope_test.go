package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectedCriticalRateLimitScopes is the registered authentication flow
// contract: every entry must stay registered in router.SetApiRouter and keeps
// its dedicated bucket. Routes sharing a scope must keep sharing it.
var expectedCriticalRateLimitScopes = map[string]string{
	"GET /api/reset_password":                  "password-reset-request",
	"POST /api/user/reset":                     "password-reset-confirm",
	"POST /api/oauth/state":                    "oauth-state",
	"POST /api/oauth/email/bind/start":         "oauth-bind",
	"POST /api/oauth/email/bind/resend":        "oauth-bind",
	"POST /api/oauth/email/bind":               "oauth-bind",
	"GET /api/oauth/wechat":                    "oauth-login",
	"POST /api/oauth/wechat/bind":              "oauth-bind",
	"GET /api/oauth/telegram/login":            "oauth-login",
	"POST /api/oauth/telegram/bind/start":      "oauth-bind",
	"GET /api/oauth/telegram/bind/:flow_token": "oauth-bind",
	"GET /api/oauth/:provider":                 "oauth-login",
	"POST /api/user/auth/refresh":              "auth-refresh",
	"POST /api/user/auth/logout":               "auth-logout",
	"POST /api/user/register":                  "auth-register",
	"POST /api/user/login":                     "auth-password-login",
	"POST /api/user/login/2fa":                 "auth-2fa",
	"POST /api/user/login/verify":              "auth-2fa",
	"POST /api/user/login/passkey/begin":       "auth-passkey-begin",
	"POST /api/user/login/passkey/finish":      "auth-passkey-finish",
	"POST /api/user/passkey/login/begin":       "auth-passkey-begin",
	"POST /api/user/passkey/login/finish":      "auth-passkey-finish",
}

func TestCriticalRateLimitScopesMatchExpectedTable(t *testing.T) {
	require.Equal(t, expectedCriticalRateLimitScopes, criticalRateLimitScopes)
	require.Equal(t, expectedCriticalRateLimitScopes, CriticalRateLimitRouteScopes())
}

func TestCriticalRateLimitAliasesShareOriginalBuckets(t *testing.T) {
	aliases := [][2]string{
		{"POST /api/oauth/email/bind/start", "POST /api/oauth/email/bind/resend"},
		{"POST /api/oauth/email/bind/start", "POST /api/oauth/email/bind"},
		{"POST /api/oauth/wechat/bind", "POST /api/oauth/telegram/bind/start"},
		{"GET /api/oauth/wechat", "GET /api/oauth/telegram/login"},
		{"GET /api/oauth/wechat", "GET /api/oauth/:provider"},
		{"POST /api/user/login/2fa", "POST /api/user/login/verify"},
		{"POST /api/user/login/passkey/begin", "POST /api/user/passkey/login/begin"},
		{"POST /api/user/login/passkey/finish", "POST /api/user/passkey/login/finish"},
	}
	for _, pair := range aliases {
		require.Equal(t, expectedCriticalRateLimitScopes[pair[0]], expectedCriticalRateLimitScopes[pair[1]],
			"%s and %s must share the original bucket", pair[0], pair[1])
	}
}

func okHandler(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

func performCriticalRequest(engine http.Handler, method, path, remoteAddr string) int {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	request.RemoteAddr = remoteAddr
	engine.ServeHTTP(recorder, request)
	return recorder.Code
}

func TestCriticalRateLimitResolvesFixedRouteBuckets(t *testing.T) {
	previousEnable, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	previousRedis := common.RedisEnabled
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 1, 60
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousEnable, previousNum, previousDuration
		common.RedisEnabled = previousRedis
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/user/login/2fa", CriticalRateLimit(), okHandler)
	engine.POST("/api/user/login/verify", CriticalRateLimit(), okHandler)
	engine.POST("/api/user/register", CriticalRateLimit(), okHandler)
	engine.POST("/api/user/login", CriticalRateLimit(), okHandler)
	engine.POST("/api/unmapped-critical-a", CriticalRateLimit(), okHandler)
	engine.POST("/api/unmapped-critical-b", CriticalRateLimit(), okHandler)
	engine.POST("/api/explicit-scope", CriticalRateLimitByScope("explicit"), okHandler)

	t.Run("routes mapping to the same scope share one bucket", func(t *testing.T) {
		ip := "198.51.100.11:12345"
		assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/user/login/2fa", ip))
		assert.Equal(t, http.StatusTooManyRequests, performCriticalRequest(engine, http.MethodPost, "/api/user/login/verify", ip))
	})

	t.Run("different scopes stay isolated", func(t *testing.T) {
		ip := "198.51.100.12:12345"
		assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/user/register", ip))
		assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/user/login", ip))
		assert.Equal(t, http.StatusTooManyRequests, performCriticalRequest(engine, http.MethodPost, "/api/user/login", ip))
		assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/explicit-scope", ip))
	})

	t.Run("unmapped routes share the generic bucket", func(t *testing.T) {
		ip := "198.51.100.13:12345"
		assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/unmapped-critical-a", ip))
		assert.Equal(t, http.StatusTooManyRequests, performCriticalRequest(engine, http.MethodPost, "/api/unmapped-critical-b", ip))
	})
}

func TestCriticalRateLimitRedisKeysKeepOriginalMarks(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	previousEnable, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 1, 60
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousEnable, previousNum, previousDuration
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/user/login/2fa", CriticalRateLimit(), okHandler)
	engine.POST("/api/user/login/verify", CriticalRateLimit(), okHandler)
	engine.POST("/api/unmapped-critical-redis", CriticalRateLimit(), okHandler)

	ip := "203.0.113.44"
	assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/user/login/2fa", ip+":12345"))
	assert.Equal(t, http.StatusTooManyRequests, performCriticalRequest(engine, http.MethodPost, "/api/user/login/verify", ip+":12345"))
	scopedKey := redisIPRateLimitKey("CT:auth-2fa", ip)
	assert.True(t, redisServer.Exists(scopedKey), "scoped bucket must keep the CT:<scope> key")
	assert.Equal(t, 60*time.Second, redisServer.TTL(scopedKey))

	genericIP := "203.0.113.45"
	assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/unmapped-critical-redis", genericIP+":12345"))
	genericKey := redisIPRateLimitKey("CT", genericIP)
	assert.True(t, redisServer.Exists(genericKey), "unknown routes must keep the generic CT key")
}

func TestCriticalRateLimitByScopeEmptyScopeUsesGenericBucketOnMappedRoute(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	previousEnable, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 1, 60
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousEnable, previousNum, previousDuration
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/user/register", CriticalRateLimitByScope(""), okHandler)
	engine.POST("/api/user/login", CriticalRateLimit(), okHandler)

	ip := "203.0.113.46"
	assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/user/register", ip+":12345"))
	assert.Equal(t, http.StatusTooManyRequests, performCriticalRequest(engine, http.MethodPost, "/api/user/register", ip+":12345"))
	assert.True(t, redisServer.Exists(redisIPRateLimitKey("CT", ip)),
		"an empty explicit scope must keep the generic CT key")
	assert.False(t, redisServer.Exists(redisIPRateLimitKey("CT:auth-register", ip)),
		"an empty explicit scope must not use the mapped auth-register bucket")

	assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/user/login", ip+":12345"))
	assert.Equal(t, http.StatusTooManyRequests, performCriticalRequest(engine, http.MethodPost, "/api/user/login", ip+":12345"))
	assert.True(t, redisServer.Exists(redisIPRateLimitKey("CT:auth-password-login", ip)),
		"the mapped route must keep its own CT:<scope> bucket")
}

func TestCriticalRateLimitDisabledLeavesRoutesUnlimited(t *testing.T) {
	previousEnable, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	previousRedis := common.RedisEnabled
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = false, 1, 60
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousEnable, previousNum, previousDuration
		common.RedisEnabled = previousRedis
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/user/login", CriticalRateLimit(), okHandler)
	engine.POST("/api/user/register", CriticalRateLimitByScope(""), okHandler)
	engine.POST("/api/explicit-disabled", CriticalRateLimitByScope("explicit"), okHandler)

	ip := "198.51.100.16:12345"
	for range 3 {
		assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/user/login", ip))
		assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/user/register", ip))
		assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/explicit-disabled", ip))
	}
}

func TestCriticalRateLimitByScopeExplicitArgumentStaysCompatible(t *testing.T) {
	previousEnable, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	previousRedis := common.RedisEnabled
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 1, 60
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousEnable, previousNum, previousDuration
		common.RedisEnabled = previousRedis
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/user/login", CriticalRateLimitByScope("legacy-explicit"), okHandler)

	ip := "198.51.100.14:12345"
	assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodPost, "/api/user/login", ip))
	assert.Equal(t, http.StatusTooManyRequests, performCriticalRequest(engine, http.MethodPost, "/api/user/login", ip))
}

func TestCriticalRateLimitInterpolatesFullPathNotRequestURL(t *testing.T) {
	previousEnable, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	previousRedis := common.RedisEnabled
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 1, 60
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousEnable, previousNum, previousDuration
		common.RedisEnabled = previousRedis
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/api/oauth/:provider", CriticalRateLimit(), okHandler)

	ip := "198.51.100.15:12345"
	assert.Equal(t, http.StatusNoContent, performCriticalRequest(engine, http.MethodGet, "/api/oauth/github", ip))
	assert.Equal(t, http.StatusTooManyRequests, performCriticalRequest(engine, http.MethodGet, "/api/oauth/discord", ip),
		"different concrete URLs of one registered pattern must share the oauth-login bucket")
}
