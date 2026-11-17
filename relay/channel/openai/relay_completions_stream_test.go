package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyCompletionStreamPreservesTextAndPrivacy(t *testing.T) {
	for _, force := range []bool{false, true} {
		for _, thinking := range []bool{false, true} {
			t.Run(fmt.Sprintf("force=%v/thinking=%v", force, thinking), func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/completions", nil)
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "requested")
				info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeCompletions, OriginModelName: "requested"}
				data := `{"id":"legacy","model":"private-upstream","choices":[{"index":0,"text":"hello","logprobs":{"tokens":["hello"],"token_logprobs":[-0.25]},"finish_reason":null}]}`
				require.NoError(t, sendStreamData(c, info, data, force, thinking))
				assert.Contains(t, w.Body.String(), `"text":"hello"`)
				assert.Contains(t, w.Body.String(), `"token_logprobs":[-0.25]`)
				assert.Contains(t, w.Body.String(), `"model":"requested"`)
				assert.NotContains(t, w.Body.String(), "private-upstream")
				assert.NotContains(t, w.Body.String(), `"delta"`)
				w.Body.Reset()
				require.NoError(t, sendStreamData(c, info, `{"error":{"type":"private-type","code":"private-code","message":"private-diagnostic"}}`, force, thinking))
				assert.NotContains(t, w.Body.String(), "private-")
			})
		}
	}
}

func TestLegacyCompletionStreamKeepsTerminalTextWithUsage(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 10
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/completions", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "requested")
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeCompletions, RelayFormat: types.RelayFormatOpenAI, OriginModelName: "requested", DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "upstream"}}
	wire := "data: " + `{"id":"legacy","model":"upstream","choices":[{"index":0,"text":"first","finish_reason":null}]}` + "\n\ndata: " + `{"id":"legacy","model":"upstream","choices":[{"index":0,"text":"last","finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}` + "\n\ndata: [DONE]\n\n"
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
	usage, apiErr := OaiStreamHandler(c, info, response)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 3, usage.PromptTokens)
	assert.Equal(t, 2, usage.CompletionTokens)
	assert.Equal(t, 5, usage.TotalTokens)
	body := w.Body.String()
	assert.Contains(t, body, `"text":"first"`)
	assert.Contains(t, body, `"text":"last"`)
	assert.Less(t, strings.Index(body, `"text":"first"`), strings.Index(body, `"text":"last"`))
	assert.Equal(t, 1, strings.Count(body, "[DONE]"))
	assert.NotContains(t, body, `"delta"`)
	assert.NotContains(t, body, `"model":"upstream"`)
}
