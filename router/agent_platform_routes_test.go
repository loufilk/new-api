package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentPlatformModuleMountsCompleteBrowserSurface(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	scopes := SetApiRouter(engine)
	require.NotNil(t, scopes.AgentPlatformRoutes)
	require.NoError(t, newChuanRouteRegistry(t).ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))
	routes := map[string]string{}
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = route.Handler
	}
	expected := []string{
		"GET /self", "GET /policy/current", "POST /operations/prepare", "GET /operations/:request_id",
		"GET /purchases", "POST /purchases/quotes", "POST /purchases", "POST /purchases/:id/evidence",
		"GET /fund-batches", "GET /fund-ledger", "GET /purchase-audit", "POST /procurement/self-use",
		"GET /purchase-refunds", "POST /purchase-refunds", "GET /deliveries", "POST /deliveries", "GET /deliveries/received", "POST /deliveries/claim", "POST /deliveries/cancel-code", "POST /deliveries/:id/confirm", "POST /deliveries/:id/cancel",
		"GET /commissions/summary", "GET /commissions/awards", "GET /commissions/ledger", "GET /commissions/debts", "GET /commissions/conversions", "POST /commissions/conversions/quote", "POST /commissions/conversions",
		"GET /commission-recipients", "POST /commission-recipients", "PUT /commission-recipients/:id",
		"GET /withdrawals", "POST /withdrawals/quote", "POST /withdrawals", "GET /withdrawals/:id",
		"GET /profiles", "GET /profiles/:id", "PATCH /profiles/:id",
		"GET /finance/policies", "POST /finance/policies", "GET /finance/purchases", "POST /finance/purchases/:id/verify", "POST /finance/purchases/:id/review",
		"GET /finance/fund-ledger", "GET /finance/purchase-audit", "GET /finance/fund-reclassifications", "POST /finance/fund-reclassifications", "POST /finance/fund-reclassifications/:id/confirm",
		"GET /finance/purchase-refunds", "POST /finance/purchase-refunds/:id/approve", "POST /finance/purchase-refunds/:id/submit", "POST /finance/purchase-refunds/:id/resolve", "GET /finance/funds/reconciliation",
		"GET /finance/withdrawals", "GET /finance/withdrawals/:id/instructions", "POST /finance/withdrawals/:id/approve", "POST /finance/withdrawals/:id/reject", "POST /finance/withdrawals/:id/start", "POST /finance/withdrawals/:id/resolve",
		"GET /finance/commissions/outbox", "GET /finance/commissions/refunds", "POST /finance/commissions/refunds", "POST /finance/commissions/reserve-release", "POST /finance/commissions/outbox/retry", "GET /finance/commissions/reconciliation",
	}
	for _, entry := range expected {
		parts := strings.SplitN(entry, " ", 2)
		path := parts[0] + " /api/agent-platform" + parts[1]
		assert.Contains(t, routes, path)
	}
}

func TestAgentPlatformEveryRouteRejectsAnonymousAndNeverCachesFinancialData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	scopes := extension.RouteScopes{Engine: engine, API: engine.Group("/api")}
	require.NoError(t, RegisterAgentPlatformRoutes(scopes))
	for _, route := range engine.Routes() {
		path := strings.ReplaceAll(strings.ReplaceAll(route.Path, ":id", "1"), ":request_id", "synthetic-request")
		request := httptest.NewRequest(route.Method, path, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		assert.Equal(t, http.StatusUnauthorized, response.Code, route.Method+" "+route.Path)
		assert.Contains(t, response.Header().Get("Cache-Control"), "no-store", route.Path)
	}
}

func TestAgentPlatformFailsClosedWithoutHostScope(t *testing.T) {
	assert.Error(t, RegisterAgentPlatformRoutes(extension.RouteScopes{}))
}
