package monitor

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func monitorOptionFixture(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{settingsOption: raw}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
}

func TestMonitorHiddenGroupsLoadExistingJSON(t *testing.T) {
	id := "route-" + strings.Repeat("a", 32)
	for _, tc := range []struct {
		name, raw, expected string
	}{
		{"fresh defaults", "", `{"enabled":true,"title":"","description":"","groups":{},"hidden_groups":{}}`},
		{"legacy omission", `{"enabled":false,"title":"Old","description":"","groups":{}}`, `{"enabled":false,"title":"Old","description":"","groups":{},"hidden_groups":{}}`},
		{"existing visibility", fmt.Sprintf(`{"enabled":true,"title":"Page","description":"","groups":{},"hidden_groups":{%q:true}}`, id), fmt.Sprintf(`{"enabled":true,"title":"Page","description":"","groups":{},"hidden_groups":{%q:true}}`, id)},
		{"visible entry normalized", fmt.Sprintf(`{"enabled":true,"groups":{},"hidden_groups":{%q:false}}`, id), `{"enabled":true,"title":"","description":"","groups":{},"hidden_groups":{}}`},
		{"legacy null", `{"enabled":true,"groups":{},"hidden_groups":null}`, `{"enabled":true,"title":"","description":"","groups":{},"hidden_groups":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorOptionFixture(t, tc.raw)
			config, err := readConfig()
			require.NoError(t, err)
			encoded, err := common.Marshal(config)
			require.NoError(t, err)
			assert.JSONEq(t, tc.expected, string(encoded))
		})
	}
}

func TestMonitorHiddenGroupsRejectInvalidStoredVisibility(t *testing.T) {
	tooMany := make(map[string]bool)
	for i := range 129 {
		tooMany[fmt.Sprintf("route-%032x", i)] = true
	}
	oversized, err := common.Marshal(tooMany)
	require.NoError(t, err)
	for _, tc := range []struct{ name, hidden string }{
		{"invalid short key", `{"route-1":true}`},
		{"invalid false key", `{"route-1":false}`},
		{"uppercase key", `{"route-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA":true}`},
		{"control in key", `{"route-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n":true}`},
		{"nonboolean", `{"route-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":"true"}`},
		{"too many", string(oversized)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorOptionFixture(t, `{"enabled":true,"groups":{},"hidden_groups":`+tc.hidden+`}`)
			_, err := readConfig()
			assert.ErrorIs(t, err, errUnavailable)
		})
	}
}

func TestMonitorHiddenGroupsFilterCachedPublicSnapshotAndAssets(t *testing.T) {
	var paths []string
	reordered := false
	visibleResultID := strings.Repeat("b", 64)
	s, router := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case upstreamPath:
			targets := `[{"id":"group-1","model":"private-model"},{"id":"group-2","model":"public-model"}]`
			if reordered {
				targets = `[{"id":"group-2","model":"public-model"},{"id":"group-1","model":"private-model"}]`
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"success":true,"data":{"enabled":true,"server_time":1800000000,"targets":%s,"results":[{"id":%q,"target_id":"group-1","benchmark":"pelican","status":"ready"},{"id":%q,"target_id":"group-2","benchmark":"candy","status":"passed"}]}}`, targets, fixtureID, visibleResultID)
		case upstreamPath + "/results/" + fixtureID:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"success":true,"data":{"prompt":"Private prompt","answer":"Private answer"}}`)
		case upstreamPath + "/artworks/" + fixtureID:
			w.Header().Set("Content-Type", "image/svg+xml")
			fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg"><circle r="5"/></svg>`)
		case upstreamPath + "/thumbnails/" + fixtureID:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
		default:
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	var initial envelope[Snapshot]
	response := requestMonitor(router, "")
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &initial))
	require.Len(t, initial.Data.Targets, 2)
	hiddenID, visibleID := initial.Data.Targets[0].ID, initial.Data.Targets[1].ID
	for _, kind := range []string{"results", "artworks", "thumbnails"} {
		require.Equal(t, http.StatusOK, requestMonitor(router, "/"+kind+"/"+fixtureID).Code)
	}
	monitorOptionFixture(t, fmt.Sprintf(`{"enabled":true,"title":"Monitor","description":"","groups":{%q:"Private label",%q:"Public label"},"hidden_groups":{%q:true}}`, hiddenID, visibleID, hiddenID))
	s.config = readConfig
	response = requestMonitor(router, "")
	require.Equal(t, http.StatusOK, response.Code)
	var filtered envelope[Snapshot]
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &filtered))
	assert.Equal(t, []Target{{ID: visibleID, Number: 2, Model: "public-model", Name: "Public label"}}, filtered.Data.Targets)
	if assert.Len(t, filtered.Data.Results, 1) {
		assert.Equal(t, visibleResultID, filtered.Data.Results[0].ID)
		assert.Equal(t, visibleID, filtered.Data.Results[0].TargetID)
	}
	assert.NotContains(t, response.Body.String(), hiddenID)
	assert.NotContains(t, response.Body.String(), "Private label")
	for _, kind := range []string{"results", "artworks", "thumbnails"} {
		blocked := requestMonitor(router, "/"+kind+"/"+fixtureID+"?hidden_groups={}&url=https://untrusted.example")
		assert.Equal(t, http.StatusNotFound, blocked.Code, kind)
		assert.Equal(t, "no-store", blocked.Header().Get("Cache-Control"))
		assert.Empty(t, blocked.Body.String())
	}
	assert.Len(t, paths, 4, "hiding a group must block even already cached detail/assets")

	// Root settings keep the full catalog so hidden groups can be made visible.
	s.RegisterSettings(router.Group("/api/option"))
	catalog := httptest.NewRecorder()
	router.ServeHTTP(catalog, httptest.NewRequest(http.MethodGet, "/api/option/model-monitor/targets", nil))
	require.Equal(t, http.StatusOK, catalog.Code)
	var targets envelope[[]Target]
	require.NoError(t, common.Unmarshal(catalog.Body.Bytes(), &targets))
	require.Len(t, targets.Data, 2)
	assert.Equal(t, hiddenID, targets.Data[0].ID)
	assert.Len(t, paths, 4)

	// Stable IDs must continue hiding the same source after an upstream reorder.
	reordered = true
	s.now = func() time.Time { return time.Unix(1800000040, 0) }
	response = requestMonitor(router, "")
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &filtered))
	assert.Equal(t, []Target{{ID: visibleID, Number: 1, Model: "public-model", Name: "Public label"}}, filtered.Data.Targets)
	require.Len(t, filtered.Data.Results, 1)
	assert.Equal(t, visibleResultID, filtered.Data.Results[0].ID)
}
