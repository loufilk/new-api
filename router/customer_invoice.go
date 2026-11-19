package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterCustomerInvoiceRoutes mounts only private dashboard endpoints. Download
// is never exposed through public storage or unauthenticated static routes.
func RegisterCustomerInvoiceRoutes(api *gin.RouterGroup) {
	customer := api.Group("/invoice", middleware.DisableCache(), middleware.UserAuth(), middleware.CustomerInvoiceSessionRequired())
	write := middleware.UserCriticalRateLimit("customer-invoice-write")
	download := middleware.DownloadRateLimit()
	customer.GET("/config", controller.GetCustomerInvoiceConfig)
	customer.GET("/summary", controller.GetCustomerInvoiceSummary)
	customer.GET("/orders", controller.ListCustomerInvoiceOrders)
	customer.GET("/applications", controller.ListCustomerInvoiceApplications)
	customer.POST("/applications", write, controller.SubmitCustomerInvoice)
	customer.GET("/applications/:id", controller.GetCustomerInvoice)
	customer.POST("/applications/:id/cancel", write, controller.CancelCustomerInvoice)
	customer.GET("/applications/:id/document", download, controller.DownloadCustomerInvoice)

	admin := api.Group("/admin/invoice", middleware.DisableCache(), middleware.RootAuth(), middleware.CustomerInvoiceSessionRequired())
	financeWrite := middleware.UserCriticalRateLimit("admin-invoice-write")
	admin.GET("/applications", controller.ListAdminCustomerInvoiceApplications)
	admin.GET("/applications/:id", controller.GetAdminCustomerInvoice)
	admin.GET("/applications/:id/document", download, controller.DownloadAdminCustomerInvoice)
	admin.POST("/applications/:id/claim", financeWrite, controller.ClaimCustomerInvoice)
	admin.POST("/applications/:id/release", financeWrite, controller.ReleaseCustomerInvoiceClaim)
	admin.POST("/applications/:id/reject", financeWrite, controller.RejectCustomerInvoice)
	admin.POST("/applications/:id/reconciliation", financeWrite, controller.FlagCustomerInvoiceReconciliation)
	admin.POST("/applications/:id/issue", financeWrite, middleware.UploadRateLimit(), controller.IssueCustomerInvoice)
}
