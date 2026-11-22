package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

func ccswitchTestChannel(channelType int) *model.Channel {
	return &model.Channel{
		Id:     channelType + 1000,
		Type:   channelType,
		Status: common.ChannelStatusEnabled,
		Name:   "ccswitch-capability",
		Key:    "test-key",
	}
}

func TestSupportsCCSwitchModelUsesAdaptorCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channelType int
		app         string
		want        bool
	}{
		{name: "openai responses", channelType: constant.ChannelTypeOpenAI, app: CCSwitchAppCodex, want: true},
		{name: "openai claude conversion", channelType: constant.ChannelTypeOpenAI, app: CCSwitchAppClaude, want: true},
		{name: "openai gemini conversion", channelType: constant.ChannelTypeOpenAI, app: CCSwitchAppGemini, want: true},
		{name: "anthropic responses conversion", channelType: constant.ChannelTypeAnthropic, app: CCSwitchAppCodex, want: true},
		{name: "codex native responses", channelType: constant.ChannelTypeCodex, app: CCSwitchAppCodex, want: true},
		{name: "codex rejects claude", channelType: constant.ChannelTypeCodex, app: CCSwitchAppClaude, want: false},
		{name: "cline responses bridge", channelType: constant.ChannelTypeCline, app: CCSwitchAppCodex, want: true},
		{name: "dify rejects responses", channelType: constant.ChannelTypeDify, app: CCSwitchAppCodex, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SupportsCCSwitchModel(ccswitchTestChannel(tc.channelType), tc.app, "gpt-4o"))
		})
	}
}

func TestSupportsCCSwitchModelRejectsDisabledAndUnknownApp(t *testing.T) {
	channel := ccswitchTestChannel(constant.ChannelTypeOpenAI)
	channel.Status = common.ChannelStatusManuallyDisabled
	assert.False(t, SupportsCCSwitchModel(channel, CCSwitchAppCodex, "gpt-4o"))
	channel.Status = common.ChannelStatusEnabled
	assert.False(t, SupportsCCSwitchModel(channel, "unknown", "gpt-4o"))
}
