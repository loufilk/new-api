package billing_setting

import (
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

// HasPriceOrRatioEntry reports whether name has a configured price, ratio, or
// tiered billing-mode entry after a single wildcard normalization. Self-use
// fallback does not count as a configured ratio.
func HasPriceOrRatioEntry(name string) bool {
	formatted := ratio_setting.FormatMatchingModelName(name)
	if _, ok := ratio_setting.GetModelPrice(formatted, false); ok {
		return true
	}
	if ratio_setting.HasConfiguredModelRatio(formatted) {
		return true
	}
	return GetBillingMode(formatted) == BillingModeTieredExpr
}

// ResolveBillingModelName shares the relay's canonical pricing lookup with
// explicit cost confirmations. It does not use the mapped upstream model.
func ResolveBillingModelName(origin string) string {
	var candidates []string
	if !reasoning.ParseModelModifiers(origin).HasModifiers() {
		candidates = append(candidates, origin)
	}
	candidates = append(candidates, hostreasoning.CanonicalBillingModelNames(origin)...)
	base := hostreasoning.BaseModelName(origin)
	// DeepSeek thinking tails (-none/-low/-max) bill at the base model's price,
	// with or without @ modifiers after them.
	if deepSeekBase, _, _, ok := reasoning.ParseDeepSeekV4ThinkingSuffix(base); ok && !model_setting.ShouldPreserveThinkingSuffix(base) {
		candidates = append(candidates, deepSeekBase)
	}
	candidates = append(candidates, base)

	seen := make(map[string]struct{}, len(candidates))
	matched := ""
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if HasPriceOrRatioEntry(name) {
			matched = name
			break
		}
	}
	if matched == "" {
		matched = base
	}
	return matched
}
