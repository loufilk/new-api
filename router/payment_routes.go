package router

import (
	"fmt"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
)

// RegisterPaymentRoutes attaches the host-owned payment provider endpoints to
// the API scopes assembled by SetApiRouter. The provider controller/service
// bodies live in the host again, while the extension registry keeps its
// payment_routes descriptor identity and PriorityBeforePlugin order: the
// ChuanAPI payment module's route set delegates to this registrar through
// extension.RouteScopes.PaymentRoutes.
func RegisterPaymentRoutes(scopes extension.RouteScopes) error {
	if scopes.API == nil || scopes.UserSelf == nil || scopes.SubscriptionUser == nil ||
		scopes.RootOptions == nil || scopes.AnonymousRequestBodyLimit == nil {
		return fmt.Errorf("router: required host payment route scopes are unavailable")
	}

	scopes.API.POST("/waffo/webhook", scopes.AnonymousRequestBodyLimit, controller.WaffoWebhook)
	scopes.API.POST("/waffo-pancake/webhook/:env", scopes.AnonymousRequestBodyLimit, controller.WaffoPancakeWebhook)

	scopes.UserSelf.POST("/waffo/amount", controller.RequestWaffoAmount)
	scopes.UserSelf.POST("/waffo/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPay)
	scopes.UserSelf.POST("/waffo-pancake/amount", controller.RequestWaffoPancakeAmount)
	scopes.UserSelf.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPancakePay)

	scopes.SubscriptionUser.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestWaffoPancakePay)

	scopes.RootOptions.GET("/waffo-pancake/catalog", controller.ListWaffoPancakeCatalog)
	scopes.RootOptions.POST("/waffo-pancake/pair", controller.CreateWaffoPancakePair)
	scopes.RootOptions.POST("/waffo-pancake/save", controller.SaveWaffoPancake)
	scopes.RootOptions.POST("/waffo-pancake/subscription-product", controller.CreateWaffoPancakeSubscriptionProduct)
	scopes.RootOptions.GET("/waffo-pancake/subscription-product-options", controller.ListWaffoPancakeSubscriptionProductOptions)
	return nil
}
