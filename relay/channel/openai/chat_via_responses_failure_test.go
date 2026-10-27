package openai

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesConvertedStreamFailureKeepsBillableUsage(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		for _, tc := range []struct {
			name, usage        string
			prompt, completion int
			estimated          bool
		}{
			{"provider_usage", `,"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15,"input_tokens_details":{"cached_tokens":3}}`, 11, 4, false},
			{"missing_usage", "", 7, 0, true},
			{"missing_output", `,"usage":{"input_tokens":11,"total_tokens":11,"input_tokens_details":{"cached_tokens":3}}`, 11, 0, true},
			{"missing_input", `,"usage":{"output_tokens":4,"total_tokens":4,"input_tokens_details":{"cached_tokens":3}}`, 7, 4, true},
		} {
			t.Run(fmt.Sprintf("%s/%s", format, tc.name), func(t *testing.T) {
				body := sseBody(
					`data: {"type":"response.created","response":{"id":"resp_1","model":"private-provider-model"}}`,
					`data: {"type":"response.output_text.delta","delta":"partial output"}`,
					`data: {"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","message":"failure at https://private-provider.example/v1"}`+tc.usage+`}}`,
				)
				c, recorder, resp := newStreamEstimateTestContext(t, body)
				_, _, _, info := newResponsesChatTestContext(t, body, true)
				info.RelayFormat = format
				info.SetEstimatePromptTokens(7)
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "public-model")

				usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
				require.NotNil(t, apiErr)
				require.NotNil(t, usage)
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled))
				assert.True(t, info.StreamStatus.ResponseFailed())
				assert.Equal(t, tc.prompt, usage.PromptTokens)
				wantCompletion := tc.completion
				if wantCompletion == 0 {
					wantCompletion = tokenkit.Estimate(info.UpstreamModelName, "partial output")
					require.Positive(t, wantCompletion)
				}
				assert.Equal(t, wantCompletion, usage.CompletionTokens)
				assert.Equal(t, tc.prompt+wantCompletion, usage.TotalTokens)
				assert.Equal(t, tc.estimated, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
				if tc.usage != "" {
					require.NotNil(t, usage.BillingUsage)
					canonical, ok := usage.BillingUsage.CanonicalUsage()
					require.True(t, ok)
					assert.Equal(t, tc.prompt, canonical.PromptTokens, "the provider snapshot used by billing must include missing-count estimates")
					assert.Equal(t, wantCompletion, canonical.CompletionTokens)
					assert.Equal(t, 3, canonical.PromptTokensDetails.CachedTokens, "estimation must retain native cache facts")
					assert.Equal(t, tc.estimated, usage.BillingUsage.Estimated)
				}
				wire := recorder.Body.String()
				assert.Contains(t, wire, "partial output")
				assert.Equal(t, 1, strings.Count(wire, `"error":`))
				assert.NotContains(t, wire, "private-provider")
				assertFailedConvertedStreamHasNoNormalTerminal(t, wire)
			})
		}
	}
}

func TestResponsesConvertedNamedToolBeforeFailureIsGeneratedOutput(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			body := sseBody(
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"lookup_weather","arguments":""}}`,
				`data: {"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","message":"provider failed"}}}`,
			)
			c, recorder, resp := newStreamEstimateTestContext(t, body)
			_, _, _, info := newResponsesChatTestContext(t, body, true)
			info.RelayFormat = format
			info.SetEstimatePromptTokens(6)
			usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			require.NotNil(t, usage, "a generated tool name is not a role-only prelude")
			assert.Equal(t, 6, usage.PromptTokens)
			assert.Equal(t, tokenkit.Estimate(info.UpstreamModelName, "lookup_weather"), usage.CompletionTokens)
			assert.Positive(t, usage.CompletionTokens)
			assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
			assertFailedConvertedStreamHasNoNormalTerminal(t, recorder.Body.String())
		})
	}
}

func TestResponsesConvertedRoleOnlyFailureRemainsRefundable(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			body := sseBody(
				`data: {"type":"response.created","response":{"id":"resp_1"}}`,
				`data: {"type":"error","code":"server_error","message":"provider failed"}`,
			)
			c, recorder, resp := newStreamEstimateTestContext(t, body)
			_, _, _, info := newResponsesChatTestContext(t, body, true)
			info.RelayFormat = format
			info.SetEstimatePromptTokens(99)
			usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			assert.Nil(t, usage, "a client-visible role does not justify retaining the pre-consumed prompt quota")
			assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
			assert.Equal(t, 1, strings.Count(recorder.Body.String(), `"error":`))
			assertFailedConvertedStreamHasNoNormalTerminal(t, recorder.Body.String())
		})
	}
}

func TestResponsesConvertedInterruptedStreamDoesNotFabricateSuccess(t *testing.T) {
	for _, ending := range []string{
		"", "data: [DONE]", "data: invalid-json",
		`data: {"type":"response.done","response":{"status":"failed","error":{"type":"server_error","message":"provider failed"}}}`,
		`data: {"type":"response.completed","response":{"status":"failed","error":{"type":"server_error","message":"provider failed"}}}`,
	} {
		t.Run(ending, func(t *testing.T) {
			body := sseBody(`data: {"type":"response.output_text.delta","delta":"interrupted"}`, ending)
			c, recorder, resp := newStreamEstimateTestContext(t, body)
			_, _, _, info := newResponsesChatTestContext(t, body, true)
			info.SetEstimatePromptTokens(9)
			usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			require.NotNil(t, usage)
			assert.Positive(t, usage.CompletionTokens)
			assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
			assert.True(t, info.StreamStatus.OutcomeSnapshot().ExpectsTerminal)
			assertFailedConvertedStreamHasNoNormalTerminal(t, recorder.Body.String())
		})
	}
}

func TestResponsesToGeminiStreamMissingUsageEstimatesGeneratedOutput(t *testing.T) {
	body := sseBody(
		`data: {"type":"response.output_text.delta","delta":"hello from a composed converter"}`,
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
	)
	c, recorder, resp := newStreamEstimateTestContext(t, body)
	_, _, _, info := newResponsesChatTestContext(t, body, true)
	info.RelayFormat = types.RelayFormatGemini
	info.SetEstimatePromptTokens(6)
	usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	want := service.ResponseText2Usage(c, "hello from a composed converter", info.UpstreamModelName, 6)
	assert.Equal(t, want.PromptTokens, usage.PromptTokens)
	assert.Equal(t, want.CompletionTokens, usage.CompletionTokens)
	assert.Equal(t, want.TotalTokens, usage.TotalTokens)
	assert.Positive(t, usage.CompletionTokens)
	assert.Contains(t, recorder.Body.String(), `"finishReason":"STOP"`)
	assert.Contains(t, recorder.Body.String(), fmt.Sprintf(`"candidatesTokenCount":%d`, want.CompletionTokens))
}

// These failures originate in the real ResponseWriter beneath Gin, rather
// than by marking a request cancelled before the handler sees any output.
func TestResponsesConvertedStreamStopsOnDownstreamDeliveryFailure(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		for _, failure := range []string{"write", "short_write", "flush", "first_write"} {
			t.Run(fmt.Sprintf("%s/%s", format, failure), func(t *testing.T) {
				body := sseBody(
					`data: {"type":"response.output_text.delta","delta":"partial output"}`,
					`data: {"type":"response.output_text.delta","delta":"must never be delivered"}`,
					`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":900,"output_tokens":900,"total_tokens":1800}}}`,
				)
				_, _, resp := newStreamEstimateTestContext(t, body)
				_, _, _, info := newResponsesChatTestContext(t, body, true)
				info.RelayFormat = format
				info.SetEstimatePromptTokens(8)
				writer := &responsesDeliveryFailureWriter{ResponseRecorder: httptest.NewRecorder(), failure: failure}
				c, _ := gin.CreateTestContext(writer)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
				require.NotNil(t, apiErr)
				require.NotNil(t, usage)
				assert.Equal(t, service.ClientGoneErrorCode, apiErr.GetErrorCode())
				assert.False(t, types.IsRecordErrorLog(apiErr))
				assert.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
				assert.Positive(t, usage.CompletionTokens)
				assert.Equal(t, tokenkit.Estimate(info.UpstreamModelName, "partial output"), usage.CompletionTokens, "even a failed role prelude belongs to an already-observed output batch")
				assert.Less(t, usage.CompletionTokens, 900, "do not read a later terminal after downstream delivery failed")
				assert.Equal(t, string(relaycommon.ResponseOutcomeCancelled), info.StreamStatus.ResponseOutcome())
				assert.NotContains(t, writer.Body.String(), "must never be delivered")
				assert.NotContains(t, writer.Body.String(), `"error":`, "a disconnected client must not receive another write")
				assertFailedConvertedStreamHasNoNormalTerminal(t, writer.Body.String())
			})
		}
	}
}

type responsesDeliveryFailureWriter struct {
	*httptest.ResponseRecorder
	failure        string
	pendingFailure bool
}

func (w *responsesDeliveryFailureWriter) Write(p []byte) (int, error) {
	if w.failure == "first_write" {
		return 0, io.ErrClosedPipe
	}
	if strings.Contains(string(p), "partial output") {
		switch w.failure {
		case "write":
			return 0, errors.New("downstream connection closed")
		case "short_write":
			return len(p) / 2, nil
		case "flush":
			w.pendingFailure = true
		}
	}
	return w.ResponseRecorder.Write(p)
}

func (w *responsesDeliveryFailureWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *responsesDeliveryFailureWriter) FlushError() error {
	if w.pendingFailure {
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}

func assertFailedConvertedStreamHasNoNormalTerminal(t *testing.T, wire string) {
	t.Helper()
	assert.NotContains(t, wire, "[DONE]")
	assert.NotContains(t, wire, `"finish_reason":"stop"`)
	assert.NotContains(t, wire, `"finishReason":"STOP"`)
	assert.NotContains(t, wire, "event: message_stop")
}
