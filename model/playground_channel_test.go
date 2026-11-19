package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPlaygroundChannelSupportsEndpointOnSameChannel(t *testing.T) {
	one := &Channel{Type: constant.ChannelTypeAdvancedCustom, Status: common.ChannelStatusEnabled}
	two := &Channel{Type: constant.ChannelTypeAdvancedCustom, Status: common.ChannelStatusEnabled}
	for path, ch := range map[string]*Channel{"/v1/chat/completions": one, "/v1/images/generations": two} {
		ch.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: path, UpstreamPath: path, Converter: "none"}}}})
	}
	require.True(t, playgroundChannelSupports(one, "same-id", constant.EndpointTypeOpenAI, false))
	require.False(t, playgroundChannelSupports(one, "same-id", constant.EndpointTypeImageGeneration, true))
	require.True(t, playgroundChannelSupports(two, "same-id", constant.EndpointTypeImageGeneration, false))
	require.False(t, playgroundChannelSupports(two, "same-id", constant.EndpointTypeOpenAI, false))
	ch := &Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}
	require.False(t, playgroundChannelSupports(ch, "gpt-image-2", constant.EndpointTypeOpenAI, false))
	require.False(t, playgroundChannelSupports(ch, "text-embedding-3-large", constant.EndpointTypeOpenAI, false))
	require.True(t, playgroundChannelSupports(ch, "custom-image-alias", constant.EndpointTypeImageGeneration, true))
	ch.Type = constant.ChannelTypeAnthropic
	require.False(t, playgroundChannelSupports(ch, "custom-image-alias", constant.EndpointTypeImageGeneration, true))
	ch.Type = constant.ChannelTypeOpenAI
	ch.ModelMapping = common.GetPointer(`{"my-model":"gpt-image-2"}`)
	require.True(t, playgroundChannelSupports(ch, "my-model", constant.EndpointTypeImageGeneration, false))
}
