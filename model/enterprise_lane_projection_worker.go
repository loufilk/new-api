//go:build enterprise_lane_lab

package model

import (
	"context"
	"database/sql"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"gorm.io/gorm"
)

const (
	enterpriseLaneProjectionBatchSize = 128
	enterpriseLaneProjectionTick      = 100 * time.Millisecond
	enterpriseLaneProjectionWindow    = 5 * time.Second
	enterpriseLaneProjectionDrainPass = 32
)

type enterpriseLaneProjectionMetrics struct {
	WindowUS               int64 `json:"window_us"`
	Batches                int   `json:"batches"`
	NonemptyBatches        int   `json:"nonempty_batches"`
	Rows                   int   `json:"rows"`
	Errors                 int   `json:"errors"`
	BatchUSSum             int64 `json:"batch_us_sum"`
	BatchUSMax             int64 `json:"batch_us_max"`
	BatchRowsMax           int   `json:"batch_rows_max"`
	MainPoolWaitCountDelta int64 `json:"main_pool_wait_count_delta"`
	MainPoolWaitUSDelta    int64 `json:"main_pool_wait_us_delta"`
	MainPoolInUse          int   `json:"main_pool_in_use"`
	MainPoolOpen           int   `json:"main_pool_open"`
}

// RunEnterpriseLaneUsageProjection is registered only by the active lab
// runtime. Each tick is bounded, with no provider call, settlement recovery,
// goroutine fan-out, or request-path queue. Errors leave durable pending rows.
func RunEnterpriseLaneUsageProjection(schedule, work context.Context) {
	db := DB
	if db == nil {
		return
	}
	pool, err := db.DB()
	if err != nil {
		common.SysError("enterprise lane projection worker could not read main database pool")
		return
	}
	windowStarted, poolBefore := time.Now(), pool.Stats()
	var stats enterpriseLaneProjectionMetrics
	report := func(now time.Time, poolAfter sql.DBStats) {
		if stats.Rows != 0 || stats.Errors != 0 {
			stats.WindowUS = now.Sub(windowStarted).Microseconds()
			// These stats describe the whole main pool, not solely this worker
			// and not PostgreSQL's row lock wait or a latency percentile.
			stats.MainPoolWaitCountDelta = poolAfter.WaitCount - poolBefore.WaitCount
			stats.MainPoolWaitUSDelta = (poolAfter.WaitDuration - poolBefore.WaitDuration).Microseconds()
			stats.MainPoolInUse, stats.MainPoolOpen = poolAfter.InUse, poolAfter.OpenConnections
			if raw, err := common.Marshal(stats); err == nil {
				common.SysLog("enterprise_lane_projection " + string(raw))
			}
		}
		windowStarted, poolBefore = now, poolAfter
		stats = enterpriseLaneProjectionMetrics{}
	}
	defer func() { report(time.Now(), pool.Stats()) }()
	lifecycle.Periodic(schedule, work, func() time.Duration { return enterpriseLaneProjectionTick }, true, func(ctx context.Context) {
		started := time.Now()
		rows, err := ProjectEnterpriseLaneUsageBatch(ctx, db, enterpriseLaneProjectionBatchSize)
		now := time.Now()
		elapsedUS := now.Sub(started).Microseconds()
		stats.Batches++
		stats.BatchUSSum += elapsedUS
		stats.BatchUSMax = max(stats.BatchUSMax, elapsedUS)
		stats.BatchRowsMax = max(stats.BatchRowsMax, rows)
		if err != nil {
			stats.Errors++
		}
		if rows != 0 {
			stats.NonemptyBatches++
			stats.Rows += rows
		}
		if now.Sub(windowStarted) >= enterpriseLaneProjectionWindow {
			report(now, pool.Stats())
		}
	})
}

// DrainEnterpriseLaneUsageProjection belongs after all HTTP producers and
// periodic projection workers have joined, but before the main pool closes.
// A zero-row SKIP LOCKED result alone is not proof of an empty durable queue.
func DrainEnterpriseLaneUsageProjection(ctx context.Context, db *gorm.DB) error {
	for range enterpriseLaneProjectionDrainPass {
		rows, err := ProjectEnterpriseLaneUsageBatch(ctx, db, enterpriseLaneProjectionBatchSize)
		if err != nil {
			return err
		}
		if rows == 0 {
			break
		}
	}
	var pending EnterpriseLaneRequest
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result := db.WithContext(readCtx).Select("request_id").Where("usage_projection_pending").Limit(1).Find(&pending)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 0 {
		return ErrEnterpriseLaneUsageProjection
	}
	return nil
}
