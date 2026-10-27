package openai

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the actual underlying Write/FlushError path through Gin. Once the
// selected frame fails, any subsequent write is itself a regression.
type streamDeliveryFailureWriter struct {
	*httptest.ResponseRecorder
	match              string
	mode               string
	failed             bool
	failOnFlush        bool
	writesAfterFailure int
}

func (w *streamDeliveryFailureWriter) Write(p []byte) (int, error) {
	if w.failed {
		w.writesAfterFailure++
		return 0, io.ErrClosedPipe
	}
	if strings.Contains(string(p), w.match) {
		switch w.mode {
		case "write":
			w.failed = true
			return 0, io.ErrClosedPipe
		case "short_write":
			w.failed = true
			return len(p) / 2, nil
		case "flush":
			w.failOnFlush = true
		}
	}
	return w.ResponseRecorder.Write(p)
}

func (w *streamDeliveryFailureWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *streamDeliveryFailureWriter) FlushError() error {
	if w.failOnFlush {
		w.failed = true
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}

func attachStreamDeliveryFailure(c *gin.Context, match, mode string) *streamDeliveryFailureWriter {
	w := &streamDeliveryFailureWriter{ResponseRecorder: httptest.NewRecorder(), match: match, mode: mode}
	writerContext, _ := gin.CreateTestContext(w)
	c.Writer = writerContext.Writer
	return w
}

func assertStreamDeliveryFailure(t *testing.T, apiErr *types.NewAPIError, w *streamDeliveryFailureWriter) {
	t.Helper()
	require.NotNil(t, apiErr)
	assert.Equal(t, service.ClientGoneErrorCode, apiErr.GetErrorCode())
	assert.Equal(t, types.ErrorTypeNewAPIError, apiErr.GetErrorType())
	assert.Equal(t, service.StatusClientClosedRequest, apiErr.StatusCode)
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.False(t, types.IsRecordErrorLog(apiErr), "a failed client socket is not an upstream channel failure")
	if w.mode == "short_write" {
		assert.ErrorIs(t, apiErr, io.ErrShortWrite)
	} else {
		assert.ErrorIs(t, apiErr, io.ErrClosedPipe)
	}
	assert.True(t, w.failed, "the selected delivery operation must actually fail")
	assert.Zero(t, w.writesAfterFailure, "do not append another frame after a failed delivery")
}

func deliveryChatChunk(delta string) string {
	return `data: {"id":"chat-delivery","created":123,"model":"upstream-model","choices":[{"index":0,"delta":` + delta + `}]}` + "\n\n"
}

func TestOaiResponsesStreamDeliveryFailurePreservesObservedUsage(t *testing.T) {
	for _, mode := range []string{"write", "short_write", "flush"} {
		for _, reported := range []bool{false, true} {
			name := mode + "/estimated"
			body := `data: {"type":"response.output_text.delta","delta":"partial output"}` + "\n\n"
			match := "partial output"
			if reported {
				name = mode + "/reported"
				body = `data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15,"input_tokens_details":{"cached_tokens":3}}}}` + "\n\n"
				match = "response.completed"
			}
			t.Run(name, func(t *testing.T) {
				body += `data: {"type":"response.output_text.delta","delta":"unobserved"}` + "\n\n" +
					`data: {"type":"response.completed","response":{"usage":{"input_tokens":900,"output_tokens":900}}}` + "\n\n"
				c, _, info, resp := responsesStreamFixture(t, strings.NewReader(body))
				info.SetEstimatePromptTokens(8)
				w := attachStreamDeliveryFailure(c, match, mode)

				usage, apiErr := OaiResponsesStreamHandler(c, info, resp)

				assertStreamDeliveryFailure(t, apiErr, w)
				require.NotNil(t, usage)
				if reported {
					assert.Equal(t, 11, usage.PromptTokens)
					assert.Equal(t, 4, usage.CompletionTokens)
					assert.Equal(t, 3, usage.PromptTokensDetails.CachedTokens)
					require.NotNil(t, usage.InputTokensDetails)
					assert.Equal(t, 3, usage.InputTokensDetails.CachedTokens)
				} else {
					assert.Equal(t, 8, usage.PromptTokens)
					assert.Equal(t, tokenkit.Count(info.UpstreamModelName, "partial output"), usage.CompletionTokens)
					assert.Positive(t, usage.CompletionTokens)
				}
				assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyResponsesStreamErrorHandled))
				assert.NotContains(t, w.Body.String(), "unobserved")
				assert.NotContains(t, w.Body.String(), "response.failed")
			})
		}
	}
}

func TestOaiStreamDeliveryFailureStopsAllFormatsAndKeepsObservedOutput(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		for _, mode := range []string{"write", "short_write", "flush"} {
			t.Run(string(format)+"/"+mode, func(t *testing.T) {
				body := deliveryChatChunk(`{"role":"assistant"}`) +
					deliveryChatChunk(`{"content":"partial output"}`) +
					deliveryChatChunk(`{"content":" already observed"}`) +
					deliveryChatChunk(`{"content":"unobserved"}`) +
					`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":900,"completion_tokens":900,"total_tokens":1800}}` + "\n\ndata: [DONE]\n\n"
				c, _, info, resp := responsesStreamFixture(t, strings.NewReader(body))
				info.RelayFormat, info.RelayMode = format, relayconstant.RelayModeChatCompletions
				info.OriginModelName = "public-model"
				info.SetEstimatePromptTokens(8)
				w := attachStreamDeliveryFailure(c, "partial output", mode)

				usage, apiErr := OaiStreamHandler(c, info, resp)

				assertStreamDeliveryFailure(t, apiErr, w)
				require.NotNil(t, usage)
				assert.Equal(t, 8, usage.PromptTokens)
				assert.Equal(t, service.ResponseText2Usage(c, "partial output already observed", info.UpstreamModelName, 8).CompletionTokens, usage.CompletionTokens)
				assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled))
				assert.NotContains(t, w.Body.String(), "already observed")
				assert.NotContains(t, w.Body.String(), "unobserved")
				assert.NotContains(t, w.Body.String(), "[DONE]")
				assert.NotContains(t, w.Body.String(), "message_stop")
				assert.NotContains(t, w.Body.String(), `"finishReason":"STOP"`)
			})
		}
	}
}

func TestOaiStreamFinalDeliveryFailureReturnsErrorAndKeepsUsage(t *testing.T) {
	cases := []struct {
		name, match string
		format      types.RelayFormat
	}{
		{"OpenAI usage", `"usage":{`, types.RelayFormatOpenAI},
		{"OpenAI DONE", "[DONE]", types.RelayFormatOpenAI},
		{"Claude delta", "event: message_delta", types.RelayFormatClaude},
		{"Claude stop", "event: message_stop", types.RelayFormatClaude},
		{"Gemini stop", `"finishReason":"STOP"`, types.RelayFormatGemini},
	}
	for _, tc := range cases {
		for _, mode := range []string{"write", "flush"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				body := deliveryChatChunk(`{"role":"assistant"}`) + deliveryChatChunk(`{"content":"generated output"}`) +
					`data: {"id":"chat-delivery","created":123,"model":"upstream-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
				c, _, info, resp := responsesStreamFixture(t, strings.NewReader(body))
				info.RelayFormat, info.RelayMode = tc.format, relayconstant.RelayModeChatCompletions
				info.OriginModelName = "public-model"
				info.ShouldIncludeUsage = true
				info.SetEstimatePromptTokens(8)
				w := attachStreamDeliveryFailure(c, tc.match, mode)

				usage, apiErr := OaiStreamHandler(c, info, resp)

				assertStreamDeliveryFailure(t, apiErr, w)
				require.NotNil(t, usage)
				assert.Equal(t, 8, usage.PromptTokens)
				assert.Positive(t, usage.CompletionTokens)
				assert.Contains(t, w.Body.String(), "generated output")
				assert.NotContains(t, w.Body.String(), `"error":`)
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled))
			})
		}
	}
}

func TestOaiStreamMalformedFrameIsNotClientGone(t *testing.T) {
	body := deliveryChatChunk(`{"content":"partial output"}`) + "data: {invalid\n\ndata: [DONE]\n\n"
	c, w, info, resp := responsesStreamFixture(t, strings.NewReader(body))
	info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
	info.SetEstimatePromptTokens(8)
	usage, apiErr := OaiStreamHandler(c, info, resp)
	require.NotNil(t, apiErr)
	assert.NotEqual(t, service.ClientGoneErrorCode, apiErr.GetErrorCode())
	assert.True(t, types.IsRecordErrorLog(apiErr))
	assert.True(t, types.IsSkipRetryError(apiErr))
	require.NotNil(t, usage)
	assert.Positive(t, usage.CompletionTokens)
	assert.Contains(t, w.Body.String(), "partial output")
	assert.Contains(t, w.Body.String(), `"error":`)
	assert.NotContains(t, w.Body.String(), "[DONE]")
}

func TestOaiStreamPingFailureReachesNativeAndChatCallers(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	oldEnabled, oldInterval := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 1
	t.Cleanup(func() { settings.PingIntervalEnabled, settings.PingIntervalSeconds = oldEnabled, oldInterval })
	for _, name := range []string{"Responses", "Chat", "ChatToResponses"} {
		t.Run(name, func(t *testing.T) {
			pr, pw := io.Pipe()
			c, _, info, resp := responsesStreamFixture(t, pr)
			resp.Body = pr
			ctx, cancel := context.WithCancel(c.Request.Context())
			c.Request = c.Request.WithContext(ctx)
			info.DisablePing = false
			info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
			info.SetEstimatePromptTokens(8)
			w := attachStreamDeliveryFailure(c, ": PING", "flush")
			t.Cleanup(func() { cancel(); _ = pr.Close(); _ = pw.Close() })
			type result struct {
				usage  *dto.Usage
				apiErr *types.NewAPIError
			}
			done := make(chan result, 1)
			go func() {
				if name == "Responses" {
					usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
					done <- result{usage, apiErr}
				} else if name == "ChatToResponses" {
					usage, apiErr := OaiChatToResponsesStreamHandler(c, info, resp)
					done <- result{usage, apiErr}
				} else {
					usage, apiErr := OaiStreamHandler(c, info, resp)
					done <- result{usage, apiErr}
				}
			}()
			body := deliveryChatChunk(`{"content":"generated output"}`)
			if name == "Responses" {
				body = `data: {"type":"response.output_text.delta","delta":"generated output"}` + "\n\n"
			}
			_, err := io.WriteString(pw, body)
			require.NoError(t, err)
			select {
			case got := <-done:
				assertStreamDeliveryFailure(t, got.apiErr, w)
				require.NotNil(t, got.usage)
				assert.Equal(t, 8, got.usage.PromptTokens)
				assert.Positive(t, got.usage.CompletionTokens)
				assert.NoError(t, c.Request.Context().Err(), "the handler must react to the failed ping without waiting for request cancellation")
				assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
				assert.NotContains(t, w.Body.String(), "[DONE]")
				assert.NotContains(t, w.Body.String(), "response.failed")
			case <-time.After(5 * time.Second):
				cancel()
				_ = pr.Close()
				<-done
				t.Fatal("ping delivery failure did not stop the upstream stream")
			}
			_, err = pw.Write([]byte("upstream still running"))
			assert.ErrorIs(t, err, io.ErrClosedPipe)
		})
	}
}
