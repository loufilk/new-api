package cline

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const nestedContentRejection = `Failed to create stream: inference request failed: failed to generate stream from Vercel: request failed with status 400: {"error":{"message":"Invalid input","type":"invalid_request_error","param":"***.***.content","code":"invalid_request_error"}}`

type trackedResponseBody struct {
	io.Reader
	closed bool
}

func (b *trackedResponseBody) Close() error { b.closed = true; return nil }

func TestClineContentErrorPreservesGatewayStatusAndStopsOnlyContentRetries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		detail clineErrorDetail
		skip   bool
		param  string
	}{
		{"nested gateway rejection", clineErrorDetail{Message: nestedContentRejection, StatusCode: 502}, true, "***.***.content"},
		{"direct structured rejection", clineErrorDetail{Message: "Invalid input", Type: "invalid_request_error", Param: "messages.2.content.0.image_url", StatusCode: 400}, true, "messages.2.content.0.image_url"},
		{"SSE structured rejection", clineErrorDetail{Message: "Invalid input", Code: "invalid_request_error", Param: "messages[2].content"}, true, "messages[2].content"},
		{"real server failure", clineErrorDetail{Message: "upstream unavailable", Type: "api_error", StatusCode: 502}, false, ""},
		{"rate limit", clineErrorDetail{Message: "rate limited", Code: "rate_limit_error", StatusCode: 429}, false, ""},
		{"unrelated invalid param", clineErrorDetail{Message: "Invalid input", Type: "invalid_request_error", Param: "temperature", StatusCode: 400}, false, "temperature"},
		{"ambiguous 400 prose", clineErrorDetail{Message: "the request failed with status 400: not JSON"}, false, ""},
		{"server status with content-like type", clineErrorDetail{Message: "temporary", Type: "invalid_request_error", Param: "messages.0.content", StatusCode: 502}, false, "messages.0.content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := newClineEnvelopeError(tc.detail)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			assert.Equal(t, tc.skip, types.IsSkipRetryError(apiErr))
			assert.Equal(t, tc.param, apiErr.ToOpenAIError().Param)
			assert.Equal(t, tc.detail.Message, apiErr.ToOpenAIError().Message)
			if tc.skip {
				assert.Equal(t, int64(400), gjson.GetBytes(apiErr.Metadata, "upstream_status_code").Int())
				assert.Equal(t, tc.param, gjson.GetBytes(apiErr.Metadata, "upstream_error_param").Str)
			} else {
				assert.Empty(t, apiErr.Metadata)
			}
		})
	}
}

func TestClineHTTPContentErrorClosesRejectedBodyAndPreservesOtherResponses(t *testing.T) {
	rejected, err := common.Marshal(map[string]any{"error": map[string]any{"message": nestedContentRejection}})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, body string
		status     int
		classified bool
	}{
		{"nested content", string(rejected), 502, true},
		{"direct content", `{"error":{"type":"invalid_request_error","message":"Invalid input","param":"messages.1.content"}}`, 400, true},
		{"rate limit", `{"error":{"type":"rate_limit_error","message":"try later"}}`, 429, false},
		{"server HTML", `<html>Bad Gateway</html>`, 502, false},
		{"large failure", strings.Repeat("x", (64<<10)+100), 502, false},
		{"successful stream", `data: [DONE]`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedResponseBody{Reader: strings.NewReader(tc.body)}
			response := &http.Response{StatusCode: tc.status, Body: body, ContentLength: int64(len(tc.body))}
			result, err := classifyClineHTTPError(response)
			if tc.classified {
				require.Error(t, err)
				require.Nil(t, result)
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.True(t, body.closed)
				return
			}
			require.NoError(t, err)
			require.Same(t, response, result)
			assert.False(t, body.closed)
			restored, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			assert.Equal(t, tc.body, string(restored))
			require.NoError(t, response.Body.Close())
			assert.True(t, body.closed)
		})
	}
}

func TestClineSSEContentFailureIsNotNormalizedAsSuccess(t *testing.T) {
	payload, err := common.Marshal(map[string]any{"success": false, "error": map[string]any{"message": nestedContentRejection}})
	require.NoError(t, err)
	data, apiErr := normalizeClineStreamPayload("error", string(payload))
	require.Nil(t, data)
	require.NotNil(t, apiErr)
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Equal(t, "***.***.content", apiErr.ToOpenAIError().Param)
	assert.NotContains(t, clineErrorFrame(apiErr), "[DONE]")
	assert.NotContains(t, clineErrorFrame(apiErr), "upstream_status_code")
}
