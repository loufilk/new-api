package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// Retention runs independently of probe availability, so disabling billable
// fingerprint requests cannot leave personal or excess shared history behind.
type modelFingerprintRetentionHandler struct{}

func (modelFingerprintRetentionHandler) Type() string            { return "model_fingerprint_retention" }
func (modelFingerprintRetentionHandler) Interval() time.Duration { return 5 * time.Minute }
func (modelFingerprintRetentionHandler) NewPayload() any         { return struct{}{} }
func (modelFingerprintRetentionHandler) Enabled() bool           { return true }

func (modelFingerprintRetentionHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if err := model.PruneModelFingerprintHistory(ctx, time.Now().Unix()); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

func init() { RegisterSystemTaskHandler(modelFingerprintRetentionHandler{}) }
