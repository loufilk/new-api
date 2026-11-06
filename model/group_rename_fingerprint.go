package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Rename only the current allowlist. Completed observations, admitted run
// identities and accounting retain the group that authorized their original work.
// The caller holds the namespace and fingerprint admission gates.
func renameGroupFingerprintTargets(tx *gorm.DB, plan *groupRenamePlan, actorID int) error {
	if len(plan.mappings) == 0 {
		return nil
	}
	var config ModelFingerprintSiteConfig
	if err := lockForUpdate(tx).First(&config, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if config.TargetsJSON != "" {
		if err := common.UnmarshalJsonStr(config.TargetsJSON, &config.Targets); err != nil {
			return err
		}
	}
	changed := false
	for i := range config.Targets {
		if renamed, ok := plan.sources[config.Targets[i].Group]; ok {
			config.Targets[i].Group = renamed
			changed = true
		}
	}
	if !changed {
		return nil
	}
	data, err := common.Marshal(config.Targets)
	if err != nil {
		return err
	}
	config.Enabled, err = modelTestEnabled(tx)
	if err != nil {
		return err
	}
	config.Version++
	config.TargetsJSON = string(data)
	config.UpdatedAt, config.UpdatedBy = time.Now().Unix(), actorID
	if err := tx.Model(&ModelFingerprintSiteConfig{}).Where("id = ?", 1).Updates(map[string]any{
		"targets_json": config.TargetsJSON, "version": config.Version,
		"updated_at": config.UpdatedAt, "updated_by": config.UpdatedBy,
	}).Error; err != nil {
		return err
	}
	audit, err := common.Marshal(config)
	if err != nil {
		return err
	}
	return tx.Create(&ModelFingerprintSiteAudit{Version: config.Version, ActorID: actorID,
		CreatedAt: config.UpdatedAt, Configuration: string(audit)}).Error
}
