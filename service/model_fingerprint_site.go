package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// The controller supplies the relay-backed executor, keeping service independent
// of controller. It must persist the result and authorize every individual probe.
type ModelFingerprintSiteExecutor func(context.Context, *model.SystemTask, string, model.ModelFingerprintSiteTarget, *model.ModelFingerprintSiteRun) error

var fingerprintSiteExecutorMu sync.RWMutex
var fingerprintSiteExecutor ModelFingerprintSiteExecutor

func RegisterModelFingerprintSiteExecutor(executor ModelFingerprintSiteExecutor) {
	fingerprintSiteExecutorMu.Lock()
	fingerprintSiteExecutor = executor
	fingerprintSiteExecutorMu.Unlock()
	RegisterSystemTaskHandler(modelFingerprintSiteHandler{})
}

type modelFingerprintSiteHandler struct{}

func (modelFingerprintSiteHandler) Type() string            { return model.SystemTaskTypeModelFingerprintSite }
func (modelFingerprintSiteHandler) Interval() time.Duration { return 15 * time.Second }
func (modelFingerprintSiteHandler) NewPayload() any         { return struct{}{} }
func (modelFingerprintSiteHandler) Enabled() bool {
	enabled, err := model.ModelTestEnabled(context.Background())
	if err != nil || !enabled {
		return false
	}
	c, err := model.GetModelFingerprintSiteConfig(context.Background())
	return err == nil && c.AutomaticEnabled && c.DailyBudgetQuota > 0
}
func (modelFingerprintSiteHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	fingerprintSiteExecutorMu.RLock()
	execute := fingerprintSiteExecutor
	fingerprintSiteExecutorMu.RUnlock()
	if execute == nil {
		failSystemTask(task, runnerID, errors.New("Fingerprint site executor unavailable"))
		return
	}
	// Never replay a run after a crash. Any previous uncompleted run may have
	// incurred provider charges: retain its full reservation and pause automation.
	var abandoned []model.ModelFingerprintSiteRun
	if err := model.DB.WithContext(ctx).Where("status = ? AND task_id <> ?", "running", task.TaskID).Find(&abandoned).Error; err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	for _, run := range abandoned {
		if err := model.FinishModelFingerprintSiteRun(ctx, run.ID, model.ModelFingerprintSiteResult{Status: "unknown", Assessment: "usage_unknown"}, nil); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
	}
	if len(abandoned) > 0 {
		failSystemTask(task, runnerID, errors.New("Fingerprint site automation paused after uncertain run"))
		return
	}
	c, err := model.GetModelFingerprintSiteConfig(ctx)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	for _, target := range c.Targets {
		if ctx.Err() != nil {
			return
		}
		if !target.Enabled {
			continue
		}
		run, err := model.ReserveModelFingerprintSiteRun(ctx, task.TaskID, runnerID, target.ID, c.Version, time.Now().Unix())
		if errors.Is(err, model.ErrFingerprintSiteNotDue) || errors.Is(err, model.ErrFingerprintSiteBudget) {
			continue
		}
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		executeErr := execute(ctx, task, runnerID, target, run)
		// Persist even after cancellation; no further network calls happen here.
		var stored model.ModelFingerprintSiteRun
		if err := model.DB.First(&stored, "id = ?", run.ID).Error; err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if stored.Status == "running" {
			err = model.FinishModelFingerprintSiteRun(context.Background(), run.ID, model.ModelFingerprintSiteResult{Status: "unknown", Assessment: "usage_unknown"}, nil)
			if err != nil {
				failSystemTask(task, runnerID, err)
				return
			}
		}
		if executeErr != nil {
			failSystemTask(task, runnerID, errors.New("Fingerprint site execution failed"))
			return
		}
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}
