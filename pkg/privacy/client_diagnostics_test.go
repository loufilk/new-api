package privacy

import (
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
	"net/http"
	"testing"
)

func TestClientDiagnosticsUseFixedMessagesWithoutLeakingSecrets(t *testing.T) {
	for _, tc := range []struct {
		status    int
		raw, want string
	}{
		{400, "thinking.type invalid; provider=https://private.test key=sk-secret", "invalid thinking configuration; check the thinking mode, budget and compatible message format"},
		{400, "tool_result missing tool_call_id secret", "invalid tool conversation; match each tool result to its tool call and preserve message order"},
		{429, "concurrency exceeded for org-secret", "too many concurrent requests; reduce parallel requests and retry later"},
		{413, "input is too long secret", "request too large"},
		{400, "unknown secret upstream response", "invalid request"},
	} {
		assert.Equal(t, tc.want, ClientErrorText(tc.status, "", nil, tc.raw))
	}
}

func TestClientErrorCodeVisibilityAppliesToHTTPAndStreaming(t *testing.T) {
	setting := operation_setting.GetLogSetting()
	previous := setting.ClientErrorCodesEnabled
	t.Cleanup(func() { setting.ClientErrorCodesEnabled = previous })
	for _, enabled := range []bool{true, false} {
		setting.ClientErrorCodesEnabled = enabled
		safe := SanitizeClientOpenAIError(nil, http.StatusTooManyRequests, types.OpenAIError{Type: "rate_limit_error", Code: "rate_limit_exceeded", Message: "concurrency exceeded key=secret"})
		assert.NotContains(t, safe.Message, "secret")
		assert.Equal(t, enabled, safe.Code != nil)
		nested := SanitizePayload([]byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"concurrency exceeded key=secret"}}`), "", true)
		assert.Equal(t, enabled, gjson.GetBytes(nested, "error.code").Exists())
		assert.Contains(t, gjson.GetBytes(nested, "error.message").Str, "concurrent")
		flat := SanitizePayload([]byte(`{"type":"error","code":"rate_limit_exceeded","message":"concurrency exceeded key=secret"}`), "", true)
		assert.Equal(t, enabled, gjson.GetBytes(flat, "code").Exists())
		assert.NotContains(t, string(flat), "secret")
		hidden := SanitizeClientOpenAIError(nil, 413, types.OpenAIError{Code: "request_too_large", Message: "private size limit"})
		assert.Nil(t, hidden.Code)
		assert.Equal(t, "request too large", hidden.Message)
	}
}

func TestClientDiagnosticSanitizationIsIdempotent(t *testing.T) {
	setting := operation_setting.GetLogSetting()
	old := setting.ClientErrorCodesEnabled
	setting.ClientErrorCodesEnabled = false
	t.Cleanup(func() { setting.ClientErrorCodesEnabled = old })
	for _, raw := range []string{"context length exceeded private", "tool_result missing tool_call_id private", "thinking.type disabled private"} {
		first := ClientErrorText(400, "", nil, raw)
		assert.Equal(t, first, ClientErrorText(400, "", nil, first))
	}
	hidden := SanitizePayload([]byte(`{"error":{"type":"request_too_large","code":413,"message":"thinking secret"}}`), "", true)
	assert.False(t, gjson.GetBytes(hidden, "error.code").Exists())
	assert.Equal(t, "request too large", gjson.GetBytes(hidden, "error.message").Str)
}

func TestReasoningAdapterDiagnosticIsVisibleWithoutUpstreamDetails(t *testing.T) {
	const want = "invalid thinking configuration; check the thinking mode, budget and compatible message format"
	assert.Equal(t, want, ClientErrorText(400, "", "reasoning_controls_rejected", "request rejected"))
	assert.Equal(t, want, ClientErrorText(400, "", nil, "reasoning_controls_rejected"))
	assert.Equal(t, want, ClientErrorText(400, "", nil, "Upstream rejected reasoning controls for this model."))
	result := SanitizePayload([]byte(`{"error":{"code":"reasoning_controls_rejected","message":"private upstream request rejected"}}`), "", true)
	assert.Equal(t, want, gjson.GetBytes(result, "error.message").Str)
	assert.NotContains(t, string(result), "private")
}

func TestAdapterToolDiagnosticsPreserveActionableCategories(t *testing.T) {
	for _, source := range []string{"tool_history_mismatch", "Upstream rejected tool history: preserve tool calls and their corresponding results."} {
		assert.Contains(t, ClientErrorText(400, "", nil, source), "match each tool result")
	}
	for _, source := range []string{"tool_schema_rejected", "Upstream rejected the tool schema."} {
		assert.Equal(t, "invalid tool definition; check tool names and JSON schemas", ClientErrorText(400, "", nil, source))
	}
}
