package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStreamEstimateTestContext(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder, *http.Response) {
	t.Helper()

	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "stream-estimate-test")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	return c, recorder, resp
}

func sseBody(lines ...string) string {
	return strings.Join(append(lines, "", ""), "\n")
}

func TestOaiResponsesToChatStreamFallbackMatchesResponseText2Usage(t *testing.T) {
	body := sseBody(
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Planning"}`,
		`data: {"type":"response.reasoning_summary_text.delta","delta":" file inspection"}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.output_text.delta","delta":" world"}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"q\":\"x\"}"}`,
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
		`data: [DONE]`,
	)
	c, _, resp := newStreamEstimateTestContext(t, body)
	_, _, _, info := newResponsesChatTestContext(t, body, true)

	usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	expectedText := "Planning file inspection" + "hello world" + "lookup" + "{\"q\":\"x\"}"
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	want := service.ResponseText2Usage(c2, expectedText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	require.NotNil(t, want)

	assert.Equal(t, want.PromptTokens, usage.PromptTokens)
	assert.Equal(t, want.CompletionTokens, usage.CompletionTokens)
	assert.Equal(t, want.TotalTokens, usage.TotalTokens)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
}

func TestOaiResponsesToChatStreamKeepsValidUpstreamUsage(t *testing.T) {
	body := sseBody(
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.output_text.delta","delta":"hello world, this text must not affect usage"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":11,"output_tokens":22,"total_tokens":33}}}`,
		`data: [DONE]`,
	)
	c, _, resp := newStreamEstimateTestContext(t, body)
	_, _, _, info := newResponsesChatTestContext(t, body, true)

	usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	assert.Equal(t, 11, usage.PromptTokens)
	assert.Equal(t, 22, usage.CompletionTokens)
	assert.Equal(t, 33, usage.TotalTokens)
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
}

func TestOaiResponsesToChatStreamZeroTotalFallsBackToLocalEstimate(t *testing.T) {
	body := sseBody(
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.output_text.delta","delta":"fallback text"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`,
		`data: [DONE]`,
	)
	c, _, resp := newStreamEstimateTestContext(t, body)
	_, _, _, info := newResponsesChatTestContext(t, body, true)

	usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	want := service.ResponseText2Usage(c2, "fallback text", info.UpstreamModelName, info.GetEstimatePromptTokens())
	assert.Equal(t, want.CompletionTokens, usage.CompletionTokens)
	assert.Equal(t, want.TotalTokens, usage.TotalTokens)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
}
