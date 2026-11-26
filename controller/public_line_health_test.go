package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/modules/chuanapi/console"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/stretchr/testify/require"
)

func TestPublicLineHealthUsesCompletedOptInBuckets(t *testing.T) {
	now := time.Unix(2000000000, 0).Truncate(time.Hour).Add(30 * time.Minute)
	end := now.Truncate(time.Hour).Unix()
	eligible := console.PublishedLine{ID: "public", Label: "Visible", RepresentativeModel: "representative", Group: "internal-group", PublishHealth: true, ObservedSince: end - 10801}
	restricted := eligible
	restricted.ID, restricted.Group, restricted.PublishHealth = "restricted", "private-canary", false
	key := lineHealthKey(eligible)
	read := func(_ context.Context, keys []model.PublicLineHealthKey, start, stop int64) ([]model.PublicLineHealthBucket, error) {
		require.Equal(t, []model.PublicLineHealthKey{key}, keys)
		require.Equal(t, end, stop)
		require.Equal(t, int64(24*3600), stop-start)
		return []model.PublicLineHealthBucket{
			{PublicLineHealthKey: key, BucketStart: end - 14400, Requests: 200, Successes: 0, LatencySamples: 200, LatencySumMs: 2000},
			{PublicLineHealthKey: key, BucketStart: end - 7200, Requests: 40, Successes: 20, LatencySamples: 40, LatencySumMs: 4000, CacheSamples: 20, CacheHits: 0},
			{PublicLineHealthKey: key, BucketStart: end - 3600, Requests: 80, Successes: 80, LatencySamples: 60, LatencySumMs: 12000, CacheSamples: 40, CacheHits: 30},
			{PublicLineHealthKey: key, BucketStart: end, Requests: 200, Successes: 0, LatencySamples: 200, LatencySumMs: 2000},
		}, nil
	}
	settings := perf_metrics_setting.PerfMetricsSetting{Enabled: true, FlushInterval: 5}
	get := func(context.Context) ([]console.PublishedLine, error) {
		return []console.PublishedLine{eligible, restricted}, nil
	}
	got, err := readPublicLineHealth(context.Background(), now, settings, get, read)
	require.NoError(t, err)
	require.Len(t, got.Lines, 1)
	line := got.Lines[0]
	require.Len(t, line.Buckets, 24)
	require.Nil(t, line.Buckets[20].SuccessRate)
	require.EqualValues(t, 0, *line.Buckets[22].CacheHitRate)
	require.EqualValues(t, 83, *line.SuccessRate)
	require.EqualValues(t, 160, *line.AverageLatencyMs)
	require.EqualValues(t, 50, *line.CacheHitRate)
	wire, err := json.Marshal(got)
	require.NoError(t, err)
	for _, key := range []string{"internal-group", "private-canary", "request_count", "cache_sample_count"} {
		require.NotContains(t, string(wire), key)
	}
}

func TestPublicLineHealthRevocationAndInvalidMeasurements(t *testing.T) {
	now := time.Unix(2000000000, 0)
	l := console.PublishedLine{ID: "public", Label: "Visible", RepresentativeModel: "representative", Group: "internal", PublishHealth: true, ObservedSince: now.Unix() - 86400}
	var reads int
	get := func(context.Context) ([]console.PublishedLine, error) {
		reads++
		if reads == 1 {
			return []console.PublishedLine{l}, nil
		}
		return nil, nil
	}
	read := func(context.Context, []model.PublicLineHealthKey, int64, int64) ([]model.PublicLineHealthBucket, error) {
		return nil, nil
	}
	out, err := readPublicLineHealth(context.Background(), now, perf_metrics_setting.PerfMetricsSetting{Enabled: true}, get, read)
	require.NoError(t, err)
	require.Empty(t, out.Lines)
	require.Equal(t, 2, reads)
	for _, invalid := range []model.PublicLineHealthBucket{
		{Requests: 19, Successes: 19, LatencySamples: 19, LatencySumMs: 200, CacheSamples: 19, CacheHits: 2},
		{Requests: 20, Successes: 30, LatencySamples: 21, LatencySumMs: 200, CacheSamples: 31, CacheHits: 3},
		{Requests: 20, Successes: 30, LatencySamples: 21, LatencySumMs: 200, CacheSamples: 30, CacheHits: 35},
	} {
		value := measurePublicHealth(invalid)
		require.Nil(t, value.SuccessRate)
		require.Nil(t, value.AverageLatencyMs)
		require.Nil(t, value.CacheHitRate)
	}
}
