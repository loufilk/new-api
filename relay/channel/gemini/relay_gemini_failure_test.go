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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiNativeRejectsInvalidSuccessfulHTTPEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		embedding  bool
		status     int
	}{
		{"provider rejection", `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"fixture rejection"}}`, false, http.StatusTooManyRequests},
		{"missing candidates", `{"usageMetadata":{"promptTokenCount":31}}`, false, http.StatusBadGateway},
		{"missing embedding", `{"usageMetadata":{"promptTokenCount":31}}`, true, http.StatusBadGateway},
		{"malformed body", `{"candidates":`, false, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
			info := &relaycommon.RelayInfo{}
			info.SetEstimatePromptTokens(30)
			resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body))}
			var usage *dto.Usage
			var err *types.NewAPIError
			if tc.embedding {
				usage, err = NativeGeminiEmbeddingHandler(c, resp, info)
			} else {
				usage, err = GeminiTextGenerationHandler(c, info, resp)
			}
			require.NotNil(t, err)
			assert.Equal(t, tc.status, err.StatusCode)
			assert.Nil(t, usage)
			assert.False(t, c.Writer.Written())
		})
	}
}

func TestGeminiStreamFailurePreservesOnlyObservedProviderUsage(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previous })

	const chunk = `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial output"}]}}]}`
	const measured = `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial output"}]}}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}}`
	const providerError = `{"error":{"code":503,"status":"UNAVAILABLE","message":"fixture rejection"}}`
	for _, tc := range []struct {
		name, body        string
		status, wantUsage int
	}{
		{"provider rejected before output", "data: " + providerError + "\n\n", http.StatusServiceUnavailable, 0},
		{"provider failed after output", "data: " + measured + "\n\ndata: " + providerError + "\n\n", http.StatusServiceUnavailable, 15},
		{"unmeasured output and unexpected EOF", "data: " + chunk + "\n\n", http.StatusBadGateway, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, StartTime: time.Now(),
				RelayFormat: types.RelayFormatOpenAI, OriginModelName: "fixture-model", ChannelMeta: &relaycommon.ChannelMeta{}}
			info.SetEstimatePromptTokens(30)
			resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body))}
			usage, err := GeminiChatStreamHandler(c, info, resp)
			require.NotNil(t, err)
			require.NotNil(t, usage)
			assert.Equal(t, tc.status, err.StatusCode)
			assert.Equal(t, tc.wantUsage, usage.TotalTokens)
			assert.NotContains(t, recorder.Body.String(), "[DONE]")
			assert.True(t, info.StreamStatus.ResponseFailed())
			if strings.Contains(tc.body, "partial output") {
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled))
				assert.Contains(t, recorder.Body.String(), `"error":`)
			} else {
				assert.False(t, c.Writer.Written())
			}
		})
	}
}
