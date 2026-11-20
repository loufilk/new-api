package gemini

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
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
