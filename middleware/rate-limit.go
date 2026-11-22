package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

const redisRateLimitNamespace = "rateLimit:v2"

// Redis rate limiting intentionally uses a fixed window. The single Lua script
// makes increment, expiry, and the limit decision atomic, while retaining the
// simple fixed-window behavior: traffic at a window boundary can burst up to
// twice the configured limit. Do not replace this with a sliding-window ZSET
// unless that externally visible behavior is intentionally changed.
const redisFixedWindowScript = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
  ttl = redis.call('TTL', KEYS[1])
end
if count > tonumber(ARGV[1]) then
  return {0, count, ttl}
end
return {1, count, ttl}
`

var inMemoryRateLimiter common.InMemoryRateLimiter

var defNext = func(c *gin.Context) {
	c.Next()
}

func redisIPRateLimitKey(mark string, clientIP string) string {
	return fmt.Sprintf("%s:ip:%s:%s", redisRateLimitNamespace, mark, clientIP)
}

func redisUserRateLimitKey(mark string, userID int) string {
	return fmt.Sprintf("%s:user:%s:%d", redisRateLimitNamespace, mark, userID)
}

func redisReplyInteger(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer reply type %T", value)
	}
}

func redisFixedWindowTake(ctx context.Context, key string, maxRequestNum int, duration int64) (bool, int64, int64, error) {
	if common.RDB == nil {
		return false, 0, 0, errors.New("Redis client is not initialized")
	}
	if key == "" {
		return false, 0, 0, errors.New("rate limit key is empty")
	}
	if maxRequestNum <= 0 {
		return false, 0, 0, errors.New("rate limit maximum must be positive")
	}
	if duration <= 0 {
		return false, 0, 0, errors.New("rate limit duration must be positive")
	}

	values, err := common.RDB.Eval(
		ctx,
		redisFixedWindowScript,
		[]string{key},
		maxRequestNum,
		duration,
	).Slice()
	if err != nil {
		return false, 0, 0, err
	}
	if len(values) != 3 {
		return false, 0, 0, fmt.Errorf("unexpected Redis rate limit reply length %d", len(values))
	}

	allowedValue, err := redisReplyInteger(values[0])
	if err != nil {
		return false, 0, 0, err
	}
	count, err := redisReplyInteger(values[1])
	if err != nil {
		return false, 0, 0, err
	}
	ttlSeconds, err := redisReplyInteger(values[2])
	if err != nil {
		return false, 0, 0, err
	}

	return allowedValue == 1, count, ttlSeconds, nil
}

func redisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	allowed, _, ttlSeconds, err := redisFixedWindowTake(
		c.Request.Context(),
		redisIPRateLimitKey(mark, c.ClientIP()),
		maxRequestNum,
		duration,
	)
	if err != nil {
		logger.LogError(c.Request.Context(), common.LogText("rate limit check failed (mark=%s): %v", mark, err))
		c.Status(http.StatusInternalServerError)
		c.Abort()
		return
	}
	if !allowed {
		writeRateLimited(c, ttlSeconds)
	}
}

func memoryRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	key := mark + c.ClientIP()
	if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
		writeRateLimited(c, duration)
		return
	}
}

// writeRateLimited rejects the request with 429 and a Retry-After hint so
// clients can back off instead of treating the rejection as a fatal error.
// The in-memory limiter cannot report the remaining window, so callers
// without a TTL pass the full window duration as a conservative upper bound.
func writeRateLimited(c *gin.Context, retryAfterSeconds int64) {
	if retryAfterSeconds > 0 {
		c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
	}
	c.Status(http.StatusTooManyRequests)
	c.Abort()
}

func rateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			redisRateLimiter(c, maxRequestNum, duration, mark)
		}
	}
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		memoryRateLimiter(c, maxRequestNum, duration, mark)
	}
}

// GlobalWebRateLimit limits dashboard pages and other fallback paths per IP.
// Files that exist in the frontend build count against a separate static
// limiter instead, so lazy-loaded chunks and icons do not use up the page
// budget. The static limiter is disabled by default.
func GlobalWebRateLimit(frontendFS static.ServeFileSystem) func(c *gin.Context) {
	webLimit := defNext
	if common.GlobalWebRateLimitEnable {
		webLimit = rateLimitFactory(common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration, "GW")
	}
	staticLimit := defNext
	if common.GlobalStaticRateLimitEnable {
		staticLimit = rateLimitFactory(common.GlobalStaticRateLimitNum, common.GlobalStaticRateLimitDuration, "GS")
	}
	return func(c *gin.Context) {
		if frontendFS.Exists("/", c.Request.URL.Path) {
			staticLimit(c)
			return
		}
		webLimit(c)
	}
}

func GlobalAPIRateLimit() func(c *gin.Context) {
	if common.GlobalApiRateLimitEnable {
		return rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "GA")
	}
	return defNext
}

// criticalRateLimitScopes maps a registered Gin route pattern to its isolated
// critical bucket. Keys are the Gin registration pattern (method + FullPath),
// never the request URL, so aliases and wildcard routes share the bucket their
// handlers were vetted against instead of each URL creating a fresh bucket.
// The mapping is the single source of truth for the host's critical auth flows:
// router/api-router.go declares these routes through CriticalRateLimit().
var criticalRateLimitScopes = map[string]string{
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

// criticalRateLimitScopeForRoute returns the isolated bucket named for a
// registered route, or an empty string when the route has no dedicated scope.
func criticalRateLimitScopeForRoute(method, fullPath string) string {
	return criticalRateLimitScopes[method+" "+fullPath]
}

// CriticalRateLimitRouteScopes returns a copy of the static route-to-bucket
// table so route coverage tests and diagnostics can assert that every scoped
// authentication route is actually registered.
func CriticalRateLimitRouteScopes() map[string]string {
	scopes := make(map[string]string, len(criticalRateLimitScopes))
	for route, scope := range criticalRateLimitScopes {
		scopes[route] = scope
	}
	return scopes
}

// CriticalRateLimit enforces the configured critical limit on every route,
// isolating unrelated critical flows while preserving the configured limit and
// duration. Routes in criticalRateLimitScopes resolve to their CT:<scope>
// bucket by registered method + FullPath; unknown routes keep the generic CT
// bucket. This prevents background session refreshes, logout, or OAuth traffic
// from exhausting the password-login bucket.
func CriticalRateLimit() func(c *gin.Context) {
	if !common.CriticalRateLimitEnable {
		return defNext
	}
	maxRequestNum, duration := common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	limiter := memoryRateLimiter
	if common.RedisEnabled {
		limiter = redisRateLimiter
	} else {
		inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	}
	return func(c *gin.Context) {
		mark := "CT"
		if scope := criticalRateLimitScopeForRoute(c.Request.Method, c.FullPath()); scope != "" {
			mark += ":" + scope
		}
		limiter(c, maxRequestNum, duration, mark)
	}
}

// CriticalRateLimitByScope keeps the explicit-scope API for callers outside the
// static route table: a non-empty scope isolates the route as CT:<scope>, while
// an empty scope intentionally keeps the generic CT bucket regardless of the
// route. Routes in the table should declare CriticalRateLimit() instead.
func CriticalRateLimitByScope(scope string) func(c *gin.Context) {
	if !common.CriticalRateLimitEnable {
		return defNext
	}
	if scope == "" {
		return rateLimitFactory(
			common.CriticalRateLimitNum,
			common.CriticalRateLimitDuration,
			"CT",
		)
	}
	return rateLimitFactory(
		common.CriticalRateLimitNum,
		common.CriticalRateLimitDuration,
		"CT:"+scope,
	)
}

func UserCriticalRateLimit(scope string) func(c *gin.Context) {
	if !common.CriticalRateLimitEnable {
		return defNext
	}
	return userRateLimitFactory(
		common.CriticalRateLimitNum,
		common.CriticalRateLimitDuration,
		"UC:"+scope,
	)
}

func DownloadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.DownloadRateLimitNum, common.DownloadRateLimitDuration, "DW")
}

func UploadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.UploadRateLimitNum, common.UploadRateLimitDuration, "UP")
}

// userRateLimitFactory creates a rate limiter keyed by authenticated user ID
// instead of client IP, making it resistant to proxy rotation attacks.
// Must be used AFTER authentication middleware (UserAuth).
func userRateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			userID := c.GetInt("id")
			if userID == 0 {
				c.Status(http.StatusUnauthorized)
				c.Abort()
				return
			}
			userRedisRateLimiter(c, maxRequestNum, duration, redisUserRateLimitKey(mark, userID))
		}
	}
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		userID := c.GetInt("id")
		if userID == 0 {
			c.Status(http.StatusUnauthorized)
			c.Abort()
			return
		}
		key := fmt.Sprintf("%s:user:%d", mark, userID)
		if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
			writeRateLimited(c, duration)
			return
		}
	}
}

// userRedisRateLimiter is like redisRateLimiter but accepts a pre-built key
// (to support user-ID-based keys).
func userRedisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, key string) {
	allowed, _, ttlSeconds, err := redisFixedWindowTake(c.Request.Context(), key, maxRequestNum, duration)
	if err != nil {
		logger.LogError(c.Request.Context(), common.LogText("rate limit check failed (key=%s): %v", key, err))
		c.Status(http.StatusInternalServerError)
		c.Abort()
		return
	}
	if !allowed {
		writeRateLimited(c, ttlSeconds)
	}
}

// SearchRateLimit returns a per-user rate limiter for search endpoints.
// Configurable via SEARCH_RATE_LIMIT_ENABLE / SEARCH_RATE_LIMIT / SEARCH_RATE_LIMIT_DURATION.
func SearchRateLimit() func(c *gin.Context) {
	if !common.SearchRateLimitEnable {
		return defNext
	}
	return userRateLimitFactory(common.SearchRateLimitNum, common.SearchRateLimitDuration, "SR")
}
