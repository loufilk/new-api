package xai

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIRequestGrokMiniEffortSuffix(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		wantEffort string
		wantModel  string
	}{
		{name: "high", model: "grok-3-mini-high", wantEffort: "high", wantModel: "grok-3-mini"},
		{name: "low", model: "grok-3-mini-low", wantEffort: "low", wantModel: "grok-3-mini"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: tt.model,
				},
			}
			request := &dto.GeneralOpenAIRequest{Model: tt.model}

			_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
			require.NoError(t, err)
			assert.Equal(t, tt.wantEffort, request.ReasoningEffort)
			assert.Equal(t, tt.wantModel, request.Model)
			assert.Equal(t, tt.wantModel, info.UpstreamModelName)
		})
	}
}

func TestConvertOpenAIRequestLeavesNonMiniHighSuffixUntouched(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "grok-4-high",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "grok-4-high",
		},
	}
	request := &dto.GeneralOpenAIRequest{Model: "grok-4-high"}

	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	assert.Equal(t, "grok-4-high", request.Model)
	assert.Empty(t, request.ReasoningEffort)
}

func TestConvertOpenAIRequestPreservesExemptGrokMiniSuffix(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	original := append([]string(nil), settings.ThinkingModelBlacklist...)
	t.Cleanup(func() { settings.ThinkingModelBlacklist = original })
	settings.ThinkingModelBlacklist = append(settings.ThinkingModelBlacklist, "grok-3-mini-high")

	info := &relaycommon.RelayInfo{
		OriginModelName: "grok-3-mini-high",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "grok-3-mini-high",
		},
	}
	request := &dto.GeneralOpenAIRequest{Model: "grok-3-mini-high"}

	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	assert.Equal(t, "grok-3-mini-high", request.Model)
	assert.Empty(t, request.ReasoningEffort)
}
