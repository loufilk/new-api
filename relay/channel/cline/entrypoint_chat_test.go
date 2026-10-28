package cline_test

import (
	"net/http"
	"strings"
	"testing"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const chatCompletionWrappedFixture = `{
	"success": true,
	"data": {
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"created": 123,
		"model": "cline-free/upstream-model",
		"vendor_extension": {"trace": "kept"},
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "hello",
				"tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"a\"}"}}],
				"reasoning_details": [{"type": "reasoning.text", "text": "think "}, {"type": "reasoning.text", "text": "then answer"}],
				"provider_metadata": {"secret": "internal-only"}
			},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
	}
}`

const chatStreamWrappedFixture = `data: {"success":true,"data":{"id":"chatcmpl-1","object":"chat.completion.chunk","created":123,"model":"cline-free/upstream-model","choices":[{"index":0,"delta":{"role":"assistant"}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"hello"}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"reasoning":"think"}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}}

data: [DONE]

`

func TestClineChatCompletionJSONThroughAdaptor(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(chatCompletionWrappedFixture))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, false, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")

	stream := false
	request := &dto.GeneralOpenAIRequest{
		Model:    env.info.OriginModelName,
		Messages: []dto.Message{{Role: "user", Content: nil}},
		Stream:   &stream,
	}
	env.mapModel(t, request, "cline-free/upstream-model")
	converted, err := env.adaptor.ConvertOpenAIRequest(env.context, env.info, request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 12, usage.(*dto.Usage).TotalTokens)

	upstreamRequest := <-received
	assert.Equal(t, http.MethodPost, upstreamRequest.method)
	assert.Equal(t, "/api/v1/chat/completions", upstreamRequest.path)
	assert.Equal(t, "Bearer test-cline-key", upstreamRequest.header.Get("Authorization"))
	assert.Equal(t, "cline-cli", upstreamRequest.header.Get("X-Client-Type"))
	assert.Equal(t, "cline-free/upstream-model", gjson.GetBytes(upstreamRequest.body, "model").String())
	assert.JSONEq(t, `[{"type":"text","text":""}]`, gjson.GetBytes(upstreamRequest.body, "messages.0.content").Raw)
	assert.True(t, gjson.GetBytes(upstreamRequest.body, "stream").Exists())
	assert.False(t, gjson.GetBytes(upstreamRequest.body, "stream").Bool())

	body := env.recorder.Body.Bytes()
	assert.False(t, gjson.ValidBytes(body) && gjson.GetBytes(body, "success").Exists())
	assert.Equal(t, "cline-free/deepseek-v4.1-flash", gjson.GetBytes(body, "model").String())
	assert.Equal(t, "hello", gjson.GetBytes(body, "choices.0.message.content").String())
	assert.Equal(t, "think then answer", gjson.GetBytes(body, "choices.0.message.reasoning_content").String())
	assert.False(t, gjson.GetBytes(body, "choices.0.message.reasoning_details").Exists())
	assert.False(t, gjson.GetBytes(body, "choices.0.message.provider_metadata").Exists())
	assert.Equal(t, "read_file", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	assert.Equal(t, "kept", gjson.GetBytes(body, "vendor_extension.trace").String())
	assert.Equal(t, int64(12), gjson.GetBytes(body, "usage.total_tokens").Int())
}

func TestClineChatCompletionSSEThroughAdaptor(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(chatStreamWrappedFixture))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	env.info.ShouldIncludeUsage = true

	stream := true
	request := &dto.GeneralOpenAIRequest{
		Model:         env.info.OriginModelName,
		Messages:      []dto.Message{{Role: "user", Content: "hello"}},
		Stream:        &stream,
		StreamOptions: &dto.StreamOptions{IncludeUsage: true},
	}
	env.mapModel(t, request, "cline-free/upstream-model")
	converted, err := env.adaptor.ConvertOpenAIRequest(env.context, env.info, request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.(*dto.Usage).TotalTokens)

	upstreamRequest := <-received
	assert.Equal(t, "cline-free/upstream-model", gjson.GetBytes(upstreamRequest.body, "model").String())
	assert.True(t, gjson.GetBytes(upstreamRequest.body, "stream_options.include_usage").Bool())

	body := env.recorder.Body.String()
	assert.NotContains(t, body, `"success"`)
	payloads := dataPayloads(body)
	require.NotEmpty(t, payloads)
	for _, payload := range payloads {
		if payload == "[DONE]" {
			continue
		}
		assert.True(t, gjson.Valid(payload), "payload must be valid JSON: %s", payload)
		assert.False(t, gjson.Get(payload, "success").Exists())
	}
	assert.Equal(t, 1, strings.Count(body, "[DONE]"))
	assert.Contains(t, body, `"content":"hello"`)
	assert.Contains(t, body, `"reasoning_content":"think"`)
	assert.Contains(t, body, `"model":"cline-free/deepseek-v4.1-flash"`)
	assert.Contains(t, body, `"total_tokens":7`)
}

func TestClineChatCompletionWrappedFailureReturnsSafeError(t *testing.T) {
	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"error":{"message":"account has no access to this model","type":"invalid_request_error"}}`))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, false, "cline-pass/kimi-k3", upstream.URL+"/api/v1")
	request := &dto.GeneralOpenAIRequest{Model: env.info.OriginModelName, Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	converted, err := env.adaptor.ConvertOpenAIRequest(env.context, env.info, request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "account has no access to this model")
	assert.Empty(t, env.recorder.Body.String(), "no client output before a leading error")
}

func TestClineChatCompletionLeadingStreamErrorReturnsBeforeOutput(t *testing.T) {
	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"success\":false,\"errors\":[{\"message\":\"free quota exhausted\"}]}\n\ndata: [DONE]\n\n"))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	stream := true
	request := &dto.GeneralOpenAIRequest{Model: env.info.OriginModelName, Messages: []dto.Message{{Role: "user", Content: "hello"}}, Stream: &stream}
	converted, err := env.adaptor.ConvertOpenAIRequest(env.context, env.info, request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "free quota exhausted")
	assert.Empty(t, env.recorder.Body.String())
}

func TestClineChatCompletionMidStreamErrorReturnsFailureWithoutDone(t *testing.T) {
	stream := "data: " + `{"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"partial"}}]}}` + "\n\n" +
		"data: " + `{"success":false,"error":{"message":"internal account id 42 rejected"}}` + "\n\n"

	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	env.info.ShouldIncludeUsage = true
	streamFlag := true
	request := &dto.GeneralOpenAIRequest{Model: env.info.OriginModelName, Messages: []dto.Message{{Role: "user", Content: "hello"}}, Stream: &streamFlag}
	converted, err := env.adaptor.ConvertOpenAIRequest(env.context, env.info, request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.NotNil(t, apiErr)
	require.NotNil(t, usage)
	assert.True(t, types.IsSkipRetryError(apiErr), "partial output must never be retried onto another channel")

	body := env.recorder.Body.String()
	assert.Contains(t, body, "partial")
	assert.Contains(t, body, `"error"`)
	assert.Contains(t, body, "upstream request failed")
	assert.NotContains(t, body, "internal account id 42 rejected")
	assert.NotContains(t, body, "[DONE]")
	require.NotNil(t, env.info.StreamStatus)
	assert.True(t, env.info.StreamStatus.ResponseFailed())
	require.NotNil(t, env.info.StreamStatus.APIError())
}

func TestClineCompatibleFormatsExposeSanitizedMidStreamFailure(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			stream := "data: " + `{"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"partial"}}]}}` + "\n\n" +
				"data: " + `{"success":false,"error":{"message":"internal account id 42 rejected"}}` + "\n\n"
			upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(stream))
			})
			env := newClineTestEnv(t, format, relayconstant.RelayModeChatCompletions, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
			request := &dto.GeneralOpenAIRequest{Model: env.info.OriginModelName, Messages: []dto.Message{{Role: "user", Content: "hello"}}}
			resp := env.send(t, request)
			usage, apiErr := env.receive(t, resp)
			require.NotNil(t, apiErr)
			require.NotNil(t, usage)
			body := env.recorder.Body.String()
			assert.Contains(t, body, "partial")
			assert.Contains(t, body, "upstream request failed")
			assert.NotContains(t, body, "internal account id 42 rejected")
			assert.NotContains(t, body, "[DONE]")
			assert.True(t, env.info.StreamStatus.ResponseFailed())
		})
	}
}

func TestClineChatCompletionDoesNotMultiplyDoneOrUsage(t *testing.T) {
	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(chatStreamWrappedFixture))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	env.info.ShouldIncludeUsage = true
	streamFlag := true
	request := &dto.GeneralOpenAIRequest{
		Model:         env.info.OriginModelName,
		Messages:      []dto.Message{{Role: "user", Content: "hello"}},
		Stream:        &streamFlag,
		StreamOptions: &dto.StreamOptions{IncludeUsage: true},
	}
	converted, err := env.adaptor.ConvertOpenAIRequest(env.context, env.info, request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	_, apiErr := env.receive(t, resp)
	require.Nil(t, apiErr)

	body := env.recorder.Body.String()
	assert.Equal(t, 1, strings.Count(body, "[DONE]"))
	usageFrames := 0
	for _, payload := range dataPayloads(body) {
		if payload == "[DONE]" || !gjson.Valid(payload) {
			continue
		}
		if gjson.Get(payload, "usage.total_tokens").Int() > 0 {
			usageFrames++
		}
	}
	assert.Equal(t, 1, usageFrames, "the upstream usage frame must be forwarded once")
}
