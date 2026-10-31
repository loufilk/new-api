package agents

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

// StartRuntimeWorkers only processes durable accounting events and maturity.
// It never invokes a payment provider. Every node may run it: database inbox
// uniqueness and row locks, not process-local leader state, prevent double credit.
// Existing events continue draining when new business entry switches are off.
func StartRuntimeWorkers(workers *lifecycle.Group) {
	if workers == nil {
		return
	}
	startAccountingWorker(workers, "agent-commission-outbox", 10*time.Second, model.ProcessAgentCommissionOutbox)
	startAccountingWorker(workers, "agent-commission-maturity", time.Minute, model.MatureAgentCommissionAccounts)
	startAccountingWorker(workers, "agent-campaign-maintenance", time.Minute, func(ctx context.Context, limit int) (int, error) {
		report, err := model.RunAgentCampaignMaintenance(ctx, limit)
		return report.ExpiredLots + report.ExpiredClaims, err
	})
}

func startAccountingWorker(workers *lifecycle.Group, name string, interval time.Duration, process func(context.Context, int) (int, error)) {
	workers.Go(name, func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return interval }, true, func(ctx context.Context) {
			// Bound each database pass so shutdown can join all producers before pool
			// closure. A canceled/failed transaction stays pending for the next pass.
			bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if _, err := process(bounded, 100); err != nil && ctx.Err() == nil {
				// Financial row contents, receipt identifiers and recipient details must
				// not escape through a database driver's raw diagnostic text.
				common.SysError(name + ": accounting pass failed; pending entries remain retryable")
			}
		})
	})
}
