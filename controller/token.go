package controller

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	relayhelper "github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type tokenAutoGroupsInput struct {
	Set    bool
	Groups []string
}

func (input *tokenAutoGroupsInput) UnmarshalJSON(data []byte) error {
	input.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		input.Groups = nil
		return nil
	}
	return common.Unmarshal(data, &input.Groups)
}

type tokenRequest struct {
	model.Token
	ExpectedVersion    *int64               `json:"expected_version"`
	Name               *string              `json:"name"`
	ExpiredTime        *int64               `json:"expired_time"`
	UnlimitedQuota     *bool                `json:"unlimited_quota"`
	ModelLimitsEnabled *bool                `json:"model_limits_enabled"`
	ModelLimits        *string              `json:"model_limits"`
	AllowIps           *string              `json:"allow_ips"`
	AutoGroups         tokenAutoGroupsInput `json:"auto_groups"`
	AutoGroupsMode     string               `json:"auto_groups_mode"`
	Group              *string              `json:"group"`
	CrossGroupRetry    *bool                `json:"cross_group_retry"`
	SmartRouting       *bool                `json:"smart_routing"`
	RoutingStrategy    *string              `json:"routing_strategy"`
	NoRetryWithinGroup *bool                `json:"no_retry_within_group"`
	ProjectLabel       *string              `json:"project_label"`
	RemainQuota        *int                 `json:"remain_quota"`
	// 指针区分“未传”和显式 false：新建时未传即开启，更新时未传则保留原值。
	AllowUserQuotaQuery *bool `json:"allow_user_quota_query"`
}

// Omitted PUT fields are not permission changes. Explicit false/empty values
// still let clients disable a setting or clear a restriction deliberately.
func (request *tokenRequest) applyEditableFields(token *model.Token) {
	if request.Name != nil {
		token.Name = *request.Name
	}
	if request.ExpiredTime != nil {
		token.ExpiredTime = *request.ExpiredTime
	}
	if request.UnlimitedQuota != nil {
		token.UnlimitedQuota = *request.UnlimitedQuota
	}
	if request.ModelLimitsEnabled != nil {
		token.ModelLimitsEnabled = *request.ModelLimitsEnabled
	}
	if request.ModelLimits != nil {
		token.ModelLimits = *request.ModelLimits
	}
	if request.AllowIps != nil {
		token.AllowIps = request.AllowIps
	}
	if request.RemainQuota != nil {
		token.RemainQuota = *request.RemainQuota
	}
}

func (request *tokenRequest) applySingleGroupSettings(token *model.Token, creating bool) error {
	if (creating && request.Group == nil) || (request.Group != nil && strings.TrimSpace(*request.Group) == "") {
		return common.NewMessage("Please select a group")
	}
	if (request.Group != nil && *request.Group == "auto") ||
		(request.SmartRouting != nil && *request.SmartRouting) ||
		(request.CrossGroupRetry != nil && *request.CrossGroupRetry) ||
		(request.RoutingStrategy != nil && *request.RoutingStrategy != "" && *request.RoutingStrategy != "auto") ||
		len(request.AutoGroups.Groups) > 0 || request.AutoGroupsMode != "" {
		return common.NewMessage("Please select a single group")
	}
	legacy := token.SmartRouting || token.Group == "auto" || token.Group == ""
	if request.Group != nil {
		token.Group = *request.Group
		token.SmartRouting = false
		token.CrossGroupRetry = false
		token.AutoGroups = ""
		token.RoutingStrategy = "auto"
	} else if legacy && (request.NoRetryWithinGroup != nil || request.SmartRouting != nil ||
		request.CrossGroupRetry != nil || request.RoutingStrategy != nil || request.AutoGroups.Set) {
		// Omission is how a metadata edit preserves every legacy route field.
		return common.NewMessage("Please select a single group")
	}
	if request.NoRetryWithinGroup != nil {
		token.NoRetryWithinGroup = *request.NoRetryWithinGroup
	}
	if request.ProjectLabel != nil {
		token.ProjectLabel = strings.TrimSpace(*request.ProjectLabel)
	}
	return token.ValidateRoutingSettings()
}

type tokenResponse struct {
	*model.Token
	SettingsVersion int64    `json:"settings_version"`
	EffectiveStatus int      `json:"effective_status"`
	AutoGroups      []string `json:"auto_groups"`
}

func maxTokenQuota() int {
	quota, err := common.WalletQuotaFromDecimalStrict(
		decimal.NewFromInt(1_000_000_000).Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
	)
	if err != nil {
		return common.MaxWalletQuota
	}
	return quota
}

func buildMaskedTokenResponse(token *model.Token) *tokenResponse {
	if token == nil {
		return nil
	}
	maskedToken := *token
	maskedToken.Key = token.GetMaskedKey()
	maskedToken.RoutingStrategy = token.GetRoutingStrategy()
	autoGroups, err := token.GetAutoGroups()
	if err != nil {
		common.SysError(common.LogText("failed to parse auto groups for token %d: %v", token.Id, err))
		autoGroups = nil
	}
	if len(autoGroups) == 0 {
		autoGroups = nil
	}
	return &tokenResponse{Token: &maskedToken, AutoGroups: autoGroups, SettingsVersion: token.CacheRevision, EffectiveStatus: token.EffectiveStatus(common.GetTimestamp())}
}

func buildMaskedTokenResponses(tokens []*model.Token) []*tokenResponse {
	maskedTokens := make([]*tokenResponse, 0, len(tokens))
	for _, token := range tokens {
		maskedTokens = append(maskedTokens, buildMaskedTokenResponse(token))
	}
	return maskedTokens
}

func getTokenRequestUserGroup(c *gin.Context) (string, error) {
	if userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup); userGroup != "" {
		return userGroup, nil
	}
	if userGroup := c.GetString("group"); userGroup != "" {
		return userGroup, nil
	}
	return model.GetUserGroup(c.GetInt("id"), false)
}

func validateTokenRequestRoutingScope(c *gin.Context, token *model.Token) bool {
	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	err = service.ValidateTokenRoutingScopeForUser(c.Request.Context(), c.GetInt("id"), userGroup, token)
	switch {
	case err == nil:
		return true
	case errors.Is(err, service.ErrTokenGroupForbidden):
		common.ApiErrorI18n(c, i18n.MsgAuthTokenGroupForbidden, map[string]any{"Group": token.Group})
	case errors.Is(err, service.ErrTokenGroupDeprecated):
		common.ApiErrorI18n(c, i18n.MsgAuthTokenGroupDeprecated, map[string]any{"Group": token.Group})
	default:
		groupConfigReadFailure(c)
	}
	return false
}

func tokenListStatuses(c *gin.Context) ([]int, error) {
	filter := c.Query("status")
	if filter == "" {
		return nil, nil
	}
	statuses := make([]int, 0, 4)
	for value := range strings.SplitSeq(filter, ",") {
		status, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return nil, common.NewMessage("Invalid token status")
		}
		switch status {
		case common.TokenStatusEnabled, common.TokenStatusDisabled, common.TokenStatusExpired, common.TokenStatusExhausted:
			if !slices.Contains(statuses, status) {
				statuses = append(statuses, status)
			}
		default:
			return nil, common.NewMessage("Invalid token status")
		}
	}
	return statuses, nil
}

func GetAllTokens(c *gin.Context) {
	userId := c.GetInt("id")
	statuses, err := tokenListStatuses(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo := common.GetPageQuery(c)
	tokens, err := model.GetAllUserTokens(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), statuses...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, err := model.CountUserTokens(userId, statuses...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func SearchTokens(c *gin.Context) {
	userId := c.GetInt("id")
	statuses, err := tokenListStatuses(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var keyword, token string
	if c.Request.Method == http.MethodPost {
		var request struct {
			Keyword string `json:"keyword"`
			Token   string `json:"token"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		keyword, token = request.Keyword, request.Token
	} else {
		keyword, token = c.Query("keyword"), c.Query("token")
	}

	pageInfo := common.GetPageQuery(c)

	tokens, total, err := model.SearchUserTokens(userId, keyword, token, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), statuses...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func GetToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}

func GetTokenAutoGroups(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	guard, guardOK := beginControllerGroupConfigRead(c)
	if !guardOK {
		return
	}
	defer guard.Close()

	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groups := service.GetUserAutoGroup(userGroup)
	selectable := service.GetUserSelectableGroups(userGroup)
	rates, err := service.LoadEffectiveGroupRates(c.Request.Context(), c.GetInt("id"), userGroup, selectable)
	if err != nil {
		groupConfigReadFailure(c)
		return
	}
	abilities, err := model.GetEnabledAbilitiesWithChannelsForGroups(selectable)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	available := make(map[string]bool, len(selectable))
	for _, ability := range abilities {
		available[ability.Group] = true
	}
	metricsAvailable := perf_metrics_setting.GetSetting().Enabled
	now := time.Now()
	windowEnd := now.Unix()
	windowStart := windowEnd - windowEnd%3600 - 23*3600
	histories := map[string]perfmetrics.TokenGroupHistory{}
	if metricsAvailable {
		histories, windowStart, windowEnd, err = perfmetrics.QueryTokenGroupHistory(c.Request.Context(), selectable, now)
		if err != nil {
			common.SysError(common.LogText("failed to query token group performance: %v", err))
			metricsAvailable = false
		}
	}
	type groupDetail struct {
		Name        string     `json:"name"`
		Ratio       float64    `json:"ratio"`
		SuccessRate *float64   `json:"success_rate"`
		Available   bool       `json:"available"`
		History     []*float64 `json:"history"`
	}
	details := make([]groupDetail, 0, len(selectable))
	for _, group := range selectable {
		history, ok := histories[group]
		if !ok {
			history.History = make([]*float64, perfmetrics.TokenGroupHistoryBuckets)
		}
		details = append(details, groupDetail{
			Name: group, Ratio: rates[group].GroupRatio,
			SuccessRate: history.SuccessRate, Available: available[group], History: history.History,
		})
	}
	maxCount := setting.GetMaxTokenAutoGroups()
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	common.ApiSuccess(c, gin.H{
		"groups":                 groups,
		"max_count":              maxCount,
		"selectable_groups":      selectable,
		"group_details":          details,
		"metrics_available":      metricsAvailable,
		"window_start":           windowStart,
		"window_end":             windowEnd,
		"history_bucket_seconds": perfmetrics.TokenGroupHistoryBucketSeconds,
	})
}

type tokenModelScopeRequest struct {
	Group          string   `json:"group"`
	SmartRouting   bool     `json:"smart_routing"`
	AutoGroupsMode string   `json:"auto_groups_mode"`
	AutoGroups     []string `json:"auto_groups"`
}

func getRoutableModelsForGroups(user *model.UserBase, groups []string, allow func(*model.Channel, string) bool) ([]string, error) {
	abilities, err := model.GetEnabledAbilitiesWithChannelsForGroups(groups)
	if err != nil {
		return nil, err
	}
	if len(abilities) == 0 {
		return []string{}, nil
	}

	channelIDs := make([]int, 0, len(abilities))
	seenChannelIDs := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		if _, exists := seenChannelIDs[ability.ChannelId]; exists {
			continue
		}
		seenChannelIDs[ability.ChannelId] = struct{}{}
		channelIDs = append(channelIDs, ability.ChannelId)
	}
	channels, err := model.GetChannelsByIds(channelIDs)
	if err != nil {
		return nil, err
	}
	channelByID := make(map[int]*model.Channel, len(channels))
	for _, channel := range channels {
		if channel != nil && channel.Status == common.ChannelStatusEnabled {
			channelByID[channel.Id] = channel
		}
	}

	acceptUnsetPricing := user.GetSetting().AcceptUnsetRatioModel
	modelSet := make(map[string]struct{})
	for _, ability := range abilities {
		modelName := strings.TrimSpace(ability.Model)
		if modelName == "" {
			continue
		}
		if !acceptUnsetPricing && !relayhelper.HasModelBillingConfig(modelName) {
			continue
		}
		channel := channelByID[ability.ChannelId]
		if channel == nil {
			continue
		}
		if allow != nil && !allow(channel, modelName) {
			continue
		}
		modelSet[modelName] = struct{}{}
	}

	models := make([]string, 0, len(modelSet))
	for modelName := range modelSet {
		models = append(models, modelName)
	}
	sort.Strings(models)
	return models, nil
}

// PreviewTokenModels returns models routable by the API key routing scope being
// edited. It intentionally ignores model_limits so the selector can add any
// model that is valid for the selected group(s). Existing whitelist selections
// remain unchanged; the frontend marks unavailable selections for explicit review.
func PreviewTokenModels(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	request := tokenModelScopeRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}

	guard, guardOK := beginControllerGroupConfigRead(c)
	if !guardOK {
		return
	}
	defer guard.Close()

	userID := c.GetInt("id")
	user, err := model.GetUserCache(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	token := &model.Token{Group: request.Group, SmartRouting: request.SmartRouting}
	var groups []string
	if !token.SmartRouting && token.Group == "auto" && request.AutoGroupsMode == "custom" {
		groups = service.FilterUserTokenAutoGroups(user.Group, request.AutoGroups)
	} else {
		if token.Group == "auto" && len(request.AutoGroups) > 0 {
			if err := token.SetAutoGroups(request.AutoGroups); err != nil {
				common.ApiError(c, err)
				return
			}
		}
		groups, err = service.GetTokenEffectiveGroupsForUser(c.Request.Context(), userID, user.Group, token)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	models, err := getRoutableModelsForGroups(user, groups, nil)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	common.ApiSuccess(c, models)
}

func GetTokenModels(c *gin.Context) {
	app, ok := relay.NormalizeCCSwitchApp(c.Query("app"))
	if !ok {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	respondWithTokenModels(c, func(channel *model.Channel, modelName string) bool {
		return relay.SupportsCCSwitchModel(channel, app, modelName)
	})
}

// GetTokenRoutableModels exposes the owned token's effective model scope for
// console tools such as image generation, without a CLI-specific protocol filter.
func GetTokenRoutableModels(c *gin.Context) {
	respondWithTokenModels(c, nil)
}

func respondWithTokenModels(c *gin.Context, allow func(*model.Channel, string) bool) {
	guard, guardOK := beginControllerGroupConfigRead(c)
	if !guardOK {
		return
	}
	defer guard.Close()

	c.Header("Cache-Control", "no-store")
	tokenID, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenID <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	userID := c.GetInt("id")
	token, err := model.GetTokenByIds(tokenID, userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if allow == nil && c.Query("endpoint") == "image-generation" {
		if token.EffectiveStatus(common.GetTimestamp()) != common.TokenStatusEnabled {
			if finishControllerGroupConfigRead(c, guard) {
				common.ApiSuccess(c, []string{})
			}
			return
		}
		allow = model.NewPlaygroundChannelFilter(constant.EndpointTypeImageGeneration)
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groups, err := service.GetTokenEffectiveGroupsForUser(c.Request.Context(), userID, user.Group, token)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	modelLimit := token.GetModelLimitsMap()
	models, err := getRoutableModelsForGroups(user, groups, func(channel *model.Channel, modelName string) bool {
		if token.ModelLimitsEnabled && !middleware.TokenModelLimitAllows(modelLimit, modelName) {
			return false
		}
		return allow == nil || allow(channel, modelName)
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	common.ApiSuccess(c, models)
}

func GetTokenKey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{
		"key": token.GetFullKey(),
	})
}

func GetTokenStatus(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(tokenId, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"object":          "credit_summary",
		"total_granted":   token.RemainQuota,
		"total_used":      0, // not supported currently
		"total_available": token.RemainQuota,
		"expires_at":      expiredAt * 1000,
	})
}

func GetTokenUsage(c *gin.Context) {
	getTokenUsage(c, nil)
}

func getTokenUsage(c *gin.Context, source *model.LaneBillingDisplayV1) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "No Authorization header",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid Bearer token",
		})
		return
	}
	scopedView, scoped, err := readLaneBillingToken(c, source)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
		return
	}
	token := &scopedView.Token
	available, used := scopedView.Available, scopedView.Used
	if !scoped {
		token, err = model.GetTokenByKey(strings.TrimPrefix(parts[1], "sk-"), false)
		if err != nil {
			common.SysError(common.LogText("failed to get token by key: %s", err.Error()))
			common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
			return
		}
		available, used = token.RemainQuota, token.UsedQuota
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	data := gin.H{
		"object":               "token_usage",
		"name":                 token.Name,
		"total_granted":        available + used,
		"total_used":           used,
		"total_available":      available,
		"unlimited_quota":      token.UnlimitedQuota,
		"model_limits":         token.GetModelLimitsMap(),
		"model_limits_enabled": token.ModelLimitsEnabled,
		"expires_at":           expiredAt,
	}
	// 用户额度只在令牌允许时返回；IP 白名单已由 TokenAuthReadOnly 检查。
	if scopedView.Account != nil {
		data["user_quota"] = gin.H{
			"total_available": scopedView.Account.Available,
			"total_used":      scopedView.Account.Used,
		}
	} else if !scoped && token.AllowUserQuotaQuery {
		userQuota, err := model.GetUserQuota(token.UserId, false)
		var userUsedQuota int
		if err == nil {
			userUsedQuota, err = model.GetUserUsedQuota(token.UserId)
		}
		if err != nil {
			common.SysError(common.LogText("failed to get user quota: %s", err.Error()))
			common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
			return
		}
		data["user_quota"] = gin.H{
			"total_available": userQuota,
			"total_used":      userUsedQuota,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    true,
		"message": "ok",
		"data":    data,
	})
}

func AddToken(c *gin.Context) {
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	guard, ok := beginControllerGroupConfigRead(c)
	if !ok {
		return
	}
	defer guard.Close()
	token := request.Token
	token.ExpiredTime = -1
	request.applyEditableFields(&token)
	if token.ExpiredTime != -1 && token.ExpiredTime <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := request.applySingleGroupSettings(&token, true); err != nil {
		common.ApiError(c, err)
		return
	}
	if utf8.RuneCountInString(token.Name) > 50 {
		common.ApiErrorT(c, "Token name is too long")
		return
	}
	params := tokenAuditParams(c)
	params["name"] = token.Name
	// 非无限额度时，检查额度值是否超出有效范围
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorT(c, "Quota value cannot be negative")
			return
		}
		maxQuotaValue := maxTokenQuota()
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorT(c, "Quota value exceeds valid range, maximum is {{max}}", map[string]any{"max": logger.FormatQuota(maxQuotaValue)})
			return
		}
	}
	// 检查用户令牌数量是否已达上限
	maxTokens := operation_setting.GetMaxUserTokens()
	count, err := model.CountUserTokens(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if int(count) >= maxTokens {
		common.ApiErrorT(c, "Maximum number of tokens reached ({{max}})", map[string]any{"max": maxTokens})
		return
	}
	if !validateTokenRequestRoutingScope(c, &token) {
		return
	}
	key, err := common.GenerateKey()
	if err != nil {
		common.ApiErrorT(c, "Failed to generate token")
		common.SysLog(common.LogText("failed to generate token key: %s", err.Error()))
		return
	}
	cleanToken := model.Token{
		UserId:             c.GetInt("id"),
		Name:               token.Name,
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		AllowIps:           token.AllowIps,
		Group:              token.Group,
		CrossGroupRetry:    token.CrossGroupRetry,
		AutoGroups:         token.AutoGroups,
		SmartRouting:       token.SmartRouting,
		RoutingStrategy:    token.RoutingStrategy,
		NoRetryWithinGroup: token.NoRetryWithinGroup,
		ProjectLabel:       token.ProjectLabel,
	}
	cleanToken.AllowUserQuotaQuery = request.AllowUserQuotaQuery == nil || *request.AllowUserQuotaQuery
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	err = cleanToken.InsertAtGroupConfigVersion(c.GetInt64("auth_version"), guard.Version)
	if errors.Is(err, model.ErrGroupConfigChanged) {
		groupConfigReadFailure(c)
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["id"] = cleanToken.Id
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	err = token.DeleteForOwner(c.GetInt64("auth_version"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func UpdateToken(c *gin.Context) {
	userId := c.GetInt("id")
	statusOnly := c.Query("status_only")
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// The explicit revocation-only transport never admits an enable/full edit.
	if c.Query("disable_only") == "true" && (statusOnly == "" || request.Token.Status != common.TokenStatusDisabled) {
		common.ApiErrorT(c, "Invalid token status")
		return
	}
	disabling := statusOnly != "" && request.Token.Status == common.TokenStatusDisabled
	var guard *model.GroupConfigRead
	if !disabling {
		var ok bool
		guard, ok = beginControllerGroupConfigRead(c)
		if !ok {
			return
		}
		defer guard.Close()
	}
	token := request.Token
	request.applyEditableFields(&token)
	params := tokenAuditParams(c)
	if token.Id > 0 {
		params["id"] = token.Id
	}
	if statusOnly == "" && utf8.RuneCountInString(token.Name) > 50 {
		common.ApiErrorT(c, "Token name is too long")
		return
	}
	cleanToken, err := model.GetTokenByIds(token.Id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["name"] = cleanToken.Name
	if statusOnly != "" && token.Status != common.TokenStatusEnabled && token.Status != common.TokenStatusDisabled {
		common.ApiErrorT(c, "Invalid token status")
		return
	}
	previous := *cleanToken
	if statusOnly == "" && request.ExpectedVersion != nil && *request.ExpectedVersion != previous.CacheRevision {
		common.ApiErrorStatus(c, http.StatusConflict, model.ErrTokenSettingsChanged)
		return
	}
	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else {
		if err := request.applySingleGroupSettings(cleanToken, false); err != nil {
			common.ApiError(c, err)
			return
		}
		if request.Group != nil && (previous.SmartRouting || previous.Group == "auto" || previous.Group == "") && request.ExpectedVersion == nil {
			// An old client must not convert a rolling configuration without
			// explicitly adopting a group against the settings it just read.
			common.ApiErrorStatus(c, http.StatusConflict, model.ErrTokenSettingsChanged)
			return
		}
		// If you add more fields, please also update token.Update()
		request.applyEditableFields(cleanToken)
		if request.ExpiredTime != nil && cleanToken.ExpiredTime != -1 && cleanToken.ExpiredTime <= 0 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		if !cleanToken.UnlimitedQuota && (request.RemainQuota != nil || previous.UnlimitedQuota) {
			if cleanToken.RemainQuota < 0 {
				common.ApiErrorT(c, "Quota value cannot be negative")
				return
			}
			if cleanToken.RemainQuota > maxTokenQuota() {
				common.ApiErrorT(c, "Quota value exceeds valid range, maximum is {{max}}", map[string]any{"max": logger.FormatQuota(maxTokenQuota())})
				return
			}
		}
		if request.AllowUserQuotaQuery != nil {
			cleanToken.AllowUserQuotaQuery = *request.AllowUserQuotaQuery
		}
		if request.Group != nil && !validateTokenRequestRoutingScope(c, cleanToken) {
			return
		}
	}
	if disabling {
		previous.Status, err = cleanToken.DisableForOwner(c.GetInt64("auth_version"))
	} else if statusOnly != "" {
		userGroup, groupErr := getTokenRequestUserGroup(c)
		if groupErr != nil {
			common.ApiError(c, groupErr)
			return
		}
		grants, groupErr := model.ListUserExclusiveGroups(c.Request.Context(), userId)
		if groupErr != nil {
			groupConfigReadFailure(c)
			return
		}
		if !finishControllerGroupConfigRead(c, guard) {
			return
		}
		// The namespace version protects this grant snapshot. The callback
		// checks the locked token's current group without another DB checkout.
		previous.Status, err = cleanToken.UpdateStatusAtGroupConfigVersion(c.GetInt64("auth_version"), guard.Version, func(current *model.Token) error {
			if current.SmartRouting {
				return nil
			}
			if current.Group == "" && model.IsExclusiveGroup(userGroup) {
				return service.ErrTokenGroupForbidden
			}
			if model.IsExclusiveGroup(current.Group) {
				if !slices.Contains(grants, current.Group) {
					return service.ErrTokenGroupForbidden
				}
				if !ratio_setting.ContainsGroupRatio(current.Group) {
					return service.ErrTokenGroupDeprecated
				}
				return nil
			}
			return service.ValidateTokenRoutingScope(userGroup, current)
		})
	} else {
		if !finishControllerGroupConfigRead(c, guard) {
			return
		}
		var finiteEditMaximum *int
		if !cleanToken.UnlimitedQuota && (request.RemainQuota != nil || previous.UnlimitedQuota) {
			maximum := maxTokenQuota()
			finiteEditMaximum = &maximum
		}
		err = cleanToken.UpdateFromSnapshotAtGroupConfigVersion(previous, c.GetInt64("auth_version"), guard.Version, finiteEditMaximum)
	}
	if errors.Is(err, model.ErrTokenSettingsChanged) {
		common.ApiErrorStatus(c, http.StatusConflict, err)
		return
	}
	if errors.Is(err, model.ErrGroupConfigChanged) {
		groupConfigReadFailure(c)
		return
	}
	if errors.Is(err, service.ErrTokenGroupForbidden) || errors.Is(err, service.ErrTokenGroupDeprecated) {
		message := i18n.MsgAuthTokenGroupForbidden
		if errors.Is(err, service.ErrTokenGroupDeprecated) {
			message = i18n.MsgAuthTokenGroupDeprecated
		}
		common.ApiErrorI18n(c, message, map[string]any{"Group": cleanToken.Group})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["name"] = cleanToken.Name
	if statusOnly != "" {
		params["from"], params["to"] = previous.Status, cleanToken.Status
	} else {
		changedFields := []string{}
		for _, field := range []struct {
			name    string
			changed bool
		}{
			{"name", previous.Name != cleanToken.Name},
			{"expired_time", previous.ExpiredTime != cleanToken.ExpiredTime},
			{"remain_quota", request.RemainQuota != nil && previous.RemainQuota != token.RemainQuota},
			{"unlimited_quota", previous.UnlimitedQuota != cleanToken.UnlimitedQuota},
			{"model_limits_enabled", previous.ModelLimitsEnabled != cleanToken.ModelLimitsEnabled},
			{"model_limits", previous.ModelLimits != cleanToken.ModelLimits},
			{"allow_ips", (previous.AllowIps == nil) != (cleanToken.AllowIps == nil) ||
				(previous.AllowIps != nil && cleanToken.AllowIps != nil && *previous.AllowIps != *cleanToken.AllowIps)},
			{"allow_user_quota_query", previous.AllowUserQuotaQuery != cleanToken.AllowUserQuotaQuery},
			{"group", previous.Group != cleanToken.Group},
			{"cross_group_retry", previous.CrossGroupRetry != cleanToken.CrossGroupRetry},
			{"auto_groups", previous.AutoGroups != cleanToken.AutoGroups},
			{"smart_routing", previous.SmartRouting != cleanToken.SmartRouting},
			{"routing_strategy", previous.RoutingStrategy != cleanToken.RoutingStrategy},
			{"no_retry_within_group", previous.NoRetryWithinGroup != cleanToken.NoRetryWithinGroup},
			{"project_label", previous.ProjectLabel != cleanToken.ProjectLabel},
		} {
			if field.changed {
				changedFields = append(changedFields, field.name)
			}
		}
		params["changed_fields"] = changedFields
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    buildMaskedTokenResponse(cleanToken),
	})
}

type TokenBatch struct {
	Ids []int `json:"ids"`
}

func DeleteTokenBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	userId := c.GetInt("id")
	count, err := model.BatchDeleteTokens(tokenBatch.Ids, userId, c.GetInt64("auth_version"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["count"] = count
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}

func GetTokenKeysBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if len(tokenBatch.Ids) > 100 {
		common.ApiErrorT(c, "Too many items in batch request, maximum is {{max}}", map[string]any{"max": 100})
		return
	}
	userId := c.GetInt("id")
	tokens, err := model.GetTokenKeysByIds(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	keysMap := make(map[int]string)
	returnedIDs := make([]int, 0, len(tokens))
	for _, t := range tokens {
		keysMap[t.Id] = t.GetFullKey()
		returnedIDs = append(returnedIDs, t.Id)
	}
	params["count"] = len(tokens)
	params["returned_ids"] = returnedIDs
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{"keys": keysMap})
}
