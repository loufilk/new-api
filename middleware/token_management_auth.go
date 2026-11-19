package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// TokenManagementAuth keeps key revocation and the masked list used to find the
// key available during a routing configuration outage. All branches retain
// dashboard authentication, live owner/role checks and the exact PAT route rule.
// The controller rejects any non-disable payload on the explicit disable-only path.
func TokenManagementAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		groupIndependent := false
		switch c.Request.Method + " " + c.FullPath() {
		case "GET /api/token/", "GET /api/token/search", "POST /api/token/search", "GET /api/token/:id",
			"DELETE /api/token/:id", "POST /api/token/batch":
			groupIndependent = true
		case http.MethodPut + " /api/token/":
			groupIndependent = c.Query("status_only") != "" && c.Query("disable_only") == "true"
		}
		authHelper(c, common.RoleCommonUser, !groupIndependent)
	}
}
