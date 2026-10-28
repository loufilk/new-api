package controller

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
	require.NoError(t, database.AutoMigrate(
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
		&model.BalanceLedger{},
	))
	model.DB = database
	model.LOG_DB = database
	common.MemoryCacheEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = false
	common.RedisEnabled = false
	common.DataExportEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	previousModelRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"kling-v1":1}`))
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
	require.NoError(t, database.Create(&model.Model{ModelName: "kling-v1", BonusEligible: common.GetPointer(false), Status: 1}).Error)
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
			assert.Contains(t, string(body), `"model_name":"kling-v1"`)
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
		Models:  "kling-v1",
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
		bytes.NewBufferString(`{"model_name":"kling-v1","prompt":"a lighthouse"}`),
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
	require.Equal(t, "kling-v1", submitContext.GetString("resolved_task_model"))
	require.Equal(t, "text_to_video", submitContext.GetString("task_action"))
	require.Nil(t, middleware.SetupContextForSelectedChannel(submitContext, &channel, "kling-v1"))

	relayInfo := &relaycommon.RelayInfo{
		UserId:          7,
		UserGroup:       "default",
		UsingGroup:      "default",
		UserQuota:       1_000_000,
		TokenGroup:      "default",
		OriginModelName: "kling-v1",
		RequestId:       "native-kling-financial-request",
		TokenId:         token.Id,
		TokenKey:        token.Key,
		ForcePreConsume: true,
		UserSetting:     kitdto.UserSetting{BillingPreference: "wallet_only", QuotaWarningEnabled: common.GetPointer(false)},
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: channel.Type, UpstreamModelName: "kling-v1"},
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
	queryContext.Params = gin.Params{{Key: "task_id", Value: "task_kling_public"}}
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
