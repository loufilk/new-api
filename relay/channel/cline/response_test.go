package cline

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeClineResponseBodyPlainPayloadPassesThrough(t *testing.T) {
	body := []byte(`{"id":"chatcmpl-1","model":"cline-free/deepseek-v4.1-flash","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	normalized, apiErr := normalizeClineResponseBody(body)
	require.Nil(t, apiErr)
	assert.JSONEq(t, string(body), string(normalized))
}

func TestNormalizeClineResponseBodyUnwrapsDataAndKeepsUnknownFields(t *testing.T) {
	body := []byte(`{
		"success": true,
		"data": {
			"id": "chatcmpl-1",
			"object": "chat.completion",
			"model": "cline-free/deepseek-v4.1-flash",
			"vendor_extension": {"trace": "abc"},
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "ok",
					"tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"a\"}"}}],
					"reasoning": "raw reasoning",
					"reasoning_details": [
						{"type": "reasoning.text", "text": "think ", "format": "text", "index": 0},
						{"type": "reasoning.text", "text": "then answer", "format": "text", "index": 1}
					],
					"provider_metadata": {"internal": "secret"}
				},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
		}
	}`)
	normalized, apiErr := normalizeClineResponseBody(body)
	require.Nil(t, apiErr)
	assert.False(t, gjsonHas(normalized, "success"))
	assert.Equal(t, "chatcmpl-1", gjsonString(normalized, "id"))
	assert.Equal(t, "abc", gjsonString(normalized, "vendor_extension.trace"))
	assert.Equal(t, "think then answer", gjsonString(normalized, "choices.0.message.reasoning_content"))
	assert.False(t, gjsonHas(normalized, "choices.0.message.reasoning"))
	assert.False(t, gjsonHas(normalized, "choices.0.message.reasoning_details"))
	assert.False(t, gjsonHas(normalized, "choices.0.message.provider_metadata"))
	assert.Equal(t, "read_file", gjsonString(normalized, "choices.0.message.tool_calls.0.function.name"))
	assert.Equal(t, `{"path":"a"}`, gjsonString(normalized, "choices.0.message.tool_calls.0.function.arguments"))
	assert.Equal(t, "ok", gjsonString(normalized, "choices.0.message.content"))
	assert.Equal(t, int64(12), gjsonInt(normalized, "usage.total_tokens"))
}

func TestNormalizeClineReasoningFallsBackToReasoningField(t *testing.T) {
	body := []byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning":"legacy reasoning"}}]}`)
	normalized, apiErr := normalizeClineResponseBody(body)
	require.Nil(t, apiErr)
	assert.Equal(t, "legacy reasoning", gjsonString(normalized, "choices.0.message.reasoning_content"))
	assert.False(t, gjsonHas(normalized, "choices.0.message.reasoning"))
}

func TestNormalizeClineReasoningFallsBackWhenDetailsEmpty(t *testing.T) {
	body := []byte(`{"choices":[{"index":0,"delta":{"reasoning":"fallback","reasoning_details":[]}}]}`)
	normalized, apiErr := normalizeClineResponseBody(body)
	require.Nil(t, apiErr)
	assert.Equal(t, "fallback", gjsonString(normalized, "choices.0.delta.reasoning_content"))
}

func TestNormalizeClineResponseBodyRejectsFailedEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name            string
		body            string
		expectedMessage string
		expectedType    string
	}{
		{name: "string error", body: `{"success":false,"error":"model not found"}`, expectedMessage: "model not found", expectedType: "api_error"},
		{name: "object error", body: `{"success":false,"error":{"message":"model not found","type":"invalid_request_error","code":"model_not_found"}}`, expectedMessage: "model not found", expectedType: "invalid_request_error"},
		{name: "errors array", body: `{"success":false,"errors":[{"message":"quota exceeded"},{"message":"try later"}]}`, expectedMessage: "quota exceeded; try later", expectedType: "api_error"},
		{name: "no detail", body: `{"success":false}`, expectedMessage: "Cline request failed", expectedType: "api_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, apiErr := normalizeClineResponseBody([]byte(tc.body))
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			assert.Contains(t, apiErr.Error(), tc.expectedMessage)
			openAIError := apiErr.ToOpenAIError()
			assert.Equal(t, tc.expectedType, openAIError.Type)
		})
	}
}

func TestNormalizeClineResponseBodyRejectsSuccessfulEnvelopeWithoutPayload(t *testing.T) {
	_, apiErr := normalizeClineResponseBody([]byte(`{"success":true,"data":null}`))
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "Cline request failed")
}

func TestNormalizeClineResponseBodyRejectsErrorsArrayWithoutChoices(t *testing.T) {
	_, apiErr := normalizeClineResponseBody([]byte(`{"errors":[{"message":"response_format json_object is not supported"}]}`))
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "response_format json_object is not supported")
}

func TestNormalizeClineResponseBodyRejectsEmptyBody(t *testing.T) {
	_, apiErr := normalizeClineResponseBody(nil)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
}

func TestNormalizeClineStreamPayload(t *testing.T) {
	for _, tc := range []struct {
		name        string
		eventType   string
		payload     string
		expectErr   string
		expectDone  bool
		expectModel string
	}{
		{name: "plain chunk", payload: `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"hi"}}]}`},
		{name: "wrapped chunk", payload: `{"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"hi"}}]}}`},
		{name: "wrapped done", payload: `{"success":true,"data":"[DONE]"}`, expectDone: true},
		{name: "failed envelope", payload: `{"success":false,"error":"boom"}`, expectErr: "boom"},
		{name: "nested error", eventType: "error", payload: `{"data":{"error":{"message":"nested boom"}}}`, expectErr: "nested boom"},
		{name: "event error", eventType: "error", payload: `{"message":"flat boom"}`, expectErr: "flat boom"},
		{name: "event error without body", eventType: "error", payload: `{}`, expectErr: "Cline request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			normalized, apiErr := normalizeClineStreamPayload(tc.eventType, tc.payload)
			if tc.expectErr != "" {
				require.NotNil(t, apiErr)
				assert.Contains(t, apiErr.Error(), tc.expectErr)
				return
			}
			require.Nil(t, apiErr)
			if tc.expectDone {
				assert.Equal(t, "[DONE]", string(normalized))
				return
			}
			assert.False(t, gjsonHas(normalized, "success"))
			assert.Equal(t, "chatcmpl-1", gjsonString(normalized, "id"))
		})
	}
}
