package partners

import (
	"fmt"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

const (
	ExtensionName = "chuanapi"
	ModelsName    = "partner_application_models"
	MigrationName = "partner_application_settings_default"
	RoutesName    = "partner_application_routes"
)

func Models() []any {
	return []any{&model.PartnerApplicationSettings{}, &model.PartnerApplication{}, &model.PartnerApplicationEvent{}}
}

func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi partners: registry must not be nil")
	}
	if err := reg.RegisterModelSet(extension.ModelSet{
		Extension: ExtensionName,
		Name:      ModelsName,
		Phase:     extension.PhaseAfterCoreModels,
		Models:    Models(),
	}); err != nil {
		return err
	}
	if err := reg.RegisterDataStep(extension.DataStep{
		Extension: ExtensionName,
		Name:      MigrationName,
		Phase:     extension.PhaseAfterCoreData,
		Run:       model.SeedPartnerApplicationSettings,
	}); err != nil {
		return err
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension: ExtensionName,
		Name:      RoutesName,
		Priority:  extension.PriorityBeforePlugin,
		Register:  registerRoutes,
	})
}

func registerRoutes(engine *gin.Engine) error {
	user := engine.Group("/api/partner-applications")
	user.Use(middleware.DisableCache(), middleware.RouteTag("api"), middleware.GlobalAPIRateLimit(), middleware.UserAuth())
	user.GET("/settings", controller.GetPartnerApplicationSettings)
	user.GET("", middleware.SearchRateLimit(), controller.ListPartnerApplications)
	user.POST("", middleware.UserCriticalRateLimit("partner-application-create"), controller.CreatePartnerApplication)
	user.GET("/:id", controller.GetPartnerApplication)
	user.POST("/:id/cancel", middleware.UserCriticalRateLimit("partner-application-cancel"), controller.CancelPartnerApplication)

	admin := engine.Group("/api/admin/partner-applications")
	admin.Use(middleware.DisableCache(), middleware.RouteTag("api"), middleware.GlobalAPIRateLimit(), middleware.AdminAuth())
	admin.GET("/settings", middleware.RequirePermission(authz.PartnerSettingsRead), controller.GetAdminPartnerApplicationSettings)
	admin.PUT("/settings", middleware.RequirePermission(authz.PartnerSettingsUpdate), middleware.UserCriticalRateLimit("partner-settings-update"), controller.UpdatePartnerApplicationSettings)
	admin.GET("", middleware.RequirePermission(authz.PartnerApplicationRead), middleware.SearchRateLimit(), controller.ListAdminPartnerApplications)
	admin.GET("/:id", middleware.RequirePermission(authz.PartnerApplicationRead), controller.GetAdminPartnerApplication)
	admin.POST("/:id/review", middleware.RequirePermission(authz.PartnerApplicationReview), middleware.UserCriticalRateLimit("partner-application-review"), controller.ReviewPartnerApplication)
	return nil
}
