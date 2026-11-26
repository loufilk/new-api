package model

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPublicLineHealthPostgreSQLUpgradeAndAggregation(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated TEST_POSTGRES_DSN")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	tx := db.Begin()
	require.NoError(t, tx.Error)
	old := DB
	DB = tx
	t.Cleanup(func() { DB = old; require.NoError(t, tx.Rollback().Error) })
	// PostgreSQL's information schema excludes temp tables from the default
	// migration lookup, so use a throwaway schema inside the rollback.
	schema := fmt.Sprintf("line_health_%d", time.Now().UnixNano())
	require.NoError(t, tx.Exec("CREATE SCHEMA "+schema).Error)
	require.NoError(t, tx.Exec("SET LOCAL search_path = "+schema).Error)
	require.NoError(t, tx.Exec(`CREATE TABLE perf_metrics (
		id bigserial PRIMARY KEY, model_name varchar(128), "group" varchar(64), bucket_ts bigint,
		request_count bigint DEFAULT 0, success_count bigint DEFAULT 0, total_latency_ms bigint DEFAULT 0,
		ttft_sum_ms bigint DEFAULT 0, ttft_count bigint DEFAULT 0, output_tokens bigint DEFAULT 0,
		generation_ms bigint DEFAULT 0
	)`).Error)
	require.NoError(t, tx.Exec(`CREATE UNIQUE INDEX idx_perf_model_group_bucket ON perf_metrics (model_name,"group",bucket_ts)`).Error)
	require.NoError(t, tx.Exec(`INSERT INTO perf_metrics (model_name,"group",bucket_ts,request_count,success_count,total_latency_ms)
		VALUES ('m','scope',7200,20,20,200000), ('m','other',7200,50,50,1000000)`).Error)
	require.NoError(t, tx.AutoMigrate(&PerfMetric{}))
	require.NoError(t, tx.AutoMigrate(&PerfMetric{}))
	for _, ts := range []int64{7200, 7500, 10800} {
		require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: "m", Group: "scope", BucketTs: ts,
			RequestCount: 20, SuccessCount: 15, TotalLatencyMs: 2000, LatencyCount: 20, MeasuredLatencyMs: 2000, CacheSampleCount: 10, CacheHitCount: 3,
		}))
	}
	key := PublicLineHealthKey{Model: "m", Group: "scope"}
	rows, err := ReadPublicLineHealthBuckets(context.Background(), []PublicLineHealthKey{key, {Model: "other", Group: "different"}}, 7200, 10800)
	require.NoError(t, err)
	require.Equal(t, []PublicLineHealthBucket{{PublicLineHealthKey: key, BucketStart: 7200, Requests: 60, Successes: 50,
		LatencySamples: 40, LatencySumMs: 4000, CacheSamples: 20, CacheHits: 6}}, rows)
}
