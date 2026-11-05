package controller

import (
	"errors"
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type fingerprintSettingsCatalogue struct {
	Groups        []string            `json:"groups"`
	ModelsByGroup map[string][]string `json:"models_by_group"`
}

// Users select a group and model. Channel priority, weight and availability are
// resolved by the ordinary request router when a run starts.
func fingerprintSiteCatalogue() (fingerprintSettingsCatalogue, error) {
	result := fingerprintSettingsCatalogue{Groups: []string{}, ModelsByGroup: map[string][]string{}}
	for group := range ratio_setting.GetGroupRatioCopy() {
		if group != "auto" && group != "" {
			result.Groups = append(result.Groups, group)
			result.ModelsByGroup[group] = []string{}
		}
	}
	slices.Sort(result.Groups)
	abilities, err := model.GetEnabledAbilitiesWithChannelsForGroups(result.Groups)
	if err != nil {
		return result, err
	}
	for _, ability := range abilities {
		if !slices.Contains(result.ModelsByGroup[ability.Group], ability.Model) {
			result.ModelsByGroup[ability.Group] = append(result.ModelsByGroup[ability.Group], ability.Model)
		}
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
	for i := range config.Targets {
		config.Targets[i].ChannelID = 0
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
	values["engine_ready"] = modelFingerprintScorer.ready() == nil
	values["default_model_filter"] = "gpt"
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
	for i := range request.Targets {
		target := &request.Targets[i]
		target.ChannelID = 0
		if !slices.Contains(catalogue.ModelsByGroup[target.Group], target.Model) {
			modelTestError(c, 400, "model_or_group_unavailable")
			return
		}
	}
	if request.AutomaticEnabled && (!request.Enabled || modelFingerprintScorer.ready() != nil) {
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
	userRuns, err := model.ListPublicModelTestRuns(c.Request.Context())
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
	common.ApiSuccess(c, gin.H{"items": runs, "user_items": userRuns, "targets": targets, "automatic_enabled": config.AutomaticEnabled, "enabled": config.Enabled})
}
