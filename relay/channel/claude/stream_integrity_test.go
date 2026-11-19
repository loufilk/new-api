package claude

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeStreamRequiresMessageStop(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 10
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, types.RelayFormatClaude} {
		t.Run(string(format), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			info := &relaycommon.RelayInfo{OriginModelName: "alias", RelayFormat: format, IsStream: true, DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "provider"}}
			wire := "data: " + `{"type":"message_start","message":{"id":"msg1","model":"provider","role":"assistant","content":[],"usage":{"input_tokens":2}}}` + "\n\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}` + "\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(wire))}
			handler := ClaudeResponsesStreamHandler
			if format != types.RelayFormatOpenAIResponses {
				handler = ClaudeStreamHandler
			}
			usage, apiErr := handler(c, resp, info)
			require.NotNil(t, apiErr)
			assert.True(t, types.IsSkipRetryError(apiErr))
			require.NotNil(t, usage)
			assert.Equal(t, 5, usage.TotalTokens)
			assert.NotContains(t, rec.Body.String(), "response.completed")
			assert.NotContains(t, rec.Body.String(), "[DONE]")
			if format == types.RelayFormatOpenAIResponses {
				assert.Contains(t, rec.Body.String(), "event: response.failed")
			} else {
				assert.Contains(t, rec.Body.String(), `"error"`)
			}
		})
	}
}

func TestClaudeBufferFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		body                   io.Reader
		read, decode, rejected bool
	}{
		{"failed_read", iotest.ErrReader(io.ErrUnexpectedEOF), true, false, false},
		{"bad_json", strings.NewReader("{"), false, true, false},
		{"explicit_error", strings.NewReader(`{"type":"error","error":{"type":"overloaded_error","message":"temporary"}}`), false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "provider"}}
			_, apiErr := ClaudeHandler(c, &http.Response{StatusCode: 200, Body: io.NopCloser(tc.body)}, info)
			require.NotNil(t, apiErr)
			assert.Equal(t, tc.read, apiErr.GetErrorCode() == types.ErrorCodeReadResponseBodyFailed)
			assert.Equal(t, tc.decode, common.GetContextKeyBool(c, constant.ContextKeyUpstreamResponseDecodeFailed))
			assert.Equal(t, tc.rejected, common.GetContextKeyBool(c, constant.ContextKeyUpstreamExplicitRejection))
		})
	}
}

func TestClaudeSDKSourceErrorAndDisconnectBeatCompletion(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	info.StreamStatus.MarkCompleted()
	require.Nil(t, StreamTerminalError(c, info, nil))
	require.NotNil(t, StreamTerminalError(c, info, io.ErrUnexpectedEOF))
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, errors.New("downstream closed"))
	require.Equal(t, service.ClientGoneErrorCode, StreamTerminalError(c, info, nil).GetErrorCode())
}
