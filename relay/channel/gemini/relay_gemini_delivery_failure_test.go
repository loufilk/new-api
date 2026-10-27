package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type geminiDeliveryFailureWriter struct {
	*httptest.ResponseRecorder
	match              string
	mode               string
	failed             bool
	failOnFlush        bool
	writesAfterFailure int
}

func (w *geminiDeliveryFailureWriter) Write(p []byte) (int, error) {
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

func (w *geminiDeliveryFailureWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *geminiDeliveryFailureWriter) FlushError() error {
	if w.failOnFlush {
		w.failed = true
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}

func TestGeminiStreamDeliveryFailurePreservesNativeUsageAndStops(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	cases := []struct {
		name, match string
		format      types.RelayFormat
		final       bool
	}{
		{"native data", "generated output", types.RelayFormatGemini, false},
		{"Chat data", "generated output", types.RelayFormatOpenAI, false},
		{"Claude data", "generated output", types.RelayFormatClaude, false},
		{"Chat prelude", `"role":"assistant"`, types.RelayFormatOpenAI, false},
		{"Claude prelude", "event: message_start", types.RelayFormatClaude, false},
		{"Chat final", "[DONE]", types.RelayFormatOpenAI, true},
		{"Claude final", "event: message_delta", types.RelayFormatClaude, true},
	}
	for _, tc := range cases {
		for _, mode := range []string{"write", "short_write", "flush"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				w := &geminiDeliveryFailureWriter{ResponseRecorder: httptest.NewRecorder(), match: tc.match, mode: mode}
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil)
				info := &relaycommon.RelayInfo{
					IsStream: true, DisablePing: true, StartTime: time.Now(),
					RelayFormat: tc.format, OriginModelName: "public-model", ShouldIncludeUsage: true,
					ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash"},
				}
				info.SetEstimatePromptTokens(8)
				body := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"generated output"}]}}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15,"cachedContentTokenCount":3}}` + "\n\n"
				if !tc.final {
					body += `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"unobserved"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":900,"candidatesTokenCount":900,"totalTokenCount":1800}}` + "\n\n"
				}
				body += "data: [DONE]\n\n"
				resp := &http.Response{Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
				var usage *dto.Usage
				var apiErr *types.NewAPIError
				if tc.format == types.RelayFormatGemini {
					usage, apiErr = GeminiTextGenerationStreamHandler(c, info, resp)
				} else {
					usage, apiErr = GeminiChatStreamHandler(c, info, resp)
				}

				require.NotNil(t, apiErr)
				assert.Equal(t, service.ClientGoneErrorCode, apiErr.GetErrorCode())
				assert.Equal(t, types.ErrorTypeNewAPIError, apiErr.GetErrorType())
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.False(t, types.IsRecordErrorLog(apiErr))
				assert.True(t, w.failed, "the selected frame must exercise a real delivery failure")
				assert.Zero(t, w.writesAfterFailure)
				require.NotNil(t, usage)
				assert.Equal(t, 11, usage.PromptTokens)
				assert.Equal(t, 4, usage.CompletionTokens)
				assert.Equal(t, 15, usage.TotalTokens)
				assert.Equal(t, 3, usage.PromptTokensDetails.CachedTokens)
				require.NotNil(t, usage.BillingUsage)
				assert.Equal(t, dto.BillingUsageSourceGeminiChat, usage.BillingUsage.Source)
				require.NotNil(t, usage.BillingUsage.GeminiUsageMetadata)
				assert.Equal(t, 11, usage.BillingUsage.GeminiUsageMetadata.PromptTokenCount)
				assert.Equal(t, 4, usage.BillingUsage.GeminiUsageMetadata.CandidatesTokenCount)
				assert.Equal(t, 3, usage.BillingUsage.GeminiUsageMetadata.CachedContentTokenCount)
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled))
				assert.NotContains(t, w.Body.String(), "unobserved")
				assert.NotContains(t, w.Body.String(), `"error":`)
				if !tc.final {
					assert.NotContains(t, w.Body.String(), "[DONE]")
					assert.NotContains(t, w.Body.String(), "message_stop")
				}
			})
		}
	}
}
