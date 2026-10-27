package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

// Lifecycle-aware application entry points. The existing scheduler, claiming
// transactions, task creation and handler implementations remain unchanged.
// Legacy StartSystemTaskRunner remains available for embedded callers.

// RunSystemTaskRunner stops claiming on schedule cancellation, then joins all
// already-claimed handlers. Their lease heartbeat lives until each handler
// finishes, or work is cancelled by the forced-shutdown deadline.
func RunSystemTaskRunner(schedule, work context.Context) {
	if !common.IsMasterNode {
		return
	}
	runnerID := fmt.Sprintf("%s-%s", common.NodeName, common.GetRandomString(8))
	var active sync.WaitGroup
	defer active.Wait()
	ticker := time.NewTicker(systemTaskRunnerIdleInterval)
	defer ticker.Stop()
	var lastScheduler, lastCleanup time.Time
	runPass := func() {
		if schedule.Err() != nil {
			return
		}
		now := time.Now()
		if now.Sub(lastCleanup) >= systemTaskStaleLockInterval {
			lastCleanup = now
			if err := model.ExpireStaleSystemTaskLocks(common.GetTimestamp()); err != nil {
				logger.LogWarn(work, fmt.Sprintf("system task stale lock cleanup failed: %v", err))
			}
		}
		if schedule.Err() != nil {
			return
		}
		if now.Sub(lastScheduler) >= systemTaskSchedulerInterval {
			lastScheduler = now
			runSystemTaskScheduler()
		}
		runSystemTaskClaimPassContext(schedule, runnerID, func(task *model.SystemTask, handler SystemTaskHandler) {
			active.Add(1)
			go func() {
				defer active.Done()
				defer func() {
					if recover() != nil {
						failSystemTask(task, runnerID, errors.New("system task handler panicked"))
					}
				}()
				runWithLeaseHeartbeatContext(work, task, runnerID, func(ctx context.Context) { handler.Run(ctx, task, runnerID) })
			}()
		})
	}
	runPass()
	for {
		select {
		case <-schedule.Done():
			return
		case <-ticker.C:
		case <-systemTaskWakeup:
		}
		runPass()
	}
}

func runSystemTaskClaimPassContext(ctx context.Context, runnerID string, dispatch func(*model.SystemTask, SystemTaskHandler)) {
	if ctx.Err() != nil {
		return
	}
	handlers := registeredSystemTaskHandlers()
	taskTypes := make([]string, 0, len(handlers))
	for _, handler := range handlers {
		taskTypes = append(taskTypes, handler.Type())
	}
	pendingTasks, err := model.FindEarliestPendingSystemTasks(taskTypes)
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("system task runner query failed: %v", err))
		return
	}
	for _, handler := range handlers {
		if ctx.Err() != nil {
			return
		}
		task := pendingTasks[handler.Type()]
		if task == nil {
			continue
		}
		claimedTask, claimed, err := model.ClaimSystemTask(task.ID, handler.Type(), runnerID, systemTaskLockUntil())
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("system task claim failed: %v", err))
			continue
		}
		if !claimed {
			continue
		}
		dispatch(claimedTask, handler)
	}
}

func runWithLeaseHeartbeatContext(parent context.Context, task *model.SystemTask, runnerID string, fn func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	interval := systemTaskLockTTL / 3
	if interval <= 0 {
		interval = systemTaskLockTTL
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	done := make(chan struct{})
	heartbeatDone := make(chan struct{})
	defer func() { close(done); <-heartbeatDone }()

	go func() {
		defer close(heartbeatDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if err := model.RenewSystemTaskLock(task.TaskID, runnerID, systemTaskLockUntil()); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	fn(ctx)
}
