package monitor

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

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixtureID = strings.Repeat("a", 64)

func monitorFixture(t *testing.T, handler http.HandlerFunc) (*Service, *gin.Engine) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	service := New(true)
	service.config = func() (Config, error) { return Config{Enabled: true}, nil }
	service.origin = upstream.URL
	service.now = func() time.Time { return time.Unix(1800000000, 0) }
	gin.SetMode(gin.TestMode)
	router := gin.New()
	service.Register(router.Group("/api/model-monitor"))
	return service, router
}

func writeSnapshot(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success":true,"data":{"enabled":true,"server_time":1800000000,"daily_cost":{"secret":1},"targets":[{"id":"group-1","group":"bblabu special","model":"model-1"}],"results":[{"id":%q,"target_id":"group-1","benchmark":"pelican","status":"ready","quality_status":"passed","quality_reason":"good","completed_at":1799999990}]}}`, fixtureID)
}

func requestMonitor(router *gin.Engine, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/model-monitor"+path, nil)
	request.Header.Set("Authorization", "Bearer private-browser-token")
	request.Header.Set("Cookie", "private-session=value")
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestSnapshotCachesPublicFieldsWithoutForwardingIdentity(t *testing.T) {
	var calls atomic.Int32
	_, router := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Equal(t, upstreamPath, r.URL.Path)
		writeSnapshot(w)
	})
	for range 2 {
		response := requestMonitor(router, "")
		require.Equal(t, http.StatusOK, response.Code)
		var body envelope[Snapshot]
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		require.Len(t, body.Data.Targets, 1)
		assert.Equal(t, body.Data.Targets[0].ID, body.Data.Results[0].TargetID)
		assert.NotEqual(t, "group-1", body.Data.Targets[0].ID)
		assert.Equal(t, "model-1", body.Data.Targets[0].Model)
		assert.False(t, body.Data.Stale)
		assert.NotContains(t, response.Body.String(), "bblabu")
		assert.NotContains(t, response.Body.String(), "daily_cost")
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
	assert.EqualValues(t, 1, calls.Load())
}

func TestSnapshotFailureRetainsDatedDataThenExpiresAndBacksOff(t *testing.T) {
	var calls atomic.Int32
	service, router := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeSnapshot(w)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "bblabu upstream private error")
	})
	require.Equal(t, http.StatusOK, requestMonitor(router, "").Code)
	service.now = func() time.Time { return time.Unix(1800000040, 0) }
	response := requestMonitor(router, "")
	require.Equal(t, http.StatusOK, response.Code)
	var body envelope[Snapshot]
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	assert.True(t, body.Data.Stale)
	assert.EqualValues(t, 1800000000, body.Data.FetchedAt)
	requestMonitor(router, "")
	assert.EqualValues(t, 2, calls.Load())
	service.now = func() time.Time { return time.Unix(1800000601, 0) }
	response = requestMonitor(router, "")
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.NotContains(t, response.Body.String(), "bblabu")
	assert.Equal(t, "15", response.Header().Get("Retry-After"))
}

func TestConcurrentReadersShareOneFetch(t *testing.T) {
	var calls atomic.Int32
	service, _ := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeSnapshot(w) })
	var readers sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		readers.Go(func() {
			<-start
			data, err := service.snapshot(context.Background())
			assert.NoError(t, err)
			assert.Len(t, data.Results, 1)
		})
	}
	close(start)
	readers.Wait()
	assert.EqualValues(t, 1, calls.Load())
}

func TestOldSourceSnapshotCannotAppearFreshAfterRefetch(t *testing.T) {
	service, router := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) { writeSnapshot(w) })
	service.now = func() time.Time { return time.Unix(1800000601, 0) }
	assert.Equal(t, http.StatusServiceUnavailable, requestMonitor(router, "").Code)
}

func TestUnknownIDsAndDisabledFeatureDoNotFetchAssets(t *testing.T) {
	var paths []string
	service, router := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) { paths = append(paths, r.URL.Path); writeSnapshot(w) })
	assert.Equal(t, http.StatusNotFound, requestMonitor(router, "/results/not-an-id").Code)
	assert.Empty(t, paths)
	assert.Equal(t, http.StatusNotFound, requestMonitor(router, "/results/"+strings.Repeat("b", 64)).Code)
	assert.Equal(t, []string{upstreamPath}, paths)
	service.enabled = false
	assert.Equal(t, http.StatusNotFound, requestMonitor(router, "/artworks/"+fixtureID).Code)
	assert.Contains(t, requestMonitor(router, "").Body.String(), `"enabled":false`)
	assert.Len(t, paths, 1)
}

func TestUpstreamRedirectAndInvalidResponsesFailClosed(t *testing.T) {
	for _, test := range []struct {
		name, body, contentType string
		status                  int
	}{
		{"redirect", "", "", http.StatusFound},
		{"html", "<html>login</html>", "text/html", http.StatusOK},
		{"business failure", `{"success":false}`, "application/json", http.StatusOK},
		{"missing schema", `{"success":true,"data":{}}`, "application/json", http.StatusOK},
		{"oversized", strings.Repeat("x", maxBody+1), "application/json", http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			_, router := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", test.contentType)
				w.Header().Set("Location", "/redirect-destination")
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			})
			assert.Equal(t, http.StatusServiceUnavailable, requestMonitor(router, "").Code)
			assert.Equal(t, http.StatusServiceUnavailable, requestMonitor(router, "").Code)
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestDetailAndArtworkStayOnOriginAndConstrainSVG(t *testing.T) {
	_, router := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case upstreamPath:
			writeSnapshot(w)
		case upstreamPath + "/results/" + fixtureID:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"success":true,"data":{"prompt":"Question","answer":"<script>text</script> bblabu https://api.bblabu.ai/link","reference_reason":"Reason","private_field":"hidden"}}`)
		case upstreamPath + "/artworks/" + fixtureID:
			w.Header().Set("Content-Type", "image/svg+xml")
			fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg"><circle r="5"><animate attributeName="r" values="5;10;5" dur="1s" repeatCount="indefinite"/></circle></svg>`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	detail := requestMonitor(router, "/results/"+fixtureID)
	require.Equal(t, http.StatusOK, detail.Code)
	assert.NotContains(t, detail.Body.String(), "bblabu")
	assert.NotContains(t, detail.Body.String(), "private_field")
	artwork := requestMonitor(router, "/artworks/"+fixtureID+"?url=https://other.example")
	require.Equal(t, http.StatusOK, artwork.Code)
	assert.Contains(t, artwork.Header().Get("Content-Type"), "image/svg+xml")
	assert.Contains(t, artwork.Header().Get("Content-Security-Policy"), "sandbox; default-src 'none'")
	assert.Equal(t, "nosniff", artwork.Header().Get("X-Content-Type-Options"))
	assert.Contains(t, artwork.Body.String(), "animate")
}

func TestArtworkRejectsActiveOrExternalContent(t *testing.T) {
	for _, body := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><foreignObject/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><image href="https://external.example/a"/></svg>`,
		`<!DOCTYPE svg><svg xmlns="http://www.w3.org/2000/svg"/>`,
		`<html/>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><text>bblabu</text></svg>`,
	} {
		assert.Error(t, validateArtwork([]byte(body), "image/svg+xml"), body)
	}
	assert.Error(t, validateArtwork([]byte(`<svg/>`), "text/html"))
}
