package controller

import (
	"maps"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetRatioConfig(c *gin.Context) {
	if !ratio_setting.IsExposeRatioEnabled() {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("The ratio config API is not enabled"))
		return
	}

	guard, ok := beginPublicGroupConfigRead(c)
	if !ok {
		return
	}
	defer guard.Close()
	data := billing_setting.GetPricingSyncData(map[string]any(ratio_setting.GetExposedData()))
	if model.HasExclusiveGroups() {
		// This endpoint is anonymous and was historically a global price
		// export. Once any private route exists, export exact names proved
		// public by the catalog rather than trying to guess private aliases
		// from billing rules or disabled channel names.
		publicGroups := service.GetUserUsableGroups("")
		publicModels := make(map[string]bool)
		for _, row := range model.GetPricing() {
			for _, group := range row.EnableGroup {
				if _, usable := publicGroups[group]; usable && ratio_setting.ContainsGroupRatio(group) {
					publicModels[row.ModelName] = true
					break
				}
			}
		}
		for key, value := range data {
			switch values := value.(type) {
			case map[string]float64:
				copies := maps.Clone(values)
				maps.DeleteFunc(copies, func(name string, _ float64) bool { return !publicModels[name] })
				data[key] = copies
			case map[string]string:
				copies := maps.Clone(values)
				maps.DeleteFunc(copies, func(name string, _ string) bool { return !publicModels[name] })
				data[key] = copies
			default:
				delete(data, key)
			}
		}
	}
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}
