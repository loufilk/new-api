package cline_test

import (
	"bytes"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise actual converter -> DTO marshal -> Cline.DoRequest -> HTTP wire,
// not a copy of conversion logic. The strict stub rejects the historical bug.
func TestClineMediaAcrossEntryProtocolsAndStreamModes(t *testing.T) {
	const imageURL = "https://example.com/image.png?signature=kept"
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", format, stream), func(t *testing.T) {
				upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
					parts := gjson.GetBytes(body, "messages.0.content")
					valid := parts.IsArray() && parts.Get("0.type").Str == "text" && parts.Get("0.text").Type == gjson.String && parts.Get("1.image_url.url").Str == imageURL
					if !valid {
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"Invalid input","param":"messages.0.content"}}`))
						return
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte(chatStreamWrappedFixture))
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(chatCompletionWrappedFixture))
					}
				})
				mode := relayconstant.RelayModeChatCompletions
				if format == types.RelayFormatOpenAIResponses {
					mode = relayconstant.RelayModeResponses
				}
				if format == types.RelayFormatClaude {
					mode = relayconstant.RelayModeUnknown
				}
				env := newClineTestEnv(t, format, mode, stream, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
				var converted any
				var err error
				switch format {
				case types.RelayFormatOpenAI:
					request := &dto.GeneralOpenAIRequest{Model: env.info.OriginModelName, Stream: &stream, Messages: []dto.Message{{Role: "user", Content: []any{map[string]any{"type": "text", "text": ""}, map[string]any{"type": "image_url", "image_url": imageURL}}}}}
					env.mapModel(t, request, "deepseek/deepseek-v4.1-flash")
					converted, err = env.adaptor.ConvertOpenAIRequest(env.context, env.info, request)
				case types.RelayFormatOpenAIResponses:
					request := &dto.OpenAIResponsesRequest{Model: env.info.OriginModelName, Stream: &stream, Input: []byte(`[{"type":"message","role":"user","content":[{"type":"input_text","text":""},{"type":"input_image","image_url":"` + imageURL + `"}]}]`)}
					env.mapModel(t, request, "deepseek/deepseek-v4.1-flash")
					converted, err = env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
				case types.RelayFormatClaude:
					request := &dto.ClaudeRequest{}
					require.NoError(t, common.Unmarshal([]byte(`{"model":"cline-free/deepseek-v4.1-flash","max_tokens":32,"messages":[{"role":"user","content":[{"type":"text","text":""},{"type":"image","source":{"type":"url","url":"`+imageURL+`"}}]}]}`), request))
					request.Stream = &stream
					env.mapModel(t, request, "deepseek/deepseek-v4.1-flash")
					converted, err = env.adaptor.ConvertClaudeRequest(env.context, env.info, request)
				}
				require.NoError(t, err)
				response := env.send(t, converted)
				_, apiErr := env.receive(t, response)
				require.Nil(t, apiErr)
				wire := <-received
				assert.Equal(t, "deepseek/deepseek-v4.1-flash", gjson.GetBytes(wire.body, "model").Str)
				assert.Equal(t, imageURL, gjson.GetBytes(wire.body, "messages.0.content.1.image_url.url").Str)
				assert.Equal(t, gjson.String, gjson.GetBytes(wire.body, "messages.0.content.0.text").Type)
				assert.NotContains(t, string(wire.body), "MimeType")
				assert.Contains(t, env.recorder.Body.String(), "hello")
			})
		}
	}
}

func TestClineFinalWireValidationRunsAfterParameterOverrides(t *testing.T) {
	var calls atomic.Int32
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(chatCompletionWrappedFixture))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, false, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	original := []byte(`{"model":"deepseek/deepseek-v4.1-flash","temperature":0,"messages":[{"role":"user","content":"original"}]}`)
	override := map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text"}, map[string]any{"type": "image_url", "image_url": "https://example.com/after.png"}}}}}
	replaced, err := relaycommon.ApplyParamOverride(original, override, nil)
	require.NoError(t, err)
	result, err := env.adaptor.DoRequest(env.context, env.info, bytes.NewReader(replaced))
	require.NoError(t, err)
	response := result.(*http.Response)
	require.NoError(t, response.Body.Close())
	wire := <-received
	assert.Equal(t, "https://example.com/after.png", gjson.GetBytes(wire.body, "messages.0.content.1.image_url.url").Str)
	assert.Equal(t, gjson.String, gjson.GetBytes(wire.body, "messages.0.content.0.text").Type)
	assert.True(t, gjson.GetBytes(wire.body, "temperature").Exists())
	for _, content := range []any{
		[]any{map[string]any{"type": "video_url", "video_url": "https://example.com/video.mp4"}},
		[]any{map[string]any{"type": "image_url", "image_url": map[string]any{"file_id": "provider-local"}}},
		[]any{map[string]any{"type": "text", "text": 42}},
	} {
		override["messages"] = []any{map[string]any{"role": "user", "content": content}}
		invalid, err := relaycommon.ApplyParamOverride(original, override, nil)
		require.NoError(t, err)
		result, err := env.adaptor.DoRequest(env.context, env.info, bytes.NewReader(invalid))
		require.Error(t, err)
		assert.Nil(t, result)
		var apiErr *types.NewAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 400, apiErr.StatusCode)
		assert.True(t, types.IsSkipRetryError(apiErr))
	}
	assert.Equal(t, int32(1), calls.Load(), "invalid content must never reach the upstream")
}
