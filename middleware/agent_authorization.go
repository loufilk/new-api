package middleware

import (
	"mime"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Legacy role 5 no longer limits personal services. Agent business routes are
// checked separately against the current Profile, never a cached role number.
func enforceAgentDashboardRoute(_ *gin.Context, _ int, _ bool) bool { return true }

func enforceAgentBusinessAccess(c *gin.Context, user *model.UserBase, identity service.AuthIdentity, accessToken bool) bool {
	if accessToken || identity.SessionID == "" {
		abortWithWebMessage(c, http.StatusForbidden, "AGENT_BROWSER_SESSION_REQUIRED", common.NewMessage("A browser sign-in is required for agent operations"))
		return false
	}
	if user.Role == common.RoleAdminUser || user.Role == common.RoleRootUser {
		return true
	}
	profile, err := model.GetAgentProfile(user.Id)
	if err != nil || !profile.Allows(model.AgentCapabilityHistory) {
		abortWithWebMessage(c, http.StatusForbidden, "AGENT_PERMISSION_DENIED", common.NewMessage("Agent profile permission denied"))
		return false
	}
	return true
}

func AgentRedemptionAuth() func(c *gin.Context) {
	return func(c *gin.Context) { authHelper(c, common.RoleAgentUser) }
}

// AgentBrowserSessionRequired is used after UserAuth/AdminAuth. Financial
// control-plane routes never accept PATs, model keys, or a cookie-only request.
// Non-simple authenticated headers are the primary CSRF barrier; origin and
// Fetch Metadata checks provide additional protection without trusting Host
// forwarding headers or disabling the guard in insecure-cookie mode.
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
