package model

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/require"
)

func TestCodexRoutingPathHonorsEffectiveBridge(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	before := *settings
	t.Cleanup(func() { *settings = before })
	ch := &Channel{Id: 94021, Type: constant.ChannelTypeCodex}
	settings.PassThroughRequestEnabled = false
	settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{Enabled: true, ChannelIDs: []int{ch.Id}, ModelPatterns: []string{"^model-a$"}}
	for _, tc := range []struct {
		path, model                   string
		globalPass, channelPass, want bool
	}{
		{"/v1/responses", "model-b", true, true, true},
		{"/v1/responses/compact", "model-b", false, false, true},
		{"/v1/chat/completions", "model-a", false, false, true},
		{"/v1/chat/completions", "model-b", false, false, false},
		{"/v1/messages", "model-a", false, false, true},
		{"/v1/messages", "model-a", false, true, false},
		{"/pg/chat/completions", "model-a", true, false, false},
		{"/v1/images/generations", "model-a", false, false, false},
	} {
		settings.PassThroughRequestEnabled = tc.globalPass
		ch.SetSetting(kitdto.ChannelSettings{PassThroughBodyEnabled: tc.channelPass})
		got, _ := ChannelSatisfiesFilters(ch, tc.model, []dto.ChannelFilter{{Kind: dto.FilterRequestPath, RequestPath: tc.path}})
		require.Equal(t, tc.want, got, tc)
	}
	// A declared plugin endpoint uses its own implementation, not the builtin
	// adaptor; unrelated channels still go through the ordinary relay filters.
	got, _ := ChannelSatisfiesFilters(ch, "model-b", []dto.ChannelFilter{{Kind: dto.FilterRequestPath, RequestPath: "/v1/chat/completions"}, {Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: "fixture", TaskPluginChannelTypes: []int{constant.ChannelTypeCodex}}})
	require.True(t, got)
}
