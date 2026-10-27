package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPaymentRoutesPreserveHostOwnedURLAndHandlerContract pins the externally
// visible payment route table after provider bodies returned to the host. The
// ChuanAPI payment route descriptor still contributes these routes through the
// host registrar, and each URL is served by the host controller handler.
func TestPaymentRoutesPreserveHostOwnedURLAndHandlerContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	reg := newChuanRouteRegistry(t)
	scopes := SetApiRouter(engine)
	require.NotNil(t, scopes.PaymentRoutes, "host router must install the payment route registrar")
	require.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))

	handlers := map[string]string{}
	for _, route := range engine.Routes() {
		handlers[route.Method+" "+route.Path] = route.Handler
	}

	expected := map[string]string{
		"POST /api/waffo/webhook":                                    "/controller.WaffoWebhook",
		"POST /api/waffo-pancake/webhook/:env":                       "/controller.WaffoPancakeWebhook",
		"POST /api/user/waffo/amount":                                "/controller.RequestWaffoAmount",
		"POST /api/user/waffo/pay":                                   "/controller.RequestWaffoPay",
		"POST /api/user/waffo-pancake/amount":                        "/controller.RequestWaffoPancakeAmount",
		"POST /api/user/waffo-pancake/pay":                           "/controller.RequestWaffoPancakePay",
		"POST /api/subscription/waffo-pancake/pay":                   "/controller.SubscriptionRequestWaffoPancakePay",
		"GET /api/option/waffo-pancake/catalog":                      "/controller.ListWaffoPancakeCatalog",
		"POST /api/option/waffo-pancake/pair":                        "/controller.CreateWaffoPancakePair",
		"POST /api/option/waffo-pancake/save":                        "/controller.SaveWaffoPancake",
		"POST /api/option/waffo-pancake/subscription-product":        "/controller.CreateWaffoPancakeSubscriptionProduct",
		"GET /api/option/waffo-pancake/subscription-product-options": "/controller.ListWaffoPancakeSubscriptionProductOptions",
	}
	for route, wantHandler := range expected {
		handler, ok := handlers[route]
		if !assert.True(t, ok, "payment route missing: %s", route) {
			continue
		}
		assert.True(t, strings.HasSuffix(handler, wantHandler),
			"route %s must be served by %s, got %s", route, wantHandler, handler)
	}
}

// TestPaymentRoutesKeepHostAccessPolicy proves the moved routes still sit
// behind the host-owned middleware scopes: user payment creation requires an
// authenticated session, while the anonymous webhook stays a public endpoint.
func TestPaymentRoutesKeepHostAccessPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalWaffoEnabled := setting.WaffoEnabled
	setting.WaffoEnabled = false
	t.Cleanup(func() { setting.WaffoEnabled = originalWaffoEnabled })

	engine := gin.New()
	reg := newChuanRouteRegistry(t)
	scopes := SetApiRouter(engine)
	require.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/user/waffo/pay", strings.NewReader(`{"amount":10}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code,
		"user payment creation must stay behind the host UserSelf auth scope")

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/waffo/webhook", strings.NewReader(`{}`))
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusForbidden, recorder.Code,
		"disabled provider webhook must stay anonymous and fail closed")
}
