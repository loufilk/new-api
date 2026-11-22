package controller

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesWebSocketNativeFailureFinishesRequest(t *testing.T) {
	for _, tc := range []struct {
		name, eventType, status string
		partial                 bool
		wantHealthRequests      int64
	}{
		{"response error", "response.error", "failed", false, 2},
		{"response error with partial usage", "response.error", "failed", true, 2},
		{"leading failed", "response.failed", "failed", false, 2},
		{"completed with failed status", "response.completed", "failed", false, 2},
		{"done with cancelled status", "response.done", "cancelled", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(ws *websocket.Conn, _ *http.Request) {
				for {
					if _, _, err := ws.ReadMessage(); err != nil {
						return
					}
					index := calls.Add(1)
					event := `{"type":"response.completed","response":{"id":"resp_second","status":"completed","output":[]}}`
					if index == 1 {
						usage := ""
						if tc.partial {
							usage = `,"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}`
						}
						event = `{"type":"` + tc.eventType + `","response":{"id":"resp_first","status":"` + tc.status + `","error":{"code":"server_error","message":"private upstream failure"}` + usage + `}}`
					}
					if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(event))) {
						return
					}
				}
			})
			require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"first"}`)))
			first := readResponsesWSTestEvent(t, fixture.client)
			assert.Equal(t, tc.eventType, first["type"], "preserve the provider's native terminal frame")
			assert.NotContains(t, first["response"].(map[string]any)["error"].(map[string]any)["message"], "private upstream failure")
			// A terminal is published after admission is released. The next call
			// must reach upstream immediately, without waiting for idle timeout or
			// getting a spurious request-already-in-progress error.
			require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"second"}`)))
			second := readResponsesWSTestEvent(t, fixture.client)
			require.Equal(t, "response.completed", second["type"], "a failed response must finish its call before the next create: %v", second)
			assert.Equal(t, "resp_second", second["response"].(map[string]any)["id"])
			fixture.closeAndWait(t)
			quotas := []int{1000}
			if tc.partial {
				quotas = []int{1000, 1000}
			}
			assertResponsesWSAccounting(t, fixture, quotas)
			assert.Equal(t, int32(2), calls.Load(), "one upstream create per request, including failures")
			assert.Equal(t, int32(1), fixture.connections.Load(), "no retry connection after a native failure")
			requests, successes := waitPerfCounters(t, tc.wantHealthRequests)
			assert.Equal(t, tc.wantHealthRequests, requests)
			assert.Equal(t, int64(1), successes, "only the second successful response counts as healthy")
		})
	}
}
