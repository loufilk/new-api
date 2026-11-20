package helper

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeGatewayErrorSSEKeepsDetails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format types.RelayFormat
		want   string
	}{
		{"openai", types.RelayFormatOpenAI, `"code":"insufficient_user_quota"`},
		{"claude", types.RelayFormatClaude, "event: error\n"},
		{"gemini", types.RelayFormatGemini, `"code":403`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			c.Set(common.RequestIdKey, "native-sse-1")
			err := types.NewErrorWithStatusCode(errors.New("wallet has insufficient quota"), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden)
			require.NoError(t, RelayErrorData(c, tc.format, err))
			body := rec.Body.String()
			require.Contains(t, body, tc.want)
			require.Contains(t, body, "wallet has insufficient quota (request id: native-sse-1)")
			require.NotContains(t, body, "[DONE]")
		})
	}
}
