package chuanapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/extension"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLlmsRouteSetPreservesLegacyRouteTableAndHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	require.NoError(t, reg.Freeze())

	engine := gin.New()
	api := engine.Group("/api")
	scopes := extension.RouteScopes{
		Engine:                    engine,
		API:                       api,
		UserSelf:                  api.Group("/user"),
		SubscriptionUser:          api.Group("/subscription"),
		RootOptions:               api.Group("/option"),
		AdminModels:               api.Group("/models"),
		PublicAPI:                 engine.Group("/public-api"),
		BrowserSession:            engine.Group("/api/user/auth"),
		AnonymousRequestBodyLimit: func(c *gin.Context) { c.Next() },
		// Provider route bodies are host-owned again; the payment route set
		// only forwards to this registrar. Real URL/handler coverage lives in
		// router/payment_routes_test.go against the production registrar.
		PaymentRoutes: func(extension.RouteScopes) error { return nil },
	}
	require.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))

	handlers := make(map[string]string)
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		handlers[key] = route.Handler
	}
	for _, key := range []string{
		http.MethodGet + " /llms.txt",
		http.MethodGet + " /llms-full.txt",
		http.MethodHead + " /llms.txt",
		http.MethodHead + " /llms-full.txt",
		http.MethodGet + " /public-api/pricing",
		http.MethodHead + " /public-api/pricing",
		http.MethodGet + " /public-api/status",
		http.MethodGet + " /api/chuanapi/public-config",
		http.MethodGet + " /api/model-monitor",
		http.MethodGet + " /api/option/model-monitor",
		http.MethodPut + " /api/option/model-monitor",
		http.MethodGet + " /api/option/model-monitor/targets",
		http.MethodGet + " /api/model-monitor/results/:id",
		http.MethodGet + " /api/model-monitor/artworks/:id",
		http.MethodGet + " /api/model-monitor/thumbnails/:id",
		http.MethodGet + " /api/user/auth/session",
	} {
		assert.Contains(t, handlers, key)
	}
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /llms.txt"], "/console.GetLlmsIndex"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodHead+" /llms.txt"], "/console.GetLlmsIndex"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /llms-full.txt"], "/console.GetLlmsFull"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodHead+" /llms-full.txt"], "/console.GetLlmsFull"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /public-api/pricing"], "/console.GetPublicPricing"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /public-api/status"], "/console.GetPublicConfig"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /api/chuanapi/public-config"], "/console.GetPublicConfig"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /api/user/auth/session"], "/console.GetAuthSession"))
}
