package setting

import (
	"slices"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

const SensitiveCheckExemptGroupsOptionKey = "SensitiveCheckExemptGroups"

var sensitiveCheckExemptGroups atomic.Value

// NormalizeSensitiveCheckExemptGroups accepts exact, case-sensitive group names,
// one per line. "auto" is a routing strategy, not an effective request group.
func NormalizeSensitiveCheckExemptGroups(value string) (string, error) {
	groups := make([]string, 0)
	for group := range strings.SplitSeq(value, "\n") {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if group == "auto" || group == "*" || !utf8.ValidString(group) ||
			utf8.RuneCountInString(group) > 128 || strings.ContainsFunc(group, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
			return "", common.NewMessage("Filtering exemptions require exact group names, one per line; auto and wildcards are not allowed")
		}
		if !slices.Contains(groups, group) {
			groups = append(groups, group)
		}
	}
	return strings.Join(groups, "\n"), nil
}

// Invalid persisted configuration fails closed: no group is exempt.
func LoadSensitiveCheckExemptGroups(value string) {
	normalized, err := NormalizeSensitiveCheckExemptGroups(value)
	if err != nil {
		normalized = ""
	}
	sensitiveCheckExemptGroups.Store(normalized)
}

func SensitiveCheckExemptGroupsToString() string {
	value, _ := sensitiveCheckExemptGroups.Load().(string)
	return value
}

func IsSensitiveCheckExemptGroup(group string) bool {
	if group == "" || group == "auto" {
		return false
	}
	for configured := range strings.SplitSeq(SensitiveCheckExemptGroupsToString(), "\n") {
		if configured == group {
			return true
		}
	}
	return false
}
