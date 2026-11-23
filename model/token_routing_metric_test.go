package model

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenRoutingMetricPostgresAtomicCountersAndCancellation(t *testing.T) {
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL fixture")
	}
	require.NoError(t, DB.AutoMigrate(&TokenRoutingMetric{}))
	const channelID = 95401
	const modelName = "route-metric-regression"
	const group = "route-metric-regression"
	require.NoError(t, DB.Where("channel_id = ?", channelID).Delete(&TokenRoutingMetric{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("channel_id = ?", channelID).Delete(&TokenRoutingMetric{}).Error)
	})

	// Concurrent upserts must preserve every count. Failed attempts count toward
	// request volume but not success latency; negative success latency is zero.
	samples := []struct {
		success bool
		latency time.Duration
	}{
		{true, 123 * time.Millisecond},
		{true, -10 * time.Millisecond},
		{false, 250 * time.Millisecond},
		{true, 20 * time.Millisecond},
	}
	errs := make(chan error, len(samples))
	var workers sync.WaitGroup
	for _, sample := range samples {
		workers.Go(func() {
			errs <- RecordTokenRoutingMetric(t.Context(), channelID, modelName, group, sample.success, sample.latency)
		})
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	rows, err := GetTokenRoutingMetrics(t.Context(), modelName, []string{group})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 4, rows[0].RequestCount)
	assert.EqualValues(t, 3, rows[0].SuccessCount)
	assert.EqualValues(t, 143, rows[0].TotalLatencyMs)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	err = RecordTokenRoutingMetric(cancelled, channelID, modelName, group, true, time.Second)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, RecordTokenRoutingMetric(t.Context(), 0, modelName, group, true, time.Second))
	require.NoError(t, RecordTokenRoutingMetric(t.Context(), channelID, "", group, true, time.Second))
	require.NoError(t, RecordTokenRoutingMetric(t.Context(), channelID, modelName, "", true, time.Second))
	require.NoError(t, RecordTokenRoutingMetric(t.Context(), channelID, modelName, "auto", true, time.Second))
	unchanged, err := GetTokenRoutingMetrics(t.Context(), modelName, []string{group})
	require.NoError(t, err)
	assert.Equal(t, rows, unchanged, "cancelled and invalid observations must not alter route health")
}
