package model

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

const ModelTestEnabledKey = "ModelTestEnabled"

var ErrModelTestsDisabled = errors.New("Model performance tests are disabled")
var ErrModelTestSettingsUnavailable = errors.New("Model performance test settings are unavailable")

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
