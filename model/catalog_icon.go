package model

import (
	_ "embed"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

//go:embed catalog_icon_ids.json
var catalogIconManifest []byte

var catalogIconVariants = func() map[string][]string {
	var variants map[string][]string
	if err := common.Unmarshal(catalogIconManifest, &variants); err != nil {
		panic("invalid bundled catalog icon manifest")
	}
	return variants
}()

var catalogIconLegacyProp = regexp.MustCompile(`^(type|shape|size|role|aria-label|color|background|iconMultiple)=\{?('[\w #\-]+'|"[\w #\-]+"|[\w#-]+)\}?$`)

// PublicCatalogIconID returns only an ID for a component shipped with the UI.
// Historical safe scalar props can identify the component but are never public;
// URLs, arbitrary values and unknown identifiers fall back to the next source.
func PublicCatalogIconID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 128 {
		return ""
	}
	base, suffix, hasSuffix := strings.Cut(raw, ".")
	variants, ok := catalogIconVariants[base]
	if !ok {
		return ""
	}
	if !hasSuffix {
		return base
	}
	key := base
	segment, rest, more := strings.Cut(suffix, ".")
	if slices.Contains(variants, segment) {
		key += "." + segment
	} else {
		// An absent but known variant historically renders as the base.
		switch segment {
		case "Mono", "Avatar", "Brand", "BrandColor", "Color", "Combine", "Text", "TextCn", "TextColor", "Simple", "Morden":
		default:
			if !catalogIconLegacyProp.MatchString(segment) {
				return ""
			}
		}
	}
	if more {
		for prop := range strings.SplitSeq(rest, ".") {
			if !catalogIconLegacyProp.MatchString(prop) {
				return ""
			}
		}
	}
	return key
}
