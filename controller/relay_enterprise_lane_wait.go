//go:build enterprise_lane_lab

package controller

import (
	"context"
	"reflect"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

// Only a request that actually waited takes this path. Do not authorize from a
// captured routing or pricing snapshot after deliberately pausing admission.
func recheckEnterpriseLaneAfterWait(c *gin.Context, id model.EnterpriseLaneIdentity, channelID int, group, endpoint, prompt string, chat enterpriseLaneChat, price enterpriseLaneRate, promptCap, completionCap, hardCap int) bool {
	ctx := c.Request.Context()
	if ctx.Err() != nil || model.CheckRequestGroupConfigVersion(c) != nil ||
		middleware.RecheckEnterpriseLaneAuthorization(c) != nil || service.CheckModelTestAccess(c) != nil {
		return false
	}
	bounded, cancel := context.WithTimeout(ctx, enterpriseLaneSQLTime)
	defer cancel()
	// Keep caller and token group scope, settings and interception unchanged
	// across the wait. This query never loads the interception reply or Key.
	var current struct {
		UserGroup   string
		TokenGroup  string
		Setting     string
		Intercepted bool
	}
	err := model.DB.WithContext(bounded).Table("users").
		Select(`users."group" AS user_group, tokens."group" AS token_group, users.setting, COALESCE(intercept.enabled, false) AS intercepted`).
		Joins("JOIN tokens ON tokens.user_id = users.id AND tokens.id = ? AND tokens.deleted_at IS NULL", id.TokenID).
		Joins("LEFT JOIN user_call_interceptions AS intercept ON intercept.user_id = users.id").
		Where("users.id = ? AND users.deleted_at IS NULL AND users.status = ?", id.UserID, common.UserStatusEnabled).Take(&current).Error
	if err != nil || current.Intercepted || current.UserGroup != common.GetContextKeyString(c, constant.ContextKeyUserGroup) ||
		current.TokenGroup != group || service.ValidateTokenGroupForUser(bounded, id.UserID, current.UserGroup, group) != nil {
		return false
	}
	var userSettings dto.UserSetting
	previous, ok := common.GetContextKeyType[dto.UserSetting](c, constant.ContextKeyUserSetting)
	if !ok || (current.Setting != "" && common.Unmarshal([]byte(current.Setting), &userSettings) != nil) ||
		!reflect.DeepEqual(previous, userSettings) || !queuedEnterpriseLaneChannelUnchanged(c, bounded, channelID, group, chat.Model, endpoint) ||
		service.CheckRequestSensitiveText(c, prompt) != nil {
		return false
	}

	// The generic pricing helper caches the personal primary-DB lookup in
	// request context. Reset only this request, and reject changed terms;
	// never replace an old group admission version with a newer one.
	model.ResetRequestUserGroupPricing(c)
	fresh, err := enterpriseLaneFrozenRate(c, chat, promptCap)
	if err != nil || !fresh.prompt.Equal(price.prompt) || !fresh.completion.Equal(price.completion) {
		return false
	}
	cap, err := fresh.quota(promptCap, completionCap, true)
	return err == nil && cap == hardCap && ctx.Err() == nil && model.CheckRequestGroupConfigVersion(c) == nil
}

func queuedEnterpriseLaneChannelUnchanged(c *gin.Context, ctx context.Context, channelID int, group, modelName, frozenEndpoint string) bool {
	var channel model.Channel
	// Exact concrete route only; cache affinity, transcript or caller input
	// cannot authorize a disabled route or substitute an upstream.
	err := model.DB.WithContext(ctx).Model(&model.Channel{}).Omit("key").
		Joins(`JOIN abilities AS lane_ability ON lane_ability.channel_id = channels.id AND lane_ability."group" = ? AND lane_ability.model = ? AND lane_ability.enabled = true`, group, modelName).
		Where("channels.id = ? AND channels.status = ? AND channels.type = ?", channelID, common.ChannelStatusEnabled, constant.ChannelTypeOpenAI).
		Take(&channel).Error
	if err != nil || !slices.Contains(channel.GetModels(), modelName) || channel.ChannelInfo.IsMultiKey ||
		(channel.OpenAIOrganization != nil && *channel.OpenAIOrganization != "") ||
		!emptyLaneOverride(channel.ParamOverride) || !emptyLaneOverride(channel.HeaderOverride) ||
		(strings.TrimSpace(channel.GetModelMapping()) != "" && strings.TrimSpace(channel.GetModelMapping()) != "{}") ||
		requestplugin.NewPlan() != nil || model_setting.GetGlobalSettings().PassThroughRequestEnabled ||
		service.ShouldChatCompletionsUseResponsesGlobal(channelID, constant.ChannelTypeOpenAI, modelName) {
		return false
	}
	var settings dto.ChannelSettings
	var other dto.ChannelOtherSettings
	previous, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	previousOther, otherOK := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	// Decode without Channel.GetSetting/GetOtherSettings: those getters
	// repair corrupt data in the DB, a queued recheck must be strictly read-only.
	if !ok || !otherOK ||
		(channel.Setting != nil && *channel.Setting != "" && common.Unmarshal([]byte(*channel.Setting), &settings) != nil) ||
		(channel.OtherSettings != "" && common.Unmarshal([]byte(channel.OtherSettings), &other) != nil) ||
		!reflect.DeepEqual(previous, settings) || !reflect.DeepEqual(previousOther, other) || !enterpriseLaneNativeChannel(c) {
		return false
	}
	endpoint, err := enterpriseLaneUpstream(channel.GetBaseURL())
	return err == nil && endpoint == frozenEndpoint
}

func emptyLaneOverride(raw *string) bool {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return true
	}
	var val map[string]any
	return common.Unmarshal([]byte(*raw), &val) == nil && len(val) == 0
}
