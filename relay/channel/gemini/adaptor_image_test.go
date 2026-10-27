package gemini

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertImagenRequestGeometry(t *testing.T) {
	for _, tc := range []struct {
		name              string
		request           dto.ImageRequest
		ratio, resolution string
	}{
		{"explicit ratio", dto.ImageRequest{AspectRatio: lo.ToPtr("3:4"), Resolution: lo.ToPtr("2K")}, "3:4", "2K"},
		{"size ratio", dto.ImageRequest{Size: "4:3"}, "4:3", ""},
		{"default", dto.ImageRequest{}, "1:1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.request.Prompt = "portrait"
			tc.request.ExtraFields = []byte(`{"aspectRatio":"7:5","imageSize":"4K","unrecognized":"not forwarded"}`)
			converted, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-generate-001"}}, tc.request)
			require.NoError(t, err)
			result, ok := converted.(dto.GeminiImageRequest)
			require.True(t, ok)
			assert.Equal(t, tc.ratio, result.Parameters.AspectRatio)
			assert.Equal(t, tc.resolution, result.Parameters.ImageSize)
			body, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.NotContains(t, string(body), "extra_fields")
			assert.NotContains(t, string(body), "unrecognized")
		})
	}
}

func TestConvertImagenRequestRejectsUnsupportedGeometry(t *testing.T) {
	for _, request := range []dto.ImageRequest{
		{Size: "3840x2160"}, {Size: "1536x1024"}, {AspectRatio: lo.ToPtr("3:2")},
		{Resolution: lo.ToPtr("4K")}, {Quality: "unknown"},
		{Size: "1024x1024", AspectRatio: lo.ToPtr("16:9")},
		{Quality: "high", Resolution: lo.ToPtr("1K")},
	} {
		_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-generate-001"}}, request)
		require.Error(t, err)
		var apiErr *types.NewAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.True(t, types.IsSkipRetryError(apiErr))
	}
	_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-fast-generate-001"}}, dto.ImageRequest{Resolution: lo.ToPtr("2K")})
	require.Error(t, err)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.True(t, types.IsSkipRetryError(apiErr))
}
