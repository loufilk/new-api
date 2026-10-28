package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type taskSubmissionTestBilling struct {
	events     *[]string
	settleErr  error
	reserveErr error
	onSettle   func()
	refunds    int
}

func (b *taskSubmissionTestBilling) Settle(int) error {
	*b.events = append(*b.events, "settle")
	if b.onSettle != nil {
		b.onSettle()
	}
	return b.settleErr
}

func (b *taskSubmissionTestBilling) Refund(*gin.Context) {
	*b.events = append(*b.events, "refund")
	b.refunds++
}

func (b *taskSubmissionTestBilling) NeedsRefund() bool        { return b.refunds == 0 }
func (b *taskSubmissionTestBilling) GetPreConsumedQuota() int { return 0 }
func (b *taskSubmissionTestBilling) Reserve(int) error {
	*b.events = append(*b.events, "reserve")
	return b.reserveErr
}

func (b *taskSubmissionTestBilling) ReserveBeforeSubmit(quota int) error { return b.Reserve(quota) }

func TestPresentTaskSubmissionUsesNativePresenterAfterPersistence(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"presenter-test",name:"Presenter",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {data:{task_id:task.task_id},upstream:task.data};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	priceData := types.PriceData{}
	priceData.AddOtherRatio("seconds", 5)
	task := &model.Task{TaskID: "task_public", SubmitTime: 123}
	task.SetData(map[string]any{"task_id": "upstream_private"})
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      task,
		RelayInfo: &relaycommon.RelayInfo{PriceData: priceData},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
	c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
	c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"data":{"task_id":"task_public"},
		"upstream":{"task_id":"upstream_private"}
	}`, recorder.Body.String())
	assert.JSONEq(t, `{"seconds":5}`, recorder.Header().Get("X-New-Api-Other-Ratios"))
}

func TestPresentTaskSubmissionFallbackUsesPersistedPublicID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      &model.Task{TaskID: "task_persisted", SubmitTime: 456},
		RelayInfo: &relaycommon.RelayInfo{OriginModelName: "video-model"},
	}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"id":"task_persisted",
		"task_id":"task_persisted",
		"status":"queued",
		"model":"video-model",
		"created_at":456
	}`, recorder.Body.String())
}

func TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{
		Protocol:  "openai_video",
		Operation: pluginruntime.HostProtocolOperation{Name: "create"},
	})
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSubmitted,
		Progress:   "0%",
		CreatedAt:  456,
		Properties: model.Properties{OriginModelName: "video-model"},
	}
	outcome := &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: task, RelayInfo: &relaycommon.RelayInfo{}}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{"id":"task_public","object":"video","model":"video-model","status":"queued","progress":0,"created_at":456}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "task_id")
}

func TestExecuteTaskSubmissionPreservesHoldWhenAcceptedInsertFails(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "task-insert-failure", 1000, 100)
	require.NoError(t, f.db.Migrator().DropTable(&model.Task{}))
	taskTable := f.db.NamingStrategy.TableName("tasks")
	var insertAttempts int
	var insertErr error
	var insertSQL string
	require.NoError(t, f.db.Callback().Create().After("gorm:create").Before("gorm:after_create").Register("test:missing_task_table", func(tx *gorm.DB) {
		if tx.Statement.Table == taskTable {
			insertAttempts++
			insertErr = tx.Error
			insertSQL = tx.Statement.SQL.String()
		}
	}))
	t.Cleanup(func() { _ = f.db.Callback().Create().Remove("test:missing_task_table") })
	calls := 0
	outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		calls++
		return &relay.TaskSubmitResult{UpstreamTaskID: "upstream-accepted", Platform: "task_plugin", Quota: 100}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	require.Equal(t, "task_insert_failed", taskErr.Code)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, insertAttempts, "the actual Task INSERT must reach the missing table")
	require.Error(t, insertErr)
	require.Contains(t, strings.ToUpper(insertSQL), "INSERT INTO")
	require.False(t, f.db.Migrator().HasTable(&model.Task{}))
	f.assertFinancial(t, model.BillingDeliveryUnknown, 100, 0, 0, 1)
	f.assertUnknownCannotRefund(t)
	require.False(t, f.c.Writer.Written())
}

func TestExecuteTaskSubmissionRollsBackInsertedTaskWhenHandoffFails(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "task-handoff-failure", 1000, 100)
	hits := installTaskPostInsertFailure(t, f.db, f.info.UserId, f.info.TaskRelayInfo.PublicTaskID)
	calls := 0
	outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		calls++
		// This quote would return half the original hold. The injected error must
		// roll that source/token adjustment back together with the real INSERT.
		return &relay.TaskSubmitResult{UpstreamTaskID: "upstream-accepted", Platform: "task_plugin", Quota: 50}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	require.Equal(t, "task_insert_failed", taskErr.Code)
	require.Equal(t, int32(1), hits.Load())
	require.Equal(t, 1, calls)
	f.assertFinancial(t, model.BillingDeliveryUnknown, 100, 0, 0, 1)
	f.assertTaskCount(t, 0)
	f.assertUnknownCannotRefund(t)
	require.False(t, f.c.Writer.Written())
}

func TestExecuteTaskSubmissionRejectsUnsupportedAtomicBilling(t *testing.T) {
	events := make([]string, 0, 3)
	db := setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{UpstreamTaskID: "accepted-extension", Platform: "task_plugin", Quota: 100}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	require.Equal(t, "task_insert_failed", taskErr.Code)
	require.ErrorContains(t, service.PersistTaskBilling(info, &model.Task{Quota: 100}), "task funding does not support an atomic handoff")
	require.Equal(t, []string{"reserve"}, events)
	require.Zero(t, billing.refunds, "accepted delivery cannot fall back to a blind extension refund")
	var count int64
	require.NoError(t, db.Model(&model.Task{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&model.BillingFinalization{}).Count(&count).Error)
	require.Zero(t, count)
	require.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionPersistsPinnedPluginProvenance(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "request-public", 1000, 100)
	database := f.db
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

	c := f.c
	c.Set(common.RequestIdKey, "request-public")
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{
		Generation: &pluginruntime.RoutingGeneration{Number: 42},
		Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{
			Key:        "document-parser",
			Name:       "Document Parser",
			Version:    "1.2.3",
			APIVersion: 1,
			Author: pluginruntime.AuthorMeta{
				Name: "Community Author",
				URL:  "https://plugins.example/author",
			},
		}},
	})
	info := f.info

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream-private",
			Quota:          100,
			Platform:       constant.TaskPlatform("document-parser"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NotNil(t, outcome.Task.PrivateData.Execution)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "request-public", outcome.Task.PrivateData.Execution.RequestID)
	assert.Equal(t, "/plugin/submit", outcome.Task.PrivateData.Execution.RequestPath)
	assert.Equal(t, "1.2.3", outcome.Task.PrivateData.Execution.TaskPlugin.Version)
	assert.Equal(t, uint64(42), outcome.Task.PrivateData.Execution.TaskPlugin.Generation)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.URL)

	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.Execution)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "document-parser", stored.PrivateData.Execution.TaskPlugin.Key)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", stored.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "upstream-private", stored.PrivateData.UpstreamTaskID)
	f.assertFinancial(t, model.BillingTaskOwned, 100, 100, 1, 1)
}

func TestExecuteTaskSubmissionPersistsExecutionOnlyRequestID(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "request-execution-only", 1000, 100)
	database := f.db
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

	c := f.c
	c.Set(common.RequestIdKey, "request-execution-only")
	info := f.info
	info.RequestId = "request-execution-only"

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream-execution-only",
			Quota:          100,
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NotNil(t, outcome.Task.PrivateData.Execution)
	assert.Equal(t, "request-execution-only", outcome.Task.PrivateData.Execution.RequestID)
	assert.Empty(t, outcome.Task.PrivateData.RequestId)
	assert.Equal(t, "request-execution-only", outcome.Task.PrivateData.EffectiveRequestID())

	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.Execution)
	assert.Equal(t, "request-execution-only", stored.PrivateData.Execution.RequestID)
	assert.Empty(t, stored.PrivateData.RequestId)
	assert.Equal(t, "request-execution-only", stored.PrivateData.EffectiveRequestID())
	f.assertFinancial(t, model.BillingTaskOwned, 100, 100, 1, 1)
}

// A submit route declaring retainResult: false persists the task row for
// billing but never writes the upstream snapshot for an immediate terminal
// result, while the in-memory task still carries it for the presenter. An
// asynchronous result on the same route is retained because polling and
// retrieval depend on it.
func TestExecuteTaskSubmissionHonorsRouteRetainResult(t *testing.T) {
	retainFalse := false
	for _, tc := range []struct {
		name          string
		immediate     *relaycommon.TaskInfo
		wantDiscarded bool
	}{
		{"immediate success is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, Progress: "100%"}, true},
		{"immediate failure is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "rejected"}, true},
		{"asynchronous result is retained", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTaskSubmissionFinancialFixture(t, "task-retain-"+tc.name, 1000, 100)
			database := f.db
			c := f.c
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
				Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{Key: "sync-images"}},
				Route:  pluginruntime.Route{Method: http.MethodPost, Path: "/sync/images", Type: pluginruntime.RouteTypeSubmit, RetainResult: &retainFalse},
			})
			info := f.info
			actual := 100
			if tc.immediate != nil && tc.immediate.Status == model.TaskStatusFailure {
				actual = 0
			}
			info.PriceData.Quota = actual
			upstream := []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)

			outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				return &relay.TaskSubmitResult{
					UpstreamTaskID: "task_public",
					Platform:       constant.TaskPlatform("sync-images"),
					TaskData:       upstream,
					Quota:          actual,
					Immediate:      tc.immediate,
				}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.JSONEq(t, string(upstream), string(outcome.Task.Data), "presenter keeps the in-memory snapshot")
			assert.Equal(t, tc.wantDiscarded, outcome.Task.PrivateData.ResultDiscarded)
			assert.Equal(t, !tc.wantDiscarded, outcome.Task.ResultRetrievable())

			var stored model.Task
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			assert.Equal(t, tc.wantDiscarded, stored.PrivateData.ResultDiscarded)
			var nullCount int64
			require.NoError(t, database.Model(&model.Task{}).Where("task_id = ? AND data IS NULL", "task_public").Count(&nullCount).Error)
			if tc.wantDiscarded {
				assert.Equal(t, int64(1), nullCount, "snapshot column must stay NULL")
				artifacts, err := projectTaskArtifacts(&stored)
				require.NoError(t, err)
				assert.Empty(t, artifacts)
			} else {
				assert.Equal(t, int64(0), nullCount)
				assert.JSONEq(t, string(upstream), string(stored.Data))
			}
			listed := model.TaskGetAllUserTask(info.UserId, 0, 10, model.SyncTaskQueryParams{})
			require.Len(t, listed, 1)
			assert.Empty(t, listed[0].Data, "task lists never select the snapshot column")
			ledgerCount := 1
			if actual == 0 {
				ledgerCount = 2
			}
			f.assertFinancial(t, model.BillingTaskOwned, actual, actual, 1, ledgerCount)
		})
	}
}

func TestExecuteTaskSubmissionPreservesHoldOnCancellationAfterUpstreamAcceptance(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "task-cancel-accepted", 1000, 100)
	requestContext, cancel := context.WithCancel(f.c.Request.Context())
	t.Cleanup(cancel)
	f.c.Request = f.c.Request.WithContext(requestContext)
	calls := 0
	outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		calls++
		cancel()
		return &relay.TaskSubmitResult{UpstreamTaskID: "upstream-accepted", Platform: "task_plugin", Quota: 100}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	require.Equal(t, "request_cancelled", taskErr.Code)
	require.Equal(t, http.StatusRequestTimeout, taskErr.StatusCode)
	require.Equal(t, 1, calls)
	f.assertFinancial(t, model.BillingDeliveryUnknown, 100, 0, 0, 1)
	f.assertTaskCount(t, 0)
	f.assertUnknownCannotRefund(t)
	require.False(t, f.c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "task-cancel-before-submit", 1000, 100)
	requestContext, cancel := context.WithCancel(f.c.Request.Context())
	cancel()
	f.c.Request = f.c.Request.WithContext(requestContext)
	submitCalled := false
	outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		submitCalled = true
		return &relay.TaskSubmitResult{UpstreamTaskID: "upstream", Platform: "task_plugin", Quota: 100}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	require.Equal(t, "request_cancelled", taskErr.Code)
	require.False(t, submitCalled)
	f.assertFinancial(t, model.BillingRefunded, 0, 0, 0, 2)
	f.assertTaskCount(t, 0)
	require.False(t, f.c.Writer.Written())
}

func TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "task-cancel-unaccepted", 1000, 100)
	requestContext, cancel := context.WithCancel(f.c.Request.Context())
	f.c.Request = f.c.Request.WithContext(requestContext)
	submitStarted := make(chan struct{})
	finished := make(chan struct{})
	var outcome *taskSubmissionOutcome
	var taskErr *dto.TaskError
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(8 * time.Second):
			fmt.Fprintf(os.Stderr, "task submission cleanup failed: %s caller-cancellation goroutine unjoined; refusing DB/global restoration\n", t.Name())
			os.Exit(1)
		}
	})
	go func() {
		defer close(finished)
		outcome, taskErr = executeTaskSubmissionWith(f.c, f.info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			close(submitStarted)
			<-c.Request.Context().Done()
			// This adapter explicitly reports no accepted result / no unknown delivery.
			return nil, service.TaskErrorWrapperLocal(c.Request.Context().Err(), "do_request_failed", http.StatusRequestTimeout)
		})
	}()
	select {
	case <-submitStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("submit did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not stop promptly after caller cancellation")
	}
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	require.Equal(t, "request_cancelled", taskErr.Code)
	require.Equal(t, http.StatusRequestTimeout, taskErr.StatusCode)
	f.assertFinancial(t, model.BillingRefunded, 0, 0, 0, 2)
	f.assertTaskCount(t, 0)
	require.False(t, f.c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "task-cancel-owned", 1000, 100)
	requestContext, cancel := context.WithCancel(f.c.Request.Context())
	t.Cleanup(cancel)
	f.c.Request = f.c.Request.WithContext(requestContext)
	ownerTable := f.db.NamingStrategy.TableName("billing_finalizations")
	hits := 0
	require.NoError(t, f.db.Callback().Update().After("gorm:update").Before("gorm:after_update").Register("test:cancel_after_task_owner", func(tx *gorm.DB) {
		values, ok := tx.Statement.Dest.(map[string]interface{})
		if tx.Statement.Table == ownerTable && ok && values["state"] == model.BillingTaskOwned && tx.Error == nil && tx.RowsAffected == 1 {
			hits++
			cancel() // The financial transaction owns a background context, not the socket.
		}
	}))
	t.Cleanup(func() { _ = f.db.Callback().Update().Remove("test:cancel_after_task_owner") })
	outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{UpstreamTaskID: "upstream", Platform: "task_plugin", Quota: 100}, nil
	})
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.Equal(t, 1, hits)
	require.ErrorIs(t, requestContext.Err(), context.Canceled)
	require.Equal(t, "task_public", outcome.Task.TaskID)
	f.assertFinancial(t, model.BillingTaskOwned, 100, 100, 1, 1)
	f.assertTaskCount(t, 1)
	require.ErrorIs(t, f.info.Billing.Settle(100), model.ErrBillingFinalized)
	f.info.Billing.Refund(f.c)
	require.NoError(t, service.PersistTaskBilling(f.info, outcome.Task))
	f.assertFinancial(t, model.BillingTaskOwned, 100, 100, 1, 1)
	require.False(t, f.c.Writer.Written())
}

// A real authorization is required before the controller can hand off a paid
// task. The mock BillingSettler remains only for its explicit capability-negative
// control and unrelated presentation-only tests in this package.
type taskSubmissionFinancialFixture struct {
	db            *gorm.DB
	c             *gin.Context
	info          *relaycommon.RelayInfo
	user          model.User
	token         model.Token
	channel       model.Channel
	initial, held int
}

func newTaskSubmissionFinancialFixture(t *testing.T, requestID string, initial, held int) *taskSubmissionFinancialFixture {
	t.Helper()
	return newTaskSubmissionFinancialFixtureForModel(t, requestID, initial, held, "plugin-model")
}

func newTaskSubmissionFinancialFixtureForModel(t *testing.T, requestID string, initial, held int, modelName string) *taskSubmissionFinancialFixture {
	t.Helper()
	// This serial fixture disables notification/cache/export producers below.
	// Establish an idle pool before changing any global DB or cache state.
	require.Zero(t, gopool.WorkerCount(), "closed task fixture requires an initially idle pool")
	require.NotEmpty(t, modelName)
	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	if err := service.ConfigureFundingFactoryRegistry(registry); err != nil && !strings.Contains(err.Error(), "already configured") {
		t.Fatal(err)
	}
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.Model{}, &model.Ability{}, &model.Vendor{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldDBType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldCache, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	oldLog, oldExport := common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	common.LogConsumeEnabled, common.DataExportEnabled = true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldDBType, oldLogType)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldCache, oldBatch
		common.LogConsumeEnabled, common.DataExportEnabled = oldLog, oldExport
		model.InvalidatePricingCache()
		model.InvalidateModelBonusEligibleCache()
	})
	f := &taskSubmissionFinancialFixture{db: db, c: taskSubmissionTestContext(), initial: initial, held: held}
	f.user = model.User{Username: "task-fixture", AffCode: "task-fixture", Quota: initial, QuotaPaid: initial, Group: "default"}
	require.NoError(t, db.Create(&f.user).Error)
	f.token = model.Token{UserId: f.user.Id, Key: "task-fixture-original-token", Name: "task-fixture", Status: common.TokenStatusEnabled, RemainQuota: initial}
	require.NoError(t, db.Create(&f.token).Error)
	f.channel = model.Channel{Name: "task-fixture", Type: constant.ChannelTypeTaskPlugin}
	require.NoError(t, db.Create(&f.channel).Error)
	f.info = taskSubmissionRelayInfo(nil)
	f.info.OriginModelName = modelName
	f.info.UserId, f.info.UserQuota = f.user.Id, initial
	f.info.TokenId, f.info.TokenKey = f.token.Id, f.token.Key
	f.info.RequestId, f.info.ForcePreConsume = requestID, true
	f.info.UserSetting.BillingPreference = "wallet_only"
	f.info.UserSetting.QuotaWarningEnabled = common.GetPointer(false)
	f.info.ChannelId = f.channel.Id
	f.info.TaskRelayInfo.LockedChannel = &f.channel
	f.info.PriceData.Quota = held
	f.info.PriceData.GroupRatioInfo.GroupRatio = 1
	f.c.Set(common.RequestIdKey, requestID)
	f.c.Set("token_name", f.token.Name)
	require.NoError(t, db.Create(&model.Model{ModelName: f.info.OriginModelName, BonusEligible: common.GetPointer(false), Status: 1}).Error)
	model.InvalidatePricingCache()
	model.InvalidateModelBonusEligibleCache()
	session, apiErr := service.NewBillingSession(f.c, f.info, held)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	f.info.Billing = session
	t.Cleanup(func() {
		waitTaskSubmissionFixtureQuiescence(t)
	})
	f.assertFinancial(t, model.BillingHeld, held, 0, 0, 1)
	return f
}

// This proves closed-fixture quiescence, not the identity of an individual
// production worker. No other producer is admitted after the initial pool=0
// check; tests are serial and notification/cache/export producers are disabled.
// A refund may use its 5 s transaction plus a separate 1 s metadata tail. On
// failure do not let Go continue later Cleanups against a still-live worker.
func waitTaskSubmissionFixtureQuiescence(t *testing.T) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if gopool.WorkerCount() == 0 {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			fmt.Fprintf(os.Stderr, "task submission cleanup failed: %s closed-fixture pool still has %d worker(s); refusing DB/global restoration\n", t.Name(), gopool.WorkerCount())
			os.Exit(1)
		}
	}
}

func (f *taskSubmissionFinancialFixture) assertFinancial(t *testing.T, state string, charge, used, requests, ledgerCount int) {
	t.Helper()
	if state == model.BillingRefunded {
		waitTaskSubmissionFixtureQuiescence(t)
	}
	var user model.User
	var token model.Token
	var channel model.Channel
	var owner model.BillingFinalization
	require.NoError(t, f.db.First(&user, f.user.Id).Error)
	require.NoError(t, f.db.First(&token, f.token.Id).Error)
	require.NoError(t, f.db.First(&channel, f.channel.Id).Error)
	require.NoError(t, f.db.Where("request_id = ?", f.info.RequestId).First(&owner).Error)
	require.Equal(t, f.initial-charge, user.Quota)
	require.Equal(t, f.initial-charge, user.QuotaPaid)
	require.Zero(t, user.QuotaBonus)
	require.Zero(t, user.QuotaReward)
	require.Equal(t, used, user.UsedQuota)
	require.Equal(t, requests, user.RequestCount)
	require.EqualValues(t, used, channel.UsedQuota)
	require.Equal(t, f.initial-charge, token.RemainQuota)
	require.Equal(t, charge, token.UsedQuota)
	require.Equal(t, state, owner.State)
	require.Equal(t, f.user.Id, owner.UserID)
	require.Equal(t, service.BillingSourceWallet, owner.Source)
	require.Equal(t, f.token.Id, owner.TokenID)
	require.Equal(t, model.BillingTokenFingerprint(f.token.Key), owner.TokenFingerprint)
	require.Equal(t, f.held, owner.PreConsumed)
	require.Equal(t, f.held, owner.SourceHeld)
	require.Equal(t, f.held, owner.TokenHeld)
	if state == model.BillingDeliveryUnknown {
		require.Equal(t, "upstream_delivery_unknown", owner.LastErrorCode)
		require.Equal(t, "review_required", owner.LastErrorClass)
		require.Zero(t, owner.TaskID)
		require.Zero(t, owner.ActualQuota)
	}
	if state == model.BillingTaskOwned {
		require.Equal(t, charge, owner.ActualQuota)
		require.Positive(t, owner.TaskID)
		require.Equal(t, f.info.TaskRelayInfo.PublicTaskID, owner.TaskPublicID)
	}
	var ledger []model.BalanceLedger
	require.NoError(t, f.db.Where("user_id = ? AND request_id = ?", f.user.Id, f.info.RequestId).Order("id").Find(&ledger).Error)
	require.Len(t, ledger, ledgerCount)
	net := 0
	for _, row := range ledger {
		net += row.Delta
	}
	require.Equal(t, -charge, net)
	var count int64
	require.NoError(t, f.db.Model(&model.BalanceLedgerOperation{}).Where("user_id = ?", f.user.Id).Count(&count).Error)
	require.EqualValues(t, ledgerCount, count)
	require.NoError(t, f.db.Model(&model.BillingFinalization{}).Where("user_id = ?", f.user.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, f.db.Model(&model.Log{}).Where("user_id = ? AND type = ?", f.user.Id, model.LogTypeConsume).Count(&count).Error)
	wantLogs := int64(0)
	if state == model.BillingTaskOwned && common.LogConsumeEnabled {
		wantLogs = 1
	}
	require.Equal(t, wantLogs, count)
}

func (f *taskSubmissionFinancialFixture) assertTaskCount(t *testing.T, want int64) {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Model(&model.Task{}).Where("user_id = ?", f.user.Id).Count(&count).Error)
	require.Equal(t, want, count)
}

func (f *taskSubmissionFinancialFixture) assertUnknownCannotRefund(t *testing.T) {
	t.Helper()
	require.False(t, f.info.Billing.NeedsRefund())
	require.ErrorIs(t, f.info.Billing.Settle(0), model.ErrBillingFinalized)
	f.info.Billing.Refund(f.c)
	f.info.Billing.Refund(f.c)
	f.assertFinancial(t, model.BillingDeliveryUnknown, f.held, 0, 0, 1)
}

// The callback runs after gorm:create has executed a real INSERT, on the same
// SQL transaction. A fresh statement prevents callback query state pollution.
// No external DB query is used while the one-connection fixture owns this Tx.
func installTaskPostInsertFailure(t *testing.T, db *gorm.DB, userID int, publicID string) *atomic.Int32 {
	t.Helper()
	hits := &atomic.Int32{}
	table := db.NamingStrategy.TableName("tasks")
	name := "test:task_post_insert_failure"
	require.NoError(t, db.Callback().Create().After("gorm:create").Before("gorm:after_create").Register(name, func(tx *gorm.DB) {
		task, ok := tx.Statement.Dest.(*model.Task)
		if !ok || tx.Statement.Table != table || task.UserId != userID || task.TaskID != publicID {
			return
		}
		if tx.Error != nil || tx.RowsAffected != 1 || task.ID <= 0 {
			return
		}
		var count int64
		err := tx.Session(&gorm.Session{NewDB: true}).Model(&model.Task{}).Where("id = ? AND user_id = ? AND task_id = ?", task.ID, userID, publicID).Count(&count).Error
		if err != nil {
			tx.AddError(err)
			return
		}
		if count != 1 {
			tx.AddError(errors.New("post-insert fixture did not observe its task"))
			return
		}
		var owner model.BillingFinalization
		if err := tx.Session(&gorm.Session{NewDB: true}).Where("request_id = ?", task.PrivateData.EffectiveRequestID()).First(&owner).Error; err != nil {
			tx.AddError(err)
			return
		}
		var user model.User
		if err := tx.Session(&gorm.Session{NewDB: true}).Select("id", "used_quota", "request_count").First(&user, userID).Error; err != nil {
			tx.AddError(err)
			return
		}
		if owner.State != model.BillingHeld || user.UsedQuota != 0 || user.RequestCount != 0 {
			tx.AddError(errors.New("post-insert fixture missed the pre-usage/pre-owner boundary"))
			return
		}
		hits.Add(1)
		tx.AddError(errors.New("forced failure after real task insert before usage and owner"))
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(name) })
	return hits
}

type taskSubmissionFinancialState struct {
	User       model.User
	Token      model.Token
	Channel    model.Channel
	Owners     []model.BillingFinalization
	Tasks      []model.Task
	Ledger     []model.BalanceLedger
	Operations []model.BalanceLedgerOperation
	Logs       []model.Log
}

func captureTaskSubmissionFinancialState(t *testing.T, db *gorm.DB, info *relaycommon.RelayInfo) taskSubmissionFinancialState {
	t.Helper()
	var state taskSubmissionFinancialState
	require.NoError(t, db.First(&state.User, info.UserId).Error)
	require.NoError(t, db.First(&state.Token, info.TokenId).Error)
	require.NoError(t, db.First(&state.Channel, info.ChannelId).Error)
	require.NoError(t, db.Where("user_id = ?", info.UserId).Order("request_id").Find(&state.Owners).Error)
	require.NoError(t, db.Where("user_id = ?", info.UserId).Order("id").Find(&state.Tasks).Error)
	require.NoError(t, db.Where("user_id = ?", info.UserId).Order("id").Find(&state.Ledger).Error)
	require.NoError(t, db.Where("user_id = ?", info.UserId).Order("id").Find(&state.Operations).Error)
	require.NoError(t, db.Where("user_id = ?", info.UserId).Order("id").Find(&state.Logs).Error)
	return state
}

func setupTaskSubmissionDatabase(t *testing.T, migrate bool, events *[]string) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	var models []any
	if migrate {
		models = append(models, &model.Task{})
	}
	database, _ := openTaskDialectDatabase(t, models...)
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register("test:task-submit-order", func(*gorm.DB) {
		*events = append(*events, "insert")
	}))
	model.DB = database
	t.Cleanup(func() { model.DB = previousDB })
	return database
}

func taskSubmissionTestContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/plugin/submit", strings.NewReader(`{}`))
	return c
}

func taskSubmissionRelayInfo(billing relaycommon.BillingSettler) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          1,
		UsingGroup:      "default",
		OriginModelName: "plugin-model",
		Billing:         billing,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:  "task_public",
			LockedChannel: &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "plugin"},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin},
	}
}

// Uses the real submit adaptor, expression evaluator, BillingSession, task row
// and consume log. Set TEST_TASK_DB_DIALECT plus TEST_MYSQL_DSN or
// TEST_POSTGRES_DSN to exercise the same contract on an external test database.
// Most fixture tables use unique prefixes; fixed-name ledger tables must be
// absent before migration, as checked below (never claim prefix isolation for them).
// openTaskDialectDatabase opens the engine selected by TEST_TASK_DB_DIALECT
// (default SQLite in memory; MySQL and PostgreSQL through TEST_MYSQL_DSN and
// TEST_POSTGRES_DSN) with a unique table prefix, migrates the given models and
// drops them on cleanup. It logs the engine version so database verification
// runs leave a record.
func openTaskDialectDatabase(t *testing.T, models ...any) (*gorm.DB, common.DatabaseType) {
	t.Helper()
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		driver = sqlite.Open(":memory:")
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("tsubmit_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	// Some ledger types implement TableName and intentionally bypass the prefix.
	// Never migrate/drop a pre-existing table from an arbitrary native DSN.
	for _, entry := range append(models, &model.BillingFinalization{}) {
		require.False(t, db.Migrator().HasTable(entry), "task fixture refuses an existing table")
	}
	require.NoError(t, db.AutoMigrate(append(models, &model.BillingFinalization{})...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(append(models, &model.BillingFinalization{})...)) })
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)
	return db, dialect
}

func TestImmediateTaskSettlementDatabase(t *testing.T) {
	require.Zero(t, gopool.WorkerCount(), "closed task fixture requires an initially idle pool")
	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	if err := service.ConfigureFundingFactoryRegistry(registry); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.Model{}, &model.Ability{}, &model.Vendor{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
		model.InvalidatePricingCache()
		model.InvalidateModelBonusEligibleCache()
	})
	require.NoError(t, db.Create(&model.Model{ModelName: "document-model", BonusEligible: common.GetPointer(false), Status: 1}).Error)
	model.InvalidatePricingCache()
	model.InvalidateModelBonusEligibleCache()

	const expression = `u("units") == 7 ? tier("missing", u("missing") * 1.0) : u("units") == 8 ? tier("invalid", -1.0) : u("units") > 3 ? tier("bulk", u("units") * 0.01) : tier("small", u("units") * 0.01)`
	withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{"document-model": expression})
	const source = `
export const meta={apiVersion:1,key:"generic-settlement",name:"Generic settlement",version:"1.0.0",author:{name:"Test"},models:["document-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/compile",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:{status:resp.body.status,reason:"provider rejected job"}};}
export function extractUsage(){return {units:4};}
export function extractUsageOnComplete(ctx,result,body){return body.usage;}
export function parseTaskResult(){throw new Error("completed submissions must not poll");}
export function buildQueryRequest(){throw new Error("completed submissions must not poll");}
`
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	for index, tc := range []struct {
		name, status      string
		actual            any
		count             float64
		settlementFailure bool
		shortage          bool
	}{
		{"partial", "SUCCESS", 2, 2, false, false}, {"zero", "SUCCESS", 0, 0, false, false}, {"larger", "SUCCESS", 6, 6, false, false},
		{"invalid usage", "SUCCESS", -1, 4, false, false}, {"expression failure", "SUCCESS", 7, 4, false, false}, {"negative result", "SUCCESS", 8, 4, false, false}, {"missing usage", "SUCCESS", nil, 4, false, false}, {"failed", "FAILURE", 9, 0, false, false},
		{"official_settlement_failure_after_insert", "SUCCESS", 2, 2, true, false},
		{"official_upward_shortage_before_insert", "SUCCESS", 6, 6, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"status": tc.status}
				if tc.actual != nil {
					body["usage"] = map[string]any{"units": tc.actual}
				}
				encoded, err := common.Marshal(body)
				if err != nil {
					panic(err)
				}
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			initial := int(20 * common.QuotaPerUnit)
			if tc.shortage {
				initial = common.QuotaRound(5 * 0.01 * common.QuotaPerUnit)
			}
			user := model.User{Username: fmt.Sprintf("task_user_%d", index), AffCode: fmt.Sprintf("task_aff_%d", index), Quota: initial, QuotaPaid: initial}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: fmt.Sprintf("immediate-original-token-%d", index), RemainQuota: initial, Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(&token).Error)
			ch := model.Channel{Name: "test provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&ch).Error)
			c := taskSubmissionTestContext()
			c.Set("group", "default")
			c.Set("username", user.Username)
			c.Set("task_request", map[string]any{"model": "document-model"})
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "document-model")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
			info := taskSubmissionRelayInfo(nil)
			info.UserId = user.Id
			info.OriginModelName = "document-model"
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.TokenId, info.TokenKey = token.Id, token.Key
			info.ChannelId = ch.Id
			info.UserQuota = initial
			info.UserSetting.QuotaWarningEnabled = common.GetPointer(false)
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID = model.GenerateTaskID()
			info.RequestId = fmt.Sprintf("task-request-%d", index)
			c.Set(common.RequestIdKey, info.RequestId)
			info.LockedChannel = &ch
			settlementFaultHits := &atomic.Int32{}
			if tc.settlementFailure {
				settlementFaultHits = installTaskPostInsertFailure(t, db, user.Id, info.PublicTaskID)
			}
			// Join even on a fatal assertion, before removing the fault callback or
			// restoring the parent fixture's DB/cache globals. The official flag
			// marks scheduling, not completion of its asynchronous refund.
			t.Cleanup(func() {
				waitTaskSubmissionFixtureQuiescence(t)
			})
			outcome, taskErr := executeTaskSubmission(c, info)
			waitTaskSubmissionFixtureQuiescence(t)
			if tc.settlementFailure {
				wantFaultHits := int32(1)
				if tc.shortage {
					wantFaultHits = 0
				}
				require.Equal(t, wantFaultHits, settlementFaultHits.Load(), "fault is after a successful Task INSERT, never the initial held receipt")
				require.Nil(t, outcome)
				require.NotNil(t, taskErr)
				assert.False(t, taskErr.NoRetry, "the existing local-error envelope is unchanged; execute returns outside the attempt loop")
				assert.True(t, taskErr.LocalError)
				assert.Empty(t, c.Writer.Header().Get("X-New-API-Task-Accepted"))
				assert.Empty(t, c.Writer.Header().Get("X-New-API-Task-ID"))
				assert.Nil(t, taskErr.Data)
				assert.False(t, c.Writer.Written())
				held := common.QuotaRound(4 * 0.01 * common.QuotaPerUnit)
				require.NotNil(t, info.Billing)
				assert.Equal(t, held, info.Billing.GetPreConsumedQuota())
				grossShares := []model.LedgerShare{{Account: model.AccountPaid, Amount: held}}
				assert.Equal(t, grossShares, service.WalletSessionDebitShares(info))
				if tc.shortage {
					assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
					assert.Equal(t, "insufficient_user_quota", taskErr.Code)
					assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}, decideTaskRetry(c, taskErr, 3))
				} else {
					assert.Equal(t, http.StatusInternalServerError, taskErr.StatusCode)
					assert.Equal(t, "task_insert_failed", taskErr.Code)
					// Hypothetical classification only: no second paid submit occurs.
					assert.Equal(t, service.PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "system"}, decideTaskRetry(c, taskErr, 3))
				}
				snapshot := captureTaskSubmissionFinancialState(t, db, info)
				require.Empty(t, snapshot.Tasks, "post-INSERT failure rolls back the row; shortage never inserts it")
				require.Len(t, snapshot.Owners, 1)
				owner := snapshot.Owners[0]
				assert.Equal(t, model.BillingDeliveryUnknown, owner.State)
				assert.Equal(t, "upstream_delivery_unknown", owner.LastErrorCode)
				assert.Equal(t, "review_required", owner.LastErrorClass)
				assert.Equal(t, held, owner.PreConsumed)
				assert.Equal(t, held, owner.SourceHeld)
				assert.Equal(t, held, owner.TokenHeld)
				assert.Equal(t, token.Id, owner.TokenID)
				assert.Equal(t, model.BillingTokenFingerprint(token.Key), owner.TokenFingerprint)
				assert.Zero(t, owner.ActualQuota)
				assert.Zero(t, owner.TaskID)
				assert.Equal(t, initial-held, snapshot.User.Quota)
				assert.Equal(t, initial-held, snapshot.User.QuotaPaid)
				assert.Zero(t, snapshot.User.QuotaBonus)
				assert.Zero(t, snapshot.User.QuotaReward)
				assert.Zero(t, snapshot.User.UsedQuota)
				assert.Zero(t, snapshot.User.RequestCount)
				assert.Zero(t, snapshot.Channel.UsedQuota)
				assert.Equal(t, initial-held, snapshot.Token.RemainQuota)
				assert.Equal(t, held, snapshot.Token.UsedQuota)
				require.Len(t, snapshot.Ledger, 1, "partial source refund must roll back with the failed Task INSERT transaction")
				assert.Equal(t, -held, snapshot.Ledger[0].Delta)
				assert.Equal(t, model.LedgerReasonBillingPreConsume, snapshot.Ledger[0].Reason)
				require.Len(t, snapshot.Operations, 1)
				assert.Empty(t, snapshot.Logs)
				assert.False(t, info.Billing.NeedsRefund())
				require.ErrorIs(t, info.Billing.Settle(0), model.ErrBillingFinalized)
				info.Billing.Refund(c)
				info.Billing.Refund(c)
				assert.Equal(t, snapshot, captureTaskSubmissionFinancialState(t, db, info), "unknown delivery cannot be blindly refunded by request cleanup")
				assert.EqualValues(t, 1, upstreamCalls.Load(), "no extra paid request after local failure")
				return
			}
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			want := common.QuotaRound(tc.count * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, want, outcome.Result.Quota)
			assert.Equal(t, want, info.PriceData.Quota)
			var stored model.Task
			require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&stored).Error)
			assert.Equal(t, want, stored.Quota)
			assert.Equal(t, model.TaskStatus(tc.status), stored.Status)
			assert.Positive(t, stored.FinishTime)
			assert.Equal(t, float64(4), info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup/(0.01*common.QuotaPerUnit))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, stored.PrivateData.BillingContext.TieredSnapshot.UsageFacts["units"])
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota)
			assert.Equal(t, want, updated.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, want, logs[0].Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			assert.Equal(t, float64(want), other["wallet_quota_deducted"], "submit log includes the final source adjustment, not just the initial hold")
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, other["usage_facts"].(map[string]any)["units"])
			}
			assert.False(t, c.Writer.Written(), "presentation must follow persistence and settlement")
			snapshot := captureTaskSubmissionFinancialState(t, db, info)
			require.Len(t, snapshot.Owners, 1)
			require.Len(t, snapshot.Tasks, 1)
			owner := snapshot.Owners[0]
			assert.Equal(t, model.BillingTaskOwned, owner.State)
			assert.Equal(t, stored.ID, owner.TaskID)
			assert.Equal(t, stored.TaskID, owner.TaskPublicID)
			assert.Equal(t, want, owner.ActualQuota)
			assert.Equal(t, token.Id, owner.TokenID)
			assert.Equal(t, model.BillingTokenFingerprint(token.Key), owner.TokenFingerprint)
			assert.Equal(t, initial-want, snapshot.Token.RemainQuota)
			assert.Equal(t, want, snapshot.Token.UsedQuota)
			assert.Equal(t, 1, snapshot.User.RequestCount)
			assert.EqualValues(t, want, snapshot.Channel.UsedQuota)
			expectedLedgerCount := 2
			if tc.count == 4 {
				expectedLedgerCount = 1
			}
			require.Len(t, snapshot.Ledger, expectedLedgerCount)
			require.Len(t, snapshot.Operations, expectedLedgerCount)
			net := 0
			for _, row := range snapshot.Ledger {
				net += row.Delta
			}
			assert.Equal(t, -want, net)
			require.ErrorIs(t, info.Billing.Settle(want), model.ErrBillingFinalized, "request settlement cannot take ownership back from the task")
			info.Billing.Refund(c)
			require.NoError(t, service.PersistTaskBilling(info, &stored), "the same task may replay the original atomic handoff")
			assert.Equal(t, snapshot, captureTaskSubmissionFinancialState(t, db, info), "owner/task/ledger/token/usage/logs must all remain unchanged on replay")
			assert.EqualValues(t, 1, upstreamCalls.Load())
		})
	}
}

func TestAcceptedSubmitStreamNeverRetries(t *testing.T) {
	c := taskSubmissionTestContext()
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "no_retry", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: 502, LocalError: true, NoRetry: true}, 3))
}

// Local task rejections carry a message but no cause; the response and the
// decision record must still be produced.
func TestRespondTaskSubmissionErrorWithoutCause(t *testing.T) {
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	c := taskSubmissionTestContext()
	taskErr := &dto.TaskError{Code: "get_channel_failed", Message: "no channel", StatusCode: http.StatusServiceUnavailable, LocalError: true}
	require.NotPanics(t, func() { respondTaskSubmissionError(c, taskErr) })
	assert.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
	events := service.RequestPolicy(c).Events()
	require.Len(t, events, 1)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "request_failed", Source: "system"}, events[0].Decision)
	assert.Equal(t, http.StatusServiceUnavailable, events[0].Status)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: http.StatusForbidden, LocalError: true, Message: "billing"}, 2), "local errors stop once the status rules do not force a retry")
}

func TestExecuteTaskSubmissionPreservesHoldWhenAcceptedFinalReserveFails(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "task-accepted-shortage", 100, 100)
	calls := 0
	outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		calls++
		return &relay.TaskSubmitResult{UpstreamTaskID: "upstream-accepted", Platform: "task_plugin", Quota: 600, Immediate: &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	require.Equal(t, "insufficient_user_quota", taskErr.Code)
	require.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	require.Equal(t, 1, calls)
	f.assertFinancial(t, model.BillingDeliveryUnknown, 100, 0, 0, 1)
	f.assertTaskCount(t, 0)
	f.assertUnknownCannotRefund(t)
	require.False(t, f.c.Writer.Written())
}
