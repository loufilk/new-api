package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPricingRoutesSeparatePublicCatalogFromDashboardAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	applyConsoleRoutes(t, SetApiRouter(engine))

	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	require.True(t, routes[http.MethodGet+" /public-api/pricing"])
	require.True(t, routes[http.MethodHead+" /public-api/pricing"])
	require.True(t, routes[http.MethodGet+" /api/pricing"])

	request := httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}
