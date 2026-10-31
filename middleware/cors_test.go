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

func TestImageBillingReviewHeaderExposedWithoutCredentialExpansion(t *testing.T) {
	t.Setenv("CONSOLE_CORS_ALLOW_ORIGINS", "https://console.example")
	engine := gin.New()
	engine.Use(CORS())
	engine.POST("/v1/images/generations", func(c *gin.Context) {
		c.Header("X-New-Api-Billing-State", "review")
		c.Header("X-Internal-Test-Secret", "not exposed")
		c.JSON(http.StatusOK, gin.H{"data": []any{}})
	})
	for _, origin := range []string{"https://console.example", "https://untrusted.example"} {
		t.Run(origin, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			request.Header.Set("Origin", origin)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, "x-new-api-billing-state", strings.ToLower(response.Header().Get("Access-Control-Expose-Headers")))
			assert.NotContains(t, strings.ToLower(response.Header().Get("Access-Control-Expose-Headers")), "x-internal-test-secret")
			if origin == "https://console.example" {
				assert.Equal(t, origin, response.Header().Get("Access-Control-Allow-Origin"))
				assert.Equal(t, "true", response.Header().Get("Access-Control-Allow-Credentials"))
			} else {
				assert.Equal(t, "*", response.Header().Get("Access-Control-Allow-Origin"))
				assert.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
			}
		})
	}
}
