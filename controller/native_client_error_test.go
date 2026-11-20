package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeGatewayErrorOverHTTP(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format types.RelayFormat
	}{
		{"openai", types.RelayFormatOpenAI},
		{"claude", types.RelayFormatClaude},
		{"gemini", types.RelayFormatGemini},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Set(common.RequestIdKey, "native-http-1")
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{"))
			c.Request.Header.Set("Content-Type", "application/json")
			Relay(c, tc.format)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			var got map[string]any
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &got))
			err, ok := got["error"].(map[string]any)
			require.True(t, ok)
			require.NotEqual(t, common.MessageWithRequestId("invalid request", "native-http-1"), err["message"])
			require.Contains(t, err["message"], "(request id: native-http-1)")
			switch tc.format {
			case types.RelayFormatClaude:
				require.Equal(t, "new_api_error", err["type"])
				require.Equal(t, "error", got["type"])
			case types.RelayFormatGemini:
				require.EqualValues(t, http.StatusBadRequest, err["code"])
				require.Equal(t, "INVALID_ARGUMENT", err["status"])
			default:
				require.Equal(t, "new_api_error", err["type"])
				require.Equal(t, "invalid_request", err["code"])
			}
		})
	}
}

func TestNativeGatewayStreamFailuresAfterKeepalive(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format types.RelayFormat
		want   string
	}{
		{"gemini", types.RelayFormatGemini, `"status":"RESOURCE_EXHAUSTED"`},
		{"responses", types.RelayFormatOpenAIResponses, `"type":"error"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil)
			c.Set(common.RequestIdKey, "native-sse-1")
			helper.SetEventStreamHeaders(c)
			require.NoError(t, helper.PingData(c))
			writeRelayStreamError(c, tc.format, types.NewErrorWithStatusCode(errors.New("local queue is full"), types.ErrorCodeInvalidRequest, http.StatusTooManyRequests))
			require.Equal(t, http.StatusOK, rec.Code, "the keepalive already committed the HTTP status")
			require.Contains(t, rec.Body.String(), tc.want)
			require.Contains(t, rec.Body.String(), "native-sse-1")
			require.NotContains(t, rec.Body.String(), "[DONE]")
		})
	}
}
