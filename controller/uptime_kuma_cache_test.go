package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUptimeKumaSnapshotCoalescesAndReportsRemoteAvailability(t *testing.T) {
	var requests atomic.Int32
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/api/status-page/demo":
			_, _ = fmt.Fprint(w, `{"publicGroupList":[{"id":1,"name":"fixtures","monitorList":[{"id":42,"name":"Gateway"}]}]}`)
		case "/api/status-page/heartbeat/demo":
			_, _ = fmt.Fprint(w, `{"heartbeatList":{"42":[{"status":1}]},"uptimeList":{"42_24":0.99}}`)
		default:
			t.Errorf("unexpected status service path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cache := &uptimeStatusCache{}
	groups := []map[string]any{{"categoryName": "Fixture", "url": server.URL, "slug": "demo"}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			results, err := cache.load(ctx, "first config", groups, server.Client())
			if assert.NoError(t, err) && assert.Len(t, results, 1) && assert.Len(t, results[0].Monitors, 1) {
				assert.Equal(t, "ready", results[0].State)
				assert.Equal(t, "Gateway", results[0].Monitors[0].Name)
			}
		})
	}
	wg.Wait()
	assert.EqualValues(t, 2, requests.Load())
	unavailable.Store(true)
	cache.latest.expiresAt = time.Time{}
	stale, err := cache.load(ctx, "first config", groups, server.Client())
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, "stale", stale[0].State)
	assert.Positive(t, stale[0].UpdatedAt)
	require.Len(t, stale[0].Monitors, 1)

	changedConfig, err := cache.load(ctx, "different config", groups, server.Client())
	require.NoError(t, err)
	require.Len(t, changedConfig, 1)
	assert.Equal(t, "unavailable", changedConfig[0].State)
	assert.Empty(t, changedConfig[0].Monitors)
}

func TestUptimeKumaRejectsOversizedRemoteResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "{}"+strings.Repeat(" ", uptimeResponseLimit))
	}))
	defer server.Close()
	var dest map[string]any
	require.ErrorContains(t, getAndDecode(t.Context(), server.Client(), server.URL, &dest), "exceeds limit")
}

func TestPublicUptimeUsesLatestHeartbeatForPublishedMonitorsOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status-page/published":
			_, _ = fmt.Fprint(w, `{"publicGroupList":[{"name":"Visible","monitorList":[{"id":1,"name":"Recovered"},{"id":2,"name":"Now down"},{"id":3,"name":"No heartbeat"},{"id":4,"name":"Incomplete"}]}]}`)
		case "/api/status-page/heartbeat/published":
			_, _ = fmt.Fprint(w, `{"heartbeatList":{"1":[{"status":0},{"status":1}],"2":[{"status":1},{"status":0}],"4":[{"status":1},{}],"5":[{"status":1}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result := fetchGroupData(t.Context(), server.Client(), map[string]any{"url": server.URL, "slug": "published", "categoryName": "Public"})
	require.Equal(t, "ready", result.State)
	assert.Equal(t, []Monitor{
		{Name: "Recovered", Group: "Visible", Status: 1},
		{Name: "Now down", Group: "Visible", Status: 0},
		{Name: "No heartbeat", Group: "Visible", Status: -1},
		{Name: "Incomplete", Group: "Visible", Status: -1},
	}, result.Monitors)
}
