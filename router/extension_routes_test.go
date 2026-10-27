package router

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/chuanapi"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/pkg/jsplugin"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newChuanRouteRegistry(t *testing.T) *extension.Registry {
	t.Helper()
	reg := extension.NewRegistry()
	require.NoError(t, chuanapi.Register(reg))
	require.NoError(t, reg.Freeze())
	return reg
}

func useRedirectFrontend(t *testing.T) {
	t.Helper()
	t.Setenv("FRONTEND_BASE_URL", "https://frontend.example")
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
}

func TestSetRouterPreservesLlmsRoutesAndCORSContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useRedirectFrontend(t)
	engine := gin.New()
	require.NoError(t, setRouterWithRegistry(engine, WebAssets{BuildFS: embed.FS{}}, newChuanRouteRegistry(t)))

	t.Run("L1 route table", func(t *testing.T) {
		var got []string
		for _, route := range engine.Routes() {
			if route.Path == "/llms.txt" || route.Path == "/llms-full.txt" {
				got = append(got, route.Method+" "+route.Path)
			}
		}
		assert.Equal(t, []string{
			http.MethodGet + " /llms.txt",
			http.MethodGet + " /llms-full.txt",
			http.MethodHead + " /llms.txt",
			http.MethodHead + " /llms-full.txt",
		}, got)
	})

	t.Run("L2 global middleware position", func(t *testing.T) {
		// Baseline SetRouter installs CORS first, then SetRelayRouter appends
		// DecompressRequestMiddleware, BodyStorageCleanup and StatsMiddleware.
		// Registry expansion must not add, remove or reorder that global chain.
		require.Len(t, engine.Handlers, 4)
		fn := runtime.FuncForPC(reflect.ValueOf(engine.Handlers[0]).Pointer())
		require.NotNil(t, fn)
		assert.Contains(t, fn.Name(), "middleware.CORS")
	})

	t.Run("L3 CORS behavior", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodOptions, "/llms.txt", nil)
		request.Header.Set("Origin", "https://playground.example")
		request.Header.Set("Access-Control-Request-Method", http.MethodGet)
		engine.ServeHTTP(recorder, request)

		assert.Equal(t, http.StatusNoContent, recorder.Code)
		assert.Equal(t, "*", recorder.Header().Get("Access-Control-Allow-Origin"))
		assert.Contains(t, recorder.Header().Get("Access-Control-Allow-Methods"), http.MethodGet)
	})
}

// M7 intentionally changes the old snapshot boundary: /llms is now a declared
// host route before the plugin generation is built, so an intersecting plugin
// fails registry validation instead of being admitted and silently shadowed.
func TestLlmsRoutesAreIncludedInPluginStaticSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	reg := newChuanRouteRegistry(t)
	scopes := SetApiRouter(engine)
	require.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))

	plugin := compileRouterPlugin(t, "llms-shadow-equivalence", "1.0.0", `[
		{method: "GET", path: "/llms.txt", type: "dynamic"}
	]`)
	pluginRegistry := jsplugin.NewRegistry()
	require.NoError(t, pluginRegistry.ReplaceOverrides([]*jsplugin.LoadedPlugin{plugin}))
	builder := newPluginGenerationBuilder(engine.Routes(), nil, testPluginRouteHandlers(
		func(c *gin.Context, _ *jsplugin.RoutingGeneration, _ jsplugin.RouteBinding) {
			c.String(http.StatusOK, "plugin")
		},
	))
	require.NoError(t, pluginRegistry.SetGenerationPreparer(builder.prepare))
	routingErrors := pluginRegistry.RoutingErrors()
	require.NotEmpty(t, routingErrors, "intersecting plugin must be rejected by the host static-route snapshot")
	var routingErrorText strings.Builder
	for pluginKey, message := range routingErrors {
		routingErrorText.WriteString(pluginKey)
		routingErrorText.WriteByte(':')
		routingErrorText.WriteString(message)
		routingErrorText.WriteByte('\n')
	}
	assert.Contains(t, strings.ToLower(routingErrorText.String()), "llms.txt")

	var llmsRoutes []string
	for _, route := range engine.Routes() {
		if strings.HasPrefix(route.Path, "/llms") {
			llmsRoutes = append(llmsRoutes, route.Method+" "+route.Path)
		}
	}
	assert.Len(t, llmsRoutes, 4)
}
