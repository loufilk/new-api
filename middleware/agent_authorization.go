package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"mime"
	"net/http"
	"strings"
)

// A07 financial control-plane operations require a browser session and JSON writes.
func AgentBrowserSessionRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := GetSessionAuthIdentity(c)
		_, pat := model.ParseAccessTokenSessionID(identity.SessionID)
		if !ok || pat || c.GetBool("use_access_token") || strings.TrimSpace(c.GetHeader("Authorization")) == "" {
			abortWithWebMessage(c, http.StatusForbidden, "AGENT_BROWSER_SESSION_REQUIRED", common.NewMessage("A browser sign-in is required for agent operations"))
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions {
			mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
			if err != nil || mediaType != "application/json" {
				abortWithWebMessage(c, http.StatusUnsupportedMediaType, "AGENT_JSON_REQUIRED", common.NewMessage("A JSON request is required"))
				return
			}
			if c.GetHeader("Sec-Fetch-Site") == "cross-site" {
				abortWithWebMessage(c, http.StatusForbidden, "AUTH_ORIGIN_FORBIDDEN", common.NewMessage("request origin is not allowed"))
				return
			}
			if len(c.Request.Header.Values("Origin")) > 0 {
				origin, valid := requestBrowserOrigin(c.Request)
				if !valid || !isAllowedSessionOrigin(c.Request, origin) {
					abortWithWebMessage(c, http.StatusForbidden, "AUTH_ORIGIN_FORBIDDEN", common.NewMessage("request origin is not allowed"))
					return
				}
			}
		}
		c.Next()
	}
}
