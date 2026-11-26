package model

import (
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// ProjectEndpointGroups returns the model-level protocols and grouped live
// channel protocols visible in a catalogue. A key of groupAliases is an allowed
// source group; its value is either its own name or a public display-only tier.
// It never modifies the shared snapshot. Model-level endpoint declarations are
// kept as documentation in the model overview, not asserted for every group.
func (p Pricing) ProjectEndpointGroups(groupAliases map[string]string) ([]constant.EndpointType, map[string][]constant.EndpointType) {
	if p.channelEndpointsByGroup == nil {
		return p.SupportedEndpointTypes, nil
	}
	visible := make(map[constant.EndpointType]bool)
	projected := make(map[string][]constant.EndpointType)
	for group, types := range p.channelEndpointsByGroup {
		key, ok := groupAliases[group]
		if !ok {
			continue
		}
		set := projected[key]
		if set == nil {
			set = []constant.EndpointType{}
		}
		for _, kind := range types {
			visible[kind] = true
			if !slices.Contains(set, kind) {
				set = append(set, kind)
			}
		}
		projected[key] = set
	}
	// Existing presentations may store type arrays that the adapter-wide cache
	// historically ignores. Preserve its priority, then extend only this
	// projection with additional explicitly declared kinds in stable order.
	order := p.SupportedEndpointTypes
	for _, kind := range slices.Sorted(maps.Keys(p.declaredEndpointInfo)) {
		if !slices.Contains(order, kind) {
			if len(order) == len(p.SupportedEndpointTypes) {
				order = slices.Clone(order)
			}
			order = append(order, kind)
		}
	}
	// Use the existing model-level protocol order for all projections, including
	// when multiple source groups share one public display-only tier.
	byGroup := make(map[string][]constant.EndpointType, len(projected))
	for key, types := range projected {
		ordered := make([]constant.EndpointType, 0, len(types))
		for _, kind := range order {
			if slices.Contains(types, kind) {
				ordered = append(ordered, kind)
			}
		}
		byGroup[key] = ordered
	}
	union := make([]constant.EndpointType, 0, len(p.SupportedEndpointTypes))
	for _, kind := range order {
		if _, declared := p.declaredEndpointInfo[kind]; visible[kind] || declared {
			union = append(union, kind)
		}
	}
	return union, byGroup
}

// Gateway adapter capabilities describe the paths it can forward, not what a
// particular upstream model accepts. Never guess from a model name prefix.
// The two DeepSeek tool IDs are reserved by the router itself (no body model).
func gatewayCatalogEndpoints(transport []constant.EndpointType, declared map[constant.EndpointType]common.EndpointInfo, name string) []constant.EndpointType {
	if name == constant.DeepSeekSearchModel || name == constant.DeepSeekFetchModel {
		return transport
	}
	var confirmed []constant.EndpointType
	for _, kind := range transport {
		if _, ok := declared[kind]; ok {
			confirmed = append(confirmed, kind)
		}
	}
	// newapi.Adaptor explicitly implements image and embedding requests, even
	// though the generic list does not always include those formats.
	for _, kind := range []constant.EndpointType{constant.EndpointTypeImageGeneration, constant.EndpointTypeEmbeddings} {
		if _, ok := declared[kind]; ok && !slices.Contains(confirmed, kind) {
			confirmed = append(confirmed, kind)
		}
	}
	return confirmed
}

// Accept both model metadata forms: arrays of protocol kinds (using their
// canonical paths), and objects specifying a local API path for each kind.
// This is display-only. Invalid or off-site paths cannot be public examples.
func parseCatalogEndpointDeclarations(raw string) map[constant.EndpointType]common.EndpointInfo {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var data any
	if common.UnmarshalJsonStr(raw, &data) != nil {
		return nil
	}
	entries := make(map[constant.EndpointType]common.EndpointInfo)
	switch value := data.(type) {
	case []any:
		for _, element := range value {
			name, ok := element.(string)
			if !ok || strings.TrimSpace(name) == "" {
				continue
			}
			kind := constant.EndpointType(strings.TrimSpace(name))
			info, _ := common.GetDefaultEndpointInfo(kind)
			entries[kind] = info // Unknown kind may be shown without a guessed URL.
		}
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(value)) {
			kind := constant.EndpointType(strings.TrimSpace(key))
			if kind == "" {
				continue
			}
			info := common.EndpointInfo{Method: "POST"}
			switch v := value[key].(type) {
			case string:
				info.Path = v
			case map[string]any:
				info.Path, _ = v["path"].(string)
				if rawMethod, exists := v["method"]; exists {
					method, ok := rawMethod.(string)
					if !ok {
						continue
					}
					info.Method = strings.ToUpper(method)
				}
			default:
				continue
			}
			if len(info.Path) > 2048 || !strings.HasPrefix(info.Path, "/") || strings.HasPrefix(info.Path, "//") ||
				strings.ContainsAny(info.Path, "\\?#") || strings.ContainsFunc(info.Path, unicode.IsControl) {
				continue
			}
			switch info.Method {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
				entries[kind] = info
			}
		}
	}
	return entries
}

// ProjectEndpointPaths derives examples from the models actually visible in
// this response. A private model can never override a public model's paths.
// The wire format only has one path per kind: omit conflicting public paths
// rather than randomly showing one model's path for another.
func (s PricingCatalogSnapshot) ProjectEndpointPaths(visible map[string][]constant.EndpointType) map[string]common.EndpointInfo {
	result := make(map[string]common.EndpointInfo)
	conflicts := make(map[string]bool)
	for _, row := range s.Rows {
		for _, kind := range visible[row.ModelName] {
			key := string(kind)
			if conflicts[key] {
				continue
			}
			info, explicit := row.declaredEndpointInfo[kind]
			if !explicit {
				info, _ = common.GetDefaultEndpointInfo(kind)
			}
			if previous, exists := result[key]; exists && previous != info {
				delete(result, key)
				conflicts[key] = true
				continue
			}
			result[key] = info
		}
	}
	for key, info := range result {
		if info.Path == "" {
			delete(result, key)
		}
	}
	return result
}
