package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gemini :countTokens is not implemented. It must be rejected like any
// unknown route before authentication, channel selection, or any upstream
// request, so the response is identical with and without credentials.
func TestGeminiCountTokensIsRejectedAsUnknownRoute(t *testing.T) {
	setupRelayRouterTestDB(t)

	user := model.User{
		Username: "count-tokens-user",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100,
	}
	require.NoError(t, model.DB.Create(&user).Error)
	require.NoError(t, model.DB.Create(&model.Token{
		UserId:         user.Id,
		Key:            "counttokenskey",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	engine := gin.New()
	SetRelayRouter(engine)

	const path = "/v1beta/models/gemini-2.5-flash:countTokens"

	tests := []struct {
		name    string
		prepare func(request *http.Request)
	}{
		{
			name:    "without credentials",
			prepare: func(*http.Request) {},
		},
		{
			name: "with a valid Gemini API key",
			prepare: func(request *http.Request) {
				request.Header.Set("x-goog-api-key", "counttokenskey")
			},
		},
		{
			name: "with a valid bearer token",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer counttokenskey")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				path,
				strings.NewReader(`{"contents":[]}`),
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
