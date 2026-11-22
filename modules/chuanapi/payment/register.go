package payment

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
)

const (
	ExtensionName = "chuanapi"
	ModelsName    = "payment_order_bindings"
	RoutesName    = "payment_routes"
)

// Register owns the ChuanAPI payment model set (the chuanapi_payment_order_bindings
// settlement snapshot) and the payment_routes descriptor. Provider protocol and
// transport bodies live in the host controller/service packages; the route set
// only forwards to the host registrar so its identity, priority and URL
// contract stay stable without a module -> host dependency edge.
func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi payment: registry must not be nil")
	}
	if err := reg.RegisterModelSet(extension.ModelSet{
		Extension: ExtensionName,
		Name:      ModelsName,
		Phase:     extension.PhaseAfterCoreModels,
		Models:    []any{&PaymentOrderBinding{}},
	}); err != nil {
		return err
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension:      ExtensionName,
		Name:           RoutesName,
		Priority:       extension.PriorityBeforePlugin,
		RegisterScoped: registerRoutes,
	})
}

func registerRoutes(scopes extension.RouteScopes) error {
	if scopes.PaymentRoutes == nil {
		return fmt.Errorf("chuanapi payment: host payment route registrar is unavailable")
	}
	return scopes.PaymentRoutes(scopes)
}
