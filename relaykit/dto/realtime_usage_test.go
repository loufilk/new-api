package dto

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRealtimeUsageCompleteAggregate(t *testing.T) {
	text, image, audio := 1, 2, 3
	segment := RealtimeUsage{TotalTokens: 30, InputTokens: 20, OutputTokens: 10,
		InputTokenDetails:  InputTokenDetails{TextTokens: 10, AudioTokens: 6, ImageTokens: 4, CachedTokens: 6, CacheWriteTokens: 2, CachedTokensDetails: &CachedTokenDetails{TextTokens: &text, ImageTokens: &image, AudioTokens: &audio}},
		OutputTokenDetails: OutputTokenDetails{TextTokens: 4, AudioTokens: 3, ImageTokens: 2, ReasoningTokens: 1}}
	var aggregate RealtimeUsage
	require.NoError(t, AddRealtimeUsage(&aggregate, &segment))
	require.NoError(t, AddRealtimeUsage(&aggregate, &segment))
	require.Equal(t, 60, aggregate.TotalTokens)
	require.Equal(t, 8, aggregate.InputTokenDetails.ImageTokens)
	require.Equal(t, 4, aggregate.InputTokenDetails.CacheWriteTokens)
	require.Equal(t, 2, *aggregate.InputTokenDetails.CachedTokensDetails.TextTokens)
	require.Equal(t, 4, *aggregate.InputTokenDetails.CachedTokensDetails.ImageTokens)
	require.Equal(t, 6, *aggregate.InputTokenDetails.CachedTokensDetails.AudioTokens)
	require.Equal(t, 4, aggregate.OutputTokenDetails.ImageTokens)
	require.Equal(t, 2, aggregate.OutputTokenDetails.ReasoningTokens)
	text, image, audio = 9, 9, 9
	require.Equal(t, 2, *aggregate.InputTokenDetails.CachedTokensDetails.TextTokens, "caller pointers cannot change a frozen aggregate")
}

func TestRealtimeUsageCacheCreationAliases(t *testing.T) {
	var aggregate RealtimeUsage
	for _, input := range []InputTokenDetails{{TextTokens: 20, CacheWriteTokens: 10}, {TextTokens: 20, CachedCreationTokens: 4}, {TextTokens: 20, CacheWriteTokens: 3, CachedCreationTokens: 3}} {
		require.NoError(t, AddRealtimeUsage(&aggregate, &RealtimeUsage{TotalTokens: 20, InputTokens: 20, InputTokenDetails: input}))
	}
	require.Equal(t, 13, aggregate.InputTokenDetails.CacheWriteTokens, "retain original provider counters")
	require.Equal(t, 7, aggregate.InputTokenDetails.CachedCreationTokens)
	require.Equal(t, 17, aggregate.AsUsage().PromptTokensDetails.CacheCreationTokensTotal(), "normalize aliases per response, not after summing different names")
}

func TestRealtimeUsageInvalidLeavesAggregateUntouched(t *testing.T) {
	for name, invalid := range map[string]RealtimeUsage{
		"overflow":              {TotalTokens: math.MaxInt, InputTokens: math.MaxInt},
		"negative":              {TotalTokens: 1, InputTokens: 1, InputTokenDetails: InputTokenDetails{AudioTokens: -1}},
		"inconsistent_total":    {TotalTokens: 2, InputTokens: 1},
		"detail_without_totals": {InputTokenDetails: InputTokenDetails{TextTokens: 20}},
	} {
		t.Run(name, func(t *testing.T) {
			var aggregate RealtimeUsage
			require.NoError(t, AddRealtimeUsage(&aggregate, &RealtimeUsage{TotalTokens: 1, InputTokens: 1}))
			before := aggregate.Clone()
			require.Error(t, AddRealtimeUsage(&aggregate, &invalid))
			require.Equal(t, before, aggregate)
		})
	}
}

func TestRealtimeUsageMissingTotalUsesKnownInputOutput(t *testing.T) {
	var aggregate RealtimeUsage
	require.NoError(t, AddRealtimeUsage(&aggregate, &RealtimeUsage{InputTokens: 20, OutputTokens: 3, InputTokenDetails: InputTokenDetails{TextTokens: 20}, OutputTokenDetails: OutputTokenDetails{TextTokens: 3}}))
	require.Equal(t, 23, aggregate.TotalTokens)
}

func TestRealtimeUsageCachedModalityPresence(t *testing.T) {
	for _, order := range []string{"known_missing_known", "missing_known", "known_zero", "known_irrelevant", "irrelevant_segments_then_known"} {
		t.Run(order, func(t *testing.T) {
			known := func(n int) RealtimeUsage {
				return RealtimeUsage{TotalTokens: 20, InputTokens: 20, InputTokenDetails: InputTokenDetails{TextTokens: 10, ImageTokens: 10, CachedTokens: 10, CachedTokensDetails: &CachedTokenDetails{ImageTokens: &n}}}
			}
			missing := known(0)
			missing.InputTokenDetails.CachedTokensDetails = nil
			irrelevant := RealtimeUsage{TotalTokens: 20, InputTokens: 20, InputTokenDetails: InputTokenDetails{TextTokens: 20}}
			var inputs []RealtimeUsage
			switch order {
			case "known_missing_known":
				inputs = []RealtimeUsage{known(10), missing, known(10)}
			case "missing_known":
				inputs = []RealtimeUsage{missing, known(10)}
			case "known_zero":
				inputs = []RealtimeUsage{known(10), known(0)}
			case "known_irrelevant":
				inputs = []RealtimeUsage{known(10), irrelevant}
			case "irrelevant_segments_then_known":
				imageOnly := known(0)
				imageOnly.InputTokenDetails.CachedTokens = 0
				imageOnly.InputTokenDetails.CachedTokensDetails = nil
				cacheOnly := irrelevant
				cacheOnly.InputTokenDetails.CachedTokens = 10
				inputs = []RealtimeUsage{imageOnly, cacheOnly, known(10)}
			}
			var aggregate RealtimeUsage
			for i := range inputs {
				require.NoError(t, AddRealtimeUsage(&aggregate, &inputs[i]))
			}
			details := aggregate.AsUsage().PromptTokensDetails.CachedTokensDetails
			require.NotNil(t, details)
			if order == "known_zero" || order == "known_irrelevant" || order == "irrelevant_segments_then_known" {
				require.NotNil(t, details.ImageTokens)
				require.Equal(t, 10, *details.ImageTokens)
			} else {
				require.Nil(t, details.ImageTokens, "partial knowledge cannot masquerade as a complete cache split")
			}
		})
	}
}
