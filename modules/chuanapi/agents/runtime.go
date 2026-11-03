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
	startAccountingWorker(workers, "agent-referral-maturity", time.Minute, model.MatureReferralCommissions)
	startAccountingWorker(workers, "agent-simple-payment-maturity", time.Minute, model.MatureAgentSimplePaymentProfits)
	startAccountingWorker(workers, "agent-simple-payment-expiry", time.Minute, model.ExpireAgentSimplePaymentOrders)
	startAccountingWorker(workers, "agent-simple-code-expiry", time.Minute, model.ExpireAgentSimpleCodes)
	startAccountingWorker(workers, "agent-simple-weekly-settlement", time.Minute, model.RunAgentSimpleWeeklySettlement)
	startAccountingWorker(workers, "agent-simple-deferred-cash", time.Minute, model.DrainAgentSimpleDeferredCash)
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
