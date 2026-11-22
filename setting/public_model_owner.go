package setting

import (
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

const PublicModelOwnedByOptionKey = "PublicModelOwnedBy"
const DefaultPublicModelOwnedBy = "chuanapi"

var publicModelOwnedBy atomic.Value

// NormalizePublicModelOwnedBy keeps the public identity independent of channel
// metadata. Clearing the option restores the site default, never the provider.
func NormalizePublicModelOwnedBy(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultPublicModelOwnedBy, nil
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 ||
		strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return "", common.NewMessage("Public model owner must be at most 128 characters and contain no control or formatting characters")
	}
	return value, nil
}

// LoadPublicModelOwnedBy also handles old or externally edited database values.
// Invalid configuration must never restore an internal channel/provider name.
func LoadPublicModelOwnedBy(value string) {
	normalized, err := NormalizePublicModelOwnedBy(value)
	if err != nil {
		normalized = DefaultPublicModelOwnedBy
	}
	publicModelOwnedBy.Store(normalized)
}

func GetPublicModelOwnedBy() string {
	if value, ok := publicModelOwnedBy.Load().(string); ok && value != "" {
		return value
	}
	return DefaultPublicModelOwnedBy
}
