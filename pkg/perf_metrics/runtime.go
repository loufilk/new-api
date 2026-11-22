package perfmetrics

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

var redisWrites = lifecycle.NewGroup()

func recordRedisBackground(fn func()) {
	if !redisWrites.Submit("performance-redis-write", gopool.Go, func(_, _ context.Context) { fn() }) {
		fn()
	}
}

func RunFlush(schedule, work context.Context) {
	lifecycle.Periodic(schedule, work, func() time.Duration {
		return time.Duration(perf_metrics_setting.GetFlushIntervalMinutes()) * time.Minute
	}, false, func(ctx context.Context) {
		setting := perf_metrics_setting.GetSetting()
		if setting.Enabled {
			_ = flushBuckets(ctx, false)
			cleanupExpiredMetrics(setting.RetentionDays)
		}
	})
}

// Drain runs after all sample producers and the periodic flush have joined.
// Include the current partial bucket; a restart must not discard that window.
func Drain(ctx context.Context) error {
	redisWrites.BeginDrain()
	if err := redisWrites.Wait(ctx); err != nil {
		return err
	}
	return flushBuckets(ctx, true)
}
