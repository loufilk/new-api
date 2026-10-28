package perfmetrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeFinalFlushRetainsFailedSamplesAndPersistsCurrentBucket(t *testing.T) {
	for _, dialect := range []struct{ name, env string }{
		{"sqlite", ""}, {"mysql", "TEST_PERF_MYSQL_DSN"}, {"postgres", "TEST_PERF_POSTGRES_DSN"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			dsn := ""
			if dialect.env != "" {
				dsn = os.Getenv(dialect.env)
				if dsn == "" {
					t.Skip("isolated test database DSN is not configured")
				}
			}
			t.Setenv("SQL_DSN", dsn)
			oldDB, oldPath, oldMaster, oldRedis := model.DB, common.SQLitePath, common.IsMasterNode, common.RedisEnabled
			oldType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			common.SQLitePath, common.IsMasterNode, common.RedisEnabled = filepath.Join(t.TempDir(), "perf.db"), false, false
			hotBuckets.Clear()
			t.Cleanup(func() {
				model.DB, common.SQLitePath, common.IsMasterNode, common.RedisEnabled = oldDB, oldPath, oldMaster, oldRedis
				common.SetDatabaseTypes(oldType, oldLogType)
				hotBuckets.Clear()
			})
			require.NoError(t, model.InitDB())
			db := model.DB
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.Migrator().DropTable(&model.PerfMetric{}))
			require.NoError(t, db.AutoMigrate(&model.PerfMetric{}))
			current := bucketStart(time.Now().Unix())
			pending := &atomicBucket{}
			pending.add(Sample{Success: true, LatencyMs: 125})
			hotBuckets.Store(bucketKey{model: "shutdown-model", group: "a", bucketTs: current}, pending)
			require.NoError(t, flushBuckets(context.Background(), false))
			var rows int64
			require.NoError(t, db.Model(&model.PerfMetric{}).Where("model_name = ?", "shutdown-model").Count(&rows).Error)
			assert.Zero(t, rows, "periodic flush must leave the current bucket hot")
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			require.Error(t, flushBuckets(cancelled, true))
			assert.EqualValues(t, 1, pending.snapshot().requestCount, "failed final flush must retain samples")
			require.NoError(t, flushBuckets(context.Background(), true))
			require.NoError(t, flushBuckets(context.Background(), true))
			var saved model.PerfMetric
			require.NoError(t, db.Where("model_name = ?", "shutdown-model").First(&saved).Error)
			assert.EqualValues(t, 1, saved.RequestCount)
			assert.EqualValues(t, 1, saved.SuccessCount)
			assert.EqualValues(t, 125, saved.TotalLatencyMs)
		})
	}
}
