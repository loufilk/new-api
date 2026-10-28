package openai

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesBufferedFailureEstimatesCompletedMessageItem(t *testing.T) {
	const output = "A completed answer from a provider that does not send text deltas."
	for _, tc := range []struct {
		name, prelude, ending      string
		wantPrompt, wantCompletion int
	}{
		{name: "eof", wantPrompt: 99},
		{name: "failed", ending: `data: {"type":"response.failed","response":{"status":"failed"}}`, wantPrompt: 99},
		{name: "deltas_and_done_are_not_added", prelude: `data: {"type":"response.output_text.delta","output_index":0,"delta":"` + output + `"}`, wantPrompt: 99},
		{name: "reported_usage_remains_authoritative", prelude: `data: {"type":"response.in_progress","response":{"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`, wantPrompt: 11, wantCompletion: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := sseBody(
				`data: {"type":"response.created","response":{"status":"in_progress"}}`,
				tc.prelude,
				`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"message_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"`+output+`"}]}}`,
				tc.ending,
			)
			c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
			info.SetEstimatePromptTokens(99)
			usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			require.NotNil(t, usage)
			wantCompletion := tc.wantCompletion
			if wantCompletion == 0 {
				wantCompletion = tokenkit.Count(info.GetUpstreamModelName(), output)
			}
			assert.Equal(t, tc.wantPrompt, usage.PromptTokens)
			assert.Equal(t, wantCompletion, usage.CompletionTokens)
			assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
			assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyResponsesPartialUsage))
			assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
			assert.Empty(t, recorder.Body.String())
		})
	}
}

func TestResponsesBufferedFailureEstimatesCompletedNonMessageItem(t *testing.T) {
	for _, tc := range []struct {
		name, item, output string
	}{
		{
			name:   "reasoning",
			item:   `{"id":"reasoning_1","type":"reasoning","summary":[{"type":"summary_text","text":"Inspect the repository before editing."}]}`,
			output: "Inspect the repository before editing.",
		},
		{
			name:   "function_arguments",
			item:   `{"id":"function_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"billing\"}"}`,
			output: `{"query":"billing"}`,
		},
		{
			name:   "custom_input",
			item:   `{"id":"custom_1","type":"custom_tool_call","call_id":"call_1","name":"run","input":"print('billing')"}`,
			output: "print('billing')",
		},
		{
			name:   "refusal",
			item:   `{"id":"message_1","type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot help with that request."}]}`,
			output: "I cannot help with that request.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := sseBody(`data: {"type":"response.output_item.done","output_index":0,"item":` + tc.item + `}`)
			c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
			info.SetEstimatePromptTokens(99)
			usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 99, usage.PromptTokens)
			assert.Equal(t, tokenkit.Count(info.GetUpstreamModelName(), tc.output), usage.CompletionTokens)
			assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
			assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
			assert.Empty(t, recorder.Body.String())
		})
	}
}

func TestResponsesBufferedFailureDeduplicatesCustomInputAndRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, item, delta, done, output string
	}{
		{
			name:   "custom_input",
			item:   `{"id":"custom_1","type":"custom_tool_call","call_id":"call_1","name":"run","input":"print('billing')"}`,
			delta:  `{"type":"response.custom_tool_call_input.delta","item_id":"custom_1","output_index":0,"delta":"print('billing')"}`,
			done:   `{"type":"response.custom_tool_call_input.done","item_id":"custom_1","output_index":0,"input":"print('billing')"}`,
			output: "print('billing')",
		},
		{
			name:   "refusal",
			item:   `{"id":"message_1","type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot help with that request."}]}`,
			delta:  `{"type":"response.refusal.delta","item_id":"message_1","output_index":0,"content_index":0,"delta":"I cannot help with that request."}`,
			done:   `{"type":"response.refusal.done","item_id":"message_1","output_index":0,"content_index":0,"refusal":"I cannot help with that request."}`,
			output: "I cannot help with that request.",
		},
	} {
		for _, observedUsage := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reported_usage=%t", tc.name, observedUsage), func(t *testing.T) {
				prelude := ""
				if observedUsage {
					prelude = `data: {"type":"response.in_progress","response":{"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`
				}
				body := sseBody(prelude,
					"data: "+tc.delta,
					"data: "+tc.done,
					`data: {"type":"response.output_item.done","output_index":0,"item":`+tc.item+`}`,
					`data: {"type":"response.failed","response":{"status":"failed","output":[`+tc.item+`]}}`,
				)
				c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
				info.SetEstimatePromptTokens(99)
				usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
				require.NotNil(t, apiErr)
				require.NotNil(t, usage)
				wantPrompt, wantCompletion := 99, tokenkit.Count(info.GetUpstreamModelName(), tc.output)
				if observedUsage {
					wantPrompt, wantCompletion = 11, 4
				}
				assert.Equal(t, wantPrompt, usage.PromptTokens)
				assert.Equal(t, wantCompletion, usage.CompletionTokens, "deltas, done items and terminal snapshots describe the same generated output")
				assert.Equal(t, wantPrompt+wantCompletion, usage.TotalTokens)
				assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
				assert.Empty(t, recorder.Body.String())
			})
		}
	}
}

// These cases cover the non-streaming client path, not downstream SSE errors.
// The observed usage predates the ending, so neither error nor EOF may erase it.
func TestResponsesBufferedFailurePreservesObservedUsage(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		for _, tc := range []struct {
			name, ending, code string
		}{
			{"failed", `data: {"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"overloaded","message":"provider failed"}}}`, "overloaded"},
			{"bare_error", `data: {"type":"error","code":"overloaded","message":"provider failed","param":"model"}`, "overloaded"},
			{"cancelled", `data: {"type":"response.cancelled","response":{"status":"cancelled"}}`, "server_error"},
			{"completed_with_failed_status", `data: {"type":"response.completed","response":{"status":"failed","error":{"type":"server_error","code":"overloaded","message":"provider failed"}}}`, "overloaded"},
			{"eof", "", ""},
			{"malformed_event", "data: invalid-json", ""},
		} {
			t.Run(fmt.Sprintf("%s/%s", format, tc.name), func(t *testing.T) {
				body := sseBody(
					`data: {"type":"response.in_progress","response":{"model":"provider-model","usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15,"input_tokens_details":{"cached_tokens":3}}}}`,
					`data: {"type":"response.output_text.delta","delta":"partial output"}`,
					tc.ending,
				)
				c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
				info.RelayFormat = format
				info.SetEstimatePromptTokens(99)

				usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
				require.NotNil(t, apiErr)
				require.NotNil(t, usage)
				assert.Equal(t, 11, usage.PromptTokens)
				assert.Equal(t, 4, usage.CompletionTokens)
				assert.Equal(t, 15, usage.TotalTokens)
				assert.Equal(t, 3, usage.PromptTokensDetails.CachedTokens)
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyResponsesPartialUsage))
				assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled), "the controller still owes the nonstream client a JSON error")
				assert.False(t, c.Writer.Written(), "buffered failures must not publish a successful response")
				assert.Empty(t, recorder.Body.String())
				assert.False(t, info.IsStream)
				assert.NotEqual(t, string(relaycommon.ResponseOutcomeCompleted), info.StreamStatus.ResponseOutcome())
				if tc.code != "" {
					assert.Equal(t, tc.code, apiErr.ToOpenAIError().Code)
				}
				if tc.name == "bare_error" {
					assert.Equal(t, "provider failed", apiErr.ToOpenAIError().Message)
					assert.Equal(t, "model", apiErr.ToOpenAIError().Param)
				}
			})
		}
	}
}

func TestResponsesBufferedIncompletePreservesUsageAndTruncation(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			body := sseBody(
				`data: {"type":"response.in_progress","response":{"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`,
				`data: {"type":"response.output_text.delta","delta":"partial output"}`,
				`data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`,
			)
			c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
			info.RelayFormat = format
			usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 11, usage.PromptTokens)
			assert.Equal(t, 4, usage.CompletionTokens)
			assert.Equal(t, 15, usage.TotalTokens)
			assert.Equal(t, string(relaycommon.ResponseOutcomeIncomplete), info.StreamStatus.ResponseOutcome())
			assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
			assert.Contains(t, recorder.Body.String(), "partial output")
			switch format {
			case types.RelayFormatOpenAI:
				assert.Contains(t, recorder.Body.String(), `"finish_reason":"length"`)
			case types.RelayFormatClaude:
				assert.Contains(t, recorder.Body.String(), `"stop_reason":"max_tokens"`)
			case types.RelayFormatGemini:
				assert.Contains(t, recorder.Body.String(), `"finishReason":"MAX_TOKENS"`)
			}
		})
	}
}

func TestResponsesBufferedFailureWithoutOutputRemainsRefundable(t *testing.T) {
	for _, ending := range []string{
		`data: {"type":"response.failed","response":{"status":"failed"}}`,
		`data: {"type":"response.error","code":"overloaded","message":"failed"}`,
		`data: {"type":"response.canceled"}`,
		"data: [DONE]", "", "data: invalid-json",
	} {
		t.Run(ending, func(t *testing.T) {
			body := sseBody(`data: {"type":"response.created","response":{"status":"in_progress"}}`, ending)
			c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
			info.SetEstimatePromptTokens(99)
			usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			assert.Nil(t, usage, "a response prelude is not observed token or tool use")
			assert.Empty(t, recorder.Body.String())
		})
	}
}

func TestResponsesBufferedReadFailureRetainsPartialUsage(t *testing.T) {
	body := sseBody(`data: {"type":"response.in_progress","response":{"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`)
	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
	resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(body), responsesBufferedReadError{}))
	usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.NotNil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 15, usage.TotalTokens)
	assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
	assert.Empty(t, recorder.Body.String())
}

type responsesBufferedReadError struct{}

func (responsesBufferedReadError) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestResponsesBufferedConversionFailureKeepsUsageWithoutPolicySuccess(t *testing.T) {
	body := sseBody(`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[42]}]}]}}`)
	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
	usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.NotNil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 15, usage.TotalTokens)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyResponsesPartialUsage))
	assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
	service.MarkRequestPolicySuccess(c, info.StreamStatus)
	assert.False(t, service.RequestPolicy(c).Successful)
	assert.Empty(t, recorder.Body.String())
}

func TestResponsesBufferedClientCancellationRetainsObservedUsage(t *testing.T) {
	body := sseBody(`data: {"type":"response.in_progress","response":{"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`)
	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
	ctx, cancel := context.WithCancel(c.Request.Context())
	t.Cleanup(cancel)
	c.Request = c.Request.WithContext(ctx)
	resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(body), responsesBufferedCancellationReader{cancel: cancel}))
	usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.NotNil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 15, usage.TotalTokens)
	assert.Equal(t, service.ClientGoneErrorCode, apiErr.GetErrorCode())
	assert.False(t, types.IsRecordErrorLog(apiErr), "a disconnected client is not an upstream account failure")
	assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
	assert.Equal(t, string(relaycommon.ResponseOutcomeCancelled), info.StreamStatus.ResponseOutcome())
	assert.Empty(t, recorder.Body.String())
}

type responsesBufferedCancellationReader struct {
	cancel context.CancelFunc
}

func (r responsesBufferedCancellationReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, io.EOF
}
