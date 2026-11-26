package model

import (
	"slices"

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
	// Use the existing model-level protocol order for all projections, including
	// when multiple source groups share one public display-only tier.
	byGroup := make(map[string][]constant.EndpointType, len(projected))
	for key, types := range projected {
		ordered := make([]constant.EndpointType, 0, len(types))
		for _, kind := range p.SupportedEndpointTypes {
			if slices.Contains(types, kind) {
				ordered = append(ordered, kind)
			}
		}
		byGroup[key] = ordered
	}
	union := make([]constant.EndpointType, 0, len(p.SupportedEndpointTypes))
	for _, kind := range p.SupportedEndpointTypes {
		if visible[kind] || slices.Contains(p.declaredEndpoints, kind) {
			union = append(union, kind)
		}
	}
	return union, byGroup
}
