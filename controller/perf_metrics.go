package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func usablePerfMetricGroups(c *gin.Context) ([]string, bool) {
	_, previouslyAdmitted := c.Get(model.GroupConfigVersionContextKey)
	guard, ok := beginPublicGroupConfigRead(c)
	if !ok {
		return nil, false
	}
	defer guard.Close()
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	userID := c.GetInt("id")
	if !previouslyAdmitted && userID > 0 {
		// Optional authentication may have read identity before a remote rename.
		user, err := model.GetUserCache(userID)
		if err != nil {
			groupConfigReadFailure(c)
			return nil, false
		}
		userGroup = user.Group
	}
	usableGroups, err := service.GetUserUsableGroupsForUser(c.Request.Context(), userID, userGroup)
	if err != nil {
		groupConfigReadFailure(c)
		return nil, false
	}
	// A non-nil empty list restricts the metrics query to zero groups.
	groups := make([]string, 0, len(usableGroups))
	for group := range usableGroups {
		if group == "auto" || ratio_setting.ContainsGroupRatio(group) {
			groups = append(groups, group)
		}
	}
	return groups, finishControllerGroupConfigRead(c, guard)
}

func GetPerfMetricsSummary(c *gin.Context) {
	if groups, ok := usablePerfMetricGroups(c); ok {
		getPerfMetricsSummary(c, groups, true)
	}
}

func GetDashboardPerfMetricsSummary(c *gin.Context) {
	getPerfMetricsSummary(c, append(lo.Keys(ratio_setting.GetGroupRatioCopy()), "auto"), false)
}

func getPerfMetricsSummary(c *gin.Context, groups []string, verifyGroupScope bool) {
	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	recentModels := c.QueryArray("recent_model")
	if len(recentModels) > perfmetrics.MaxRecentModels {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	result, err := perfmetrics.QuerySummaryAll(hours, groups, recentModels...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if verifyGroupScope && !metricGroupScopeStillCurrent(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func GetPerfMetrics(c *gin.Context) {
	modelName := c.Query("model")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "model is required",
		})
		return
	}

	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	groups, ok := usablePerfMetricGroups(c)
	if !ok {
		return
	}
	result, err := perfmetrics.Query(perfmetrics.QueryParams{
		Model:         modelName,
		Group:         c.Query("group"),
		Hours:         hours,
		AllowedGroups: groups,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if !metricGroupScopeStillCurrent(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// Do not hold the policy lock across a potentially slow metrics query. Before
// emitting its rows, check that the groups used for the query are still current.
func metricGroupScopeStillCurrent(c *gin.Context) bool {
	guard, ok := beginControllerGroupConfigRead(c)
	if ok {
		guard.Close()
	}
	return ok
}
