package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPricingRateLimitsExposeOnlyAuthorizedConcreteGroupPolicies(t *testing.T) {
	enabled, minutes := setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes
	total, success := setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount
	ratios, overrides := ratio_setting.GroupRatio2JSONString(), setting.ModelRequestRateLimitGroup2JSONString()
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes = enabled, minutes
		setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = total, success
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(ratios))
		require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(overrides))
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"special":2,"private":3}`))
	require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(`{"special":[0,7],"private":[700,500]}`))
	setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes = true, 5
	setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = 100, 60
	allowed := []string{"default", "special", "auto", "all", "not-configured"}
	assert.Equal(t, map[string]pricingRequestLimit{"default": {5, 100, 60, "shared"}, "special": {5, 0, 7, "group"}}, pricingRateLimits(allowed))
	setting.ModelRequestRateLimitDurationMinutes = -1
	assert.Empty(t, pricingRateLimits(allowed))
	setting.ModelRequestRateLimitDurationMinutes, setting.ModelRequestRateLimitEnabled = 5, false
	assert.Empty(t, pricingRateLimits(allowed))
}
