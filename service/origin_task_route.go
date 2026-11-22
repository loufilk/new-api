package service

import (
	"errors"
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// TokenModelLimitAllows shares HTTP, WebSocket and origin-task name matching.
func TokenModelLimitAllows(limit map[string]bool, name string) bool {
	return name != "" && (limit[name] || limit[ratio_setting.FormatMatchingModelName(name)] || limit[ratio_setting.RoutingMatchModelName(name)])
}

func originTaskAccessDenied() *ChannelSelectError {
	return &ChannelSelectError{StatusCode: http.StatusForbidden, Code: types.ErrorCodeAccessDenied, MessageID: i18n.MsgDistributorGroupAccessDenied}
}

func originTaskGroupUnavailable() *ChannelSelectError {
	return &ChannelSelectError{StatusCode: http.StatusServiceUnavailable, Code: "group_config_changed", MessageID: i18n.MsgGroupConfigChanged}
}

// authorizedOriginTaskGroup runs under the caller's group configuration read
// guard. A previous task fixes upstream identity, never the current key's grant.
func authorizedOriginTaskGroup(c *gin.Context, channel *model.Channel, name string, tasks []*model.Task) (string, *ChannelSelectError) {
	if c == nil || c.Request == nil || channel == nil || name == "" {
		return "", originTaskAccessDenied()
	}
	limited := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
	limit, _ := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyTokenModelLimit)
	if limited && !TokenModelLimitAllows(limit, name) {
		return "", &ChannelSelectError{StatusCode: http.StatusForbidden, Code: types.ErrorCodeAccessDenied, MessageID: i18n.MsgDistributorTokenModelForbidden, Params: map[string]any{"Model": name}}
	}
	groupScope := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	groups := tokenRoutingGroups(c, groupScope)
	// Shared channels can sell the same upstream at different group prices.
	// Never relabel a known origin group, even if another group also serves it.
	for _, task := range tasks {
		if task == nil || task.ChannelId != channel.Id {
			return "", originTaskAccessDenied()
		}
		if originName := task.Properties.OriginModelName; limited && originName != "" && !TokenModelLimitAllows(limit, originName) {
			return "", originTaskAccessDenied()
		}
		if group := task.Group; group != "" && group != "auto" {
			if !slices.Contains(groups, group) {
				return "", originTaskAccessDenied()
			}
			groups = []string{group}
		}
	}
	available, err := model.EnabledGroupsForOriginChannel(c.Request.Context(), channel.Id, name, groups)
	if err != nil {
		return "", &ChannelSelectError{StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeGetChannelFailed, MessageID: i18n.MsgDistributorNoAvailableChannel, Params: map[string]any{"Group": groupScope, "Model": name}}
	}
	for _, group := range groups {
		if slices.Contains(available, group) {
			return group, nil
		}
	}
	return "", originTaskAccessDenied()
}

// AuthorizeNativeOriginTask applies the same guard as normal distribution when
// the native handler discovers the actual origin channel after the distributor.
func AuthorizeNativeOriginTask(c *gin.Context, channel *model.Channel, name string, origin *model.Task) (string, *types.NewAPIError) {
	guard, err := model.BeginRequestGroupConfigRead(c)
	if err != nil {
		return "", types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgGroupConfigChanged)), "group_config_changed", http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
	}
	defer guard.Close()
	group, scopeErr := authorizedOriginTaskGroup(c, channel, name, []*model.Task{origin})
	if err := guard.Finish(); err != nil {
		scopeErr = originTaskGroupUnavailable()
	}
	if scopeErr != nil {
		return "", types.NewErrorWithStatusCode(errors.New(i18n.T(c, scopeErr.MessageID, scopeErr.Params)), scopeErr.Code, scopeErr.StatusCode, types.ErrOptionWithSkipRetry())
	}
	return group, nil
}
