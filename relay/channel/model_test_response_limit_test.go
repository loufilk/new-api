package channel

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelTestUpstreamResponseBoundBeforeDecode(t *testing.T) {
	for _, size := range []int{32, 512 << 10, (512 << 10) + 1} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, true)
		response := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat("x", size)))}
		err := limitModelTestResponse(c, response)
		if size > 512<<10 {
			require.Error(t, err)
			require.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamResponseDecodeFailed))
		} else {
			require.NoError(t, err)
			body, readErr := io.ReadAll(response.Body)
			require.NoError(t, readErr)
			require.Len(t, body, size)
		}
	}
}
