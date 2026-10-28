package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm/clause"
)

// HasConfiguredModelForGroups checks routing configuration, not availability.
// Disabled abilities deliberately count: a temporary outage must not be
// presented as a client's group/model mismatch. Cache misses are not evidence
// of absence, so this error-path-only probe reads the authoritative abilities.
// The caller must provide a short, bounded context and authorized groups only.
func HasConfiguredModelForGroups(ctx context.Context, groups []string, modelName string) (bool, error) {
	if len(groups) == 0 || modelName == "" {
		return false, errors.New("model support scope is empty")
	}
	groupValues := make([]any, 0, len(groups))
	for _, group := range groups {
		if group == "" || group == "auto" {
			return false, errors.New("model support scope is unresolved")
		}
		groupValues = append(groupValues, group)
	}
	if common.MemoryCacheEnabled && channelCacheExpired() {
		return false, ErrChannelCacheStale
	}
	if DB == nil {
		return false, errors.New("model support database is not initialized")
	}
	modelNames := []string{modelName}
	if normalized := ratio_setting.RoutingMatchModelName(modelName); normalized != "" && normalized != modelName {
		modelNames = append(modelNames, normalized)
	}
	var channelIDs []int
	result := DB.WithContext(ctx).Model(&Ability{}).
		Where(clause.IN{Column: clause.Column{Name: "group"}, Values: groupValues}).
		Where("model IN ?", modelNames).
		Limit(1).Pluck("channel_id", &channelIDs)
	return len(channelIDs) > 0, result.Error
}
