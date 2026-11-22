package controller

import (
	"sort"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
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
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(200, gin.H{
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
	})
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
