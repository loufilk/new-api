package jimeng

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertImageRequestRejectsUnsupportedGeometry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request dto.ImageRequest
	}{
		{"size", dto.ImageRequest{Size: "1024x1024"}},
		{"ratio", dto.ImageRequest{AspectRatio: lo.ToPtr("16:9")}},
		{"resolution", dto.ImageRequest{Resolution: lo.ToPtr("2K")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&Adaptor{}).ConvertImageRequest(nil, nil, tc.request)
			require.Error(t, err)
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiErr))
		})
	}
}

func TestConvertImageRequestPreservesExistingTypedExtraGeometry(t *testing.T) {
	var request dto.ImageRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"jimeng_high_aes_general_v21_L","prompt":"landscape","extra_fields":{"width":512,"height":384,"unrecognized":"not forwarded"}}`), &request))
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, nil, request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"width":512`)
	assert.Contains(t, string(body), `"height":384`)
	assert.NotContains(t, string(body), "unrecognized")
}
