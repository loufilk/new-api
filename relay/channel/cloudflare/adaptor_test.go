package cloudflare

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingResponsePreservesDataAndUsage(t *testing.T) {
	for _, embedding := range []string{`[0.125,-0.25,0.5]`, `"AACAPwAAAEA="`} {
		t.Run(embedding, func(t *testing.T) {
			body := `{"object":"list","model":"@cf/baai/bge-base-en-v1.5","data":[{"object":"embedding","index":0,"embedding":` + embedding + `}],"usage":{"prompt_tokens":7,"total_tokens":7}}`
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
			info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, RelayFormat: types.RelayFormatOpenAI}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}

			usage, apiErr := (&Adaptor{}).DoResponse(c, response, info)

			require.Nil(t, apiErr)
			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.JSONEq(t, body, recorder.Body.String())
			parsed, ok := usage.(*dto.Usage)
			require.True(t, ok)
			assert.Equal(t, 7, parsed.PromptTokens)
			assert.Equal(t, 7, parsed.TotalTokens)
		})
	}
}
