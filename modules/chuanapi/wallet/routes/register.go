package routes

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
)

const (
	ExtensionName = "chuanapi"
	RoutesName    = "wallet_bonus_policy_routes"
)

// Register owns HTTP wiring for Wallet user and admin surfaces. Keeping middleware
// dependencies in this adapter package leaves the wallet domain/runtime package
// independent of middleware -> service, so core service tests can import the
// wallet implementation without creating a package cycle.
func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi wallet routes: registry must not be nil")
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension:      ExtensionName,
		Name:           RoutesName,
		Priority:       extension.PriorityBeforePlugin,
		RegisterScoped: registerRoutes,
	})
}

func registerRoutes(scopes extension.RouteScopes) error {
	if scopes.UserSelf == nil || scopes.AdminModels == nil {
		return fmt.Errorf("chuanapi wallet routes: required host API route scopes are unavailable")
	}
	scopes.UserSelf.GET("/wallet", walletmodule.GetWalletSummary)
	scopes.UserSelf.GET("/wallet/ledger", walletmodule.GetWalletLedger)
	scopes.UserSelf.GET("/referral/rewards", walletmodule.GetReferralRewards)
	scopes.UserSelf.POST("/aff_transfer", middleware.UserCriticalRateLimit("aff-transfer"), walletmodule.TransferAffQuota)

	scopes.AdminModels.GET("/bonus_eligible/audits", walletmodule.GetModelBonusEligibleAudits)
	scopes.AdminModels.POST("/bonus_eligible/batch", walletmodule.BatchUpdateModelBonusEligible)
	return nil
}
