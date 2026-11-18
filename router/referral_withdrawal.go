package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func RegisterReferralWithdrawalRoutes(api *gin.RouterGroup) {
	user := api.Group("/referral-withdrawals", middleware.DisableCache(), middleware.UserAuth())
	user.GET("/summary", controller.GetReferralWithdrawalSummary)
	user.GET("", controller.ListReferralWithdrawals)
	user.GET("/:id", controller.GetReferralWithdrawal)
	user.POST("/quote", middleware.CriticalRateLimit(), controller.QuoteReferralWithdrawal)
	user.POST("", middleware.CriticalRateLimit(), controller.CreateReferralWithdrawal)
	user.POST("/:id/cancel", middleware.CriticalRateLimit(), controller.CancelReferralWithdrawal)
	admin := api.Group("/admin/referral-withdrawals", middleware.DisableCache(), middleware.AdminAuth())
	admin.GET("/config", middleware.RequirePermission(authz.ReferralWithdrawalSettingsRead), controller.GetReferralWithdrawalConfig)
	admin.PUT("/config", middleware.RequirePermission(authz.ReferralWithdrawalSettingsUpdate), middleware.CriticalRateLimit(), controller.UpdateReferralWithdrawalConfig)
	admin.GET("", middleware.RequirePermission(authz.ReferralWithdrawalRead), controller.ListAdminReferralWithdrawals)
	admin.GET("/:id", middleware.RequirePermission(authz.ReferralWithdrawalRead), controller.GetAdminReferralWithdrawal)
	admin.POST("/:id/review", middleware.RequirePermission(authz.ReferralWithdrawalReview), middleware.CriticalRateLimit(), controller.ReviewReferralWithdrawal)
	admin.POST("/:id/pay", middleware.RequirePermission(authz.ReferralWithdrawalPay), middleware.CriticalRateLimit(), controller.PayReferralWithdrawal)
}
