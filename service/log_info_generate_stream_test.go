package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendRelayLogAdminInfoIncludesStreamDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.UpstreamRequestIdKey, "req_upstream_123")

	start := time.Unix(1_700_000_000, 0)
	status := relaycommon.NewStreamStatus()
	status.RecordError("reasoning replay recovery failed")
	status.SetEndReason(relaycommon.StreamEndReasonTimeout, errors.New("output-loop detected"))
	info := &relaycommon.RelayInfo{
		StartTime:             start,
		FirstResponseTime:     start.Add(275 * time.Millisecond),
		IsStream:              true,
		ReceivedResponseCount: 17,
		StreamStatus:          status,
		ChannelMeta:           &relaycommon.ChannelMeta{},
	}
	other := model.NewLogOther()

	AppendRelayLogAdminInfo(c, info, other)

	snapshot := other.Snapshot()
	adminInfo, ok := snapshot["admin_info"].(map[string]any)
	require.True(t, ok)
	diagnostics, ok := adminInfo["stream_diagnostics"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 17, diagnostics["event_count"])
	assert.Equal(t, int64(275), diagnostics["first_event_ms"])
	assert.Equal(t, "req_upstream_123", diagnostics["upstream_request_id"])
	assert.Equal(t, "timeout", diagnostics["termination"])
	assert.Equal(t, false, diagnostics["client_disconnected"])
	assert.Equal(t, false, diagnostics["upstream_eof"])
	assert.Equal(t, true, diagnostics["idle_timeout"])
	assert.Equal(t, true, diagnostics["output_loop_error"])
	assert.Equal(t, true, diagnostics["reasoning_recovery_error"])
}

func TestAppendRelayLogAdminInfoOmitsStreamDiagnosticsForNonStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	other := model.NewLogOther()

	AppendRelayLogAdminInfo(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, other)

	adminInfo, ok := other.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, adminInfo, "stream_diagnostics")
}
