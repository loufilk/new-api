package console

import (
	"net/http"

	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// GetAuthSession exposes only the browser identity needed by the marketing UI.
// SessionAuth validates the existing cookie without issuing or rotating tokens;
// PAT and bearer credentials deliberately fail this check.
func GetAuthSession(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "AUTH_SESSION_REQUIRED",
			"message": "a dashboard login session is required",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user": gin.H{"id": identity.UserID, "username": c.GetString("username")},
		},
	})
}
