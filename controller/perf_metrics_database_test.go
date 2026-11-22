package controller

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerfMetricGroupFiltersDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		kind, env string
		typ       common.DatabaseType
	}{
		{"sqlite", "", common.DatabaseTypeSQLite},
		{"mysql", "TEST_MYSQL_DSN", common.DatabaseTypeMySQL},
		{"postgres", "TEST_POSTGRES_DSN", common.DatabaseTypePostgreSQL},
	} {
		t.Run(dialect.kind, func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " is not configured")
			}
			db, _ := newAuditTestDatabase(t, dialect.kind, dsn)
			useLogExportDatabases(t, db, db, dialect.typ, dialect.typ)
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

			metrics, err := model.GetPerfMetrics("fixture-model", "visible", 3600, 10800)
			require.NoError(t, err)
			require.Len(t, metrics, 4)
			for _, metric := range metrics {
				assert.Equal(t, "visible", metric.Group)
			}
			metrics, err = model.GetPerfMetrics("fixture-model", "", 3600, 10800)
			require.NoError(t, err)
			assert.Len(t, metrics, 5, "the unfiltered history keeps all groups")

			summaries, err := model.GetPerfMetricsSummaryAll(3600, 10800, []string{"visible"})
			require.NoError(t, err)
			assert.ElementsMatch(t, []model.PerfMetricSummary{
				{ModelName: "fixture-model", RequestCount: 6, SuccessCount: 5},
				{ModelName: "other-model", RequestCount: 50},
			}, summaries)
			summaries, err = model.GetPerfMetricsSummaryAll(3600, 10800, []string{})
			require.NoError(t, err)
			assert.Empty(t, summaries, "an empty permitted-group list must not expose summaries")
			summaries, err = model.GetPerfMetricsSummaryAll(3600, 10800, nil)
			require.NoError(t, err)
			assert.ElementsMatch(t, []model.PerfMetricSummary{
				{ModelName: "fixture-model", RequestCount: 106, SuccessCount: 5},
				{ModelName: "other-model", RequestCount: 50},
			}, summaries)

			buckets, err := model.GetPerfMetricsSummaryBucketsAll(3600, 10800, []string{"visible"})
			require.NoError(t, err)
			assert.ElementsMatch(t, []model.PerfMetricSummaryBucket{
				{ModelName: "fixture-model", BucketTs: 3600, RequestCount: 1, SuccessCount: 1},
				{ModelName: "fixture-model", BucketTs: 7200, RequestCount: 2, SuccessCount: 1},
				{ModelName: "fixture-model", BucketTs: 7500, RequestCount: 3, SuccessCount: 3},
				{ModelName: "other-model", BucketTs: 7500, RequestCount: 50},
			}, buckets)
			buckets, err = model.GetPerfMetricsSummaryBucketsAll(3600, 10800, []string{})
			require.NoError(t, err)
			assert.Empty(t, buckets, "an empty permitted-group list must not expose buckets")

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
		})
	}
}
