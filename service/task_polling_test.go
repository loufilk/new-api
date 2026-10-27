package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type taskPollingFetchAdaptor struct {
	mu           sync.Mutex
	initInfo     *relaycommon.RelayInfo
	taskIDs      []string
	fetched      chan string
	blockTaskID  string
	blockStarted chan struct{}
	releaseBlock chan struct{}
	blockOnce    sync.Once
}

type batchPollingAdaptor struct {
	taskPollingFetchAdaptor
	batchCalls int
	batchIDs   []string
	results    map[string]*BatchTaskResult
}

func (a *batchPollingAdaptor) FetchMode() string { return "batch" }
func (a *batchPollingAdaptor) FetchBatchTasks(_ string, _ string, tasks []*model.Task, _ string) (*http.Response, error) {
	a.batchCalls++
	a.batchIDs = a.batchIDs[:0]
	for _, task := range tasks {
		a.batchIDs = append(a.batchIDs, task.GetUpstreamTaskID())
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte(`{}`)))}, nil
}
func (a *batchPollingAdaptor) ParseBatchResult(_ []*model.Task, _ *http.Response, _ []byte) (map[string]*BatchTaskResult, error) {
	if a.results != nil {
		return a.results, nil
	}
	results := make(map[string]*BatchTaskResult, len(a.batchIDs))
	for _, taskID := range a.batchIDs {
		results[taskID] = &BatchTaskResult{TaskInfo: relaycommon.TaskInfo{TaskID: taskID, Status: model.TaskStatusInProgress, Url: "https://example.com/result"}}
	}
	return results, nil
}

func (a *taskPollingFetchAdaptor) Init(info *relaycommon.RelayInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initInfo = info
}
func (a *taskPollingFetchAdaptor) initChannelMeta() *relaycommon.ChannelMeta {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.initInfo == nil {
		return nil
	}
	return a.initInfo.ChannelMeta
}

func (a *taskPollingFetchAdaptor) FetchTask(_ string, _ string, task *model.Task, _ string) (*http.Response, error) {
	taskID := ""
	if task != nil {
		taskID = task.GetUpstreamTaskID()
	}
	if taskID == a.blockTaskID && a.releaseBlock != nil {
		a.blockOnce.Do(func() {
			if a.blockStarted != nil {
				close(a.blockStarted)
			}
		})
		<-a.releaseBlock
	}

	a.mu.Lock()
	a.taskIDs = append(a.taskIDs, taskID)
	a.mu.Unlock()
	if a.fetched != nil {
		select {
		case a.fetched <- taskID:
		default:
		}
	}

	response := taskdto.TaskResponse[model.Task]{
		Code: taskdto.TaskSuccessCode,
		Data: model.Task{
			TaskID:   taskID,
			Status:   model.TaskStatusInProgress,
			Progress: "30%",
		},
	}
	responseBody, err := common.Marshal(response)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (a *taskPollingFetchAdaptor) ParseTaskResult(*model.Task, *http.Response, []byte) (*relaycommon.TaskInfo, error) {
	return &relaycommon.TaskInfo{Status: model.TaskStatusInProgress}, nil
}

func (a *taskPollingFetchAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *taskPollingFetchAdaptor) fetchCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.taskIDs)
}

func (a *taskPollingFetchAdaptor) fetchedTaskIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.taskIDs...)
}

func TestRedactVideoResponseBodyPreservesPollingPayloadShape(t *testing.T) {
	rawVideo := strings.Repeat("a", 300)
	body, err := common.Marshal(map[string]any{
		"done": true,
		"name": "operations/provider-task",
		"response": map[string]any{
			"bytesBase64Encoded": "secret-bytes",
			"video":              rawVideo,
			"videos": []any{
				map[string]any{
					"bytesBase64Encoded": "other-secret-bytes",
					"mimeType":           "video/mp4",
					"uri":                "https://media.example/video.mp4",
				},
			},
		},
	})
	require.NoError(t, err)

	var stored map[string]any
	require.NoError(t, common.Unmarshal(redactVideoResponseBody(body), &stored))
	assert.Equal(t, true, stored["done"])
	assert.Equal(t, "operations/provider-task", stored["name"])
	response, ok := stored["response"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, response, "bytesBase64Encoded")
	assert.Equal(t, strings.Repeat("a", 256)+"...", response["video"])
	videos, ok := response["videos"].([]any)
	require.True(t, ok)
	require.Len(t, videos, 1)
	video, ok := videos[0].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, video, "bytesBase64Encoded")
	assert.Equal(t, "video/mp4", video["mimeType"])
	assert.Equal(t, "https://media.example/video.mp4", video["uri"])
}

func seedTaskPollingChannel(t *testing.T, id int, disableSleep bool) {
	t.Helper()
	ch := &model.Channel{
		Id:     id,
		Type:   constant.ChannelTypeKling,
		Name:   "polling_channel",
		Key:    "sk-test",
		Status: common.ChannelStatusEnabled,
	}
	if disableSleep {
		ch.SetOtherSettings(dto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	}
	require.NoError(t, model.DB.Create(ch).Error)
}

func seedPollingTask(t *testing.T, channelID int, publicID string, upstreamID string) *model.Task {
	t.Helper()
	task := &model.Task{
		TaskID:    publicID,
		Platform:  constant.TaskPlatform("kling"),
		UserId:    1,
		ChannelId: channelID,
		Action:    constant.TaskActionImageToVideo,
		Status:    model.TaskStatusInProgress,
		Progress:  "30%",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: upstreamID,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)
	return task
}

func TestUpdateVideoTasksDefaultSleepWaitsBetweenTasks(t *testing.T) {
	truncate(t)

	const channelID = 101
	seedTaskPollingChannel(t, channelID, false)
	first := seedPollingTask(t, channelID, "task_public_1", "upstream_1")
	second := seedPollingTask(t, channelID, "task_public_2", "upstream_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		channelID: {
			first.GetUpstreamTaskID(),
			second.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		first.GetUpstreamTaskID():  first,
		second.GetUpstreamTaskID(): second,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, adaptor.fetchCount())
}

func TestPollingPassesExecutingChannelTypeToAdaptor(t *testing.T) {
	truncate(t)
	const channelID = 112
	baseURL := "https://gateway.example"
	gateway := &model.Channel{Id: channelID, Type: constant.ChannelTypeNewAPI, Name: "gateway", Key: "sk-gateway", Status: common.ChannelStatusEnabled, BaseURL: &baseURL}
	gateway.HeaderOverride = common.GetPointer(`{"X-API-Key":"{api_key}"}`)
	gateway.SetOtherSettings(dto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	require.NoError(t, model.DB.Create(gateway).Error)
	task := seedPollingTask(t, channelID, "task_gateway", "upstream_gateway")
	taskChannels := map[int][]string{channelID: {task.GetUpstreamTaskID()}}
	tasks := map[string]*model.Task{task.GetUpstreamTaskID(): task}

	perTask := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return perTask }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })
	require.NoError(t, UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"), taskChannels, tasks))
	meta := perTask.initChannelMeta()
	require.NotNil(t, meta, "per-task polling initializes the adaptor with channel metadata")
	assert.Equal(t, constant.ChannelTypeNewAPI, meta.ChannelType, "the adaptor derives the upstream kind from the channel type")
	assert.Equal(t, channelID, meta.ChannelId)
	assert.Equal(t, baseURL, meta.ChannelBaseUrl)
	assert.Equal(t, map[string]any{"X-API-Key": "{api_key}"}, meta.HeadersOverride)

	batch := &batchPollingAdaptor{}
	require.NoError(t, UpdateBatchTasks(context.Background(), batch, taskChannels, tasks))
	meta = batch.initChannelMeta()
	require.NotNil(t, meta, "batch polling initializes the adaptor with channel metadata")
	assert.Equal(t, constant.ChannelTypeNewAPI, meta.ChannelType)
	assert.Equal(t, channelID, meta.ChannelId)
	assert.Equal(t, baseURL, meta.ChannelBaseUrl)
	assert.Equal(t, map[string]any{"X-API-Key": "{api_key}"}, meta.HeadersOverride)
}

func TestDispatchPlatformUpdateUsesFetchMode(t *testing.T) {
	truncate(t)
	const channelID = 109
	seedTaskPollingChannel(t, channelID, true)
	task := seedPollingTask(t, channelID, "task_batch", "upstream_batch")
	taskChannels := map[int][]string{channelID: {task.GetUpstreamTaskID()}}
	tasks := map[string]*model.Task{task.GetUpstreamTaskID(): task}

	batch := &batchPollingAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return batch }
	DispatchPlatformUpdate(context.Background(), "batch-plugin", taskChannels, tasks)
	assert.Equal(t, 1, batch.batchCalls)
	assert.Equal(t, 0, batch.fetchCount())
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, "https://example.com/result", persisted.GetResultURL())

	perTask := &taskPollingFetchAdaptor{}
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return perTask }
	DispatchPlatformUpdate(context.Background(), "per-task-plugin", taskChannels, tasks)
	assert.Equal(t, 1, perTask.fetchCount())

	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return nil }
	assert.NotPanics(t, func() { DispatchPlatformUpdate(context.Background(), "missing-plugin", taskChannels, tasks) })
	GetTaskAdaptorFunc = previousFactory
}

func TestUpdateBatchTasksSettlesTieredUsageForTerminalStates(t *testing.T) {
	testCases := []struct {
		name        string
		status      model.TaskStatus
		units       float64
		actualQuota int
	}{
		{name: "success with usage", status: model.TaskStatusSuccess, units: 3, actualQuota: 3_000},
		{name: "failure with usage", status: model.TaskStatusFailure, units: 3, actualQuota: 0},
		{name: "success with zero usage", status: model.TaskStatusSuccess, units: 0, actualQuota: 0},
		{name: "failure with zero usage", status: model.TaskStatusFailure, units: 0, actualQuota: 0},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncate(t)

			const userID, tokenID, channelID = 41, 41, 141
			const initialQuota, preConsumedQuota = 10_000, 5_000
			const tokenRemain = 8_000
			seedUser(t, userID, initialQuota)
			seedToken(t, tokenID, userID, "sk-batch-tiered", tokenRemain)
			seedTaskPollingChannel(t, channelID, true)

			expression := `tier("actual", u("units"))`
			task := makeTask(userID, channelID, preConsumedQuota, tokenID, BillingSourceWallet, 0)
			task.TaskID = "task_batch_tiered_" + string(testCase.status)
			task.Platform = "batch-plugin"
			task.PrivateData.UpstreamTaskID = "upstream_batch_tiered_" + string(testCase.status)
			task.SetData(map[string]any{"provider_payload": "must-be-preserved"})
			task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
				ExprString:       expression,
				ExprHash:         billingexpr.ExprHashString(expression),
				GroupRatio:       1,
				QuotaPerUnit:     1_000,
				ExprVersion:      1,
				TaskUsageBilling: true,
			}
			require.NoError(t, model.DB.Create(task).Error)

			upstreamID := task.GetUpstreamTaskID()
			reason := ""
			if testCase.status == model.TaskStatusFailure {
				reason = "upstream failed"
			}
			result := &BatchTaskResult{TaskInfo: relaycommon.TaskInfo{
				TaskID:     upstreamID,
				Status:     string(testCase.status),
				Reason:     reason,
				UsageFacts: map[string]any{"units": testCase.units},
			}}
			adaptor := &batchPollingAdaptor{results: map[string]*BatchTaskResult{upstreamID: result}}
			taskIDs := []string{upstreamID}
			taskMap := map[string]*model.Task{upstreamID: task}

			require.NoError(t, UpdateBatchTasks(context.Background(), adaptor, map[int][]string{channelID: taskIDs}, taskMap))

			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, testCase.status, persisted.Status)
			assert.Equal(t, testCase.actualQuota, persisted.Quota)
			var persistedData map[string]any
			require.NoError(t, common.Unmarshal(persisted.Data, &persistedData))
			assert.Equal(t, "must-be-preserved", persistedData["provider_payload"])
			assert.Equal(t, initialQuota+(preConsumedQuota-testCase.actualQuota), getUserQuota(t, userID))
			assert.Equal(t, tokenRemain+(preConsumedQuota-testCase.actualQuota), getTokenRemainQuota(t, tokenID))
			assert.Equal(t, int64(1), countLogs(t))
			if testCase.status == model.TaskStatusFailure {
				log := getLastLog(t)
				require.NotNil(t, log)
				assert.Equal(t, model.LogTypeRefund, log.Type)
			}

			// A duplicate terminal response must not settle the same task twice.
			require.NoError(t, UpdateBatchTasks(context.Background(), adaptor, map[int][]string{channelID: taskIDs}, taskMap))
			assert.Equal(t, initialQuota+(preConsumedQuota-testCase.actualQuota), getUserQuota(t, userID))
			assert.Equal(t, int64(1), countLogs(t))
		})
	}
}

func TestUpdateBatchTasksRefundsFailedTieredTask(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 43, 43, 143
	const initialQuota, preConsumedQuota, tokenRemain = 10_000, 5_000, 8_000
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, "sk-batch-tiered-refund", tokenRemain)
	seedTaskPollingChannel(t, channelID, true)

	expression := `tier("actual", u("units"))`
	task := makeTask(userID, channelID, preConsumedQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task_batch_tiered_refund"
	task.Platform = "batch-plugin"
	task.PrivateData.UpstreamTaskID = "upstream_batch_tiered_refund"
	task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
		ExprString:       expression,
		ExprHash:         billingexpr.ExprHashString(expression),
		GroupRatio:       1,
		QuotaPerUnit:     1_000,
		ExprVersion:      1,
		TaskUsageBilling: true,
		UsageFacts:       map[string]any{"units": float64(5)},
		EstimatedTier:    "actual",
	}
	require.NoError(t, model.DB.Create(task).Error)

	upstreamID := task.GetUpstreamTaskID()
	adaptor := &batchPollingAdaptor{results: map[string]*BatchTaskResult{
		upstreamID: {TaskInfo: relaycommon.TaskInfo{
			TaskID:     upstreamID,
			Status:     model.TaskStatusFailure,
			Reason:     "upstream failed",
			UsageFacts: map[string]any{"units": float64(5)},
		}},
	}}
	require.NoError(t, UpdateBatchTasks(context.Background(), adaptor, map[int][]string{channelID: {upstreamID}}, map[string]*model.Task{upstreamID: task}))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, persisted.Status)
	assert.Zero(t, persisted.Quota)
	assert.Equal(t, initialQuota+preConsumedQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumedQuota, getTokenRemainQuota(t, tokenID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumedQuota, log.Quota)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	assert.Equal(t, "tiered_expr", other["billing_mode"])
	assert.Equal(t, "actual", other["matched_tier"])
	facts, ok := other["usage_facts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"units": float64(5)}, facts)
}

func TestUpdateBatchTasksRefundsFailedTaskWithoutUsageSettlement(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 42, 42, 142
	const initialQuota, preConsumedQuota, tokenRemain = 10_000, 4_000, 7_000
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, "sk-batch-refund", tokenRemain)
	seedTaskPollingChannel(t, channelID, true)

	task := makeTask(userID, channelID, preConsumedQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task_batch_refund"
	task.Platform = "batch-plugin"
	task.Properties.OriginModelName = "missing-batch-token-price"
	task.PrivateData.UpstreamTaskID = "upstream_batch_refund"
	task.PrivateData.BillingContext.OriginModelName = "missing-batch-token-price"
	require.NoError(t, model.DB.Create(task).Error)

	upstreamID := task.GetUpstreamTaskID()
	adaptor := &batchPollingAdaptor{results: map[string]*BatchTaskResult{
		upstreamID: {TaskInfo: relaycommon.TaskInfo{TaskID: upstreamID, Status: model.TaskStatusFailure, Reason: "upstream failed", TotalTokens: 123}},
	}}
	require.NoError(t, UpdateBatchTasks(context.Background(), adaptor, map[int][]string{channelID: {upstreamID}}, map[string]*model.Task{upstreamID: task}))

	assert.Equal(t, initialQuota+preConsumedQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumedQuota, getTokenRemainQuota(t, tokenID))
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestUpdateVideoTasksCanSkipPollingSleepPerChannel(t *testing.T) {
	truncate(t)

	const channelID = 102
	seedTaskPollingChannel(t, channelID, true)
	first := seedPollingTask(t, channelID, "task_public_3", "upstream_3")
	second := seedPollingTask(t, channelID, "task_public_4", "upstream_4")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		channelID: {
			first.GetUpstreamTaskID(),
			second.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		first.GetUpstreamTaskID():  first,
		second.GetUpstreamTaskID(): second,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, adaptor.fetchCount())
}

// Hold both first responses until each channel has actually entered FetchTask.
// A serialized channel dispatcher cannot cross this barrier: its first worker
// cannot finish (or begin its default sleep) before the other worker arrives.
type taskPollingFirstFetchBarrierAdaptor struct {
	taskPollingFetchAdaptor
	firstTaskIDs map[string]bool
	firstFetched chan string
	nextFetched  chan string
	releaseFirst <-chan struct{}
}

func (a *taskPollingFirstFetchBarrierAdaptor) FetchTask(baseURL string, key string, task *model.Task, proxy string) (*http.Response, error) {
	resp, err := a.taskPollingFetchAdaptor.FetchTask(baseURL, key, task, proxy)
	taskID := task.GetUpstreamTaskID()
	if a.firstTaskIDs[taskID] {
		a.firstFetched <- taskID
		<-a.releaseFirst
	} else {
		a.nextFetched <- taskID
	}
	return resp, err
}

func TestUpdateVideoTasksDefaultSleepDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const firstChannelID = 201
	const secondChannelID = 202
	seedTaskPollingChannel(t, firstChannelID, false)
	seedTaskPollingChannel(t, secondChannelID, false)
	firstChannelFirst := seedPollingTask(t, firstChannelID, "task_public_5", "upstream_a_1")
	firstChannelSecond := seedPollingTask(t, firstChannelID, "task_public_6", "upstream_a_2")
	secondChannelFirst := seedPollingTask(t, secondChannelID, "task_public_7", "upstream_b_1")
	secondChannelSecond := seedPollingTask(t, secondChannelID, "task_public_8", "upstream_b_2")

	// Freeze identities before polling can replace each Task's PrivateData.
	firstA, secondA := firstChannelFirst.GetUpstreamTaskID(), firstChannelSecond.GetUpstreamTaskID()
	firstB, secondB := secondChannelFirst.GetUpstreamTaskID(), secondChannelSecond.GetUpstreamTaskID()
	taskChannels := map[int][]string{
		firstChannelID:  {firstA, secondA},
		secondChannelID: {firstB, secondB},
	}
	tasks := map[string]*model.Task{
		firstA: firstChannelFirst, secondA: firstChannelSecond,
		firstB: secondChannelFirst, secondB: secondChannelSecond,
	}
	releaseFirst := make(chan struct{})
	adaptor := &taskPollingFirstFetchBarrierAdaptor{
		firstTaskIDs: map[string]bool{firstA: true, firstB: true},
		firstFetched: make(chan string, 2), nextFetched: make(chan string, 2),
		releaseFirst: releaseFirst,
	}
	var releaseOnce sync.Once
	releaseResponses := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	ctx, cancel := context.WithCancel(context.Background())
	pollingDone := make(chan struct{})
	var pollingErr error
	joinPolling := func() {
		t.Helper()
		select {
		case <-pollingDone:
		case <-time.After(5 * time.Second):
			t.Fatal("owned polling operation did not finish")
		}
	}
	t.Cleanup(func() {
		cancel()
		releaseResponses()
		joinPolling()
		GetTaskAdaptorFunc = previousFactory
	})
	go func() {
		// UpdateVideoTasks joins its real channel workers before returning.
		defer close(pollingDone)
		pollingErr = UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), taskChannels, tasks)
	}()

	// Startup/metadata SQL is not part of the 50ms default-sleep assertion.
	// The bounded arrival barrier still rejects genuinely serialized channels.
	startup := time.NewTimer(500 * time.Millisecond)
	defer startup.Stop()
	firstFetched := make([]string, 0, 2)
	for len(firstFetched) < 2 {
		select {
		case taskID := <-adaptor.firstFetched:
			firstFetched = append(firstFetched, taskID)
		case <-pollingDone:
			t.Fatal("polling finished before both channels reached their first fetch")
		case <-startup.C:
			t.Fatal("both channels must reach their first fetch independently")
		}
	}
	require.ElementsMatch(t, []string{firstA, firstB}, firstFetched)
	observation := time.NewTimer(50 * time.Millisecond)
	defer observation.Stop()
	releaseResponses()
	select {
	case taskID := <-adaptor.nextFetched:
		t.Fatalf("default channel sleep was skipped before second fetch %s", taskID)
	case <-pollingDone:
		t.Fatal("polling must remain in the default sleep until cancelled")
	case <-observation.C:
	}
	cancel()
	joinPolling()
	require.ErrorIs(t, pollingErr, context.Canceled)
	assert.ElementsMatch(t, []string{firstA, firstB}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksSlowChannelDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const slowChannelID = 251
	const fastChannelID = 252
	seedTaskPollingChannel(t, slowChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	slowTask := seedPollingTask(t, slowChannelID, "task_public_slow", "upstream_slow_1")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_fast_1", "upstream_fast_parallel_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_fast_2", "upstream_fast_parallel_2")
	// Polling updates the Task objects, including GORM's PrivateData assignment.
	// Freeze the fixture identities before any worker starts instead of reading
	// those mutable objects from the concurrent Eventually observer.
	slowTaskID := slowTask.GetUpstreamTaskID()
	fastFirstID := fastFirst.GetUpstreamTaskID()
	fastSecondID := fastSecond.GetUpstreamTaskID()
	taskChannels := map[int][]string{
		slowChannelID: {slowTaskID},
		fastChannelID: {fastFirstID, fastSecondID},
	}
	tasks := map[string]*model.Task{
		slowTaskID:   slowTask,
		fastFirstID:  fastFirst,
		fastSecondID: fastSecond,
	}

	adaptor := &taskPollingFetchAdaptor{
		fetched:      make(chan string, 4),
		blockTaskID:  slowTaskID,
		blockStarted: make(chan struct{}),
		releaseBlock: make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseBlockedTask := func() {
		releaseOnce.Do(func() {
			close(adaptor.releaseBlock)
		})
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }

	ctx, cancel := context.WithCancel(context.Background())
	pollingDone := make(chan struct{})
	var pollingErr error
	joinPolling := func() {
		t.Helper()
		select {
		case <-pollingDone:
		case <-time.After(5 * time.Second):
			t.Fatal("owned polling operation did not finish")
		}
	}
	t.Cleanup(func() {
		releaseBlockedTask()
		cancel()
		joinPolling()
		GetTaskAdaptorFunc = previousFactory
	})
	gopool.Go(func() {
		// UpdateVideoTasks has joined all channel workers before it returns.
		defer close(pollingDone)
		pollingErr = UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), taskChannels, tasks)
	})

	select {
	case <-adaptor.blockStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("slow channel did not start blocking")
	}

	require.Eventually(t, func() bool {
		fetchedTaskIDs := adaptor.fetchedTaskIDs()
		return len(fetchedTaskIDs) == 2 &&
			fetchedTaskIDs[0] == fastFirstID &&
			fetchedTaskIDs[1] == fastSecondID
	}, 500*time.Millisecond, 10*time.Millisecond)

	releaseBlockedTask()
	joinPolling()
	require.NoError(t, pollingErr)
	assert.ElementsMatch(t, []string{
		slowTaskID,
		fastFirstID,
		fastSecondID,
	}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksMixedChannelSleepSettings(t *testing.T) {
	truncate(t)

	const sleepyChannelID = 301
	const fastChannelID = 302
	seedTaskPollingChannel(t, sleepyChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	sleepyFirst := seedPollingTask(t, sleepyChannelID, "task_public_9", "upstream_sleepy_1")
	sleepySecond := seedPollingTask(t, sleepyChannelID, "task_public_10", "upstream_sleepy_2")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_11", "upstream_fast_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_12", "upstream_fast_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		sleepyChannelID: {
			sleepyFirst.GetUpstreamTaskID(),
			sleepySecond.GetUpstreamTaskID(),
		},
		fastChannelID: {
			fastFirst.GetUpstreamTaskID(),
			fastSecond.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		sleepyFirst.GetUpstreamTaskID():  sleepyFirst,
		sleepySecond.GetUpstreamTaskID(): sleepySecond,
		fastFirst.GetUpstreamTaskID():    fastFirst,
		fastSecond.GetUpstreamTaskID():   fastSecond,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ElementsMatch(t, []string{"upstream_sleepy_1", "upstream_fast_1", "upstream_fast_2"}, adaptor.fetchedTaskIDs())
}

func TestUpdateSunoTasksStalePollsRefundExactlyOnce(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 401, 401, 401
	const initialUserQuota, initialTokenQuota, taskQuota = 10_000, 6_000, 2_500
	const publicTaskID, upstreamTaskID = "suno_public_refund_once", "suno_upstream_refund_once"

	seedUser(t, userID, initialUserQuota)
	seedToken(t, tokenID, userID, "sk-suno-refund-once", initialTokenQuota)
	baseURL := "https://suno.invalid"
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeSunoAPI,
		Name:    "suno_refund_once",
		Key:     "sk-suno-channel",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
	}).Error)

	task := makeTask(userID, channelID, taskQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = publicTaskID
	task.Platform = constant.TaskPlatformSuno
	task.Status = model.TaskStatusInProgress
	task.Progress = "50%"
	task.SubmitTime = time.Now().Unix()
	task.PrivateData.UpstreamTaskID = upstreamTaskID
	require.NoError(t, model.DB.Create(task).Error)

	var firstPollTask model.Task
	var staleSecondPollTask model.Task
	require.NoError(t, model.DB.First(&firstPollTask, task.ID).Error)
	require.NoError(t, model.DB.First(&staleSecondPollTask, task.ID).Error)

	adaptor := &batchPollingAdaptor{results: map[string]*BatchTaskResult{
		upstreamTaskID: {TaskInfo: relaycommon.TaskInfo{
			TaskID: upstreamTaskID,
			Status: model.TaskStatusFailure,
			Reason: "upstream failed",
		}},
	}}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	require.NoError(t, updateBatchTasks(context.Background(), adaptor, channelID, []string{upstreamTaskID}, map[string]*model.Task{
		upstreamTaskID: &firstPollTask,
	}))
	require.NoError(t, updateBatchTasks(context.Background(), adaptor, channelID, []string{upstreamTaskID}, map[string]*model.Task{
		upstreamTaskID: &staleSecondPollTask,
	}))

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Zero(t, reloaded.Quota)
	assert.Equal(t, initialUserQuota+taskQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota+taskQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestRunTaskPollingOnceDoesNotRefundHistoricalFailedTask(t *testing.T) {
	truncate(t)

	const userID, initialQuota, taskQuota = 402, 10_000, 1_200
	seedUser(t, userID, initialQuota)

	task := makeTask(userID, 0, taskQuota, 0, BillingSourceWallet, 0)
	task.TaskID = "historical_failed_already_refunded"
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.SubmitTime = time.Now().Add(-90 * 24 * time.Hour).Unix()
	task.UpdatedAt = time.Now().Add(-time.Minute).Unix()
	require.NoError(t, model.DB.Create(task).Error)

	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return &taskPollingFetchAdaptor{}
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	summary := RunTaskPollingOnce(context.Background(), nil)

	assert.Zero(t, summary.UnfinishedTasks)
	assert.Equal(t, initialQuota, getUserQuota(t, userID))
	assert.Equal(t, taskQuota, getTaskQuota(t, task.ID))
	assert.Equal(t, int64(0), countLogs(t))
}

// Missing local metadata cannot establish delivery failure. Keep the precharge
// and its accounting evidence while the task waits for operator review.
func assertPollingFailureAccountingUnchanged(t *testing.T, userID, tokenID, channelID, walletQuota, tokenRemain, held int, channelPresent bool) {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, model.DB.First(&user, userID).Error)
	require.NoError(t, model.DB.First(&token, tokenID).Error)
	assert.Equal(t, walletQuota, user.Quota)
	assert.Equal(t, walletQuota, user.QuotaPaid)
	assert.Zero(t, user.QuotaBonus)
	assert.Zero(t, user.QuotaReward)
	assert.Equal(t, held, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	assert.Equal(t, tokenRemain, token.RemainQuota)
	assert.Equal(t, held, token.UsedQuota)
	if channelPresent {
		var channel model.Channel
		require.NoError(t, model.DB.First(&channel, channelID).Error)
		assert.Equal(t, int64(held), channel.UsedQuota)
	} else {
		var channels int64
		require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Count(&channels).Error)
		assert.Zero(t, channels, "polling must not recreate the deleted channel")
	}
	for _, row := range []any{&model.BalanceLedger{}, &model.BalanceLedgerOperation{}} {
		var count int64
		require.NoError(t, model.DB.Model(row).Where("user_id = ?", userID).Count(&count).Error)
		assert.Zero(t, count, "missing metadata must not write a money adjustment")
	}
	var logs int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ?", userID).Count(&logs).Error)
	assert.Zero(t, logs)
}

func expectedPollingReviewPrivateData(t *testing.T, task *model.Task, reason string) model.TaskPrivateData {
	t.Helper()
	// Freeze before polling: MarkTaskPollingReview reloads and replaces the
	// caller's task, so comparing against that mutated caller would be vacuous.
	encoded, err := common.Marshal(task.PrivateData)
	require.NoError(t, err)
	var expected model.TaskPrivateData
	require.NoError(t, common.Unmarshal(encoded, &expected))
	expected.FinancialReviewReason = reason
	return expected
}

func assertPollingReviewSurvivesTimeout(t *testing.T, id int64, progress, reason string, expected model.TaskPrivateData) {
	t.Helper()
	oldTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	t.Cleanup(func() { constant.TaskTimeoutMinutes = oldTimeout })
	// Move only the submission timestamp beyond the cutoff; never sleep or
	// manufacture a terminal outcome to make a refund eligible.
	require.NoError(t, model.DB.Model(&model.Task{}).Where("id = ?", id).
		Update("submit_time", time.Now().Add(-2*time.Hour).Unix()).Error)
	assert.Empty(t, model.GetAllUnFinishSyncTasks(100), "review tasks are excluded from ordinary polling")
	assert.Empty(t, model.GetTimedOutUnfinishedTasks(time.Now().Add(-time.Minute).Unix(), 100), "review tasks are excluded from timeout selection")
	sweepTimedOutTasks(context.Background())
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, id).Error)
	assert.EqualValues(t, model.TaskStatusUnknown, persisted.Status)
	assert.Equal(t, progress, persisted.Progress)
	assert.Equal(t, reason, persisted.FailReason)
	assert.Equal(t, expected, persisted.PrivateData)
	assert.Zero(t, persisted.FinishTime)
	assert.False(t, persisted.SettlementPending)
}

func TestRunTaskPollingOnceKeepsPrechargeWhenUpstreamIDMissing(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 404, 404, 404
	const initialQuota, tokenRemain, preConsumedQuota = 10_000, 8_000, 1_200
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, "sk-missing-upstream", tokenRemain)
	seedTaskPollingChannel(t, channelID, true)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumedQuota, 1)

	task := makeTask(userID, channelID, preConsumedQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = ""
	task.Platform = constant.TaskPlatformSuno
	task.Progress = "50%"
	task.SubmitTime = time.Now().Unix()
	task.PrivateData.PluginState = []byte(`{"provider_state":"keep-missing-id"}`)
	task.PrivateData.PollFailures = 2
	const reason = "Missing upstream task identity; delivery requires operator review"
	expectedPrivate := expectedPollingReviewPrivateData(t, task, reason)
	require.NoError(t, model.DB.Create(task).Error)

	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return &taskPollingFetchAdaptor{} }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })
	previousQueryLimit := constant.TaskQueryLimit
	constant.TaskQueryLimit = 100
	t.Cleanup(func() { constant.TaskQueryLimit = previousQueryLimit })

	summary := RunTaskPollingOnce(context.Background(), nil)

	assert.Equal(t, 1, summary.UnfinishedTasks)
	assert.Zero(t, summary.NullTasksFailed, "unknown delivery must not be reported as proven failure")
	assert.Equal(t, preConsumedQuota, getTaskQuota(t, task.ID))
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusUnknown, persisted.Status)
	assert.Equal(t, "50%", persisted.Progress)
	assert.Equal(t, reason, persisted.FailReason)
	assert.Equal(t, expectedPrivate, persisted.PrivateData)
	assertPollingFailureAccountingUnchanged(t, userID, tokenID, channelID, initialQuota, tokenRemain, preConsumedQuota, true)
	summary = RunTaskPollingOnce(context.Background(), nil)
	assert.Zero(t, summary.UnfinishedTasks, "the review task is not selected again")
	assert.Zero(t, summary.NullTasksFailed)
	assertPollingReviewSurvivesTimeout(t, task.ID, "50%", reason, expectedPrivate)
	assert.Equal(t, preConsumedQuota, getTaskQuota(t, task.ID))
	assertPollingFailureAccountingUnchanged(t, userID, tokenID, channelID, initialQuota, tokenRemain, preConsumedQuota, true)
}

func TestUpdateBatchTasksKeepsPrechargeWhenChannelMissing(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 405, 405, 405
	const initialQuota, tokenRemain, preConsumedQuota = 10_000, 8_000, 1_300
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, "sk-missing-batch-channel", tokenRemain)
	seedTaskPollingChannel(t, channelID, true)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumedQuota, 1)

	task := makeTask(userID, channelID, preConsumedQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task_missing_batch_channel"
	task.Platform = "batch-plugin"
	task.PrivateData.UpstreamTaskID = "upstream_missing_batch_channel"
	task.Progress = "40%"
	task.PrivateData.PluginState = []byte(`{"provider_state":"keep-missing-batch-channel"}`)
	task.PrivateData.PollFailures = 2
	const reason = "Missing channel metadata; upstream delivery requires operator review"
	expectedPrivate := expectedPollingReviewPrivateData(t, task, reason)
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, model.DB.Delete(&model.Channel{}, channelID).Error)

	upstreamID := task.GetUpstreamTaskID()
	adaptor := &batchPollingAdaptor{}
	for range 2 {
		err := updateBatchTasks(context.Background(), adaptor, channelID, []string{upstreamID}, map[string]*model.Task{upstreamID: task})
		require.ErrorIs(t, err, gorm.ErrRecordNotFound, "missing local metadata remains a deferred lookup error")
		assertPollingFailureAccountingUnchanged(t, userID, tokenID, channelID, initialQuota, tokenRemain, preConsumedQuota, false)
	}
	assert.Zero(t, adaptor.batchCalls)
	assert.Equal(t, preConsumedQuota, getTaskQuota(t, task.ID))
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusUnknown, persisted.Status)
	assert.Equal(t, "40%", persisted.Progress)
	assert.Equal(t, reason, persisted.FailReason)
	assert.Equal(t, expectedPrivate, persisted.PrivateData)
	assertPollingReviewSurvivesTimeout(t, task.ID, "40%", reason, expectedPrivate)
	assert.Equal(t, preConsumedQuota, getTaskQuota(t, task.ID))
	assertPollingFailureAccountingUnchanged(t, userID, tokenID, channelID, initialQuota, tokenRemain, preConsumedQuota, false)
}

func TestUpdateVideoTasksKeepsPrechargeWhenChannelMissing(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 406, 406, 406
	const initialQuota, tokenRemain, preConsumedQuota = 10_000, 8_000, 1_400
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, "sk-missing-video-channel", tokenRemain)
	seedTaskPollingChannel(t, channelID, true)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumedQuota, 1)

	task := makeTask(userID, channelID, preConsumedQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task_missing_video_channel"
	task.Platform = constant.TaskPlatform("kling")
	task.PrivateData.UpstreamTaskID = "upstream_missing_video_channel"
	task.Progress = "60%"
	task.PrivateData.PluginState = []byte(`{"provider_state":"keep-missing-video-channel"}`)
	task.PrivateData.PollFailures = 2
	const reason = "Missing channel metadata; upstream delivery requires operator review"
	expectedPrivate := expectedPollingReviewPrivateData(t, task, reason)
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, model.DB.Delete(&model.Channel{}, channelID).Error)

	previousFactory := GetTaskAdaptorFunc
	adaptor := &taskPollingFetchAdaptor{}
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	upstreamID := task.GetUpstreamTaskID()
	for range 2 {
		err := updateVideoTasks(context.Background(), task.Platform, channelID, []string{upstreamID}, map[string]*model.Task{upstreamID: task})
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		assertPollingFailureAccountingUnchanged(t, userID, tokenID, channelID, initialQuota, tokenRemain, preConsumedQuota, false)
	}
	assert.Zero(t, adaptor.fetchCount())
	assert.Equal(t, preConsumedQuota, getTaskQuota(t, task.ID))
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusUnknown, persisted.Status)
	assert.Equal(t, "60%", persisted.Progress)
	assert.Equal(t, reason, persisted.FailReason)
	assert.Equal(t, expectedPrivate, persisted.PrivateData)
	assertPollingReviewSurvivesTimeout(t, task.ID, "60%", reason, expectedPrivate)
	assert.Equal(t, preConsumedQuota, getTaskQuota(t, task.ID))
	assertPollingFailureAccountingUnchanged(t, userID, tokenID, channelID, initialQuota, tokenRemain, preConsumedQuota, false)
}

func TestSweepTimedOutTasksPreservesAllHeldEvidence(t *testing.T) {
	truncate(t)
	const userID, channelID = 403, 403
	const initialQuota, legacyQuota, modernQuota = 10_000, 1_800, 1_200
	seedUser(t, userID, initialQuota)
	seedTaskPollingChannel(t, channelID, true)
	seedChargedAccounting(t, userID, channelID, 0, legacyQuota+modernQuota, 2)
	var tasks []*model.Task
	for i, quota := range []int{legacyQuota, modernQuota} {
		task := makeTask(userID, channelID, quota, 0, BillingSourceWallet, 0)
		task.TaskID = []string{"legacy_timeout", "modern_timeout"}[i]
		task.PrivateData.UpstreamTaskID = "upstream_" + task.TaskID
		task.Progress = "50%"
		task.SubmitTime = []int64{1771718399, 1771718400}[i]
		require.NoError(t, model.DB.Create(task).Error)
		tasks = append(tasks, task)
	}
	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })
	for range 2 {
		sweepTimedOutTasks(context.Background())
		for _, task := range tasks {
			var stored model.Task
			require.NoError(t, model.DB.First(&stored, task.ID).Error)
			assert.EqualValues(t, model.TaskStatusUnknown, stored.Status)
			assert.Equal(t, task.Quota, stored.Quota)
			assert.Equal(t, task.PrivateData.WalletDebit, stored.PrivateData.WalletDebit)
			assert.Equal(t, "Task timed out; upstream delivery requires operator review", stored.FailReason)
			assert.Equal(t, stored.FailReason, stored.PrivateData.FinancialReviewReason)
			assert.Nil(t, stored.PrivateData.FinancialIntent)
			assert.False(t, stored.SettlementPending)
		}
		user := getUserBalances(t, userID)
		assert.Equal(t, initialQuota, user.QuotaPaid)
		assertQuotaInvariant(t, user)
		assert.Equal(t, legacyQuota+modernQuota, user.UsedQuota)
		assert.Equal(t, 2, user.RequestCount)
		var channel model.Channel
		require.NoError(t, model.DB.First(&channel, channelID).Error)
		assert.EqualValues(t, legacyQuota+modernQuota, channel.UsedQuota)
		assert.Empty(t, ledgerRowsFor(t, userID))
		assert.Zero(t, countLogs(t))
	}
}

type scriptedPollingAdaptor struct {
	statusCode int
	body       []byte
	fetchErr   error
	parse      *relaycommon.TaskInfo
	parseErr   error
}

func (a *scriptedPollingAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *scriptedPollingAdaptor) FetchTask(string, string, *model.Task, string) (*http.Response, error) {
	if a.fetchErr != nil {
		return nil, a.fetchErr
	}
	code := a.statusCode
	if code == 0 {
		code = http.StatusOK
	}
	body := a.body
	if body == nil {
		body = []byte(`{}`)
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(body))}, nil
}
func (a *scriptedPollingAdaptor) ParseTaskResult(*model.Task, *http.Response, []byte) (*relaycommon.TaskInfo, error) {
	if a.parseErr != nil {
		return nil, a.parseErr
	}
	if a.parse != nil {
		return a.parse, nil
	}
	return &relaycommon.TaskInfo{Status: model.TaskStatusInProgress}, nil
}
func (a *scriptedPollingAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return 0
}

type scriptedBatchPollingAdaptor struct {
	scriptedPollingAdaptor
	results map[string]*BatchTaskResult
}

func (a *scriptedBatchPollingAdaptor) FetchMode() string { return "batch" }
func (a *scriptedBatchPollingAdaptor) FetchBatchTasks(string, string, []*model.Task, string) (*http.Response, error) {
	return a.FetchTask("", "", nil, "")
}
func (a *scriptedBatchPollingAdaptor) ParseBatchResult([]*model.Task, *http.Response, []byte) (map[string]*BatchTaskResult, error) {
	if a.parseErr != nil {
		return nil, a.parseErr
	}
	return a.results, nil
}

func TestUpdateVideoSingleTaskPollClassification(t *testing.T) {
	testCases := []struct {
		name          string
		statusCode    int
		fetchErr      error
		parse         *relaycommon.TaskInfo
		parseErr      error
		priorFailures int
		priorState    string
		maxFailures   int
		wantStatus    model.TaskStatus
		wantFailures  int
		wantRefund    bool
		wantReason    string
		wantState     string
		wantUnchanged bool
	}{
		{
			name:          "404 preserves hold for review",
			statusCode:    http.StatusNotFound,
			wantStatus:    model.TaskStatusUnknown,
			wantReason:    "Upstream task not found; delivery requires operator review",
			wantUnchanged: false,
		},
		{
			name:          "401 increments without changing status",
			statusCode:    http.StatusUnauthorized,
			wantStatus:    model.TaskStatusInProgress,
			wantFailures:  1,
			wantUnchanged: true,
		},
		{
			name:          "429 reaches threshold and preserves hold",
			statusCode:    http.StatusTooManyRequests,
			priorFailures: 2,
			maxFailures:   3,
			wantStatus:    model.TaskStatusUnknown,
			wantFailures:  3,
			wantReason:    "Failed to query the upstream task status; delivery requires operator review",
		},
		{
			name:          "UNKNOWN increments",
			statusCode:    http.StatusOK,
			parse:         &relaycommon.TaskInfo{Status: model.TaskStatusUnknown, Reason: "weird"},
			wantStatus:    model.TaskStatusInProgress,
			wantFailures:  1,
			wantUnchanged: true,
		},
		{
			name:          "valid 2xx resets the failure counter",
			statusCode:    http.StatusOK,
			parse:         &relaycommon.TaskInfo{Status: model.TaskStatusInProgress},
			priorFailures: 5,
			wantStatus:    model.TaskStatusInProgress,
			wantFailures:  0,
		},
		{
			name:       "omit state preserves previous plugin state",
			statusCode: http.StatusOK,
			parse:      &relaycommon.TaskInfo{Status: model.TaskStatusInProgress},
			priorState: `{"req_key":"keep"}`,
			wantStatus: model.TaskStatusInProgress,
			wantState:  `{"req_key":"keep"}`,
		},
		{
			name:       "returned state replaces plugin state",
			statusCode: http.StatusOK,
			parse: &relaycommon.TaskInfo{
				Status:      model.TaskStatusInProgress,
				PluginState: []byte(`{"req_key":"new"}`),
			},
			priorState: `{"req_key":"old"}`,
			wantStatus: model.TaskStatusInProgress,
			wantState:  `{"req_key":"new"}`,
		},
		{
			name:          "other 4xx non-terminal is unrecognized",
			statusCode:    http.StatusBadRequest,
			parse:         &relaycommon.TaskInfo{Status: model.TaskStatusInProgress},
			wantStatus:    model.TaskStatusInProgress,
			wantFailures:  1,
			wantUnchanged: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncate(t)
			const userID, tokenID, channelID = 510, 510, 510
			const initialQuota, preConsumed, tokenRemain = 10_000, 4_000, 7_000
			seedUser(t, userID, initialQuota)
			seedToken(t, tokenID, userID, "sk-poll-class", tokenRemain)
			ch := &model.Channel{Id: channelID, Type: constant.ChannelTypeKling, Name: "poll", Key: "sk-test", Status: common.ChannelStatusEnabled}

			if testCase.maxFailures > 0 {
				previous := constant.TaskPollMaxFailures
				constant.TaskPollMaxFailures = testCase.maxFailures
				t.Cleanup(func() { constant.TaskPollMaxFailures = previous })
			}

			task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
			task.TaskID = "task_poll_class"
			task.PrivateData.UpstreamTaskID = "upstream_poll_class"
			task.PrivateData.PollFailures = testCase.priorFailures
			if testCase.priorState != "" {
				task.PrivateData.PluginState = []byte(testCase.priorState)
			}
			require.NoError(t, model.DB.Create(task).Error)

			adaptor := &scriptedPollingAdaptor{statusCode: testCase.statusCode, fetchErr: testCase.fetchErr, parse: testCase.parse, parseErr: testCase.parseErr}
			require.NoError(t, updateVideoSingleTask(context.Background(), adaptor, ch, task.GetUpstreamTaskID(), map[string]*model.Task{
				task.GetUpstreamTaskID(): task,
			}))

			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.EqualValues(t, testCase.wantStatus, persisted.Status)
			assert.Equal(t, testCase.wantFailures, persisted.PrivateData.PollFailures)
			if testCase.wantUnchanged {
				assert.Empty(t, persisted.FailReason)
			}
			if testCase.wantReason != "" {
				assert.Equal(t, testCase.wantReason, persisted.FailReason)
			}
			if testCase.wantState != "" {
				assert.JSONEq(t, testCase.wantState, string(persisted.PrivateData.PluginState))
			}
			if testCase.wantRefund {
				assert.Equal(t, initialQuota+preConsumed, getUserQuota(t, userID))
				assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
				assert.Zero(t, persisted.Quota)
				log := getLastLog(t)
				require.NotNil(t, log)
				assert.Equal(t, model.LogTypeRefund, log.Type)
			} else {
				assert.Equal(t, initialQuota, getUserQuota(t, userID))
				assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
			}
		})
	}
}

func TestUpdateBatchTasksPollClassification(t *testing.T) {
	testCases := []struct {
		name         string
		statusCode   int
		resultStatus model.TaskStatus
		wantStatus   model.TaskStatus
		wantFailures int
		wantRefund   bool
		wantReason   string
	}{
		{
			name:       "404 preserves batch holds for review",
			statusCode: http.StatusNotFound,
			wantStatus: model.TaskStatusUnknown,
			wantReason: "Upstream task not found; delivery requires operator review",
		},
		{
			name:         "401 increments every task",
			statusCode:   http.StatusUnauthorized,
			wantStatus:   model.TaskStatusInProgress,
			wantFailures: 1,
		},
		{
			name:         "UNKNOWN increments",
			statusCode:   http.StatusOK,
			resultStatus: model.TaskStatusUnknown,
			wantStatus:   model.TaskStatusInProgress,
			wantFailures: 1,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncate(t)
			const userID, tokenID, channelID = 610, 610, 610
			const initialQuota, preConsumed, tokenRemain = 10_000, 4_000, 7_000
			seedUser(t, userID, initialQuota)
			seedToken(t, tokenID, userID, "sk-batch-class", tokenRemain)
			seedTaskPollingChannel(t, channelID, true)

			task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
			task.TaskID = "task_batch_class"
			task.PrivateData.UpstreamTaskID = "upstream_batch_class"
			require.NoError(t, model.DB.Create(task).Error)
			upstreamID := task.GetUpstreamTaskID()

			adaptor := &scriptedBatchPollingAdaptor{
				scriptedPollingAdaptor: scriptedPollingAdaptor{statusCode: testCase.statusCode},
			}
			if testCase.resultStatus != "" {
				adaptor.results = map[string]*BatchTaskResult{
					upstreamID: {TaskInfo: relaycommon.TaskInfo{TaskID: upstreamID, Status: string(testCase.resultStatus), Reason: "weird"}},
				}
			}

			require.NoError(t, UpdateBatchTasks(context.Background(), adaptor, map[int][]string{channelID: {upstreamID}}, map[string]*model.Task{upstreamID: task}))

			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.EqualValues(t, testCase.wantStatus, persisted.Status)
			assert.Equal(t, testCase.wantFailures, persisted.PrivateData.PollFailures)
			if testCase.wantReason != "" {
				assert.Equal(t, testCase.wantReason, persisted.FailReason)
			}
			if testCase.wantRefund {
				assert.Equal(t, initialQuota+preConsumed, getUserQuota(t, userID))
				assert.Zero(t, persisted.Quota)
			} else {
				assert.Equal(t, initialQuota, getUserQuota(t, userID))
			}
		})
	}
}
