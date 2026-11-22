package controller

import (
	"github.com/QuantumNous/new-api/common"
	"maps"

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
	group := user.Group
	usableGroup := service.GetUserUsableGroups(group)
	pricing := filterPricingByUsableGroups(model.GetPricing(), usableGroup)
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	for groupName := range groupRatio {
		if _, ok := usableGroup[groupName]; !ok {
			delete(groupRatio, groupName)
			continue
		}
		if ratio, ok := ratio_setting.GetGroupGroupRatio(group, groupName); ok {
			groupRatio[groupName] = ratio
		}
	}

	autoGroups := service.GetUserAutoGroup(group)
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            model.GetVendors(),
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": model.GetSupportedEndpointMap(),
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
