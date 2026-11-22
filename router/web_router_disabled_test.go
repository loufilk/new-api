package router

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisabledWebAssetsServeJSONNotFoundAndKeepPluginDispatcher(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plugin := compileRouterPlugin(t, "disabled-ui-route", "1.0.0", `[
		{method: "GET", path: "/vendor/disabled-ui", type: "dynamic"}
	]`)
	outer, registry := newPluginRouterTest(t, []*jsplugin.LoadedPlugin{plugin}, nil)
	SetWebRouter(outer, WebAssets{Disabled: true}, (&pluginRouteDispatcher{registry: registry}).dispatch)

	page := performPluginRequest(outer, http.MethodGet, "/dashboard")
	require.Equal(t, http.StatusNotFound, page.Code)
	assert.Contains(t, page.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, page.Body.String(), "Invalid URL")
	assert.Contains(t, page.Header().Get("Cache-Control"), "no-store")

	api := performPluginRequest(outer, http.MethodPost, "/api/missing")
	require.Equal(t, http.StatusNotFound, api.Code)
	assert.Contains(t, api.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, api.Body.String(), "Invalid URL")

	extension := performPluginRequest(outer, http.MethodGet, "/vendor/disabled-ui")
	require.Equal(t, http.StatusOK, extension.Code)
	assert.Equal(t, "disabled-ui-route", extension.Body.String())
}

func TestDisabledWebAssetsTakePrecedenceOverFrontendBaseUrlRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useRedirectFrontend(t)
	engine := gin.New()
	require.NoError(t, setRouterWithRegistry(engine, WebAssets{Disabled: true}, newChuanRouteRegistry(t)))

	page := performPluginRequest(engine, http.MethodGet, "/dashboard")
	require.Equal(t, http.StatusNotFound, page.Code, "a disabled build must answer JSON 404 instead of redirecting")
	assert.Empty(t, page.Header().Get("Location"), "FRONTEND_BASE_URL must not redirect an api_only build")
	assert.Contains(t, page.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, page.Body.String(), "Invalid URL")

	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	// Extension and host routes keep their original registration order through
	// the same plugin dispatcher the redirect branch used.
	require.True(t, routes[http.MethodGet+" /llms.txt"])
	require.True(t, routes[http.MethodGet+" /llms-full.txt"])

	api := performPluginRequest(engine, http.MethodGet, "/api/status")
	require.Equal(t, http.StatusOK, api.Code)
}

func TestDisabledWebAssetsKeepRegisteredAPIRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("FRONTEND_BASE_URL", "")
	engine := gin.New()
	require.NoError(t, setRouterWithRegistry(engine, WebAssets{Disabled: true}, newChuanRouteRegistry(t)))

	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	require.True(t, routes[http.MethodGet+" /api/status"])
	require.True(t, routes[http.MethodGet+" /llms.txt"])
	require.True(t, routes[http.MethodGet+" /llms-full.txt"])

	page := performPluginRequest(engine, http.MethodGet, "/dashboard")
	require.Equal(t, http.StatusNotFound, page.Code)
	assert.Contains(t, page.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, page.Body.String(), "Invalid URL")

	status := performPluginRequest(engine, http.MethodGet, "/api/status")
	require.Equal(t, http.StatusOK, status.Code)
}
