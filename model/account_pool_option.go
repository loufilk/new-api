package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
)

const AccountPoolEnabledKey = "AccountPoolEnabled"

// AccountPoolEnabled reads the primary database, not the periodically refreshed
// OptionMap. A missing/invalid setting is opt-out, including on existing sites.
// Reading this flag never inserts a row or changes connection/history settings.
func AccountPoolEnabled(ctx context.Context) (bool, error) {
	if DB == nil {
		return false, errors.New("account pool settings unavailable")
	}
	var option Option
	err := DB.WithContext(ctx).Where(Option{Key: AccountPoolEnabledKey}).Limit(1).Find(&option).Error
	return err == nil && option.Value == "true", err
}

func validateOptInFeatureEnabled(value string) error {
	if value != "true" && value != "false" {
		return common.NewMessage("Invalid parameters")
	}
	return nil
}
