package router

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine, assets WebAssets) error {
	return setRouterWithRegistry(router, assets, extension.Default())
}

// setRouterWithRegistry keeps the production composition path injectable for
// contract tests without mutating the process-wide write-once registry.
func setRouterWithRegistry(router *gin.Engine, assets WebAssets, registry *extension.Registry) error {
	router.Use(middleware.CORS())
	routeScopes := SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetTaskPluginProtocolRouter(router)
	SetVideoRouter(router)
	SetTaskRouter(router)
	pluginDispatcher, err := setPluginAndExtensionRoutes(routeScopes, registry)
	if err != nil {
		return err
	}
	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if common.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		common.SysLog(common.LogText("FRONTEND_BASE_URL is ignored on master node"))
	}
	// api_only builds disable the embedded dashboard. That decision precedes
	// the legacy frontend redirect: a disabled build must answer JSON 404 on
	// its own origin instead of bouncing visitors to FRONTEND_BASE_URL.
	if frontendBaseUrl == "" || assets.Disabled {
		SetWebRouter(router, assets, pluginDispatcher)
	} else {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
		router.NoRoute(
			pluginDispatcher,
			middleware.RouteTag("web"),
			middleware.AccessTokenAudit(),
			func(c *gin.Context) {
				c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
			},
		)
	}
	return nil
}

// setPluginAndExtensionRoutes owns the two registry expansion points around
// SetPluginRouter's static-route snapshot. BeforePlugin routes are visible to
// plugin conflict detection; AfterPlugin routes preserve the historical
// /llms.txt behavior and remain outside that snapshot.
func setPluginAndExtensionRoutes(scopes extension.RouteScopes, registry *extension.Registry) (gin.HandlerFunc, error) {
	if registry == nil {
		return nil, fmt.Errorf("router: extension registry must not be nil")
	}
	if scopes.Engine == nil {
		return nil, fmt.Errorf("router: extension route engine must not be nil")
	}
	if err := registry.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin); err != nil {
		return nil, err
	}
	pluginDispatcher := SetPluginRouter(scopes.Engine)
	if err := registry.ApplyRoutesWithScopes(scopes, extension.PriorityAfterPlugin); err != nil {
		return nil, err
	}
	return pluginDispatcher, nil
}
