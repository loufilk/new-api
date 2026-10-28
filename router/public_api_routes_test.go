package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	consolemodule "github.com/QuantumNous/new-api/modules/chuanapi/console"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applyConsoleRoutes(t *testing.T, scopes extension.RouteScopes) {
	t.Helper()
	registry := extension.NewRegistry()
	require.NoError(t, consolemodule.Register(registry))
	require.NoError(t, registry.Freeze())
	require.NoError(t, registry.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))
}

// TestConsolePublicRoutesStayAnonymousAndKeepBrowserURLs protects the route
// contract: the marketing site keeps requesting the browser-visible URLs while
// the handlers live in the console extension, and /api/status no longer carries
// product bonus fields or authentication settings.
func TestConsolePublicRoutesStayAnonymousAndKeepBrowserURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	applyConsoleRoutes(t, SetApiRouter(engine))

	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, key := range []string{
		http.MethodGet + " /public-api/pricing",
		http.MethodHead + " /public-api/pricing",
		http.MethodGet + " /public-api/status",
		http.MethodHead + " /public-api/status",
		http.MethodGet + " /api/chuanapi/public-config",
		http.MethodHead + " /api/chuanapi/public-config",
		http.MethodGet + " /api/user/auth/session",
	} {
		require.True(t, routes[key], "route must stay registered: %s", key)
	}

	statusRecorder := httptest.NewRecorder()
	engine.ServeHTTP(statusRecorder, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	require.Equal(t, http.StatusOK, statusRecorder.Code)
	assert.NotContains(t, statusRecorder.Body.String(), "signup_bonus_enabled")
	assert.NotContains(t, statusRecorder.Body.String(), "referral_bonus")
	assert.NotContains(t, statusRecorder.Body.String(), "passkey_origins")

	for _, path := range []string{"/public-api/status", "/api/chuanapi/public-config"} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, recorder.Code, path)
		var payload struct {
			Success bool           `json:"success"`
			Data    map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload), path)
		require.True(t, payload.Success, path)
		require.Contains(t, payload.Data, "register_enabled", path)
		require.Contains(t, payload.Data, "quota_per_unit", path)
		assert.NotContains(t, recorder.Body.String(), "passkey_origins", path)
	}

	headRecorder := httptest.NewRecorder()
	engine.ServeHTTP(headRecorder, httptest.NewRequest(http.MethodHead, "/public-api/status", nil))
	assert.Equal(t, http.StatusOK, headRecorder.Code)
}
