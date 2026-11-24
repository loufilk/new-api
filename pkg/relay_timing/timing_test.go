package relay_timing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayTimingSamplePermille(t *testing.T) {
	for raw, want := range map[string]uint64{
		"": 0, "abc": 0, "-1": 0, "1001": 0, "0": 0, " 50 ": 50, "1000": 1000,
	} {
		assert.Equal(t, want, parseSamplePermille(raw), raw)
	}
}

func TestRelayTimingReportSeparatesStages(t *testing.T) {
	ingress := time.Now().Add(-20 * time.Millisecond)
	s := &Sample{
		ingress:              ingress,
		entered:              ingress.Add(6 * time.Millisecond),
		initialRouteSelected: ingress.Add(4 * time.Millisecond),
	}
	s.RecordBillingPreparation(time.Now().Add(-2 * time.Millisecond))
	report := s.Finish(http.StatusOK, false, ingress.Add(10*time.Millisecond), ingress.Add(15*time.Millisecond))
	require.NotNil(t, report.IngressToRelayMS)
	require.NotNil(t, report.IngressToRouteSelectionMS)
	require.NotNil(t, report.BillingPrepareMS)
	require.NotNil(t, report.IngressToFirstUpstreamEventMS)
	require.NotNil(t, report.IngressToFirstUsableOutputMS)
	assert.EqualValues(t, 6, *report.IngressToRelayMS)
	assert.EqualValues(t, 4, *report.IngressToRouteSelectionMS)
	assert.GreaterOrEqual(t, *report.BillingPrepareMS, int64(2))
	assert.EqualValues(t, 10, *report.IngressToFirstUpstreamEventMS)
	assert.EqualValues(t, 15, *report.IngressToFirstUsableOutputMS)
	failed := s.Finish(http.StatusServiceUnavailable, true, time.Time{}, ingress.Add(-time.Second))
	assert.Nil(t, failed.IngressToFirstUpstreamEventMS)
	assert.Nil(t, failed.IngressToFirstUsableOutputMS)
	assert.True(t, failed.RelayAPIError)
	assert.Equal(t, http.StatusServiceUnavailable, failed.Status)
}

func TestRelayTimingTracePreservesExistingRequestAndTrace(t *testing.T) {
	s := &Sample{entered: time.Now()}
	const private = "not-for-timing-logs"
	existingCalled := false
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { existingCalled = info.Reused },
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://local.invalid/path?key="+private, strings.NewReader(private))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+private)
	req.GetBody = nil // Match an admission policy that prohibits transparent replay.

	traced, finish := s.traceUpstream(req)
	require.NotNil(t, finish)
	assert.NotSame(t, req, traced)
	assert.Equal(t, req.Body, traced.Body)
	assert.Equal(t, req.ContentLength, traced.ContentLength)
	assert.Nil(t, traced.GetBody)
	assert.Equal(t, req.Header, traced.Header)
	assert.Equal(t, req.URL, traced.URL)
	trace := httptrace.ContextClientTrace(traced.Context())
	require.NotNil(t, trace)
	trace.GetConn("origin" + private)
	trace.GotConn(httptrace.GotConnInfo{Reused: true})
	trace.WroteRequest(httptrace.WroteRequestInfo{})
	trace.GotFirstResponseByte()
	finish(&http.Response{StatusCode: http.StatusOK, ProtoMajor: 2})

	report := s.Finish(http.StatusOK, false, time.Time{}, time.Time{})
	require.Len(t, report.Upstreams, 1)
	upstream := report.Upstreams[0]
	assert.True(t, existingCalled)
	require.NotNil(t, upstream.ConnectionReused)
	assert.True(t, *upstream.ConnectionReused)
	assert.NotNil(t, upstream.GetConnectionMS)
	assert.NotNil(t, upstream.WrittenToHeadersMS)
	assert.NotNil(t, upstream.DispatchToResultMS)
	assert.Equal(t, 2, upstream.ProtocolMajor)
	assert.Equal(t, http.StatusOK, upstream.Status)
	encoded, err := common.Marshal(report)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), private)
	assert.NotContains(t, string(encoded), "local.invalid")
}

func TestRelayTimingTraceBoundsRetriesAndOmitsMissedStages(t *testing.T) {
	s := &Sample{entered: time.Now()}
	req, err := http.NewRequest(http.MethodGet, "https://local.invalid", nil)
	require.NoError(t, err)
	for i := range maxUpstreamTraces + 2 {
		traced, finish := s.traceUpstream(req)
		if i >= maxUpstreamTraces {
			assert.Same(t, req, traced)
			assert.Nil(t, finish)
			continue
		}
		trace := httptrace.ContextClientTrace(traced.Context())
		trace.GetConn("fixture")
		trace.GotConn(httptrace.GotConnInfo{})
		trace.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("synthetic failure")})
		finish(nil)
	}
	report := s.Finish(http.StatusBadGateway, true, time.Time{}, time.Time{})
	assert.Equal(t, maxUpstreamTraces+2, report.UpstreamAttempts)
	require.Len(t, report.Upstreams, maxUpstreamTraces)
	assert.Nil(t, report.Upstreams[0].WrittenToHeadersMS)
	assert.Zero(t, report.Upstreams[0].Status)
	require.NotNil(t, report.Upstreams[0].ConnectionReused)
	assert.False(t, *report.Upstreams[0].ConnectionReused)
}
