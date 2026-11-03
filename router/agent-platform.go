package router

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterAgentPlatformRoutes is the host security boundary used by the agent
// module. Agent qualification never replaces ordinary UserAuth; each operation
// additionally checks the current profile/session/policy in its transaction.
func RegisterAgentPlatformRoutes(scopes extension.RouteScopes) error {
	if scopes.API == nil {
		return fmt.Errorf("agent platform: API mount is unavailable")
	}
	group := scopes.API.Group("/agent-platform")
	group.Use(middleware.DisableCache(), middleware.UserAuth(), middleware.AgentBrowserSessionRequired())
	group.Use(func(c *gin.Context) { c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10); c.Next() })
	criticalWriteLimit := middleware.UserCriticalRateLimit("agent-platform")
	group.Use(func(c *gin.Context) {
		// Reads and recovery retain the host API limiter and all browser/session
		// guards, but must not exhaust the shared critical write budget.
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return
		default:
			criticalWriteLimit(c)
		}
	})
	group.GET("/self", controller.GetAgentPlatformSelf)
	group.GET("/simple/reminders/unread", controller.GetAgentSimpleUnreadReminders)
	group.POST("/simple/reminders/acknowledge", controller.AcknowledgeAgentSimpleReminders)
	group.GET("/simple/balance", controller.GetAgentSimpleBalance)
	group.GET("/simple/codes", controller.GetAgentSimpleCodes)
	group.POST("/simple/codes", controller.CreateAgentSimpleCode)
	group.POST("/simple/codes/:id/cancel", controller.CancelAgentSimpleCode)
	group.POST("/simple/recharge", controller.RequestAgentSimpleRecharge)
	group.POST("/simple/payment/quote", controller.QuoteAgentSimplePayment)
	group.GET("/simple/payment-methods", controller.GetAgentSimplePaymentMethods)
	group.POST("/simple/payment", controller.CreateAgentSimplePayment)
	group.GET("/simple/payment-orders", controller.GetAgentSimplePayments)
	group.GET("/simple/payment-fees", middleware.RootAuth(), controller.GetAgentSimplePaymentFees)
	group.POST("/simple/payment-fees", middleware.RootAuth(), controller.UpdateAgentSimplePaymentFee)
	group.POST("/simple/payment/refunds", middleware.RootAuth(), controller.RefundAgentSimplePayment)
	group.POST("/simple/payment/withdrawals", middleware.RootAuth(), controller.WithdrawAgentSimplePayment)
	group.POST("/simple/payment/transfer", controller.TransferAgentSimplePaymentProfit)
	group.POST("/simple/finance/status", middleware.RootAuth(), controller.UpdateAgentSimpleStatus)
	if scopes.PublicAPI != nil {
		publicPayments := scopes.PublicAPI.Group("/agent-pay")
		if scopes.AnonymousRequestBodyLimit != nil {
			publicPayments.POST("/:public_id", scopes.AnonymousRequestBodyLimit, controller.GetAgentSimplePublicPayment)
		} else {
			publicPayments.POST("/:public_id", controller.GetAgentSimplePublicPayment)
		}
	}
	group.POST("/simple/finance/adjust", middleware.RootAuth(), controller.AdjustAgentSimpleBalance)
	group.POST("/simple/finance/credit-limit", middleware.RootAuth(), controller.SetAgentSimpleCreditLimit)
	group.POST("/simple/finance/repayment", middleware.RootAuth(), controller.RepayAgentSimpleCredit)
	group.GET("/simple/finance/agents", middleware.RootAuth(), controller.ListAgentSimpleAgents)
	group.GET("/simple/finance/history/:kind", controller.ListAgentSimpleHistory)
	group.POST("/simple/finance/codes/:id/reveal", middleware.RootAuth(), controller.RevealAgentSimpleCode)
	group.POST("/simple/finance/withdrawal", middleware.RootAuth(), controller.RecordAgentSimpleWithdrawal)
	group.POST("/simple/finance/referral-transfer", controller.TransferReferralToAgentBalance)
	group.POST("/simple/finance/exit", middleware.RootAuth(), controller.SettleAgentSimpleExit)
	return nil
}
