package privacy

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
)

func TestSanitizePayloadRewritesOnlyRecognizedModelFields(t *testing.T) {
	payload := `{"model":"private-upstream","vendor_extension":{"integer":9007199254740993,"nullable":null},"output":[{"type":"function_call","arguments":"{\"model\":\"user-content\"}"}],"user_text":"model: private-upstream"}`
	result := SanitizePayload([]byte(payload), "requested-model", false)

	assert.Equal(t, "requested-model", stringValue(t, result, "model"))
	assert.Contains(t, string(result), `"integer":9007199254740993`, "numeric lexemes must survive patching")
	assert.Contains(t, string(result), `"arguments":"{\"model\":\"user-content\"}"`, "tool arguments are user content")
	assert.Contains(t, string(result), `"user_text":"model: private-upstream"`, "unrelated strings are untouched")
	assert.Contains(t, string(result), `"nullable":null`)
}

func TestSanitizePayloadModelProtocolShapes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		path    string
	}{
		{name: "chat", payload: `{"model":"private","choices":[]}`, path: "model"},
		{name: "gemini native", payload: `{"modelVersion":"private","candidates":[]}`, path: "modelVersion"},
		{name: "claude message start", payload: `{"type":"message_start","message":{"model":"private","content":[]}}`, path: "message.model"},
		{name: "responses event", payload: `{"type":"response.created","response":{"model":"private"}}`, path: "response.model"},
		{name: "realtime session", payload: `{"type":"session.created","session":{"model":"private"}}`, path: "session.model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := SanitizePayload([]byte(tc.payload), "requested-model", false)
			assert.Equal(t, "requested-model", stringValue(t, result, tc.path))
		})
	}
}

func TestSanitizePayloadLeavesAbsentOrNonStringModelsAlone(t *testing.T) {
	for _, payload := range []string{
		`{"choices":[]}`,
		`{"model":null}`,
		`{"model":42}`,
		`{"model":{}}`,
		`[{"model":"private"}]`,
		`not json`,
		`[DONE]`,
		``,
	} {
		assert.Equal(t, payload, string(SanitizePayload([]byte(payload), "requested-model", false)), payload)
	}
}

func TestSanitizePayloadScrubsOpenAIErrorObject(t *testing.T) {
	payload := `{"error":{"message":"upstream private-provider-model rejected org-secret-42","type":"invalid_request_error","code":"org-secret-42","param":"api_key","metadata":{"provider":"private"}}}`
	result := SanitizePayload([]byte(payload), "requested-model", true)

	assert.Equal(t, "invalid request", stringValue(t, result, "error.message"))
	assert.Equal(t, "invalid_request_error", stringValue(t, result, "error.type"))
	assert.False(t, exists(t, result, "error.code"), "provider-specific codes are dropped")
	assert.False(t, exists(t, result, "error.param"))
	assert.False(t, exists(t, result, "error.metadata"))
	assert.NotContains(t, string(result), "private")
	assert.NotContains(t, string(result), "org-secret")
}

func TestSanitizePayloadKeepsAllowlistedErrorCodes(t *testing.T) {
	payload := `{"error":{"message":"context too long","type":"invalid_request_error","code":"context_length_exceeded"}}`
	result := SanitizePayload([]byte(payload), "", true)

	assert.Equal(t, "invalid request", stringValue(t, result, "error.message"))
	assert.Equal(t, "context_length_exceeded", stringValue(t, result, "error.code"))
}

func TestSanitizePayloadScrubsResponsesErrors(t *testing.T) {
	nested := `{"type":"response.failed","sequence_number":3,"response":{"id":"resp_1","model":"private","error":{"code":"server_error","message":"raw upstream detail","metadata":{"trace":"private"}}}}`
	result := SanitizePayload([]byte(nested), "requested-model", true)
	assert.Equal(t, "requested-model", stringValue(t, result, "response.model"))
	assert.Equal(t, "server_error", stringValue(t, result, "response.error.code"))
	assert.Equal(t, "upstream request failed", stringValue(t, result, "response.error.message"))
	assert.False(t, exists(t, result, "response.error.metadata"))
	assert.Equal(t, float64(3), numberValue(t, result, "sequence_number"))
	assert.NotContains(t, string(result), "raw upstream detail")

	flat := `{"type":"error","code":"private-code","message":"raw upstream detail","param":"model","sequence_number":2}`
	result = SanitizePayload([]byte(flat), "", true)
	assert.Equal(t, "upstream request failed", stringValue(t, result, "message"))
	assert.False(t, exists(t, result, "code"))
	assert.False(t, exists(t, result, "param"))
	assert.Equal(t, float64(2), numberValue(t, result, "sequence_number"))

	// An error frame without a message must not gain one, so successful-frame
	// shapes and absent fields stay exactly as the upstream sent them.
	absent := `{"type":"response.failed","response":{"id":"first","status":"failed"}}`
	result = SanitizePayload([]byte(absent), "", true)
	assert.False(t, exists(t, result, "message"))
	assert.False(t, exists(t, result, "response.error"))
}

func TestSanitizePayloadScrubsClaudeAndGeminiErrors(t *testing.T) {
	claude := `{"type":"error","error":{"type":"overloaded_error","message":"provider-private overload"}}`
	result := SanitizePayload([]byte(claude), "", true)
	assert.Equal(t, "overloaded_error", stringValue(t, result, "error.type"))
	assert.Equal(t, "upstream request failed", stringValue(t, result, "error.message"))

	gemini := `{"error":{"code":429,"message":"quota for org-private exceeded","status":"RESOURCE_EXHAUSTED","details":[{"private":"detail"}]}}`
	result = SanitizePayload([]byte(gemini), "", true)
	assert.Equal(t, float64(429), numberValue(t, result, "error.code"))
	assert.Equal(t, "RESOURCE_EXHAUSTED", stringValue(t, result, "error.status"))
	assert.Equal(t, "too many requests", stringValue(t, result, "error.message"))
	assert.False(t, exists(t, result, "error.details"))
	assert.NotContains(t, string(result), "org-private")

	unknownStatus := `{"error":{"code":500,"message":"raw","status":"SECRET_STATUS"}}`
	result = SanitizePayload([]byte(unknownStatus), "", true)
	assert.False(t, exists(t, result, "error.status"))

	unknownNumericCode := `{"error":{"code":70000,"message":"raw","type":"server_error"}}`
	result = SanitizePayload([]byte(unknownNumericCode), "", true)
	assert.False(t, exists(t, result, "error.code"), "non-status numeric codes are dropped")
	assert.Equal(t, "upstream request failed", stringValue(t, result, "error.message"))

	statusNumericCode := `{"error":{"code":503,"message":"raw","type":"server_error"}}`
	result = SanitizePayload([]byte(statusNumericCode), "", true)
	assert.Equal(t, float64(503), numberValue(t, result, "error.code"))
}

func TestSanitizeClientPayloadAppendsLocalRequestID(t *testing.T) {
	c := newPrivacyContextForPayload("req-local-9")
	payload := []byte(`{"error":{"code":"server_error","message":"raw upstream"}}`)
	result := SanitizeClientPayload(c, payload)
	assert.Equal(t, "upstream request failed (request id: req-local-9)", stringValue(t, result, "error.message"))

	c.Set(common.RequestIdKey, "req-local-10")
	result = SanitizeClientPayload(c, []byte(`{"type":"error","code":"server_error","message":"raw upstream","param":"x"}`))
	assert.Equal(t, "upstream request failed (request id: req-local-10)", stringValue(t, result, "message"))
	assert.False(t, exists(t, result, "param"))

	result = SanitizeClientPayload(nil, []byte(`{"error":{"code":"server_error","message":"raw upstream"}}`))
	assert.Equal(t, "upstream request failed", stringValue(t, result, "error.message"),
		"a nil context must not panic and must not fabricate a request id")
}

func TestSanitizePayloadFlatErrorEnvelopeDropsUnknownFields(t *testing.T) {
	payload := `{"type":"error","code":"private-provider-code","message":"raw upstream detail","param":"api_key","metadata":{"trace":"private"},"upstream_request_id":"upstream-private","request_id":"vendor-request","sequence_number":4,"event_id":"evt_1","stream_id":"planner"}`
	result := SanitizePayload([]byte(payload), "", true)

	assert.Equal(t, "upstream request failed", stringValue(t, result, "message"))
	assert.False(t, exists(t, result, "code"), "provider-specific codes are dropped")
	assert.False(t, exists(t, result, "param"))
	assert.False(t, exists(t, result, "metadata"))
	assert.False(t, exists(t, result, "upstream_request_id"))
	assert.False(t, exists(t, result, "request_id"))
	assert.NotContains(t, string(result), "private")
	assert.Equal(t, "error", stringValue(t, result, "type"))
	assert.Equal(t, float64(4), numberValue(t, result, "sequence_number"), "stream correlation is preserved")
	assert.Equal(t, "evt_1", stringValue(t, result, "event_id"))
	assert.Equal(t, "planner", stringValue(t, result, "stream_id"))
}

func TestSanitizePayloadFlatErrorEnvelopeKeepsSafeCodesAndNestedSanitized(t *testing.T) {
	payload := `{"type":"response.failed","sequence_number":3,"response":{"id":"resp_1","model":"private","status":"failed","error":{"code":"server_error","message":"raw","metadata":{"trace":"private"}}}}`
	result := SanitizePayload([]byte(payload), "public", true)

	assert.Equal(t, "response.failed", stringValue(t, result, "type"))
	assert.Equal(t, float64(3), numberValue(t, result, "sequence_number"))
	assert.Equal(t, "public", stringValue(t, result, "response.model"))
	assert.Equal(t, "server_error", stringValue(t, result, "response.error.code"))
	assert.Equal(t, "upstream request failed", stringValue(t, result, "response.error.message"))
	assert.False(t, exists(t, result, "response.error.metadata"))
	assert.Equal(t, "resp_1", stringValue(t, result, "response.id"))
	assert.NotContains(t, string(result), "private")
}

func TestSanitizePayloadKeepsUserContentThatLooksLikeErrors(t *testing.T) {
	payload := `{"choices":[{"message":{"role":"assistant","content":"error: private-upstream failed","tool_calls":[{"function":{"name":"lookup","arguments":"{\"error\":{\"message\":\"private\"}}"}}]}}],"usage":{"total_tokens":3}}`
	result := SanitizePayload([]byte(payload), "", true)
	assert.Equal(t, payload, string(result), "content and tool arguments must not be rewritten")
}

func TestSanitizePayloadUnrestrictedKeepsDiagnostics(t *testing.T) {
	payload := `{"model":"private","error":{"message":"raw upstream detail","type":"server_error"}}`
	result := SanitizePayload([]byte(payload), "requested-model", false)
	assert.Equal(t, "requested-model", stringValue(t, result, "model"), "the requested model is always the client-visible identity")
	assert.Equal(t, "raw upstream detail", stringValue(t, result, "error.message"), "administrators keep diagnostics")
}
