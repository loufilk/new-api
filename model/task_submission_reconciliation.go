package model

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// BillingTaskPersistenceEvidence describes only observations of the original
// local destination. It says nothing about upstream completion or retry safety.
type BillingTaskPersistenceEvidence struct {
	RequestID    string     `json:"request_id"`
	TaskID       string     `json:"task_id"`
	Observation  string     `json:"observation"`
	BillingState string     `json:"billing_state,omitempty"`
	TaskStatus   TaskStatus `json:"task_status,omitempty"`
}

// InspectBillingTaskPersistence is an operator/internal SELECT-only probe. The
// caller must supply the exact original receipt, not today's token/subscription
// or a guessed held amount. Unlike ReadCommittedBillingTask, this never claims
// or locks an owner. Absence may race a still-running COMMIT and is therefore
// only not_observed. There is intentionally no public endpoint or retry action.
func InspectBillingTaskPersistence(ctx context.Context, receipt BillingFinalization, taskPublicID string) (BillingTaskPersistenceEvidence, error) {
	var evidence BillingTaskPersistenceEvidence
	if ctx == nil {
		return evidence, errors.New("task persistence reconciliation requires a context")
	}
	if err := receipt.validate(); err != nil {
		return evidence, err
	}
	if taskPublicID == "" || len(taskPublicID) > 191 {
		return evidence, errors.New("invalid billing task destination")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var owner BillingFinalization
	if err := DB.WithContext(ctx).Where("request_id = ?", receipt.RequestID).First(&owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return BillingTaskPersistenceEvidence{RequestID: receipt.RequestID, TaskID: taskPublicID, Observation: "not_observed"}, nil
		}
		return evidence, err
	}
	if !sameBillingIdentity(owner, receipt) {
		return evidence, errors.New("billing task reconciliation identity mismatch")
	}
	if owner.State != BillingTaskOwned {
		switch owner.State {
		case BillingRefundPending, BillingRefunded, BillingSettled:
			return BillingTaskPersistenceEvidence{RequestID: receipt.RequestID, TaskID: taskPublicID, Observation: "other_terminal_owner", BillingState: owner.State}, nil
		default:
			return evidence, errors.New("unknown billing terminal owner")
		}
	}
	if owner.TaskPublicID != taskPublicID || owner.TaskID <= 0 {
		return evidence, errors.New("billing task reconciliation destination mismatch")
	}
	var task Task
	if err := DB.WithContext(ctx).Select("id", "task_id", "user_id", "private_data", "status").First(&task, owner.TaskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return BillingTaskPersistenceEvidence{RequestID: receipt.RequestID, TaskID: taskPublicID, Observation: "task_not_observed", BillingState: owner.State}, nil
		}
		return evidence, err
	}
	if task.TaskID != taskPublicID || !billingTaskIdentityMatches(&owner, &task) {
		return evidence, errors.New("billing task reconciliation identity mismatch")
	}
	switch task.Status {
	case TaskStatusNotStart, TaskStatusSubmitted, TaskStatusQueued, TaskStatusInProgress, TaskStatusFailure, TaskStatusSuccess, TaskStatusUnknown:
	default:
		task.Status = TaskStatusUnknown
	}
	return BillingTaskPersistenceEvidence{RequestID: receipt.RequestID, TaskID: taskPublicID, Observation: "committed", BillingState: owner.State, TaskStatus: task.Status}, nil
}
