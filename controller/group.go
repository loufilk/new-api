package controller

import (
	"errors"
	"maps"
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetGroups(c *gin.Context) {
	guard, err := model.BeginGroupConfigRead(c.Request.Context())
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Group settings changed or are temporarily unavailable. Retry the request."))
		return
	}
	defer guard.Close()

	groupNames := make([]string, 0)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupNames = append(groupNames, groupName)
	}
	if err := guard.Finish(); err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Group settings changed or are temporarily unavailable. Retry the request."))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}

const channelStatusView = "channel-status"

// Status publication is narrower than key access: per-account overrides
// and private entitlements do not publish a group on this page.
func channelStatusPublicGroups() map[string]string {
	groups := service.GetUserUsableGroups("")
	delete(groups, "auto")
	delete(groups, "all")
	return groups
}

func GetUserGroups(c *gin.Context) {
	channelStatus := c.Query("view") == channelStatusView
	userId := c.GetInt("id")
	if channelStatus && userId <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("Unauthorized"))
		return
	}
	guard, err := model.BeginGroupConfigRead(c.Request.Context())
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Group settings changed or are temporarily unavailable. Retry the request."))
		return
	}
	defer guard.Close()

	usableGroups := make(map[string]map[string]any)
	userGroup := ""
	if channelStatus {
		user, lookupErr := model.ReadGroupHealthUser(c.Request.Context(), userId)
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) || (lookupErr == nil && user.Status != common.UserStatusEnabled) {
			common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("Unauthorized"))
			return
		}
		if lookupErr != nil {
			groupConfigReadFailure(c)
			return
		}
		userGroup = user.Group
	} else if userId > 0 {
		user, lookupErr := model.GetUserCache(userId)
		if lookupErr != nil {
			groupConfigReadFailure(c)
			return
		}
		userGroup = user.Group
	}
	var userUsableGroups map[string]string
	if channelStatus {
		public := channelStatusPublicGroups()
		userUsableGroups = service.GetUserUsableGroups(userGroup)
		for group := range userUsableGroups {
			if _, visible := public[group]; !visible {
				delete(userUsableGroups, group)
			}
		}
	} else {
		userUsableGroups, err = service.GetUserUsableGroupsForUser(c.Request.Context(), userId, userGroup)
		if err != nil {
			groupConfigReadFailure(c)
			return
		}
	}
	catalog, err := model.GetGroupCatalogProjection(c.Request.Context(), userUsableGroups)
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	rates, err := service.LoadEffectiveGroupRates(c.Request.Context(), userId, userGroup, slices.Collect(maps.Keys(userUsableGroups)))
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			usableGroups[groupName] = map[string]any{
				"ratio": rates[groupName].GroupRatio,
				"desc":  desc,
			}
			if entry, ok := catalog[groupName]; ok && userId > 0 {
				usableGroups[groupName]["catalog"] = entry
			}
		}
	}
	if _, ok := userUsableGroups["auto"]; ok {
		usableGroups["auto"] = map[string]any{
			"ratio": "auto",
			"desc":  setting.GetUsableGroupDescription("auto"),
		}
	}
	if err := guard.Finish(); err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Group settings changed or are temporarily unavailable. Retry the request."))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
	})
}
