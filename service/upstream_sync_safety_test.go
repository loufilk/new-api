package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
)

func TestRelayErrorSourcePreservesClientDisconnect(t *testing.T) {
	assert.Equal(t, "local", RelayErrorSource(nil))
	disconnected := types.NewError(errors.New("client disconnected"), ClientGoneErrorCode)
	assert.Equal(t, "client", RelayErrorSource(disconnected))
	wrapped := types.NewError(disconnected, types.ErrorCodeInvalidRequest)
	assert.Equal(t, "client", RelayErrorSource(wrapped))
}

func TestTaskPluginReportRequiresAcknowledgedSettlement(t *testing.T) {
	previous := ReportTaskFunc
	t.Cleanup(func() { ReportTaskFunc = previous })
	reports := 0
	ReportTaskFunc = func(*model.Task) { reports++ }
	reportFinalTask(nil)
	task := &model.Task{Status: model.TaskStatusSuccess}
	task.PrivateData.RequestPlugins = &model.TaskRequestPlugins{}
	reportFinalTask(task)
	assert.Zero(t, reports, "unsettled task must not claim a final cost")
	task.PrivateData.FinancialOperationID = "task:financial:1:settle:10"
	task.SettlementPending = true
	reportFinalTask(task)
	assert.Zero(t, reports, "pending financial intent must not be reported")
	task.SettlementPending = false
	task.PrivateData.FinancialReviewReason = "uncertain commit"
	reportFinalTask(task)
	assert.Zero(t, reports, "review task must not be reported")
	task.PrivateData.FinancialReviewReason = ""
	reportFinalTask(task)
	assert.Equal(t, 1, reports)
}
