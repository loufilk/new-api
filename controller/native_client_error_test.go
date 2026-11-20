package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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
			require.Equal(t, "new_api_error", err["type"])
			require.NotEqual(t, common.MessageWithRequestId("invalid request", "native-http-1"), err["message"])
			require.Contains(t, err["message"], "(request id: native-http-1)")
			if tc.format == types.RelayFormatClaude {
				require.Equal(t, "error", got["type"])
			} else {
				require.Equal(t, "invalid_request", err["code"])
			}
		})
	}
}
