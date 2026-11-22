package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterPricingByUsableGroups(t *testing.T) {
	pricing := []Pricing{
		{ModelName: "all-model", EnableGroup: []string{"all"}},
		{ModelName: "default-model", EnableGroup: []string{"default"}},
		{ModelName: "vip-model", EnableGroup: []string{"vip"}},
		{ModelName: "mixed-model", EnableGroup: []string{"vip", "default"}},
	}

	t.Run("empty pricing", func(t *testing.T) {
		assert.Empty(t, FilterPricingByUsableGroups(nil, map[string]string{"default": "Default"}))
	})

	t.Run("empty groups", func(t *testing.T) {
		assert.Empty(t, FilterPricingByUsableGroups(pricing, nil))
	})

	t.Run("all and intersections", func(t *testing.T) {
		filtered := FilterPricingByUsableGroups(pricing, map[string]string{"default": "Default"})
		var names []string
		for _, item := range filtered {
			names = append(names, item.ModelName)
		}
		assert.Equal(t, []string{"all-model", "default-model", "mixed-model"}, names)
	})
}
