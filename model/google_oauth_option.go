package model

import (
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var googleOptionWriteMu sync.Mutex

// Google credentials must never reach a SQL logger, including DEBUG=true. Only
// publish the new runtime configuration after its durable write commits.
func updateGoogleOAuthOption(key, value string) error {
	googleOptionWriteMu.Lock()
	defer googleOptionWriteMu.Unlock()
	value = strings.TrimSpace(value)
	switch key {
	case "google.client_secret":
		if value == "" {
			return nil
		} // Empty preserves the previously configured secret.
		if len(value) > 4096 {
			return common.NewMessage("Invalid parameters")
		}
	case "google.client_id":
		if len(value) > 255 {
			return common.NewMessage("Invalid parameters")
		}
	case "GoogleOAuthEnabled":
		if value != "true" && value != "false" {
			return common.NewMessage("Invalid parameters")
		}
	default:
		return common.NewMessage("Invalid parameters")
	}
	option := Option{Key: key, Value: value}
	if err := DB.Session(&gorm.Session{Logger: logger.Discard}).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&option).Error; err != nil {
		// Some database errors contain a complete rejected row. Do not log/return it.
		return common.NewMessage("Failed to save Google OAuth settings. Please try again.")
	}
	return updateOptionMap(key, value)
}
