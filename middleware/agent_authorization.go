package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// Match the registered route template, never a URL prefix or caller-supplied
// owner ID. New dashboard routes are denied to agents unless added explicitly.
func agentDashboardRouteAllowed(method, route string) bool {
	switch method + " " + route {
	case "GET /api/user/self", "GET /api/user/aff", "GET /api/user/auth/session",
		"GET /api/redemption/", "GET /api/redemption/search", "GET /api/redemption/:id",
		"POST /api/redemption/", "POST /api/redemption/batch", "PUT /api/redemption/",
		"DELETE /api/redemption/invalid", "DELETE /api/redemption/:id",
		"GET /api/agent-redemption/accounts", "GET /api/agent-redemption/accounts/:id/ledger",
		"GET /api/agent-redemption/invitations":
		return true
	default:
		return false
	}
}

func enforceAgentDashboardRoute(c *gin.Context, role int, accessToken bool) bool {
	if role != common.RoleAgentUser {
		return true
	}
	// Agent administration is a browser-session-only control plane. Existing
	// PATs from a previous role must not become an alternate privilege path.
	if accessToken || !agentDashboardRouteAllowed(c.Request.Method, c.FullPath()) {
		abortWithWebMessage(c, http.StatusForbidden, "AUTH_INSUFFICIENT_PRIVILEGE", common.NewMessage("Unauthorized, insufficient privileges"))
		return false
	}
	return true
}

func AgentRedemptionAuth() func(c *gin.Context) {
	return func(c *gin.Context) { authHelper(c, common.RoleAgentUser) }
}
