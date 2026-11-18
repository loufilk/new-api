package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func RegisterCommerceRoutes(api *gin.RouterGroup) {
	api.GET("/invoices/payments/epay/notify", controller.InvoiceEpayNotify)
	api.POST("/invoices/payments/epay/notify", controller.InvoiceEpayNotify)
	user := api.Group("", middleware.DisableCache(), middleware.UserAuth())
	user.GET("/commerce/config", controller.GetCommerceConfig)
	user.GET("/invoices/payment-options", controller.GetInvoiceFeePaymentOptions)
	user.GET("/invoices/:id/payment-options", controller.GetInvoiceFeePaymentOptions)
	user.GET("/invoices/eligible-orders", controller.GetEligibleInvoiceOrders)
	user.GET("/invoices", controller.ListInvoiceApplications)
	user.GET("/invoices/:id", controller.GetInvoiceApplication)
	user.POST("/invoices", middleware.CriticalRateLimit(), controller.CreateInvoiceApplication)
	user.POST("/invoices/:id/pay", middleware.CriticalRateLimit(), controller.PayInvoiceFee)
	user.POST("/invoices/:id/cancel", middleware.CriticalRateLimit(), controller.CancelInvoiceApplication)
	user.GET("/partner-applications", controller.ListPartnerApplications)
	user.GET("/partner-applications/:id", controller.GetPartnerApplication)
	user.POST("/partner-applications", middleware.CriticalRateLimit(), controller.CreatePartnerApplication)
	user.POST("/partner-applications/:id/cancel", middleware.CriticalRateLimit(), controller.CancelPartnerApplication)
	admin := api.Group("/admin", middleware.DisableCache(), middleware.AdminAuth())
	admin.GET("/commerce/config", middleware.RequirePermission(authz.CommerceSettingsRead), controller.GetCommerceConfig)
	admin.PUT("/commerce/config", middleware.RequirePermission(authz.CommerceSettingsUpdate), middleware.CriticalRateLimit(), controller.UpdateCommerceConfig)
	admin.GET("/invoices", middleware.RequirePermission(authz.CommerceInvoiceRead), controller.ListAdminInvoiceApplications)
	admin.GET("/invoices/:id", middleware.RequirePermission(authz.CommerceInvoiceRead), controller.GetAdminInvoiceApplication)
	admin.POST("/invoices/:id/review", middleware.RequirePermission(authz.CommerceInvoiceReview), middleware.CriticalRateLimit(), controller.ReviewInvoiceApplication)
	admin.GET("/partner-applications", middleware.RequirePermission(authz.CommercePartnerRead), controller.ListAdminPartnerApplications)
	admin.GET("/partner-applications/:id", middleware.RequirePermission(authz.CommercePartnerRead), controller.GetAdminPartnerApplication)
	admin.POST("/partner-applications/:id/review", middleware.RequirePermission(authz.CommercePartnerReview), middleware.CriticalRateLimit(), controller.ReviewPartnerApplication)
}
