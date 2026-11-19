package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// CustomerInvoiceSessionRequired is installed after UserAuth/RootAuth. Invoice
// APIs use the console's revocable session bearer, not cookies, API keys or PATs.
// Multipart uploads and bodyless cancellations get the same write-origin checks.
func CustomerInvoiceSessionRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := GetSessionAuthIdentity(c)
		_, pat := model.ParseAccessTokenSessionID(identity.SessionID)
		if !ok || pat || identity.UserID != c.GetInt("id") || c.GetBool("use_access_token") || c.GetHeader("Authorization") == "" {
			abortWithWebMessage(c, http.StatusForbidden, "AUTH_SESSION_REQUIRED", common.NewMessage(accessTokenSessionRequiredMsg))
			return
		}
		if c.Request.Method == http.MethodPost {
			if c.GetHeader("Sec-Fetch-Site") == "cross-site" {
				abortWithWebMessage(c, http.StatusForbidden, "AUTH_ORIGIN_FORBIDDEN", common.NewMessage("request origin is not allowed"))
				return
			}
			if len(c.Request.Header.Values("Origin")) != 0 {
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
