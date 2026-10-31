package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestKlingNativeRouteSubmitPollSettleAndQuery(t *testing.T) {
	// This is the same closed serial fixture as the Task controller tests:
	// assert quiescence before mutating globals, and disable other producers.
	require.Zero(t, gopool.WorkerCount())
	oldGinMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldGinMode) })
	service.InitHttpClient()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMemoryCache := common.MemoryCacheEnabled
	previousBatchUpdate := common.BatchUpdateEnabled
	previousLogConsume := common.LogConsumeEnabled
	previousRedisEnabled := common.RedisEnabled
	previousExport := common.DataExportEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(append([]any{
		&model.User{},
		&model.Token{},
		&model.BillingFinalization{},
		&model.Model{},
		&model.Ability{},
		&model.Vendor{},
		&model.Channel{},
		&model.Task{},
		&model.Log{},
		&model.BalanceLedgerOperation{},
		&model.BalanceLedger{}}, model.BonusSourceModels()...)...))
	model.DB = database
	model.LOG_DB = database
	common.MemoryCacheEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = false
	common.RedisEnabled = false
	common.DataExportEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	previousModelRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"kling-v3":1}`))
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.MemoryCacheEnabled = previousMemoryCache
		common.BatchUpdateEnabled = previousBatchUpdate
		common.LogConsumeEnabled = previousLogConsume
		common.RedisEnabled = previousRedisEnabled
		common.DataExportEnabled = previousExport
		common.SetDatabaseTypes(previousMainType, previousLogType)
		model.InvalidatePricingCache()
		model.InvalidateModelBonusEligibleCache()
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatios))
	})
	require.NoError(t, database.Create(&model.User{
		Id:        7,
		Username:  "native-route-user",
		AffCode:   "native-route-user",
		Group:     "default",
		Quota:     1_000_000,
		QuotaPaid: 1_000_000,
	}).Error)

	token := model.Token{UserId: 7, Key: "native-route-original-token", Name: "native-route-token", Status: common.TokenStatusEnabled, RemainQuota: 1_000_000}
	require.NoError(t, database.Create(&token).Error)
	require.NoError(t, database.Create(&model.Model{ModelName: "kling-v3", BonusEligible: common.GetPointer(false), Status: 1}).Error)
	model.InvalidatePricingCache()
	model.InvalidateModelBonusEligibleCache()
	t.Cleanup(func() { waitTaskSubmissionFixtureQuiescence(t) })

	var submitCalls atomic.Int32
	var queryCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/kling/v1/videos/text2video":
			submitCalls.Add(1)
			body, readErr := io.ReadAll(r.Body)
			if !assert.NoError(t, readErr) {
				http.Error(w, "read request", http.StatusInternalServerError)
				return
			}
			assert.Contains(t, string(body), `"model_name":"kling-v3"`)
			_, _ = io.WriteString(w, `{"code":0,"message":"","data":{"task_id":"kling-private-1","task_status":"submitted"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/kling/v1/videos/text2video/kling-private-1":
			queryCalls.Add(1)
			_, _ = io.WriteString(w, `{"code":0,"message":"","data":{"task_id":"kling-private-1","task_status":"succeed","task_status_msg":"","task_result":{"videos":[{"id":"video-private","url":"https://cdn.example/video.mp4","duration":"5"}]},"final_unit_deduction":"1"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	channel := model.Channel{
		Type:    constant.ChannelTypeKling,
		Name:    "kling-native-e2e",
		Key:     "sk-test",
		BaseURL: &upstream.URL,
		Status:  common.ChannelStatusEnabled,
		Models:  "kling-v3",
		Group:   "default",
	}
	require.NoError(t, database.Create(&channel).Error)

	generation := pluginruntime.DefaultRegistry.Generation()
	require.NotNil(t, generation)
	submitBinding, found := generation.LookupDeclaredRoute(http.MethodPost, "/kling/v1/videos/text2video")
	require.True(t, found)
	require.Equal(t, "kling", submitBinding.Plugin.Meta.Key)

	submitRecorder := httptest.NewRecorder()
	submitContext, _ := gin.CreateTestContext(submitRecorder)
	submitContext.Request = httptest.NewRequest(
		http.MethodPost,
		"/kling/v1/videos/text2video",
		bytes.NewBufferString(`{"model_name":"kling-v3","prompt":"a lighthouse"}`),
	)
	submitContext.Request.Header.Set("Content-Type", "application/json")
	submitContext.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
		Generation: generation,
		Plugin:     submitBinding.Plugin,
		Route:      submitBinding.Route,
	})
	common.SetContextKey(submitContext, constant.ContextKeyUserId, 7)
	common.SetContextKey(submitContext, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(submitContext, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(submitContext, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(submitContext, constant.ContextKeyUserQuota, 1_000_000)
	common.SetContextKey(submitContext, constant.ContextKeyTokenId, token.Id)
	common.SetContextKey(submitContext, constant.ContextKeyTokenKey, token.Key)
	submitContext.Set(common.RequestIdKey, "native-kling-financial-request")
	submitContext.Set("token_name", token.Name)

	middleware.PrepareTaskPluginRoute()(submitContext)
	require.False(t, submitContext.IsAborted(), submitRecorder.Body.String())
	require.Equal(t, "kling-v3", submitContext.GetString("resolved_task_model"))
	require.Equal(t, "text_to_video", submitContext.GetString("task_action"))
	require.Nil(t, middleware.SetupContextForSelectedChannel(submitContext, &channel, "kling-v3"))

	relayInfo := &relaycommon.RelayInfo{
		UserId:          7,
		UserGroup:       "default",
		UsingGroup:      "default",
		UserQuota:       1_000_000,
		TokenGroup:      "default",
		OriginModelName: "kling-v3",
		RequestId:       "native-kling-financial-request",
		TokenId:         token.Id,
		TokenKey:        token.Key,
		ForcePreConsume: true,
		UserSetting:     kitdto.UserSetting{BillingPreference: "wallet_only", QuotaWarningEnabled: common.GetPointer(false)},
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: channel.Type, UpstreamModelName: "kling-v3"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action:        submitContext.GetString("task_action"),
			PublicTaskID:  "task_kling_public",
			LockedChannel: &channel,
		},
	}

	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	if err := service.ConfigureFundingFactoryRegistry(registry); err != nil {
		require.EqualError(t, err, "service: funding factory registry already configured")
	}
	// Nonzero true held authorization is captured before the native submit
	// adaptor reprices/reserves its full quote; do not use the free-task bypass.
	session, apiErr := service.NewBillingSession(submitContext, relayInfo, 1)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	relayInfo.Billing = session
	initialHeld := captureTaskSubmissionFinancialState(t, database, relayInfo)
	require.Len(t, initialHeld.Owners, 1)
	require.Equal(t, model.BillingHeld, initialHeld.Owners[0].State)
	require.Equal(t, 1, initialHeld.Owners[0].SourceHeld)
	require.Equal(t, 1, initialHeld.Owners[0].TokenHeld)
	require.Equal(t, token.Id, initialHeld.Owners[0].TokenID)
	require.Equal(t, model.BillingTokenFingerprint(token.Key), initialHeld.Owners[0].TokenFingerprint)
	require.Equal(t, 999_999, initialHeld.User.Quota)
	require.Equal(t, 999_999, initialHeld.Token.RemainQuota)
	require.Len(t, initialHeld.Ledger, 1)
	require.Len(t, initialHeld.Operations, 1)

	outcome, taskErr := executeTaskSubmissionWith(submitContext, relayInfo, relay.RelayTaskSubmit)
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.False(t, submitContext.Writer.Written())

	presentTaskSubmission(submitContext, outcome)
	require.Equal(t, http.StatusOK, submitRecorder.Code)
	assert.Contains(t, submitRecorder.Body.String(), `"task_id":"task_kling_public"`)
	assert.NotContains(t, submitRecorder.Body.String(), "kling-private-1")
	assert.Equal(t, int32(1), submitCalls.Load())

	var persisted model.Task
	require.NoError(t, database.Where("task_id = ?", "task_kling_public").First(&persisted).Error)
	assert.Equal(t, constant.TaskPlatform("kling"), persisted.Platform)
	assert.Equal(t, "kling-private-1", persisted.PrivateData.UpstreamTaskID)
	assert.Equal(t, model.TaskStatus(model.TaskStatusNotStart), persisted.Status)

	submittedQuota := persisted.Quota
	require.Greater(t, submittedQuota, 1, "the original native quote must reach a real downward poll adjustment")
	submitted := captureTaskSubmissionFinancialState(t, database, relayInfo)
	require.Len(t, submitted.Owners, 1)
	require.Equal(t, model.BillingTaskOwned, submitted.Owners[0].State)
	require.Equal(t, submittedQuota, submitted.Owners[0].ActualQuota)
	require.Equal(t, submittedQuota, submitted.Owners[0].SourceHeld)
	require.Equal(t, submittedQuota, submitted.Owners[0].TokenHeld)
	require.Equal(t, persisted.ID, submitted.Owners[0].TaskID)
	require.Equal(t, token.Id, persisted.PrivateData.TokenId)
	require.Equal(t, model.BillingTokenFingerprint(token.Key), persisted.PrivateData.TokenFingerprint)
	require.Equal(t, relayInfo.RequestId, persisted.PrivateData.EffectiveRequestID())
	require.Equal(t, 2, persisted.PrivateData.FinancialVersion)
	require.True(t, persisted.PrivateData.SubmissionAccounted)
	require.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: submittedQuota}}, persisted.PrivateData.WalletDebit)
	require.Equal(t, 1_000_000-submittedQuota, submitted.User.Quota)
	require.Equal(t, 1_000_000-submittedQuota, submitted.Token.RemainQuota)
	require.Equal(t, submittedQuota, submitted.Token.UsedQuota)
	require.Equal(t, submittedQuota, submitted.User.UsedQuota)
	require.Equal(t, 1, submitted.User.RequestCount)
	require.EqualValues(t, submittedQuota, submitted.Channel.UsedQuota)
	require.Len(t, submitted.Ledger, 2)
	require.Len(t, submitted.Operations, 2)

	previousAdaptorFactory := service.GetTaskAdaptorFunc
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		return relay.GetTaskAdaptor(platform)
	}
	t.Cleanup(func() { service.GetTaskAdaptorFunc = previousAdaptorFactory })
	service.DispatchPlatformUpdate(
		context.Background(),
		persisted.Platform,
		map[int][]string{channel.Id: {"kling-private-1"}},
		map[string]*model.Task{"kling-private-1": &persisted},
	)

	require.NoError(t, database.Where("task_id = ?", "task_kling_public").First(&persisted).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), persisted.Status)
	assert.Equal(t, "100%", persisted.Progress)
	assert.Equal(t, 1, persisted.Quota)
	assert.Equal(t, int32(1), queryCalls.Load())
	var settledUser model.User
	require.NoError(t, database.First(&settledUser, 7).Error)
	assert.Equal(t, 999_999, settledUser.Quota)
	assert.Equal(t, 999_999, settledUser.QuotaPaid)

	final := captureTaskSubmissionFinancialState(t, database, relayInfo)
	require.Equal(t, 999_999, final.Token.RemainQuota)
	require.Equal(t, 1, final.Token.UsedQuota)
	require.Equal(t, 1, final.User.UsedQuota)
	require.Equal(t, 1, final.User.RequestCount)
	require.EqualValues(t, 1, final.Channel.UsedQuota)
	require.Equal(t, submitted.Owners, final.Owners, "the original handoff owner is immutable; the task owns its terminal adjustment")
	require.False(t, persisted.SettlementPending)
	require.Nil(t, persisted.PrivateData.FinancialIntent)
	require.Empty(t, persisted.PrivateData.FinancialReviewReason)
	require.NotEmpty(t, persisted.PrivateData.FinancialOperationID)
	require.Equal(t, 1, persisted.PrivateData.FinancialActualQuota)
	require.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 1}}, persisted.PrivateData.WalletDebit)
	require.Len(t, final.Ledger, 3)
	require.Len(t, final.Operations, 3)
	net := 0
	for _, entry := range final.Ledger {
		net += entry.Delta
	}
	require.Equal(t, -1, net)
	require.Len(t, final.Logs, 1, "the original fixture disables consumption logs, but the real downward adjustment is audited")
	require.Equal(t, model.LogTypeRefund, final.Logs[0].Type)
	require.Equal(t, submittedQuota-1, final.Logs[0].Quota)
	require.Equal(t, model.BillingTokenFingerprint(persisted.PrivateData.FinancialOperationID), final.Logs[0].RequestId)
	require.ErrorIs(t, session.Settle(1), model.ErrBillingFinalized)
	session.Refund(submitContext)
	applied, err := service.ApplyTaskQuota(context.Background(), &persisted, 1, false)
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, final, captureTaskSubmissionFinancialState(t, database, relayInfo), "terminal task replay cannot move money, Token, usage or logs twice")

	queryBinding, found := generation.LookupDeclaredRoute(http.MethodGet, "/kling/v1/videos/text2video/:task_id")
	require.True(t, found)
	queryRecorder := httptest.NewRecorder()
	queryContext, _ := gin.CreateTestContext(queryRecorder)
	queryContext.Request = httptest.NewRequest(http.MethodGet, "/kling/v1/videos/text2video/task_kling_public", nil)
	// The inner plugin router names wildcards by position.
	queryContext.Params = gin.Params{{Key: "s4", Value: "task_kling_public"}}
	queryContext.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
		Generation: generation,
		Plugin:     queryBinding.Plugin,
		Route:      queryBinding.Route,
	})
	common.SetContextKey(queryContext, constant.ContextKeyUserId, 7)

	middleware.PrepareTaskPluginRoute()(queryContext)

	require.True(t, queryContext.IsAborted())
	require.Equal(t, http.StatusOK, queryRecorder.Code)
	assert.Contains(t, queryRecorder.Body.String(), `"task_id":"task_kling_public"`)
	assert.Contains(t, queryRecorder.Body.String(), `"task_status":"succeed"`)
	assert.NotContains(t, queryRecorder.Body.String(), "kling-private-1")
	assert.NotContains(t, queryRecorder.Body.String(), upstream.URL)
}

// This added native SSE path uses a real PostgreSQL funding session. The old
// Kling fixture above remains independent and is not run by the PG-only matrix.
func TestDoubaoRelayedImageStreamSettlement(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL 18.6 TEST_POSTGRES_DSN and TEST_TASK_DB_DIALECT=postgres")
	}
	oldGinMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldGinMode) })
	service.InitHttpClient()
	previousDeadline := constant.TaskPluginProtocolTimeoutSeconds
	constant.TaskPluginProtocolTimeoutSeconds = 1
	const seedream = "doubao-seedream-5-0-lite-260128"
	previousPrices := ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"`+seedream+`":0.02}`))
	t.Cleanup(func() {
		constant.TaskPluginProtocolTimeoutSeconds = previousDeadline
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
	})
	const image = `data: {"type":"image_generation.partial_succeeded","model":"` + seedream + `","created":1,"image_index":0,"url":"https://cdn.example/0.png","size":"2048x2048"}` + "\n\n"
	const completed = `data: {"type":"image_generation.completed","model":"` + seedream + `","created":1,"usage":{"generated_images":1,"output_tokens":16384,"total_tokens":16384}}` + "\n\n"
	const done = "data: [DONE]\n\n"
	const rejected = `data: {"error":{"code":"AuthenticationError","message":"rejected"}}` + "\n\n"
	var submitCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		submitCalls.Add(1)
		var request struct {
			Prompt string `json:"prompt"`
		}
		_ = common.DecodeJson(r.Body, &request)
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(frame string) { _, _ = io.WriteString(w, frame); w.(http.Flusher).Flush() }
		switch request.Prompt {
		case "finished", "insert_fails", "task_lost_ack", "fallback_lost_ack", "fallback_rollback", "fallback_log_failure", "separate_log_failure", "separate_log_lost_ack":
			write(image + completed + done)
		case "reserve_rollback", "reserve_lost_ack", "funds_shortage":
			second := strings.ReplaceAll(strings.ReplaceAll(image, `"image_index":0`, `"image_index":1`), "0.png", "1.png")
			write(image + second + strings.ReplaceAll(completed, `"generated_images":1`, `"generated_images":2`) + done)
		case "rejected":
			write(rejected + done)
		case "missing_payload":
			write(completed + done)
		case "broken":
			write(image)
			panic(http.ErrAbortHandler)
		case "eof":
			write(image)
		case "unreadable":
			write(": heartbeat\n\ndata: {\"type\":\n\n")
		case "stalled":
			write(": heartbeat\n\n")
			for {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(100 * time.Millisecond):
					write(": keep-alive\n\n")
				}
			}
		}
	}))
	defer upstream.Close()
	generation := pluginruntime.DefaultRegistry.Generation()
	require.NotNil(t, generation)
	binding, found := generation.LookupDeclaredRoute(http.MethodPost, "/api/v3/images/generations")
	require.True(t, found)
	require.Equal(t, "doubao", binding.Plugin.Meta.Key)
	for _, tc := range []struct {
		prompt, firstFrame string
		charge             int
		unknown            bool
	}{
		{"finished", image, 10_000, false}, {"rejected", rejected, 0, false},
		{"insert_fails", image, 10_000, false},
		{"reserve_rollback", image, 20_000, false}, {"reserve_lost_ack", image, 20_000, false},
		{"task_lost_ack", image, 10_000, false}, {"fallback_lost_ack", image, 10_000, false},
		{"fallback_rollback", image, 30_000, false}, {"fallback_log_failure", image, 30_000, false},
		{"funds_shortage", image, 10_000, false},
		{"separate_log_failure", image, 10_000, false}, {"separate_log_lost_ack", image, 10_000, false},
		{"missing_payload", completed, 30_000, true}, {"broken", image, 30_000, true},
		{"eof", image, 30_000, true}, {"unreadable", ": heartbeat\n\n", 30_000, true},
		{"stalled", ": heartbeat\n\n", 30_000, true},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			initial, held, requestImages := 1_000_000, 30_000, "3"
			if strings.HasPrefix(tc.prompt, "reserve_") || tc.prompt == "funds_shortage" {
				held, requestImages = 10_000, "1"
			}
			if tc.prompt == "funds_shortage" {
				initial = 10_000
			}
			f := newTaskSubmissionFinancialFixtureForModel(t, "native-stream-"+tc.prompt, initial, held, seedream)
			var version string
			require.NoError(t, f.db.Raw("SHOW server_version_num").Scan(&version).Error)
			require.Equal(t, "180006", version)
			f.channel.Type, f.channel.Name, f.channel.Key, f.channel.BaseURL = constant.ChannelTypeDoubaoVideo, "ark-native-stream", "synthetic-ark-key", &upstream.URL
			f.channel.Status, f.channel.Models, f.channel.Group = common.ChannelStatusEnabled, seedream, "default"
			require.NoError(t, f.db.Save(&f.channel).Error)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			f.c = c
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v3/images/generations", strings.NewReader(`{"model":"`+seedream+`","prompt":"`+tc.prompt+`","stream":true,"sequential_image_generation":"auto","sequential_image_generation_options":{"max_images":`+requestImages+`}}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Generation: generation, Plugin: binding.Plugin, Route: binding.Route})
			common.SetContextKey(c, constant.ContextKeyUserId, f.user.Id)
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUserQuota, initial)
			common.SetContextKey(c, constant.ContextKeyTokenId, f.token.Id)
			common.SetContextKey(c, constant.ContextKeyTokenKey, f.token.Key)
			c.Set(common.RequestIdKey, f.info.RequestId)
			c.Set("token_name", f.token.Name)
			middleware.PrepareTaskPluginRoute()(c)
			require.False(t, c.IsAborted(), recorder.Body.String())
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, &f.channel, seedream))
			f.info.Action, f.info.PublicTaskID = c.GetString("task_action"), "task_"+tc.prompt
			f.info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: f.channel.Id, ChannelType: f.channel.Type, UpstreamModelName: seedream}
			var insertFailures *atomic.Int32
			if tc.prompt == "insert_fails" || strings.HasPrefix(tc.prompt, "fallback_") {
				insertFailures = installTaskPostInsertFailure(t, f.db, f.user.Id, f.info.PublicTaskID)
			}
			var logFailures atomic.Int32
			if strings.HasPrefix(tc.prompt, "separate_log_") {
				dsn := os.Getenv("TEST_POSTGRES_LOG_DSN")
				require.NotEmpty(t, dsn, "separate log acceptance requires a dedicated disposable PostgreSQL database")
				logDB, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{NamingStrategy: f.db.NamingStrategy})
				require.NoError(t, err)
				sqlDB, err := logDB.DB()
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
				require.False(t, logDB.Migrator().HasTable(&model.Log{}), "refuse existing log fixtures")
				require.NoError(t, logDB.AutoMigrate(&model.Log{}))
				t.Cleanup(func() { require.NoError(t, logDB.Migrator().DropTable(&model.Log{})) })
				model.LOG_DB = logDB
				callback := logDB.Callback().Create().Before("gorm:create")
				if tc.prompt == "separate_log_lost_ack" {
					callback = logDB.Callback().Create().After("gorm:commit_or_rollback_transaction")
				}
				require.NoError(t, callback.Register("test:separate_submit_log_failure", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Log" && tx.Error == nil {
						logFailures.Add(1)
						tx.AddError(errors.New("synthetic separate-log write rollback"))
					}
				}))
			}
			if tc.prompt == "fallback_log_failure" {
				name := "test:native_fallback_log_failure"
				require.NoError(t, f.db.Callback().Create().After("gorm:create").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Log" && insertFailures.Load() > 0 && tx.Error == nil {
						logFailures.Add(1)
						tx.AddError(errors.New("synthetic post-log-write failure"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, f.db.Callback().Create().Remove(name)) })
			}
			var fault *nativeTaskCommitFaultPool
			beforeCalls := submitCalls.Load()
			outcome, taskErr := executeTaskSubmissionWith(c, f.info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *taskdto.TaskError) {
				result, err := relay.RelayTaskSubmit(c, info)
				if err == nil {
					switch tc.prompt {
					case "reserve_rollback", "reserve_lost_ack", "task_lost_ack":
						fault = installNativeTaskCommitFault(t, f.db, 1, tc.prompt == "reserve_rollback")
					case "fallback_lost_ack", "fallback_rollback":
						// Failed INSERT rolls back before Commit, then owner probe,
						// settlement intent, and the source/token/usage/log commit.
						fault = installNativeTaskCommitFault(t, f.db, 3, tc.prompt == "fallback_rollback")
					}
				}
				return result, err
			})
			waitTaskSubmissionFixtureQuiescence(t)
			assert.Equal(t, beforeCalls+1, submitCalls.Load(), "unknown delivery cannot retry the paid submit")
			assert.True(t, strings.HasPrefix(recorder.Body.String(), tc.firstFrame), recorder.Body.String())
			if tc.unknown {
				require.NotNil(t, taskErr)
				assert.True(t, taskErr.NoRetry)
				assert.True(t, taskErr.DeliveryUnknown)
				assert.Nil(t, outcome)
				f.assertFinancial(t, model.BillingDeliveryUnknown, 30_000, 0, 0, 1)
				f.assertTaskCount(t, 0)
				f.assertUnknownCannotRefund(t)
				relayed := recorder.Body.String()
				respondTaskSubmissionError(c, taskErr)
				assert.Equal(t, relayed, recorder.Body.String(), "no JSON error is appended to native SSE")
				return
			}
			if fault != nil {
				require.True(t, fault.fired.Load(), "the real driver COMMIT boundary must be reached")
			}
			if tc.prompt == "insert_fails" || tc.prompt == "task_lost_ack" || strings.HasPrefix(tc.prompt, "fallback_") || tc.prompt == "funds_shortage" {
				require.NotNil(t, taskErr)
				assert.Equal(t, "task_insert_failed", taskErr.Code)
				assert.Nil(t, outcome)
				if insertFailures != nil {
					assert.EqualValues(t, 1, insertFailures.Load())
				}
				if tc.prompt == "fallback_log_failure" {
					assert.EqualValues(t, 1, logFailures.Load())
				}
				state := captureTaskSubmissionFinancialState(t, f.db, f.info)
				if tc.prompt == "task_lost_ack" {
					require.Len(t, state.Tasks, 1)
				} else {
					assert.Empty(t, state.Tasks)
				}
				require.Len(t, state.Owners, 1)
				wantState := model.BillingSettled
				wantUsed, wantRequests, wantLogs := tc.charge, 1, 1
				if tc.prompt == "task_lost_ack" {
					wantState = model.BillingTaskOwned
				}
				if tc.prompt == "fallback_rollback" || tc.prompt == "fallback_log_failure" || tc.prompt == "funds_shortage" {
					wantState, wantUsed, wantRequests, wantLogs = model.BillingSettlementReview, 0, 0, 0
				}
				assert.Equal(t, wantState, state.Owners[0].State)
				actual := 10_000
				if tc.prompt == "funds_shortage" {
					actual = 20_000
				}
				assert.Equal(t, actual, state.Owners[0].ActualQuota)
				assert.Equal(t, f.initial-tc.charge, state.User.Quota)
				assert.Equal(t, f.initial-tc.charge, state.User.QuotaPaid)
				assert.Zero(t, state.User.QuotaBonus)
				assert.Zero(t, state.User.QuotaReward)
				assert.Equal(t, f.initial-tc.charge, state.Token.RemainQuota)
				assert.Equal(t, tc.charge, state.Token.UsedQuota)
				assert.Equal(t, wantUsed, state.User.UsedQuota)
				assert.Equal(t, wantRequests, state.User.RequestCount)
				assert.EqualValues(t, wantUsed, state.Channel.UsedQuota)
				require.Len(t, state.Logs, wantLogs)
				if wantLogs == 1 {
					assert.Equal(t, tc.charge, state.Logs[0].Quota)
					assert.Equal(t, f.info.RequestId, state.Logs[0].RequestId)
					assert.Contains(t, state.Logs[0].Other, `"request_path":"/api/v3/images/generations"`)
				}
				net := 0
				for _, entry := range state.Ledger {
					net += entry.Delta
				}
				assert.Equal(t, -tc.charge, net)
				assert.False(t, f.info.Billing.NeedsRefund())
				f.info.Billing.Refund(c)
				_ = f.info.Billing.Settle(actual)
				waitTaskSubmissionFixtureQuiescence(t)
				assert.Equal(t, state, captureTaskSubmissionFinancialState(t, f.db, f.info), "replay must not change funds, usage, owner or logs")
				relayed := recorder.Body.String()
				respondTaskSubmissionError(c, taskErr)
				assert.Equal(t, relayed, recorder.Body.String())
				return
			}
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.Equal(t, tc.charge, outcome.Task.Quota)
			status := model.TaskStatusSuccess
			if tc.prompt == "rejected" {
				status = model.TaskStatusFailure
			}
			assert.Equal(t, model.TaskStatus(status), outcome.Task.Status)
			state := captureTaskSubmissionFinancialState(t, f.db, f.info)
			assert.Equal(t, f.initial-tc.charge, state.User.Quota)
			assert.Equal(t, f.initial-tc.charge, state.User.QuotaPaid)
			assert.Equal(t, f.initial-tc.charge, state.Token.RemainQuota)
			assert.Equal(t, tc.charge, state.Token.UsedQuota)
			assert.Equal(t, tc.charge, state.User.UsedQuota)
			assert.Equal(t, 1, state.User.RequestCount)
			assert.EqualValues(t, tc.charge, state.Channel.UsedQuota)
			if strings.HasPrefix(tc.prompt, "separate_log_") {
				assert.Empty(t, state.Logs)
				var count int64
				require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
				want := int64(0)
				if tc.prompt == "separate_log_lost_ack" {
					want = 1
				}
				assert.Equal(t, want, count)
				assert.EqualValues(t, 1, logFailures.Load())
				require.NoError(t, service.PersistTaskBillingAndLog(c, f.info, outcome.Task))
				assert.EqualValues(t, 1, logFailures.Load(), "a replay cannot duplicate a separate-log projection")
			} else {
				require.Len(t, state.Logs, 1)
				assert.Equal(t, tc.charge, state.Logs[0].Quota)
			}
			require.Len(t, state.Owners, 1)
			assert.Equal(t, model.BillingTaskOwned, state.Owners[0].State)
			assert.Equal(t, tc.charge, state.Owners[0].ActualQuota)
			f.info.Billing.Refund(c)
			assert.ErrorIs(t, f.info.Billing.Settle(tc.charge), model.ErrBillingFinalized)
			assert.Equal(t, state, captureTaskSubmissionFinancialState(t, f.db, f.info), "terminal replay cannot alter funds")
			relayed := recorder.Body.String()
			presentTaskSubmission(c, outcome)
			assert.Equal(t, relayed, recorder.Body.String(), "no JSON is appended to native SSE")
		})
	}
}
