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
		assert.False(t, strings.HasPrefix(route.Path, "/api/agent-redemption"), "the retired budget API must not remain reachable: %s", route.Path)
	}
	expected := []string{
		"GET /simple/reminders/unread", "POST /simple/reminders/acknowledge",
		"GET /self", "GET /simple/balance", "GET /simple/codes", "POST /simple/codes", "POST /simple/codes/:id/cancel",
		"POST /simple/recharge", "POST /simple/payment/quote", "GET /simple/payment-methods", "POST /simple/payment", "GET /simple/payment-orders",
		"GET /simple/payment-fees", "POST /simple/payment-fees", "POST /simple/payment/refunds", "POST /simple/payment/withdrawals", "POST /simple/payment/transfer",
		"POST /simple/finance/status", "POST /simple/finance/adjust", "POST /simple/finance/credit-limit", "POST /simple/finance/repayment",
		"GET /simple/finance/agents", "GET /simple/finance/history/:kind", "POST /simple/finance/codes/:id/reveal", "POST /simple/finance/withdrawal", "POST /simple/finance/referral-transfer", "POST /simple/finance/exit",
	}
	for _, path := range []string{"POST /api/agent-platform/purchases", "POST /api/agent-platform/withdrawals", "POST /api/agent-platform/campaigns/claim", "POST /api/agent-platform/operations/prepare", "PATCH /api/agent-platform/profiles/:id"} {
		assert.NotContains(t, routes, path, "A07 does not expose the retired workflow")
	}
	for _, entry := range expected {
		parts := strings.SplitN(entry, " ", 2)
		path := parts[0] + " /api/agent-platform" + parts[1]
		assert.Contains(t, routes, path)
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/agent-redemption/accounts"},
		{http.MethodGet, "/api/agent-redemption/accounts/1/ledger"},
		{http.MethodPost, "/api/agent-redemption/allocations"},
		{http.MethodGet, "/api/agent-redemption/invitations"},
		{http.MethodGet, "/api/agent-redemption/invitation-policy"},
		{http.MethodPut, "/api/agent-redemption/invitation-policy"},
	} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(request.method, request.path, strings.NewReader(`{}`)))
		assert.Equal(t, http.StatusNotFound, response.Code, request.method+" "+request.path)
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
