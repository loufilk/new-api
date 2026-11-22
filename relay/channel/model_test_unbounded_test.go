package channel

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelTestUnboundedOutputPreservesProviderBody(t *testing.T) {
	for _, body := range []string{`{"model":"synthetic","messages":[]}`, `{"max_tokens":8192}`, `{"generationConfig":{"maxOutputTokens":8192}}`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, true)
		common.SetContextKey(c, constant.ContextKeyModelTestOutputLimit, 0)
		req, err := http.NewRequest(http.MethodPost, "http://unused.invalid", bytes.NewBufferString(body))
		require.NoError(t, err)
		require.NoError(t, validateModelTestOutputBudget(c, req))
		preserved, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		assert.Equal(t, body, string(preserved), "provider-required defaults are not replaced by a fingerprint-specific cap")
	}
}
