package model

import (
	"fmt"
	"maps"

	"github.com/QuantumNous/new-api/common"
)

// Both spellings are accepted by the existing config registry and share runtime
// maps. Never leave an old persisted alias able to resurrect a renamed group.
var groupOptionAliases = map[string]string{
	"group_ratio_setting.group_ratio":       "GroupRatio",
	"group_ratio_setting.group_group_ratio": "GroupGroupRatio",
}

func canonicalGroupOptionKey(key string) string {
	if canonical, ok := groupOptionAliases[key]; ok {
		return canonical
	}
	return key
}

func normalizeGroupAliasRequest(request GroupRenameApplyRequest) (GroupRenameApplyRequest, error) {
	request.Options = maps.Clone(request.Options)
	for alias, canonical := range groupOptionAliases {
		value, ok := request.Options[alias]
		if !ok {
			continue
		}
		if other, exists := request.Options[canonical]; exists && other != value {
			return request, fmt.Errorf("%w: conflicting %s and %s", ErrGroupRenameInvalidOption, canonical, alias)
		}
		request.Options[canonical] = value
	}
	return request, nil
}

// Keep numeric literals exact: JSON round trips through float64 would hide a
// conflicting high-precision price. Cosmetic differences can be reconciled by
// an explicit settings save; a rename alone must not guess which price to keep.
func validateGroupOptionAliases(current, provided map[string]string) error {
	for alias, canonical := range groupOptionAliases {
		value, exists := current[alias]
		if !exists {
			continue
		}
		if _, hasCanonical := current[canonical]; !hasCanonical {
			continue
		}
		if _, explicit := provided[canonical]; explicit {
			continue
		}
		var aliasJSON, canonicalJSON map[string]common.RawMessage
		if err := common.UnmarshalJsonStr(value, &aliasJSON); err != nil {
			return err
		}
		if err := common.UnmarshalJsonStr(current[canonical], &canonicalJSON); err != nil {
			return err
		}
		a, err := common.Marshal(aliasJSON)
		if err != nil {
			return err
		}
		b, err := common.Marshal(canonicalJSON)
		if err != nil {
			return err
		}
		if string(a) != string(b) {
			return fmt.Errorf("%w: conflicting %s and %s; explicitly save the intended value before renaming", ErrGroupRenameInvalidOption, canonical, alias)
		}
	}
	return nil
}

func synchronizeGroupOptionAliases(current, updates map[string]string) {
	for alias, canonical := range groupOptionAliases {
		value, changed := updates[canonical]
		if !changed {
			continue
		}
		_, exists := current[alias]
		_, explicit := updates[alias]
		if exists || explicit {
			updates[alias] = value
		}
	}
}
