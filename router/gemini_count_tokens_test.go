package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gemini :countTokens is not implemented. It must be rejected like any
// unknown route before authentication, channel selection, or any upstream
// request, so the response is identical with and without credentials.
func TestGeminiCountTokensIsRejectedAsUnknownRoute(t *testing.T) {
	engine := gin.New()
	SetRelayRouter(engine)

	tests := []struct {
		name    string
		prepare func(request *http.Request)
	}{
		{
			name:    "without credentials",
			prepare: func(*http.Request) {},
		},
		{
			name: "with a Gemini API key header",
			prepare: func(request *http.Request) {
				request.Header.Set("x-goog-api-key", "counttokenskey")
			},
		},
		{
			name: "with a bearer token header",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer counttokenskey")
			},
		},
	}

	for _, prefix := range []string{"/v1", "/v1beta"} {
		for _, action := range []string{"countTokens", "unsupportedMethod"} {
			for _, test := range tests {
				path := prefix + "/models/gemini-2.5-flash:" + action
				t.Run(prefix+"/"+action+"/"+test.name, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					request := httptest.NewRequest(
						http.MethodPost,
						path,
						strings.NewReader(`{"contents":[{"parts":[{"text":"size this prompt only"}]}]}`),
					)
					request.Header.Set("Content-Type", "application/json")
					test.prepare(request)

					engine.ServeHTTP(recorder, request)

					require.Equal(t, http.StatusNotFound, recorder.Code)
					assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")

					var payload struct {
						Error types.OpenAIError `json:"error"`
					}
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
					assert.Equal(t, "invalid_request_error", payload.Error.Type)
					assert.Contains(t, payload.Error.Message, "Invalid URL (POST "+path+")")
				})
			}
		}
	}
}
