package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func hasConfiguredOrdinaryPrice(modelName string) bool {
	// Relay checks the expression mode first; an unavailable expression never
	// legitimately falls back to a stale ratio or fixed price.
	if billing_setting.GetBillingMode(modelName) == billing_setting.BillingModeTieredExpr {
		expression, ok := billing_setting.GetBillingExpr(modelName)
		if !ok || strings.TrimSpace(expression) == "" {
			return false
		}
		_, err := billingexpr.CompileFromCache(expression)
		return err == nil && !billingexpr.UsedVars(expression)["u"]
	}
	_, fixed := ratio_setting.GetModelPrice(modelName, false)
	// HasConfiguredModelRatio distinguishes an explicitly priced/free model
	// from the generic self-use or per-user opt-in fallback ratio.
	return fixed || ratio_setting.HasConfiguredModelRatio(modelName)
}

// WithOrdinaryPriceScope produces a viewer-specific quote without mutating
// the shared catalogue. An ordinary route in a private group cannot justify
// publishing a base quote to viewers who only have its task route.
func (p Pricing) WithOrdinaryPriceScope(usableGroups map[string]string) Pricing {
	if p.OrdinaryPriceAvailable == nil || !*p.OrdinaryPriceAvailable || p.ordinaryPriceGroups == nil {
		return p
	}
	if len(usableGroups) > 0 && p.ordinaryPriceGroups.Contains("all") {
		return p
	}
	for group := range usableGroups {
		if p.ordinaryPriceGroups.Contains(group) {
			return p
		}
	}
	p.OrdinaryPriceAvailable = common.GetPointer(false)
	return p
}
