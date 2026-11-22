package ratio_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// ValidateGroupSpecialUsableGroupJSON rejects contradictory grants and denials
// on writes, without normalizing historical configuration or choosing a winner.
func ValidateGroupSpecialUsableGroupJSON(raw string) error {
	var groups map[string]map[string]string
	if err := common.UnmarshalJsonStr(raw, &groups); err != nil {
		return err
	}
	for userGroup, rules := range groups {
		for key := range rules {
			target, hidden := strings.CutPrefix(key, "-:")
			if !hidden {
				continue
			}
			_, bareVisible := rules[target]
			_, prefixedVisible := rules["+:"+target]
			if bareVisible || prefixedVisible {
				return common.NewMessage("Group {{target}} cannot be both extra visible and hidden for {{group}}. Remove one rule before saving.", map[string]any{"target": target, "group": userGroup})
			}
		}
	}
	return nil
}
