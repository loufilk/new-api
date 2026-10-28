package controller

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

func StartRuntimeWorkers(group *lifecycle.Group, balanceInterval time.Duration) {
	group.Go("task-plugin-sync", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return 30 * time.Second }, true, func(ctx context.Context) {
			if err := syncTaskPluginsOnceContext(ctx); err != nil {
				common.SysError("task plugin synchronization failed: " + common.MaskSensitiveLogInfo(err.Error()))
			}
		})
	})
	if balanceInterval > 0 {
		group.Go("channel-balance-update", func(schedule, work context.Context) {
			lifecycle.Periodic(schedule, work, func() time.Duration { return balanceInterval }, false, func(context.Context) {
				if err := updateAllChannelsBalance(); err != nil {
					common.SysError("channel balance update failed: " + common.MaskSensitiveLogInfo(err.Error()))
				}
			})
		})
	}
}
