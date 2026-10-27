package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleCORSAllowsSecurityProofWithoutTrustingOtherOrigins(t *testing.T) {
	t.Setenv("CONSOLE_CORS_ALLOW_ORIGINS", "https://console.example")
	engine := gin.New()
	engine.Use(CORS())
	engine.POST("/api/user/self", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, origin := range []string{"https://console.example", "https://untrusted.example"} {
		t.Run(origin, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, "/api/user/self", nil)
			request.Header.Set("Origin", origin)
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", "authorization,content-type,x-security-proof,x-auth-session")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, http.StatusNoContent, response.Code)
			if origin == "https://console.example" {
				assert.Equal(t, origin, response.Header().Get("Access-Control-Allow-Origin"))
				assert.Equal(t, "true", response.Header().Get("Access-Control-Allow-Credentials"))
				assert.Contains(t, strings.ToLower(response.Header().Get("Access-Control-Allow-Headers")), "x-security-proof")
				return
			}
			assert.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
		})
	}
}
