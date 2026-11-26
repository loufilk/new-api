package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogCleanupSystemTaskReceiptReplaysOnlyOriginalCutoff(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
	firstID := "systask_" + uuid.NewString()
	differentID := "systask_" + uuid.NewString()
	start := func(query string) (channelAPIResponse, string) {
		return callChannelAPI(t, CreateLogCleanupSystemTaskWithReceipt, http.MethodPost, "/api/system-task/log-cleanup/receipt-v1?"+query, "")
	}
	bad, _ := start("target_timestamp=100&task_id=bad")
	assert.False(t, bad.Success)
	missing, _ := start("target_timestamp=100")
	assert.False(t, missing.Success)
	overflow, _ := start("target_timestamp=9223372036854775808&task_id=" + firstID)
	assert.False(t, overflow.Success)
	first, raw := start("target_timestamp=100&task_id=" + firstID)
	require.True(t, first.Success, raw)
	assert.Contains(t, string(first.Data), firstID)
	rejected, conflict := start("target_timestamp=200&task_id=" + firstID)
	assert.False(t, rejected.Success)
	assert.Contains(t, conflict, "log_cleanup_task_id_conflict")

	another, raw := start("target_timestamp=200&task_id=" + differentID)
	require.False(t, another.Success, raw)
	assert.Contains(t, raw, "log_cleanup_already_running")
	assert.Contains(t, string(another.Data), firstID)
	assert.NotContains(t, string(another.Data), differentID)

	var saved *model.SystemTask
	saved, err := model.GetSystemTaskByTaskID(firstID)
	require.NoError(t, err)
	_, claimed, err := model.ClaimSystemTask(saved.ID, model.SystemTaskTypeLogCleanup, "fixture", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(firstID, "fixture", model.SystemTaskStatusSucceeded, nil, ""))
	found, raw := callChannelAPI(t, GetSystemTask, http.MethodGet, "/api/system-task/"+firstID, "", gin.Param{Key: "task_id", Value: firstID})
	require.True(t, found.Success, raw)
	assert.Contains(t, string(found.Data), `"status":"succeeded"`)
	assert.Contains(t, string(found.Data), `"target_timestamp":100`)
	replayed, raw := start("target_timestamp=100&task_id=" + firstID)
	require.True(t, replayed.Success, raw)
	assert.Contains(t, string(replayed.Data), `"status":"succeeded"`)
	var total int64
	require.NoError(t, db.Model(&model.SystemTask{}).Count(&total).Error)
	assert.EqualValues(t, 1, total)
}

// The previous router had just the legacy destructive endpoint. A new
// client hitting that router cannot cause it to create work without a receipt.
func TestLogCleanupSystemTaskNewPathIsNotTheLegacyWrite(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
	old := gin.New()
	old.POST("/api/system-task/log-cleanup", CreateLogCleanupSystemTask)
	id := "systask_" + uuid.NewString()
	recorder := httptest.NewRecorder()
	old.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/system-task/log-cleanup/receipt-v1?target_timestamp=100&task_id="+id, nil))
	assert.Equal(t, http.StatusNotFound, recorder.Code)
	var total int64
	require.NoError(t, db.Model(&model.SystemTask{}).Count(&total).Error)
	assert.Zero(t, total)
	legacy, raw := callChannelAPI(t, CreateLogCleanupSystemTask, http.MethodPost, "/api/system-task/log-cleanup?target_timestamp=100", "")
	require.True(t, legacy.Success, raw)
	require.NoError(t, db.Model(&model.SystemTask{}).Count(&total).Error)
	assert.EqualValues(t, 1, total)
}

func TestReadCurrentChannelBalanceSystemTaskDoesNotQueueOrExposeAnotherType(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
	other, err := model.CreateSystemTask(model.SystemTaskTypeLogCleanup, nil, nil)
	require.NoError(t, err)
	read := func() (channelAPIResponse, string) {
		return callChannelAPI(t, GetCurrentChannelBalanceTask, http.MethodGet, "/api/channel/update_balance/task/current", "")
	}
	empty, raw := read()
	require.True(t, empty.Success, raw)
	assert.NotContains(t, raw, other.TaskID)
	assert.Equal(t, "null", string(empty.Data))
	balance, err := model.CreateSystemTask(model.SystemTaskTypeChannelBalance, nil, nil)
	require.NoError(t, err)
	response, raw := read()
	require.True(t, response.Success, raw)
	assert.Contains(t, raw, balance.TaskID)
	assert.NotContains(t, raw, other.TaskID)
	var total int64
	require.NoError(t, db.Model(&model.SystemTask{}).Count(&total).Error)
	assert.EqualValues(t, 2, total)
}
