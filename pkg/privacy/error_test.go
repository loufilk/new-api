package privacy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newPrivacyTestContext(role int, requestedModel string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("role", role)
	if requestedModel != "" {
		common.SetContextKey(c, constant.ContextKeyOriginalModel, requestedModel)
	}
	return c
}

func newPrivacyContextForPayload(requestID string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "public-model")
	c.Set(common.RequestIdKey, requestID)
	return c
}

func stringValue(t *testing.T, payload []byte, path string) string {
	t.Helper()
	return gjson.GetBytes(payload, path).Str
}

func numberValue(t *testing.T, payload []byte, path string) float64 {
	t.Helper()
	return gjson.GetBytes(payload, path).Float()
}

func exists(t *testing.T, payload []byte, path string) bool {
	t.Helper()
	return gjson.GetBytes(payload, path).Exists()
}

func TestSanitizeClientOpenAIError(t *testing.T) {
	c := newPrivacyTestContext(common.RoleCommonUser, "")
	c.Set(common.RequestIdKey, "req-local-1")

	source := types.OpenAIError{
		Message:  "upstream private-provider failed",
		Type:     "invalid_request_error",
		Param:    "api_key",
		Code:     "context_length_exceeded",
		Metadata: common.RawMessage(`{"provider":"private"}`),
	}
	safe := SanitizeClientOpenAIError(c, http.StatusBadRequest, source)
	assert.Equal(t, "invalid request (request id: req-local-1)", safe.Message)
	assert.Equal(t, "invalid_request_error", safe.Type)
	assert.Equal(t, "context_length_exceeded", safe.Code)
	assert.Empty(t, safe.Param)
	assert.Empty(t, safe.Metadata)

	adversarial := types.OpenAIError{Message: "secret", Type: "private_type", Code: "org-secret"}
	safe = SanitizeClientOpenAIError(c, http.StatusBadGateway, adversarial)
	assert.Equal(t, "upstream request failed (request id: req-local-1)", safe.Message)
	assert.Equal(t, "server_error", safe.Type)
	assert.Nil(t, safe.Code)

	rateLimited := SanitizeClientOpenAIError(c, http.StatusTooManyRequests, types.OpenAIError{Message: "raw", Type: "rate_limit_error", Code: "rate_limit_exceeded"})
	assert.Equal(t, "too many requests (request id: req-local-1)", rateLimited.Message)
	assert.Equal(t, "rate_limit_error", rateLimited.Type)
	assert.Equal(t, "rate_limit_exceeded", rateLimited.Code)
}

func TestSanitizeClientOpenAIErrorKeepsChannelTestDiagnostics(t *testing.T) {
	c := newPrivacyTestContext(common.RoleRootUser, "")
	common.SetContextKey(c, constant.ContextKeyChannelTest, true)
	source := types.OpenAIError{Message: "raw upstream detail", Type: "server_error", Code: "org-secret"}
	assert.Equal(t, source, SanitizeClientOpenAIError(c, http.StatusBadGateway, source))
}

func TestSanitizeClientClaudeError(t *testing.T) {
	c := newPrivacyTestContext(common.RoleCommonUser, "")
	c.Set(common.RequestIdKey, "req-local-2")
	safe := SanitizeClientClaudeError(c, http.StatusUnauthorized, types.ClaudeError{Type: "authentication_error", Message: "invalid key sk-private"})
	assert.Equal(t, "authentication_error", safe.Type)
	assert.Equal(t, "authentication failed (request id: req-local-2)", safe.Message)

	unknown := SanitizeClientClaudeError(c, http.StatusBadGateway, types.ClaudeError{Type: "provider_private", Message: "raw"})
	assert.Equal(t, "server_error", unknown.Type)
	assert.Equal(t, "upstream request failed (request id: req-local-2)", unknown.Message)
}

func TestSanitizeClientOpenAIErrorNilContext(t *testing.T) {
	source := types.OpenAIError{Message: "raw upstream detail", Type: "server_error", Code: "context_length_exceeded"}
	safe := SanitizeClientOpenAIError(nil, http.StatusBadRequest, source)
	assert.Equal(t, "invalid request", safe.Message)
	assert.Equal(t, "server_error", safe.Type)
	assert.Equal(t, "context_length_exceeded", safe.Code)

	claude := SanitizeClientClaudeError(nil, http.StatusBadGateway, types.ClaudeError{Type: "server_error", Message: "raw"})
	assert.Equal(t, "upstream request failed", claude.Message)
	assert.Equal(t, "server_error", claude.Type)
}

func TestSafeErrorCodeRestrictsNumericCodesToStatusRange(t *testing.T) {
	c := newPrivacyTestContext(common.RoleCommonUser, "")
	statusCode := SanitizeClientOpenAIError(c, http.StatusBadGateway, types.OpenAIError{Message: "raw", Type: "server_error", Code: 503})
	assert.Equal(t, 503, statusCode.Code)

	for _, code := range []any{70000, float64(70000), 0, -1, 99, 600} {
		safe := SanitizeClientOpenAIError(c, http.StatusBadGateway, types.OpenAIError{Message: "raw", Type: "server_error", Code: code})
		assert.Nil(t, safe.Code, "numeric code %v must not be exposed", code)
	}
}

func TestRestrictedCoversEveryPublicRelayCaller(t *testing.T) {
	assert.True(t, Restricted(nil))
	assert.True(t, Restricted(newPrivacyTestContext(0, "")))
	assert.True(t, Restricted(newPrivacyTestContext(common.RoleCommonUser, "")))
	assert.True(t, Restricted(newPrivacyTestContext(common.RoleAdminUser, "")))
	assert.True(t, Restricted(newPrivacyTestContext(common.RoleRootUser, "")),
		"administrator diagnostics live in admin_info logs, not in the relay response")

	channelTest := newPrivacyTestContext(common.RoleAdminUser, "")
	common.SetContextKey(channelTest, constant.ContextKeyChannelTest, true)
	assert.False(t, Restricted(channelTest), "channel tests are synthetic upstream probes")
}

func TestRequestedModelUsesOriginalContextKey(t *testing.T) {
	assert.Empty(t, RequestedModel(nil))
	assert.Equal(t, "requested", RequestedModel(newPrivacyTestContext(common.RoleCommonUser, "requested")))
}

func TestSafeErrorTextCategories(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		category string
		want     string
	}{
		{name: "bad request", status: http.StatusBadRequest, want: "invalid request"},
		{name: "unauthorized", status: http.StatusUnauthorized, want: "authentication failed"},
		{name: "forbidden", status: http.StatusForbidden, want: "access denied"},
		{name: "not found", status: http.StatusNotFound, want: "not found"},
		{name: "conflict", status: http.StatusConflict, want: "request conflict"},
		{name: "too many requests", status: http.StatusTooManyRequests, want: "too many requests"},
		{name: "server error", status: http.StatusBadGateway, want: "upstream request failed"},
		{name: "category only", category: "rate_limit_exceeded", want: "too many requests"},
		{name: "unknown category", category: "private-category", want: "request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SafeErrorText(tc.status, tc.category))
		})
	}
}

func TestIsSafeErrorCode(t *testing.T) {
	require.True(t, IsSafeErrorCode("invalid_request"))
	require.True(t, IsSafeErrorCode("context_length_exceeded"))
	require.False(t, IsSafeErrorCode("org-secret-42"))
	require.False(t, IsSafeErrorCode(""))
}
