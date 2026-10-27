package middleware

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

const accessLogPathSnapshotKey = "newapi_access_log_path_snapshot"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(redactTaskArtifactAccessQuery())
	server.Use(snapshotAccessLogPath())
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		var requestID string
		if param.Keys != nil {
			requestID, _ = param.Keys[common.RequestIdKey].(string)
		}
		tag, _ := param.Keys[RouteTagKey].(string)
		if tag == "" {
			tag = "web"
		}
		path, ok := param.Keys[accessLogPathSnapshotKey].(string)
		if !ok {
			// Never fall back to Gin's combined path/query display string.
			path = "[unavailable]"
		}
		return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			tag,
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			path,
		)
	}))
}

// snapshotAccessLogPath runs after artifact preprocessing but before the logger
// and downstream handlers. Gin's display path combines a decoded path and raw
// query, which cannot be safely split if the path itself contains a question mark.
func snapshotAccessLogPath() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.EscapedPath()
		cleanPath := c.Request.URL.Path
		rawQuery := c.Request.URL.RawQuery
		switch {
		case strings.HasPrefix(cleanPath, "/api/oauth/"), strings.HasPrefix(cleanPath, "/oauth/"),
			cleanPath == "/api/stripe/webhook", cleanPath == "/api/creem/webhook", cleanPath == "/api/waffo/webhook",
			strings.HasPrefix(cleanPath, "/api/waffo-pancake/webhook/"), cleanPath == "/api/user/epay/notify",
			cleanPath == "/api/subscription/epay/notify", cleanPath == "/api/subscription/epay/return":
			// Strip credentials from logs only, retaining the request for verification.
			rawQuery = ""
		}
		if rawQuery != "" {
			path += "?" + redactCredentialAccessLogQuery(rawQuery)
		}
		c.Set(accessLogPathSnapshotKey, path)
		c.Next()
	}
}

// redactCredentialAccessLogQuery sanitizes only an already-separated raw query.
// The original URL and RequestURI remain available to authentication and signature
// verification. Keep non-secret diagnostic query fields byte-for-byte.
func redactCredentialAccessLogQuery(rawQuery string) string {
	if _, err := url.ParseQuery(rawQuery); err != nil {
		// A malformed query cannot be classified reliably; never log it verbatim.
		return "[REDACTED]"
	}

	fields := strings.Split(rawQuery, "&")
	for i, field := range fields {
		name, _, _ := strings.Cut(field, "=")
		decodedName, err := url.QueryUnescape(name)
		if err != nil {
			return "[REDACTED]"
		}
		if relaycommon.IsSensitiveURLQueryKey(decodedName) {
			fields[i] = name + "=[REDACTED]"
		}
	}
	return strings.Join(fields, "&")
}
