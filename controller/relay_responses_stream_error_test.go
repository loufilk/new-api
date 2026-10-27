package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayPreservesHandledResponsesFailureWithoutJSONSuffix(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{"))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyResponsesStreamErrorHandled, true)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	const failure = "event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n"
	_, err := c.Writer.WriteString(failure)
	require.NoError(t, err)
	// Exercise Relay's outer error writer without depending on channel/database
	// setup. A validation failure reaches the same deferred writer as relay errors.
	Relay(c, types.RelayFormatOpenAIResponses)
	assert.Equal(t, failure, w.Body.String())
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
}

func TestShouldRetryNeverReplaysHandledResponsesStream(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyResponsesStreamErrorHandled, true)
	for _, code := range []types.ErrorCode{types.ErrorCodeBadResponse, types.ErrorCodeChannelNoAvailableKey} {
		apiErr := types.NewOpenAIError(errors.New("stream already failed"), code, http.StatusBadGateway)
		assert.Equal(t, "stop", service.DecideRelayRetry(c, apiErr, 3).Action)
		assert.False(t, service.ShouldRetryRelayError(c, apiErr, 3), "even a channel-classified error must not splice a new response into an existing SSE stream")
	}
}

func TestRelayPreservesHandledChatStreamFailureAndRecordsPolicyStop(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{"))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyStreamErrorHandled, true)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	const failure = "data: {\"error\":{\"type\":\"server_error\",\"message\":\"upstream request failed\"}}\n\n"
	_, err := c.Writer.WriteString(failure)
	require.NoError(t, err)

	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, failure, w.Body.String(), "handled stream failure must not gain a JSON suffix")
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	policy := service.RequestPolicy(c)
	assert.False(t, policy.Successful)
	events := policy.Events()
	require.NotEmpty(t, events)
	assert.Equal(t, "stop", events[len(events)-1].Decision.Action)
	assert.Equal(t, "request_failed", events[len(events)-1].Decision.Reason)
}

func TestShouldRetryNeverReplaysHandledChatStream(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyStreamErrorHandled, true)
	for _, code := range []types.ErrorCode{types.ErrorCodeBadResponse, types.ErrorCodeChannelNoAvailableKey} {
		apiErr := types.NewOpenAIError(errors.New("partial stream failed"), code, http.StatusBadGateway)
		assert.Equal(t, "stop", service.DecideRelayRetry(c, apiErr, 3).Action)
		assert.False(t, service.ShouldRetryRelayError(c, apiErr, 3), "partial output must not be spliced with a retry")
	}
}
