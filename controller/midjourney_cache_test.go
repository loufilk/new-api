package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMidjourneyPollingDefersStaleChannelCache(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Midjourney{}, &model.Channel{})
	oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, common.MemoryCacheEnabled = db, false
	common.SetDatabaseTypes(dialect, oldLog)
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled = oldDB, oldMemory
		common.SetDatabaseTypes(oldMain, oldLog)
	})
	service.InitHttpClient()
	var calls atomic.Int32
	now := time.Now().UnixMilli()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/mj/task/list-by-condition", r.URL.Path)
		body, err := common.Marshal([]dto.MidjourneyDto{{MjId: "cache-task", Status: "IN_PROGRESS", Progress: "50%", SubmitTime: now}})
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(upstream.Close)
	channel := &model.Channel{Key: "test-key", Name: "cache-fixture", BaseURL: &upstream.URL, Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(channel).Error)
	task := &model.Midjourney{MjId: "cache-task", UserId: 7, ChannelId: channel.Id, Status: "IN_PROGRESS", Progress: "30%", SubmitTime: now, Quota: 1400}
	require.NoError(t, db.Create(task).Error)
	const hook = "test:midjourney-stale-cache"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Channel" {
			tx.AddError(fmt.Errorf("lookup interrupted: %w", model.ErrChannelCacheStale))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(hook) })
	for range 2 {
		summary := runMidjourneyTaskUpdateOnce(context.Background(), nil)
		assert.Equal(t, 1, summary.UnfinishedTasks)
		assert.Zero(t, summary.NullTasksFailed)
	}
	var stored model.Midjourney
	require.NoError(t, db.First(&stored, task.Id).Error)
	assert.Equal(t, "IN_PROGRESS", stored.Status)
	assert.Equal(t, "30%", stored.Progress)
	assert.Equal(t, 1400, stored.Quota)
	assert.Empty(t, stored.FailReason)
	assert.Nil(t, stored.WalletDebit, "legacy receipt provenance must remain unknown")
	assert.False(t, stored.RequiresFinancialReview())
	assert.Zero(t, stored.FinishTime)
	assert.Zero(t, calls.Load())
	require.NoError(t, db.Callback().Query().Remove(hook))
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	require.NoError(t, db.First(&stored, task.Id).Error)
	assert.Equal(t, "IN_PROGRESS", stored.Status)
	assert.Equal(t, "50%", stored.Progress)
	assert.Equal(t, 1400, stored.Quota)
	assert.Nil(t, stored.WalletDebit)
	assert.Empty(t, stored.FailReason)
	assert.False(t, stored.RequiresFinancialReview())
	assert.EqualValues(t, 1, calls.Load(), "the existing upstream task resumes after recovery")
	require.NoError(t, db.Delete(channel).Error)
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	require.NoError(t, db.First(&stored, task.Id).Error)
	assert.Equal(t, "UNKNOWN", stored.Status, "missing channel metadata does not prove upstream failure")
	assert.Equal(t, "50%", stored.Progress)
	assert.Equal(t, 1400, stored.Quota)
	assert.Nil(t, stored.WalletDebit, "missing metadata cannot reconstruct a refund receipt")
	assert.Equal(t, "Missing channel metadata; upstream delivery requires operator review", stored.FailReason)
	assert.True(t, stored.RequiresFinancialReview())
	assert.Zero(t, stored.FinishTime)
	assert.EqualValues(t, 1, calls.Load())
	beforeReview := stored

	// Restoring local metadata alone must not resume a task awaiting operator review.
	require.NoError(t, db.Create(channel).Error)
	assert.False(t, model.HasUnfinishedMidjourneyTasks())
	assert.Empty(t, model.GetAllUnFinishTasks())
	for range 2 {
		summary := runMidjourneyTaskUpdateOnce(context.Background(), nil)
		assert.Zero(t, summary.UnfinishedTasks)
		assert.Zero(t, summary.ChannelsScanned)
		assert.Zero(t, summary.NullTasksFailed)
	}
	require.NoError(t, db.First(&stored, task.Id).Error)
	assert.Equal(t, beforeReview, stored)
	assert.EqualValues(t, 1, calls.Load(), "reviewed tasks must not be polled automatically again")
}
