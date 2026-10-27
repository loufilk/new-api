// Package support contributes the ticket schema and authenticated routes without
// putting host implementation dependencies back into the composition root.
package support

import (
	"fmt"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const (
	ExtensionName = "chuanapi"
	ModelsName    = "support_models"
	RoutesName    = "support_routes"
)

func Models() []any {
	return []any{&model.SupportTicket{}, &model.SupportTicketMessage{}}
}

func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi support: registry must not be nil")
	}
	if err := reg.RegisterModelSet(extension.ModelSet{
		Extension: ExtensionName,
		Name:      ModelsName,
		Phase:     extension.PhaseAfterCoreModels,
		Models:    Models(),
	}); err != nil {
		return err
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension: ExtensionName,
		Name:      RoutesName,
		Priority:  extension.PriorityBeforePlugin,
		Register:  registerSupportRoutes,
	})
}

// registerSupportRoutes wires the ticket API into the host's shared request
// budget. These groups hang off the raw engine rather than the /api group in
// router.SetApiRouter, so they must opt into the same middleware the group would
// otherwise inherit: cache protection, the API route tag, and the configured
// global API rate limit. The per-user write limiter is attached after auth so it
// can key on the authenticated user instead of a rotatable client IP, and the
// create and reply actions use separate scopes so one cannot exhaust the other.
func registerSupportRoutes(engine *gin.Engine) error {
	user := engine.Group("/api/support/tickets")
	user.Use(middleware.DisableCache())
	user.Use(middleware.RouteTag("api"))
	user.Use(middleware.GlobalAPIRateLimit())
	user.Use(middleware.UserAuth())
	user.GET("", middleware.SearchRateLimit(), controller.ListSupportTickets)
	user.POST("", middleware.UserCriticalRateLimit("support-ticket-create"), controller.CreateSupportTicket)
	user.GET("/:id", controller.GetSupportTicket)
	user.POST("/:id/replies", middleware.UserCriticalRateLimit("support-ticket-reply"), controller.ReplySupportTicket)
	user.POST("/:id/read", controller.MarkSupportTicketRead)

	admin := engine.Group("/api/admin/support/tickets")
	admin.Use(middleware.DisableCache())
	admin.Use(middleware.RouteTag("api"))
	admin.Use(middleware.GlobalAPIRateLimit())
	admin.Use(middleware.AdminAuth())
	admin.GET("", middleware.SearchRateLimit(), controller.ListAdminSupportTickets)
	admin.GET("/:id", controller.GetAdminSupportTicket)
	admin.POST("/:id/replies", middleware.UserCriticalRateLimit("support-ticket-reply"), controller.ReplyAdminSupportTicket)
	admin.POST("/:id/read", controller.MarkAdminSupportTicketRead)
	admin.PUT("/:id/status", controller.UpdateAdminSupportTicketStatus)
	return nil
}
