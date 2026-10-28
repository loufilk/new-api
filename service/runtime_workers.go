package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

func StartRuntimeWorkers(group *lifecycle.Group) {
	group.Go("system-instance-reporter", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return systemInstanceReportInterval }, true, func(context.Context) { reportSystemInstanceWithLog() })
	})
	if !common.IsMasterNode {
		return
	}
	group.Go("codex-credential-refresh", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return codexCredentialRefreshTickInterval }, true, runCodexCredentialAutoRefreshOnceContext)
	})
	group.Go("subscription-maintenance", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return subscriptionResetTickInterval }, true, runSubscriptionQuotaResetOnceContext)
	})
	group.Go("auth-artifact-cleanup", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return authArtifactCleanupInterval }, true, func(context.Context) { cleanupAuthArtifacts() })
	})
	group.Go("system-task-runner", RunSystemTaskRunner)
}
