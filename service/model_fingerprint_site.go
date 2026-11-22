package service

import (
	"context"
	"github.com/QuantumNous/new-api/model"
)

// Not a ScheduledSystemTaskHandler: even a legacy enabled configuration cannot
// produce another job. An old queued job can be taken off the queue, but never
// calls a provider. In-flight receipts and personal observations are untouched.
type retiredFingerprintSiteHandler struct{}

func (retiredFingerprintSiteHandler) Type() string { return model.SystemTaskTypeModelFingerprintSite }
func (retiredFingerprintSiteHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusFailed, nil, "Fingerprint monitoring retired; no new probes dispatched"); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}
func init() { RegisterSystemTaskHandler(retiredFingerprintSiteHandler{}) }
