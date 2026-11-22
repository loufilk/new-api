package oairesponses

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesImageURLNormalizationPreservesDetailAndInput(t *testing.T) {
	original := map[string]any{"url": "https://example.test/a.png"}
	part := map[string]any{"type": "input_image", "image_url": original, "detail": "original"}
	got, err := responsesImagePartToChatImageURL(part)
	require.NoError(t, err)
	assert.NotContains(t, got, "detail", "Responses-only detail must not break strict Chat upstreams")
	assert.NotContains(t, original, "detail", "normalization must not mutate a reused request")
	original["detail"] = "low"
	got, err = responsesImagePartToChatImageURL(part)
	require.NoError(t, err)
	assert.Equal(t, "low", got["detail"], "an existing nested detail remains authoritative")
	got, err = responsesImagePartToChatImageURL(map[string]any{"image_url": "data:image/png;base64,aGk="})
	require.NoError(t, err)
	assert.Equal(t, "data:image/png;base64,aGk=", got["url"])
}

func TestResponsesRejectsUnresolvedImageReferencesOnMessageAndToolOutput(t *testing.T) {
	for _, image := range []string{
		`{"type":"input_image","file_id":"file-private-secret"}`,
		`{"type":"input_image","image_url":null}`,
		`{"type":"input_image","image_url":{"url":0}}`,
		`{"type":"input_image","image_url":""}`,
		`{"type":"input_image","image_url":false}`,
	} {
		for _, input := range []string{
			`[{"role":"user","content":[` + image + `]}]`,
			`[{"type":"function_call_output","call_id":"call_1","output":[` + image + `]}]`,
		} {
			got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
				Model: "deepseek/deepseek-v4.1-flash", Input: json.RawMessage(input),
			})
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Nil(t, got)
			assert.Equal(t, 400, apiErr.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.NotContains(t, err.Error(), "private-secret")
		}
	}
}
