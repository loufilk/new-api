package controller

import (
	"errors"
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelfingerprint"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type fingerprintRouteOption struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
	Model string `json:"model"`
}
type fingerprintSettingsCatalogue struct {
	Groups        []string                 `json:"groups"`
	ModelsByGroup map[string][]string      `json:"models_by_group"`
	Routes        []fingerprintRouteOption `json:"routes"`
}

// This catalogue contains identifiers, not complete channel or credential objects.
func fingerprintSiteCatalogue() (fingerprintSettingsCatalogue, error) {
	result := fingerprintSettingsCatalogue{Groups: []string{}, ModelsByGroup: map[string][]string{}, Routes: []fingerprintRouteOption{}}
	for group := range ratio_setting.GetGroupRatioCopy() {
		if group != "auto" && group != "" {
			result.Groups = append(result.Groups, group)
		}
	}
	slices.Sort(result.Groups)
	abilities, err := model.GetEnabledAbilitiesWithChannelsForGroups(result.Groups)
	if err != nil {
		return result, err
	}
	var channels []struct {
		ID   int
		Name string
	}
	if err = model.DB.Model(&model.Channel{}).Select("id", "name").Where("status = ?", common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return result, err
	}
	names := map[int]string{}
	for _, channel := range channels {
		names[channel.ID] = channel.Name
	}
	for _, ability := range abilities {
		// Automatic probes currently use the standard OpenAI HTTP adaptor. Other
		// transports require a separately reviewed bounded operational billing path.
		if ability.ChannelType != constant.ChannelTypeOpenAI {
			continue
		}
		if !slices.Contains(result.ModelsByGroup[ability.Group], ability.Model) {
			result.ModelsByGroup[ability.Group] = append(result.ModelsByGroup[ability.Group], ability.Model)
		}
		result.Routes = append(result.Routes, fingerprintRouteOption{ability.ChannelId, names[ability.ChannelId], ability.Group, ability.Model})
	}
	for group := range result.ModelsByGroup {
		slices.Sort(result.ModelsByGroup[group])
	}
	return result, nil
}

func GetModelFingerprintSettings(c *gin.Context) {
	if _, ok := requireBrowserSession(c); !ok {
		return
	}
	config, err := model.GetModelFingerprintSiteConfig(c.Request.Context())
	if err != nil {
		modelTestError(c, 503, "model_test_settings_unavailable")
		return
	}
	catalogue, err := fingerprintSiteCatalogue()
	if err != nil {
		modelTestError(c, 503, "catalogue_unavailable")
		return
	}
	body, err := common.Marshal(config)
	if err != nil {
		modelTestError(c, 503, "model_test_settings_unavailable")
		return
	}
	values := gin.H{}
	if err = common.Unmarshal(body, &values); err != nil {
		modelTestError(c, 503, "model_test_settings_unavailable")
		return
	}
	values["catalogue"] = catalogue
	values["engine_ready"] = modelfingerprint.Ready() == nil
	values["default_model_filter"] = "gpt"
	values["budget_note"] = "Quota reservations are not a provider spending cap. Unknown usage stops automatic tests."
	common.ApiSuccess(c, values)
}

func UpdateModelFingerprintSettings(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	var request struct {
		Enabled           bool                               `json:"enabled"`
		Version           int64                              `json:"version"`
		ExpectedVersion   int64                              `json:"expected_version"`
		AutomaticEnabled  bool                               `json:"automatic_enabled"`
		DefaultPeriodSecs int64                              `json:"default_period_secs"`
		DailyBudgetQuota  int64                              `json:"daily_budget_quota"`
		Targets           []model.ModelFingerprintSiteTarget `json:"targets"`
	}
	if common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10), &request) != nil {
		modelTestError(c, 400, "invalid_selection")
		return
	}
	guard, ok := beginControllerGroupConfigRead(c)
	if !ok {
		return
	}
	defer guard.Close()
	catalogue, err := fingerprintSiteCatalogue()
	if err != nil {
		modelTestError(c, 503, "catalogue_unavailable")
		return
	}
	for _, target := range request.Targets {
		if request.AutomaticEnabled && target.Enabled {
			if _, pricingErr := fingerprintSitePricing(target); pricingErr != nil {
				modelTestError(c, http.StatusBadRequest, "automatic_static_pricing_required")
				return
			}
		}
		if !slices.ContainsFunc(catalogue.Routes, func(route fingerprintRouteOption) bool {
			return route.ID == target.ChannelID && route.Group == target.Group && route.Model == target.Model
		}) {
			modelTestError(c, 400, "model_or_group_unavailable")
			return
		}
	}
	if request.AutomaticEnabled && (!request.Enabled || modelfingerprint.Ready() != nil) {
		modelTestError(c, 400, "fingerprint_unavailable")
		return
	}
	config := model.ModelFingerprintSiteConfig{Enabled: request.Enabled, AutomaticEnabled: request.AutomaticEnabled, DefaultPeriodSecs: request.DefaultPeriodSecs, DailyBudgetQuota: request.DailyBudgetQuota, Targets: request.Targets}
	if err = model.ValidateModelFingerprintSiteConfig(&config); err != nil {
		modelTestError(c, 400, "invalid_selection")
		return
	}
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	_, err = model.UpdateModelFingerprintSiteConfig(c.Request.Context(), request.ExpectedVersion, identity.UserID, config)
	if errors.Is(err, model.ErrFingerprintSiteConflict) {
		modelTestError(c, 409, "configuration_changed")
		return
	}
	if err != nil {
		modelTestError(c, 503, "model_test_settings_unavailable")
		return
	}
	GetModelFingerprintSettings(c)
}

func GetSiteModelFingerprints(c *gin.Context) {
	runs, err := model.ListModelFingerprintSitePublicRuns(c.Request.Context())
	if err != nil {
		modelTestError(c, 503, "history_unavailable")
		return
	}
	config, err := model.GetModelFingerprintSiteConfig(c.Request.Context())
	if err != nil {
		modelTestError(c, 503, "model_test_settings_unavailable")
		return
	}
	targets := make([]gin.H, 0, len(config.Targets))
	for _, target := range config.Targets {
		period := target.PeriodSecs
		if period == 0 {
			period = config.DefaultPeriodSecs
		}
		targets = append(targets, gin.H{"id": target.ID, "group": target.Group, "model": target.Model, "enabled": target.Enabled, "period_secs": period})
	}
	common.ApiSuccess(c, gin.H{"items": runs, "targets": targets, "automatic_enabled": config.AutomaticEnabled, "enabled": config.Enabled})
}
