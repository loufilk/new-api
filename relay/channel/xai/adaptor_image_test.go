package xai

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

func TestConvertImageRequestPreservesGeometry(t *testing.T) {
	var request dto.ImageRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"grok-imagine-image","prompt":"landscape","aspect_ratio":"16:9","resolution":"2k","unknown":"not forwarded"}`), &request))
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, nil, request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"grok-imagine-image","prompt":"landscape","n":1,"aspect_ratio":"16:9","resolution":"2k"}`, string(body))
}

func TestConvertImageRequestRejectsUnsupportedGeometry(t *testing.T) {
	for _, request := range []dto.ImageRequest{
		{AspectRatio: lo.ToPtr("7:5")}, {Resolution: lo.ToPtr("4k")}, {Size: "1536x1024"},
	} {
		_, err := (&Adaptor{}).ConvertImageRequest(nil, nil, request)
		require.Error(t, err)
		var apiErr *types.NewAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.True(t, types.IsSkipRetryError(apiErr))
	}
}

func TestConvertImageRequestOmitsUnspecifiedGeometry(t *testing.T) {
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, nil, dto.ImageRequest{Model: "grok-imagine-image", Prompt: "landscape"})
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"grok-imagine-image","prompt":"landscape","n":1}`, string(body))
}
