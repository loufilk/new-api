package service

import (
	"context"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
)

// LoadEffectiveGroupRates quotes only the requested group names. Callers must
// filter those names by their independently authorized routing scope first.
func LoadEffectiveGroupRates(ctx context.Context, userID int, userGroup string, groups []string) (map[string]types.GroupRatioInfo, error) {
	policy, err := model.LoadEffectiveUserGroupPricing(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]types.GroupRatioInfo, len(groups))
	for _, group := range groups {
		result[group] = policy.ResolveGroupRatio(userGroup, group)
	}
	return result, nil
}
