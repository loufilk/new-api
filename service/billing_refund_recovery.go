package service

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// Only new, explicitly admitted refund_ready_v2 rows are executable. The
// existing scheduler supplies a lease; row locks and the terminal owner also
// make overlap with the immediate worker safe. Historical queues stay inert.
type billingRefundRecoveryHandler struct{}

func (billingRefundRecoveryHandler) Type() string            { return "billing_refund_v2" }
func (billingRefundRecoveryHandler) Interval() time.Duration { return 15 * time.Second }
func (billingRefundRecoveryHandler) NewPayload() any         { return nil }
func (billingRefundRecoveryHandler) Enabled() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rows, err := model.GetReadyBillingRefunds(ctx, 1)
	if err != nil {
		return false
	}
	if len(rows) != 0 {
		return true
	}
	hasTasks, err := model.HasPendingTaskFinancialEvidence(ctx)
	if err != nil {
		return false
	}
	if hasTasks {
		return true
	}
	mj, err := model.GetCurrentMidjourneyRefunds(ctx, 1)
	return err == nil && len(mj) != 0
}

type billingRecoveryCursor struct {
	TaskID int64 `json:"task_cursor"`
	MJID   int64 `json:"mj_cursor"`
}

func loadBillingRecoveryCursor(ctx context.Context, task *model.SystemTask) (billingRecoveryCursor, error) {
	var cursor billingRecoveryCursor
	state := task.State
	if state == "" {
		var prior model.SystemTask
		err := model.DB.WithContext(ctx).Select("id", "state").Where("type = ? AND id < ? AND state <> ''", task.Type, task.ID).Order("id DESC").First(&prior).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return cursor, err
		}
		state = prior.State
	}
	if state != "" {
		if err := common.Unmarshal([]byte(state), &cursor); err != nil {
			return cursor, err
		}
	}
	if cursor.TaskID < 0 || cursor.MJID < 0 {
		return cursor, errors.New("invalid billing recovery cursor")
	}
	return cursor, nil
}

func persistBillingRecoveryCursor(parent context.Context, task *model.SystemTask, runnerID string, cursor billingRecoveryCursor) error {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	return model.UpdateSystemTaskStateWithContext(ctx, task.TaskID, runnerID, cursor)
}

func (billingRefundRecoveryHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	// Each family receives its own bounded slice. Slow source failures cannot
	// consume every pass before healthy task/MJ refunds are even considered.
	processed := 0
	var scanErr error
	readyCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	rows, err := model.GetReadyBillingRefunds(readyCtx, 32)
	if err != nil {
		scanErr = err
	}
	for _, row := range rows {
		if readyCtx.Err() != nil {
			break
		}
		executeBillingRefund(readyCtx, row.RequestID)
		processed++
	}
	cancel()

	taskCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	cursor, cursorErr := loadBillingRecoveryCursor(taskCtx, task)
	if cursorErr != nil {
		scanErr = cursorErr
	}
	if cursorErr == nil {
		tasks, next, err := model.GetCurrentTaskFinancialIntentsAfter(taskCtx, 32, cursor.TaskID)
		if err == nil && len(tasks) == 0 && next == cursor.TaskID && cursor.TaskID != 0 {
			cursor.TaskID = 0
			tasks, next, err = model.GetCurrentTaskFinancialIntentsAfter(taskCtx, 32, 0)
		}
		if err != nil {
			scanErr = err
		}
		complete := err == nil
		for i := range tasks {
			if taskCtx.Err() != nil {
				complete = false
				break
			}
			entry := &tasks[i]
			cursor.TaskID = entry.ID
			// Persist before the attempt. A crash or a poison item only delays
			// this intent until wraparound; its money/receipt remains durable.
			if err := persistBillingRecoveryCursor(taskCtx, task, runnerID, cursor); err != nil {
				scanErr = err
				complete = false
				break
			}
			operationCtx, stop := context.WithTimeout(taskCtx, 5*time.Second)
			intent := entry.PrivateData.FinancialIntent
			if _, err := ApplyTaskQuota(operationCtx, entry, intent.ActualQuota, intent.Kind == "refund"); err != nil {
				common.SysError("task financial intent remains pending for retry or review")
			}
			stop()
			processed++
		}
		if complete {
			cursor.TaskID = next
			if err := persistBillingRecoveryCursor(taskCtx, task, runnerID, cursor); err != nil {
				scanErr = err
			}
		}
	}
	cancel()

	mjCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	if cursorErr == nil {
		tasks, err := model.GetCurrentMidjourneyRefundsAfter(mjCtx, 32, cursor.MJID)
		if err == nil && len(tasks) == 0 && cursor.MJID != 0 {
			cursor.MJID = 0
			tasks, err = model.GetCurrentMidjourneyRefundsAfter(mjCtx, 32, 0)
		}
		if err != nil {
			scanErr = err
		}
		for i := range tasks {
			if mjCtx.Err() != nil {
				break
			}
			cursor.MJID = int64(tasks[i].Id)
			if err := persistBillingRecoveryCursor(mjCtx, task, runnerID, cursor); err != nil {
				scanErr = err
				break
			}
			if !tasks[i].HasCurrentBillingIdentity() {
				continue
			}
			operationCtx, stop := context.WithTimeout(mjCtx, 5*time.Second)
			RefundMidjourneyQuota(operationCtx, &tasks[i], "Composition failed")
			stop()
			processed++
		}
	}
	cancel()
	status, code := model.SystemTaskStatusSucceeded, ""
	if scanErr != nil || ctx.Err() != nil {
		status, code = model.SystemTaskStatusFailed, "refund_scan_failed"
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, status, map[string]int{"processed": processed}, code); err != nil {
		common.SysError("failed to finish billing refund recovery task")
	}
}

func init() { RegisterSystemTaskHandler(billingRefundRecoveryHandler{}) }

func executeBillingRefund(parent context.Context, requestID string) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	_, err := model.ApplyBillingRefund(ctx, requestID)
	cancel()
	if err == nil {
		return
	}
	code, class := model.BillingRefundErrorCode(err)
	common.SysError(fmt.Sprintf("billing refund incomplete: request_id=%s code=%s class=%s", requestID, code, class))
	// A fresh, bounded context is required after a transaction deadline. This
	// metadata update cannot revive a row whose COMMIT actually succeeded.
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if recordErr := model.RecordBillingRefundFailure(ctx, requestID, err); recordErr != nil {
		common.SysError("failed to record billing refund retry metadata")
	}
}
