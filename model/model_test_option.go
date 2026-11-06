package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const ModelTestEnabledKey = "ModelTestEnabled"

var ErrModelTestsDisabled = errors.New("Model performance tests are disabled")
var ErrModelTestSettingsUnavailable = errors.New("Model performance test settings are unavailable")
var ErrModelTestTargetUnavailable = errors.New("Model or group is unavailable for testing")

// ModelTestEnabled is intentionally independent of the option cache and of the
// account pool, ordinary playground, relay and referral switches. Old sites with
// no row are off; read operations never insert defaults or rewrite other data.
func ModelTestEnabled(ctx context.Context) (bool, error) {
	if DB == nil {
		return false, ErrModelTestSettingsUnavailable
	}
	return modelTestEnabled(DB.WithContext(ctx))
}

func modelTestEnabled(db *gorm.DB) (bool, error) {
	var option Option
	err := db.Where(Option{Key: ModelTestEnabledKey}).Limit(1).Find(&option).Error
	return err == nil && option.Value == "true", err
}

func RequireModelTestEnabled(ctx context.Context) error {
	enabled, err := ModelTestEnabled(ctx)
	if err != nil {
		return ErrModelTestSettingsUnavailable
	}
	if !enabled {
		return ErrModelTestsDisabled
	}
	return nil
}

// RequireModelTestTarget authorizes one invocation against current durable
// settings, independently of the quote, catalogue and option caches. Each
// challenge must be admitted separately; an already admitted upstream request
// may finish and settle after an administrator revokes its target.
func RequireModelTestTarget(ctx context.Context, group, modelName string) error {
	if DB == nil {
		return ErrModelTestSettingsUnavailable
	}
	return requireModelTestTarget(DB.WithContext(ctx), group, modelName)
}

func requireModelTestTarget(db *gorm.DB, group, modelName string) error {
	var permission struct {
		Value       string
		TargetsJSON string
	}
	// One statement is the admission snapshot. Reading the switch and targets
	// separately could combine values from opposite sides of a settings save.
	targets := db.Model(&ModelFingerprintSiteConfig{}).Select("targets_json").Where("id = ?", 1)
	if err := db.Model(&Option{}).Select("value, (?) AS targets_json", targets).
		Where(Option{Key: ModelTestEnabledKey}).Limit(1).Find(&permission).Error; err != nil {
		return ErrModelTestSettingsUnavailable
	}
	if permission.Value != "true" {
		return ErrModelTestsDisabled
	}
	config := ModelFingerprintSiteConfig{Enabled: true}
	if permission.TargetsJSON != "" {
		if err := common.UnmarshalJsonStr(permission.TargetsJSON, &config.Targets); err != nil {
			return ErrModelTestSettingsUnavailable
		}
	}
	for _, target := range config.EnabledTargets() {
		if target.Group == group && target.Model == modelName {
			return nil
		}
	}
	return ErrModelTestTargetUnavailable
}
