package controller

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecentPerfMetricBucketsPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	useLogExportDatabases(t, db, db, common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	require.NoError(t, db.AutoMigrate(&model.PerfMetric{}))
	rows := []model.PerfMetric{
		{ModelName: "fixture-model", Group: "visible", BucketTs: 7200, RequestCount: 2, SuccessCount: 1},
		{ModelName: "fixture-model", Group: "visible", BucketTs: 7500, RequestCount: 3, SuccessCount: 3},
		{ModelName: "fixture-model", Group: "hidden", BucketTs: 7500, RequestCount: 100, SuccessCount: 0},
		{ModelName: "fixture-model", Group: "visible", BucketTs: 3600, RequestCount: 1, SuccessCount: 1},
		{ModelName: "fixture-model", Group: "visible", BucketTs: 10800},
		{ModelName: "other-model", Group: "visible", BucketTs: 7500, RequestCount: 50},
	}
	require.NoError(t, db.Create(&rows).Error)
	got, err := model.GetRecentPerfMetricSuccessBuckets("fixture-model", 3600, 10800, []string{"visible"}, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, model.PerfMetricSummaryBucket{ModelName: "fixture-model", BucketTs: 7200, RequestCount: 5, SuccessCount: 4}, got[0])
	got, err = model.GetRecentPerfMetricSuccessBuckets("fixture-model", 3600, 10800, []string{}, 2)
	require.NoError(t, err)
	assert.Empty(t, got, "an empty permitted-group list must not expose metrics")
	got, err = model.GetRecentPerfMetricSuccessBuckets("fixture-model", 3600, 10800, nil, 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.EqualValues(t, 105, got[0].RequestCount)
	assert.EqualValues(t, 3600, got[1].BucketTs)
}
