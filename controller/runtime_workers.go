package controller

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

// Balance updates are scheduled by channelBalanceHandler through the managed
// system-task runner. Do not add a second per-node ticker for the same setting.
func StartRuntimeWorkers(group *lifecycle.Group, _ time.Duration) {
	keyDiagnosticWorkers.Store(group)
	group.Go("task-plugin-sync", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return 30 * time.Second }, true, func(ctx context.Context) {
			if err := SyncTaskPluginsOnceContext(ctx); err != nil {
				common.SysError("task plugin synchronization failed: " + common.MaskSensitiveLogInfo(err.Error()))
			}
		})
	})
}
