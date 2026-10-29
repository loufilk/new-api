package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGPT61ChatCapabilitiesFollowPinnedModelRules(t *testing.T) {
	for _, tc := range []struct {
		model, effort        string
		restricted, sampling bool
	}{
		{"gpt-6.1-sol", "", true, false},
		{"gpt-6.1-sol", "none", true, false},
		{"gpt-6.1-sol-2026-10-28", "none", true, false},
		{"gpt-6.1-terra", "", true, false},
		{"gpt-6.1-terra", "medium", true, false},
		{"gpt-6.1-terra", "none", true, true},
		{"gpt-6.1-terra-2026-10-28", "none", true, true},
		{"gpt-6-sol", "", true, true},
		{"gpt-6-astra", "none", true, false},
		{"gpt-6.1-terra-custom", "", false, true},
		{"gpt-6.1-sol-2026-99-99", "none", false, true},
		{"gpt-7", "", false, true},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			capabilities := GetOpenAIChatCapabilities(tc.model, tc.effort)
			assert.Equal(t, tc.restricted, capabilities.UseMaxCompletionTokens)
			assert.Equal(t, tc.restricted, capabilities.UseDeveloperRole)
			assert.Equal(t, tc.sampling, capabilities.SupportsTemperature)
			assert.Equal(t, tc.sampling, capabilities.SupportsTopP)
			assert.Equal(t, tc.sampling, capabilities.SupportsLogProbs)
		})
	}
}
