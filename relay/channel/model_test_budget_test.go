package channel_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelTestFinalConvertedThinkingBudgetIsEnforcedBeforeHTTP(t *testing.T) {
	setupModelTestSwitchPostgres(t)
	service.InitHttpClient()
	for _, selected := range []uint{1024, 2048, 4096} {
		t.Run(fmt.Sprint(selected), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var payload map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &payload))
				assert.Equal(t, float64(selected), payload["max_tokens"])
				w.WriteHeader(200)
			}))
			defer upstream.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/test", bytes.NewReader(nil))
			common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, true)
			common.SetContextKey(c, constant.ContextKeyModelTestOutputLimit, int(selected))
			original := &dto.GeneralOpenAIRequest{Model: "claude-3-7-sonnet@thinking:8192", MaxTokens: &selected, Messages: []dto.Message{{Role: "user", Content: "Draw a pelican"}}}
			info := &relaycommon.RelayInfo{OriginModelName: original.Model, Request: original, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: original.Model}}
			outbound, err := common.DeepCopy(original)
			require.NoError(t, err)
			require.NoError(t, helper.ModelMappedHelper(c, info, outbound))
			require.NoError(t, helper.ApplyReasoningModelSuffix(c, info, outbound))
			converted, err := (&claude.Adaptor{}).ConvertOpenAIRequest(c, info, outbound)
			require.NoError(t, err)
			body, err := common.Marshal(converted)
			require.NoError(t, err)
			req, err := http.NewRequest("POST", upstream.URL, bytes.NewReader(body))
			require.NoError(t, err)
			response, err := channel.DoRequest(c, req, info)
			if selected == 1024 {
				require.Error(t, err)
				assert.Zero(t, calls.Load())
				assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
			} else {
				require.NoError(t, err)
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				assert.EqualValues(t, 1, calls.Load())
			}
		})
	}
}

func TestModelTestFinalOutputGuardRejectsMissingConflictingAndRaisedLimits(t *testing.T) {
	service.InitHttpClient()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	for _, body := range []string{`{}`, `{"max_tokens":8193}`, `{"max_tokens":2048,"max_completion_tokens":4096}`, `{"max_tokens":0}`, `{"max_tokens":"2048"}`, `{"generationConfig":{"maxOutputTokens":4096}}`, `{"generationConfig":{"maxOutputTokens":0}}`, `{"max_tokens":2048,"generationConfig":{"maxOutputTokens":4096}}`, `{"generationConfig":{"maxOutputTokens":"2048"}}`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/test", nil)
		common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, true)
		common.SetContextKey(c, constant.ContextKeyModelTestOutputLimit, 2048)
		req, err := http.NewRequest("POST", upstream.URL, bytes.NewBufferString(body))
		require.NoError(t, err)
		_, err = channel.DoRequest(c, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
		assert.Error(t, err)
	}
	assert.Zero(t, calls.Load())
}
