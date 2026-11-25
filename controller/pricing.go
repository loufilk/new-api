package controller

import (
	"sort"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		// The pricing cache is shared by all viewers.
		groups := make([]string, 0, len(item.EnableGroup))
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok || group == "all" {
				groups = append(groups, group)
			}
		}
		if len(groups) > 0 {
			item.EnableGroup = groups
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	guard, guardOK := beginControllerGroupConfigRead(c)
	if !guardOK {
		return
	}
	defer guard.Close()

	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	ctx := c.Request.Context()
	usableGroup, err := service.GetUserUsableGroupsForUser(ctx, user.Id, user.Group)
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	pricing := filterPricingByUsableGroups(model.GetPricing(), usableGroup)
	groups := make([]string, 0, len(usableGroup))
	for key := range usableGroup {
		if key != "auto" && key != "all" {
			groups = append(groups, key)
		}
	}
	sort.Strings(groups)
	rates, err := service.LoadEffectiveGroupRates(ctx, user.Id, user.Group, groups)
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	groupRatio := make(map[string]float64, len(rates))
	groupRateSource := make(map[string]string, len(rates))
	for key, rate := range rates {
		groupRatio[key] = rate.GroupRatio
		groupRateSource[key] = string(rate.RateSource)
	}
	catalogue, err := model.GetGroupCatalogProjection(ctx, usableGroup)
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	autoGroups := service.GetUserAutoGroup(user.Group)
	vendorIDs := make(map[int]struct{})
	visibleEndpoints := make(map[string]struct{})
	for _, row := range pricing {
		vendorIDs[row.VendorID] = struct{}{}
		for _, endpoint := range row.SupportedEndpointTypes {
			visibleEndpoints[string(endpoint)] = struct{}{}
		}
	}
	vendors := make([]model.PricingVendor, 0)
	for _, vendor := range model.GetVendors() {
		if _, ok := vendorIDs[vendor.ID]; ok {
			vendors = append(vendors, vendor)
		}
	}
	endpoints := make(map[string]common.EndpointInfo)
	for endpoint, info := range model.GetSupportedEndpointMap() {
		if _, ok := visibleEndpoints[endpoint]; ok {
			endpoints[endpoint] = info
		}
	}
	rateLimits := pricingRateLimits(groups)
	activityScopes := make(map[string]bool)
pricedModels:
	for _, row := range pricing {
		for _, name := range row.EnableGroup {
			if name == "all" {
				for _, name := range groups {
					activityScopes[name] = groupRateSource[name] == "group"
				}
				// A wildcard model already includes all authorized concrete groups.
				break pricedModels
			}
			if _, allowed := groupRateSource[name]; allowed {
				activityScopes[name] = groupRateSource[name] == "group"
			}
		}
	}
	activity, _ := model.ProjectGroupMarketActivity(guard.Version, activityScopes)
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	c.Header("Cache-Control", "private, no-store")
	payload := gin.H{
		"group_catalog":      catalogue,
		"group_rate_source":  groupRateSource,
		"success":            true,
		"data":               pricing,
		"vendors":            vendors,
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": endpoints,
		"auto_groups":        autoGroups,
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	}
	if len(activity) > 0 {
		payload["group_market_activity"] = activity
	}
	if len(rateLimits) > 0 {
		payload["group_rate_limits"] = rateLimits
	}
	c.JSON(200, payload)
}

type pricingRequestLimit struct {
	WindowMinutes      int    `json:"window_minutes"`
	TotalRequests      int    `json:"total_requests"`
	SuccessfulRequests int    `json:"successful_requests"`
	Scope              string `json:"scope"`
}

// Project the running admission settings, never the private bucket IDs or
// unscoped configuration map. Both bucket types span models for this user.
func pricingRateLimits(allowed []string) map[string]pricingRequestLimit {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	minutes := setting.ModelRequestRateLimitDurationMinutes
	if !setting.ModelRequestRateLimitEnabled || minutes <= 0 || minutes > 3650*24*60 {
		return nil
	}
	limits := make(map[string]pricingRequestLimit, len(allowed))
	for _, group := range allowed {
		if !ratio_setting.ContainsGroupRatio(group) || group == "" || group == "auto" || group == "all" {
			continue
		}
		total, success, isolated := setting.GetGroupRateLimit(group)
		scope := "group"
		if !isolated {
			scope = "shared"
			total, success = setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount
		}
		if total < 0 || success < 0 || int64(total) > (1<<53)-1 || int64(success) > (1<<53)-1 {
			continue // Invalid/unrepresentable configuration is not a published policy.
		}
		limits[group] = pricingRequestLimit{WindowMinutes: minutes, TotalRequests: total, SuccessfulRequests: success, Scope: scope}
	}
	return limits
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccessT(c, "Model ratios reset", nil)
}
