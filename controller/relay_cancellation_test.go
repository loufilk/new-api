package controller

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRelayCancelledBeforeDispatchDoesNotSelectOrWrite(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		// Even a malformed body must not cause a JSON suffix to be sent to a dead
		// client, and no price lookup, quota reservation or channel selection runs.
		c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader("{")).WithContext(ctx)
		Relay(c, format)
		assert.Empty(t, w.Body.String())
		assert.False(t, c.Writer.Written())
		assert.Zero(t, service.RequestPolicy(c).Attempts)
	}
}
