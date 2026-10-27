package console

import (
	"fmt"
	"os"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/modules/chuanapi/console/monitor"
	"github.com/gin-gonic/gin"
)

const (
	ExtensionName    = "chuanapi"
	RoutesName       = "llms_routes"
	PublicRoutesName = "console_public_routes"
)

// Register contributes ChuanAPI-owned public console routes. The module owns
// both handler implementation and route wiring, so the composition root never
// imports the host controller package.
func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi console: registry must not be nil")
	}
	if err := reg.RegisterRouteSet(extension.RouteSet{
		Extension: ExtensionName,
		Name:      RoutesName,
		Priority:  extension.PriorityBeforePlugin,
		Register:  registerLlmsRoutes,
	}); err != nil {
		return err
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension:      ExtensionName,
		Name:           PublicRoutesName,
		Priority:       extension.PriorityBeforePlugin,
		RegisterScoped: registerPublicRoutes,
	})
}

func registerLlmsRoutes(engine *gin.Engine) error {
	engine.GET("/llms.txt", GetLlmsIndex)
	engine.HEAD("/llms.txt", GetLlmsIndex)
	engine.GET("/llms-full.txt", GetLlmsFull)
	engine.HEAD("/llms-full.txt", GetLlmsFull)
	return nil
}

// registerPublicRoutes attaches console-owned public endpoints to host-built
// route scopes so middleware ordering (cache protection, route tag, rate
// limiting, session cookie origin guard, session auth) stays host-owned and is
// never reconstructed inside the module.
func registerPublicRoutes(scopes extension.RouteScopes) error {
	if scopes.API == nil || scopes.PublicAPI == nil || scopes.BrowserSession == nil || scopes.RootOptions == nil {
		return fmt.Errorf("chuanapi console: required host API route scopes are unavailable")
	}
	scopes.BrowserSession.GET("/session", GetAuthSession)
	scopes.API.GET("/chuanapi/public-config", GetPublicConfig)
	scopes.API.HEAD("/chuanapi/public-config", GetPublicConfig)
	scopes.PublicAPI.GET("/status", GetPublicConfig)
	scopes.PublicAPI.HEAD("/status", GetPublicConfig)
	scopes.PublicAPI.GET("/pricing", GetPublicPricing)
	scopes.PublicAPI.HEAD("/pricing", GetPublicPricing)
	monitorService := monitor.New(os.Getenv("MODEL_MONITOR_ENABLED") != "false")
	monitorService.Register(scopes.API.Group("/model-monitor"))
	monitorService.RegisterSettings(scopes.RootOptions)
	return nil
}
