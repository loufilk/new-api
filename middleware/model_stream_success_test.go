package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestModelSuccessSlotsRequireStreamSuccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*relaycommon.StreamStatus)
		want      bool
	}{
		{"complete", func(s *relaycommon.StreamStatus) {
			s.RequireTerminal()
			s.MarkCompleted()
			s.SetEndReason(relaycommon.StreamEndReasonDone, nil)
		}, true},
		{"cancelled after 200", func(s *relaycommon.StreamStatus) {
			s.MarkCancelled()
			s.SetEndReason(relaycommon.StreamEndReasonClientGone, nil)
		}, false},
		{"provider failed after 200", func(s *relaycommon.StreamStatus) {
			s.MarkFailed("upstream_error", "server_error", http.StatusServiceUnavailable)
		}, false},
		{"truncated protocol", func(s *relaycommon.StreamStatus) {
			s.RequireTerminal()
			s.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Writer.WriteHeaderNow()
			status := relaycommon.NewStreamStatus()
			tc.configure(status)
			common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
			assert.Equal(t, tc.want, modelRequestSucceeded(c))
		})
	}
}
