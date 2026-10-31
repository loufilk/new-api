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
	group.GET("/governance/self", controller.GetAgentGovernanceSelf)
	group.POST("/governance/submit", controller.SubmitAgentGovernance)
	group.GET("/contributions", controller.GetAgentContributions)
	group.GET("/credit", controller.GetAgentCredit)
	group.GET("/credit/reconciliation", middleware.AdminAuth(), controller.GetAgentCreditReconciliation)
	group.POST("/credit/actions/:action", controller.AgentCreditAction)
	group.GET("/deposit", controller.GetAgentDeposit)
	group.POST("/deposit/actions/:action", controller.AgentDepositAction)
	group.GET("/agreements", controller.GetAgentAgreements)
	group.GET("/agreements/:id", controller.GetAgentAgreement)
	group.POST("/agreements/:action", middleware.AdminAuth(), controller.ExecuteAgentAgreement)
	group.GET("/statements/:kind", middleware.AdminAuth(), controller.GetAgentStatements)
	group.GET("/statements/:kind/:id", middleware.AdminAuth(), controller.GetAgentStatementDetail)
	// Gin selects the static lines subtree before :kind; its deeper routes
	// otherwise shadow the generic line detail path. Keep both landing routes
	// explicit and select the resource kind on the server, never from input.
	group.GET("/statements/lines", middleware.AdminAuth(), func(c *gin.Context) {
		c.AddParam("kind", "lines")
		controller.GetAgentStatements(c)
	})
	group.GET("/statements/lines/:id", middleware.AdminAuth(), func(c *gin.Context) {
		c.AddParam("kind", "lines")
		controller.GetAgentStatementDetail(c)
	})
	group.GET("/statements/lines/:id/candidates", middleware.AdminAuth(), controller.GetAgentStatementCandidates)
	group.GET("/statements/lines/:id/evidence", middleware.AdminAuth(), controller.GetAgentStatementEvidence)
	group.POST("/statements/:action", middleware.AdminAuth(), controller.ExecuteAgentStatement)
	group.GET("/campaigns", controller.AgentCampaignListHandler("campaigns", false))
	group.POST("/campaigns/claim", controller.ClaimAgentCampaign)
	group.GET("/campaign-claims", controller.AgentCampaignListHandler("claims", false))
	group.GET("/campaign-claims/:request_id", controller.GetAgentCampaignClaim)
	group.GET("/bonus-sources", controller.AgentCampaignListHandler("sources", false))
	for _, kind := range []string{"allocations", "restorations", "removals"} {
		group.GET("/bonus-"+kind, controller.AgentCampaignListHandler(kind, false))
	}
	campaignAdmin := group.Group("/admin", middleware.AdminAuth())
	campaignAdmin.GET("/campaigns", controller.AgentCampaignListHandler("campaigns", true))
	campaignAdmin.GET("/campaign-claims", controller.AgentCampaignListHandler("claims", true))
	campaignAdmin.GET("/bonus-sources", controller.AgentCampaignListHandler("sources", true))
	for _, kind := range []string{"allocations", "restorations", "removals"} {
		campaignAdmin.GET("/bonus-"+kind, controller.AgentCampaignListHandler(kind, true))
	}
	campaignAdmin.GET("/campaign-budgets", controller.AgentCampaignListHandler("budgets", true))
	campaignAdmin.GET("/bonus-anomalies", controller.AgentCampaignListHandler("anomalies", true))
	campaignAdmin.POST("/campaigns/propose", controller.AgentCampaignOperationHandler("campaign.propose"))
	campaignAdmin.POST("/campaigns/approve", controller.AgentCampaignOperationHandler("campaign.approve"))
	campaignAdmin.POST("/campaigns/review", controller.AgentCampaignOperationHandler("campaign.review"))
	campaignAdmin.POST("/campaigns/revoke", controller.AgentCampaignOperationHandler("campaign.revoke"))
	group.GET("/policy/current", controller.GetCurrentAgentPlatformPolicy)
	group.POST("/operations/prepare", controller.PrepareAgentPlatformOperation)
	group.GET("/operations/:request_id", controller.GetAgentPlatformOperation)
	group.GET("/purchases", controller.GetAgentPurchaseOrders)
	group.POST("/purchases/quotes", controller.CreateAgentPurchaseQuote)
	group.POST("/purchases", controller.CreateAgentPurchaseOrder)
	group.POST("/purchases/:id/evidence", controller.SubmitAgentPurchaseEvidence)
	group.GET("/fund-batches", controller.GetAgentFundBatches)
	group.GET("/fund-ledger", controller.GetAgentFundLedger)
	group.GET("/purchase-audit", controller.GetAgentPurchaseAudit)
	group.POST("/procurement/self-use", controller.AgentProcurementSelfUse)
	group.GET("/purchase-refunds", controller.GetAgentPurchaseRefunds)
	group.POST("/purchase-refunds", controller.RequestAgentPurchaseRefund)
	group.GET("/deliveries", controller.GetAgentDeliveries)
	group.POST("/deliveries", controller.CreateAgentDelivery)
	group.GET("/deliveries/received", controller.GetReceivedAgentDeliveries)
	group.POST("/deliveries/claim", controller.ClaimAgentDelivery)
	group.POST("/deliveries/cancel-code", controller.CancelAgentLegacyCode)
	group.POST("/deliveries/:id/confirm", controller.ConfirmAgentDelivery)
	group.POST("/deliveries/:id/cancel", controller.CancelAgentDelivery)
	group.GET("/commissions/summary", controller.GetAgentCommissionSummary)
	for _, kind := range []string{"awards", "ledger", "debts", "conversions"} {
		group.GET("/commissions/"+kind, controller.AgentCommissionListHandler(kind, false))
	}
	group.GET("/commission-recipients", controller.AgentCommissionListHandler("recipients", false))
	group.POST("/commission-recipients", controller.ChangeAgentCommissionRecipient)
	group.PUT("/commission-recipients/:id", controller.ChangeAgentCommissionRecipient)
	group.POST("/withdrawals/quote", controller.CreateAgentCommissionWithdrawalQuote)
	group.GET("/withdrawals", controller.AgentCommissionListHandler("withdrawals", false))
	group.GET("/withdrawals/:id", controller.GetAgentCommissionWithdrawal)
	group.POST("/withdrawals", controller.RequestAgentCommissionWithdrawal)
	group.POST("/commissions/conversions/quote", controller.CreateAgentCommissionConversionQuote)
	group.POST("/commissions/conversions", controller.ExecuteAgentCommissionConversion)
	// Existing management URLs are separate from the self-service financial
	// surfaces. AdminAuth is additional, never a substitute for browser/session
	// validation or the in-transaction maker/checker and MFA checks.
	profiles := group.Group("/profiles", middleware.AdminAuth())
	profiles.GET("", controller.GetAgentProfiles)
	profiles.GET("/:id", controller.GetAgentProfileDetail)
	profiles.PATCH("/:id", controller.UpdateAgentProfile)
	finance := group.Group("/finance", middleware.AdminAuth())
	finance.GET("/governance/:user_id", controller.GetAgentGovernanceFinance)
	finance.POST("/governance/:user_id/review", controller.ReviewAgentGovernance)
	finance.POST("/governance/:user_id/override", middleware.RootAuth(), controller.OverrideAgentGovernance)
	finance.GET("/contributions", controller.GetAgentContributionsFinance)
	finance.GET("/policies", controller.GetAgentPlatformPolicies)
	finance.GET("/fund-ledger", controller.GetAgentFundLedger)
	finance.GET("/purchase-audit", controller.GetAgentPurchaseAudit)
	finance.GET("/fund-reclassifications", controller.GetAgentFundReclassifications)
	finance.POST("/fund-reclassifications", controller.RecordAgentFundReclassification)
	finance.POST("/fund-reclassifications/:id/confirm", controller.ConfirmAgentFundReclassification)
	finance.POST("/policies", middleware.RootAuth(), controller.PublishAgentPlatformPolicy)
	finance.GET("/purchases", controller.GetAgentPurchaseOrders)
	finance.POST("/purchases/:id/verify", controller.RecordAgentPurchasePayment)
	finance.POST("/purchases/:id/review", controller.ConfirmAgentPurchasePayment)
	finance.GET("/purchase-refunds", controller.GetAgentPurchaseRefunds)
	finance.GET("/purchase-refunds/:id/instructions", controller.AgentManualRefundInstructionHandler("purchase"))
	finance.GET("/deposit-refunds/:id/instructions", controller.AgentManualRefundInstructionHandler("deposit"))
	finance.POST("/purchase-refunds/:id/approve", controller.ApproveAgentPurchaseRefund)
	finance.POST("/purchase-refunds/:id/submit", controller.SubmitAgentPurchaseRefund)
	finance.POST("/purchase-refunds/:id/resolve", controller.ResolveAgentPurchaseRefund)
	finance.GET("/funds/reconciliation", controller.ReconcileAgentFunds)
	finance.GET("/withdrawals", controller.AgentCommissionListHandler("withdrawals", true))
	finance.GET("/withdrawals/:id/instructions", controller.GetAgentCommissionPayoutInstructions)
	for _, action := range []struct{ path, action string }{{"approve", "commission.withdraw.approve"}, {"reject", "commission.withdraw.reject"}, {"start", "commission.withdraw.submit"}, {"resolve", "commission.withdraw.resolve"}} {
		finance.POST("/withdrawals/:id/"+action.path, controller.AgentCommissionWithdrawalActionHandler(action.action))
	}
	for _, kind := range []string{"outbox", "refunds"} {
		finance.GET("/commissions/"+kind, controller.AgentCommissionListHandler(kind, true))
	}
	finance.POST("/commissions/refunds", controller.RecordAgentCommissionRefund)
	finance.POST("/commissions/reserve-release", controller.ReleaseAgentCommissionReserve)
	finance.POST("/commissions/outbox/retry", controller.RetryAgentCommissionOutbox)
	finance.GET("/commissions/reconciliation", controller.ReconcileAgentCommissions)
	return nil
}
