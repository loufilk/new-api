package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelFingerprintRetentionRegisteredWithoutFeatureAvailability(t *testing.T) {
	var registered SystemTaskHandler
	for _, handler := range registeredSystemTaskHandlers() {
		if handler.Type() == "model_fingerprint_retention" {
			require.Nil(t, registered, "only one retention handler may be registered")
			registered = handler
		}
	}
	require.NotNil(t, registered, "retention must start without registering a probe executor")
	scheduled, ok := registered.(ScheduledSystemTaskHandler)
	require.True(t, ok)
	assert.Equal(t, 5*time.Minute, scheduled.Interval())
	assert.Equal(t, struct{}{}, scheduled.NewPayload())

	// Enabling cleanup must not require a database read or the billable feature's
	// enabled flag; it is also eligible while the feature configuration is absent.
	savedDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = savedDB })
	assert.True(t, scheduled.Enabled())
}

func TestModelFingerprintRetentionSchedulerDeduplicatesAndWaitsFiveMinutes(t *testing.T) {
	truncate(t)
	handler := modelFingerprintRetentionHandler{}
	withSystemTaskRegistry(t, handler)
	runSystemTaskScheduler()
	require.EqualValues(t, 1, countSystemTasks(t, handler.Type()))
	runSystemTaskScheduler()
	require.EqualValues(t, 1, countSystemTasks(t, handler.Type()), "pending cleanup must not be duplicated")

	latest, err := model.GetLatestSystemTask(handler.Type())
	require.NoError(t, err)
	require.NotNil(t, latest)
	_, claimed, err := model.ClaimSystemTask(latest.ID, handler.Type(), "retention-test", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(latest.TaskID, "retention-test", model.SystemTaskStatusSucceeded, nil, ""))
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("task_id = ?", latest.TaskID).Update("updated_at", common.GetTimestamp()-240).Error)
	runSystemTaskScheduler()
	assert.EqualValues(t, 1, countSystemTasks(t, handler.Type()), "cleanup is not due at four minutes")
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("task_id = ?", latest.TaskID).Update("updated_at", common.GetTimestamp()-360).Error)
	runSystemTaskScheduler()
	assert.EqualValues(t, 2, countSystemTasks(t, handler.Type()), "cleanup becomes due after five minutes")
}

func TestModelFingerprintRetentionCancellationFinishesClaimedTask(t *testing.T) {
	truncate(t)
	handler := modelFingerprintRetentionHandler{}
	task, err := model.CreateSystemTask(handler.Type(), handler.NewPayload(), nil)
	require.NoError(t, err)
	require.NotNil(t, task)
	claimedTask, claimed, err := model.ClaimSystemTask(task.ID, handler.Type(), "retention-cancel-test", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	handler.Run(ctx, claimedTask, "retention-cancel-test")
	finished, err := model.GetLatestSystemTask(handler.Type())
	require.NoError(t, err)
	require.NotNil(t, finished)
	assert.Equal(t, task.TaskID, finished.TaskID)
	assert.Equal(t, model.SystemTaskStatusFailed, finished.Status)
	assert.EqualValues(t, 1, countSystemTasks(t, handler.Type()), "cancellation must not create another task")
}
