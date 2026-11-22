package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseCopyHidesProviderHeadersAndRetainsLocalCorrelation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header(common.RequestIdKey, "local-request")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "public-model")
	upstream := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"Content-Type": {"application/json"}, "Retry-After": {"2"},
		"X-Request-Id": {"private-request"}, "Openai-Organization": {"private-org"},
		"X-Upstream-Model": {"private-model"}, "X-Provider-Name": {"private-provider"},
		"Server": {"private-server"}, "Via": {"private-route"},
	}}
	IOCopyBytesGracefully(c, upstream, []byte(`{"model":"private-model","choices":[],"usage":{"total_tokens":8}}`))
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "local-request", recorder.Header().Get(common.RequestIdKey))
	assert.Equal(t, "private-request", c.GetString(common.UpstreamRequestIdKey))
	assert.Equal(t, "2", recorder.Header().Get("Retry-After"))
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	for _, key := range []string{"X-Request-Id", "Openai-Organization", "X-Upstream-Model", "X-Provider-Name", "Server", "Via"} {
		assert.Empty(t, recorder.Header().Get(key), key)
	}
	assert.JSONEq(t, `{"model":"public-model","choices":[],"usage":{"total_tokens":8}}`, recorder.Body.String())
}
