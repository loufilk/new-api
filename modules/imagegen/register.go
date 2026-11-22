package imagegen

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/gin-gonic/gin"
)

const (
	ExtensionName = "imagegen"
	RoutesName    = "async_image_routes"
)

// Register contributes the generic asynchronous image task HTTP surface.
// The synchronous OpenAI-compatible image relay remains part of the generic
// relay pipeline; this module owns only the separately added async task API.
func Register(reg *extension.Registry, workers *lifecycle.Group) error {
	if reg == nil {
		return fmt.Errorf("imagegen: registry must not be nil")
	}
	if workers == nil {
		return fmt.Errorf("imagegen: runtime workers must not be nil")
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension: ExtensionName,
		Name:      RoutesName,
		Priority:  extension.PriorityBeforePlugin,
		Register:  func(engine *gin.Engine) error { return registerRoutes(engine, workers) },
	})
}

func registerRoutes(engine *gin.Engine, workers *lifecycle.Group) error {
	if engine == nil {
		return fmt.Errorf("imagegen: engine must not be nil")
	}
	handler := NewAsyncImageHandler(engine, workers)
	routes := engine.Group("/v1/images/tasks")
	routes.Use(middleware.RouteTag("relay"), middleware.SystemPerformanceCheck())
	routes.POST("/generations", middleware.TokenAuth(), handler.Submit)
	routes.POST("/edits", middleware.TokenAuth(), handler.Submit)
	// Result reads use the dashboard session rather than quota-bearing token auth;
	// exhausting token quota must not hide a completed image from its owner.
	routes.GET("/:id", middleware.UserAuth(), handler.Poll)
	return nil
}
