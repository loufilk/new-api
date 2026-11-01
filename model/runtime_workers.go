package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

// StartRuntimeWorkers is called once by the application's composition root.
// A periodic callback is joined before its database can be closed.
func StartRuntimeWorkers(group *lifecycle.Group, frequency int) {
	if frequency <= 0 {
		frequency = 60
	}
	interval := time.Duration(frequency) * time.Second
	group.Go("ordinary-referral-rewards", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return 10 * time.Second }, true, func(ctx context.Context) {
			bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if _, err := ProcessReferralRewardOutbox(bounded, 100); err != nil && ctx.Err() == nil {
				common.SysError("ordinary referral reward processing failed; durable events remain retryable")
			}
		})
	})
	group.Go("wallet-bonus-source-maintenance", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return time.Minute }, true, func(ctx context.Context) {
			bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if _, err := RunBonusSourceMaintenance(bounded, 100); err != nil && ctx.Err() == nil {
				common.SysError("wallet bonus source maintenance failed; balances remain unchanged on failed transactions")
			}
		})
	})
	if common.MemoryCacheEnabled {
		group.Go("channel-cache", func(schedule, _ context.Context) { RunChannelCacheSync(schedule, frequency) })
	}
	group.Go("options", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return interval }, false, func(context.Context) { loadOptionsFromDatabase() })
	})
	group.Go("quota-data", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return time.Duration(common.DataExportInterval) * time.Minute }, true, func(context.Context) {
			if common.DataExportEnabled {
				SaveQuotaDataCache()
			}
		})
	})
	if common.BatchUpdateEnabled {
		group.Go("batch-updater", func(schedule, work context.Context) {
			lifecycle.Periodic(schedule, work, func() time.Duration { return time.Duration(common.BatchUpdateInterval) * time.Second }, false, func(context.Context) { batchUpdate() })
		})
	}
	group.Go("group-config-subscriber", func(schedule, _ context.Context) { RunGroupConfigSubscriber(schedule) })
}

// FlushRuntimeData runs only after HTTP, task handlers and detached refund
// work have joined, so no producer can race the final flush.
func FlushRuntimeData() {
	if common.BatchUpdateEnabled {
		batchUpdate()
	}
	if common.DataExportEnabled {
		SaveQuotaDataCache()
	}
}
