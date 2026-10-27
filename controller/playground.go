package controller

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func Playground(c *gin.Context) {
	var newAPIError *types.NewAPIError

	defer func() {
		if newAPIError != nil {
			c.JSON(newAPIError.StatusCode, gin.H{
				"error": newAPIError.ToOpenAIError(common.UpstreamAddresses(c)...),
			})
		}
	}()

	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		newAPIError = types.NewError(errors.New(i18n.T(c, i18n.MsgRelayAccessTokenUnsupported)), types.ErrorCodeAccessDenied, types.ErrOptionWithSkipRetry())
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAI, nil, nil)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		return
	}

	userId := c.GetInt("id")

	// Write user context to ensure acceptUnsetRatio is available
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		return
	}
	userCache.WriteContext(c)
	if userCache.Quota <= 0 {
		newAPIError = types.NewErrorWithStatusCode(
			errors.New("钱包余额不足，请充值后使用试验场"),
			types.ErrorCodeInsufficientUserQuota,
			http.StatusForbidden,
			types.ErrOptionWithSkipRetry(),
			types.ErrOptionWithNoRecordErrorLog(),
		)
		return
	}

	tempToken := &model.Token{
		UserId: userId,
		Name:   fmt.Sprintf("playground-%s", relayInfo.UsingGroup),
		Group:  relayInfo.UsingGroup,
	}
	_ = middleware.SetupContextForToken(c, tempToken)

	Relay(c, types.RelayFormatOpenAI)
}

type playgroundGroupInfo struct {
	Name  string  `json:"name"`
	Desc  string  `json:"desc"`
	Ratio float64 `json:"ratio"`
}

func PlaygroundBootstrap(c *gin.Context) {
	userID := c.GetInt("id")
	user, err := model.GetUserById(userID, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userCache, err := model.GetUserCache(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	groups := service.GetUserUsableGroups(userCache.Group)
	// 复用定价目录投影：仅返回已通过状态、计费可用性与能力投影的模型，
	// 再与各分组的可路由模型求交集，避免引入定价目录外的模型。
	pricing := model.GetPricing()
	pricingSet := make(map[string]struct{}, len(pricing))
	for _, p := range pricing {
		pricingSet[p.ModelName] = struct{}{}
	}

	groupInfos := make([]playgroundGroupInfo, 0, len(groups))
	modelsByGroup := make(map[string][]string, len(groups))
	modelSet := make(map[string]struct{})
	for name, desc := range groups {
		groupInfos = append(groupInfos, playgroundGroupInfo{Name: name, Desc: desc, Ratio: service.GetUserGroupRatio(userCache.Group, name)})
		groupModels := playgroundModelsForGroup(name, pricingSet)
		modelsByGroup[name] = groupModels
		for _, modelName := range groupModels {
			modelSet[modelName] = struct{}{}
		}
	}
	sort.Slice(groupInfos, func(i, j int) bool { return groupInfos[i].Name < groupInfos[j].Name })
	models := make([]string, 0, len(modelSet))
	for modelName := range modelSet {
		models = append(models, modelName)
	}
	sort.Strings(models)

	common.ApiSuccess(c, gin.H{
		"id": user.Id, "username": user.Username, "display_name": user.DisplayName,
		"quota": userCache.Quota, "quota_per_unit": common.QuotaPerUnit,
		"quota_display_type":     operation_setting.GetQuotaDisplayType(),
		"currency_symbol":        operation_setting.GetCurrencySymbol(),
		"currency_exchange_rate": operation_setting.GetUsdToCurrencyRate(operation_setting.USDExchangeRate),
		"groups":                 groupInfos, "models": models, "models_by_group": modelsByGroup,
	})
}

// playgroundModelsForGroup 返回分组内「已启用、可路由且存在于定价目录」的模型。
// 结果排序并去重，且只包含当前可用分组的能力，不泄露其他分组的模型。
func playgroundModelsForGroup(group string, pricingSet map[string]struct{}) []string {
	raw := model.GetGroupEnabledModels(group)
	seen := make(map[string]struct{}, len(raw))
	result := make([]string, 0, len(raw))
	for _, name := range raw {
		if _, ok := pricingSet[name]; !ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
