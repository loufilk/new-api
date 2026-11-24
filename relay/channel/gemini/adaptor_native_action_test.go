package gemini

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiNativeEmbeddingsAndGenerationUseRequestType(t *testing.T) {
	for _, tc := range []struct {
		name, model, action string
		request             dto.Request
	}{
		{"single alias", "vector-v1", "embedContent", &dto.GeminiEmbeddingRequest{}},
		{"batch alias", "vector-v1", "batchEmbedContents", &dto.GeminiBatchEmbeddingRequest{}},
		{"generation alias looks like an embedding", "embedding-compatible-chat", "generateContent", &dto.GeminiChatRequest{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayMode: relayconstant.RelayModeGemini, RelayFormat: types.RelayFormatGemini, Request: tc.request,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://gemini-fixture.invalid", UpstreamModelName: tc.model},
			}
			got, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			assert.Contains(t, got, "/models/"+tc.model+":"+tc.action)
		})
	}
}

func TestGeminiVersionFallbackAtRequestURL(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	original := settings.VersionSettings
	t.Cleanup(func() { settings.VersionSettings = original })
	settings.VersionSettings = map[string]string{"listed-model": "v1"}
	for _, tc := range []struct {
		name, model, action, version string
		req                          dto.Request
		format                       types.RelayFormat
	}{
		{"unmapped generation", "chat", "generateContent", "v1beta", &dto.GeminiChatRequest{}, types.RelayFormatGemini},
		{"unmapped embedding", "vector", "embedContent", "v1beta", &dto.GeminiEmbeddingRequest{}, types.RelayFormatGemini},
		{"unmapped imagen", "imagen-test", "predict", "v1beta", nil, types.RelayFormatOpenAI},
		{"model override", "listed-model", "generateContent", "v1", &dto.GeminiChatRequest{}, types.RelayFormatGemini},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{Request: tc.req, RelayFormat: tc.format, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://gemini-fixture.invalid", UpstreamModelName: tc.model}}
			got, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, "https://gemini-fixture.invalid/"+tc.version+"/models/"+tc.model+":"+tc.action, got)
		})
	}
}
