package openai

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The upstream deliberately sends no successor until the current frame reaches
// Flush. A one-frame lookahead cannot pass this handshake.
type chatFlushRecorder struct {
	*httptest.ResponseRecorder
	flushed chan string
}

func (w *chatFlushRecorder) FlushError() error {
	w.ResponseRecorder.Flush()
	w.flushed <- w.Body.String()
	return nil
}

func TestNativeChatFirstOutputDoesNotWaitForNextUpstreamEvent(t *testing.T) {
	for _, tc := range []struct {
		name, choice, content string
		prelude, thinking     bool
	}{
		{"first text", `{"delta":{"content":"visible"}}`, "visible", false, false},
		{"role then text", `{"delta":{"content":"visible"}}`, "visible", true, false},
		{"thinking to content", `{"delta":{"reasoning_content":"analysis"}}`, "analysis", true, true},
		{"tool", `{"delta":{"tool_calls":[{"id":"call-1","function":{"name":"weather","arguments":"{}"}}]}}`, "weather", false, false},
		{"combined finish", `{"delta":{"content":"final-token"},"finish_reason":"stop"}`, "final-token", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pr, pw := io.Pipe()
			c, _, info, response := responsesStreamFixture(t, pr)
			response.Body = pr
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "public")
			info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
			info.ChannelSetting.ThinkingToContent = tc.thinking
			info.ThinkingContentInfo.IsFirstThinkingContent = true
			w := &chatFlushRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan string, 10)}
			gw, _ := gin.CreateTestContext(w)
			c.Writer = gw.Writer
			done := make(chan struct{})
			var usage *dto.Usage
			var streamErr *types.NewAPIError
			go func() {
				defer close(done)
				usage, streamErr = OaiStreamHandler(c, info, response)
			}()
			defer func() { _ = pw.Close(); _ = pr.Close(); <-done }()
			expectFlushed := func(frame, visible string) {
				t.Helper()
				_, err := io.WriteString(pw, frame)
				require.NoError(t, err)
				select {
				case body := <-w.flushed:
					require.Contains(t, body, visible)
					require.NotContains(t, body, "upstream-model")
				case <-time.After(2 * time.Second):
					t.Fatal("gateway held a nonterminal output frame waiting for another upstream event")
				}
			}
			if tc.prelude {
				expectFlushed(deliveryChatChunk(`{"role":"assistant"}`), `"role":"assistant"`)
			}
			beforeContent := time.Now()
			expectFlushed("data: "+`{"model":"upstream-model","choices":[`+tc.choice+`]}`+"\n\n", tc.content)
			_, err := io.WriteString(pw, "data: "+`{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`+"\n\ndata: [DONE]\n\n")
			require.NoError(t, err)
			<-done
			require.Nil(t, streamErr)
			require.NotNil(t, usage)
			assert.Equal(t, 13, usage.TotalTokens)
			assert.False(t, info.FirstDownstreamContentTime.Before(beforeContent), "role or headers must not set the content timestamp")
			body := w.Body.String()
			assert.Equal(t, 1, strings.Count(body, tc.content), "do not replay the last ordinary frame during finalization")
			assert.Equal(t, 1, strings.Count(body, "[DONE]"))
			assert.NotContains(t, body, `"prompt_tokens":9`, "omit the usage-only trailer when not requested")
		})
	}
}

func TestNativeChatFirstOutputIgnoresControlAndFilteredFields(t *testing.T) {
	for _, tc := range []struct {
		name, delta string
		force, want bool
	}{
		{"role and empty tool list", `{"role":"assistant","content":"","tool_calls":[]}`, false, false},
		{"tool placeholder", `{"tool_calls":[{"index":0}]}`, false, false},
		{"legacy function arguments", `{"function_call":{"arguments":"{}"}}`, false, true},
		{"audio payload", `{"audio":{"data":"YWJj"}}`, false, true},
		{"refusal", `{"refusal":"no"}`, false, true},
		{"typed formatting removed refusal", `{"refusal":"no"}`, true, false},
		{"whitespace", `{"content":" "}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, info, _ := responsesStreamFixture(t, strings.NewReader(""))
			info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
			require.NoError(t, sendStreamData(c, info, `{"choices":[{"delta":`+tc.delta+`}]}`, tc.force, false))
			assert.Equal(t, tc.want, !info.FirstDownstreamContentTime.IsZero())
		})
	}
}

func TestNativeChatMixedFinishAndUsagePreservesBothContracts(t *testing.T) {
	for _, include := range []bool{false, true} {
		body := deliveryChatChunk(`{"content":"hello"}`) + "data: " + `{"model":"upstream-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}` + "\n\ndata: [DONE]\n\n"
		c, w, info, resp := responsesStreamFixture(t, strings.NewReader(body))
		info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
		info.ShouldIncludeUsage = include
		usage, streamErr := OaiStreamHandler(c, info, resp)
		require.Nil(t, streamErr)
		require.NotNil(t, usage)
		assert.Equal(t, 13, usage.TotalTokens)
		wire := w.Body.String()
		assert.Equal(t, 1, strings.Count(wire, `"finish_reason":"stop"`))
		assert.Equal(t, 1, strings.Count(wire, "[DONE]"))
		assert.Equal(t, include, strings.Contains(wire, `"prompt_tokens":9`))
	}
}

func TestNativeChatExtendedOutputAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, delta string
		audio       bool
	}{
		{"refusal", `{"refusal":"cannot complete"}`, false},
		{"legacy function", `{"function_call":{"name":"weather","arguments":"{}"}}`, false},
		{"audio with text", `{"content":"intro","audio":{"data":"YmFzZTY0YWJj","transcript":"spoken"}}`, true},
	} {
		for _, receipt := range []string{"missing", "prompt-only", "measured"} {
			for _, ending := range []string{"done", "disconnect"} {
				t.Run(tc.name+"/"+receipt+"/"+ending, func(t *testing.T) {
					snapshot := ""
					switch receipt {
					case "prompt-only":
						snapshot = `,"usage":{"prompt_tokens":9,"total_tokens":9}`
					case "measured":
						snapshot = `,"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}`
					}
					wire := "data: " + `{"id":"extended","model":"private-model","choices":[{"delta":` + tc.delta + `}]` + snapshot + "}\n\ndata: [DONE]\n\n"
					c, recorder, info, resp := responsesStreamFixture(t, strings.NewReader(wire))
					common.SetContextKey(c, constant.ContextKeyOriginalModel, "public-model")
					info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
					info.SetEstimatePromptTokens(11)
					var broken *streamDeliveryFailureWriter
					if ending == "disconnect" {
						broken = attachStreamDeliveryFailure(c, `"id":"extended"`, "flush")
						recorder = broken.ResponseRecorder
					}
					usage, streamErr := OaiStreamHandler(c, info, resp)
					unknown := receipt == "prompt-only" || (receipt == "missing" && tc.audio)
					assert.Equal(t, unknown, common.GetContextKeyBool(c, constant.ContextKeyUpstreamResponseUsageIncomplete))
					switch {
					case broken != nil:
						assertStreamDeliveryFailure(t, streamErr, broken)
					case unknown:
						require.NotNil(t, streamErr)
					default:
						require.Nil(t, streamErr)
					}
					assert.Equal(t, !unknown && broken == nil, strings.Contains(recorder.Body.String(), "[DONE]"))
					assert.NotContains(t, recorder.Body.String(), "private-model")
					assert.NotContains(t, recorder.Body.String(), "prompt_tokens", "opted-out counters never leak, even when combined with raw output")
					assert.Contains(t, recorder.Body.String(), "public-model")
					switch receipt {
					case "measured":
						require.NotNil(t, usage)
						assert.Equal(t, 13, usage.TotalTokens)
						assert.Equal(t, 4, usage.CompletionTokens)
					case "prompt-only":
						require.NotNil(t, usage)
						assert.Equal(t, 9, usage.PromptTokens)
						assert.Zero(t, usage.CompletionTokens, "keep the native fact and preserve the reservation for review")
					case "missing":
						if tc.audio {
							assert.Nil(t, usage, "neither base64 nor a transcript measures audio usage")
						} else {
							require.NotNil(t, usage)
							assert.Equal(t, 11, usage.PromptTokens)
							assert.Positive(t, usage.CompletionTokens)
						}
					}
				})
			}
		}
	}
}
