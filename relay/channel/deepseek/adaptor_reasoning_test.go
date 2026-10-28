package deepseek

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIRequestKeepsDeepSeekV4MaxEffort(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "deepseek-v4-chat-max",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "deepseek-v4-chat-max",
		},
	}
	request := &dto.GeneralOpenAIRequest{Model: "deepseek-v4-chat-max"}

	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	assert.Equal(t, "deepseek-v4-chat", request.Model)
	assert.Equal(t, "max", request.ReasoningEffort)
	assert.Equal(t, "deepseek-v4-chat", info.UpstreamModelName)
}

func TestConvertOpenAIRequestPreservesExemptDeepSeekV4Suffix(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	original := append([]string(nil), settings.ThinkingModelBlacklist...)
	t.Cleanup(func() { settings.ThinkingModelBlacklist = original })
	settings.ThinkingModelBlacklist = append(settings.ThinkingModelBlacklist, "deepseek-v4-chat-max")

	info := &relaycommon.RelayInfo{
		OriginModelName: "deepseek-v4-chat-max",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "deepseek-v4-chat-max",
		},
	}
	request := &dto.GeneralOpenAIRequest{Model: "deepseek-v4-chat-max"}

	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	assert.Equal(t, "deepseek-v4-chat-max", request.Model)
	assert.Empty(t, request.ReasoningEffort)
	assert.Equal(t, "deepseek-v4-chat-max", info.UpstreamModelName)
}
