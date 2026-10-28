package relay_test

import (
	"errors"
	"fmt"
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
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/pkg/walletport"
	"github.com/QuantumNous/new-api/relay/channel/deepseek"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Exercise controller.Relay, the real HTTP adaptor, retry policy, reservation,
// wallet settlement, token accounting and the separate consume-log database.
// Optional DSNs must refer to disposable test databases; no rows are deleted.
func TestConvertedResponsesFailureAccountingDatabaseMatrix(t *testing.T) {
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	oldConsume, oldErrorLog := common.LogConsumeEnabled, constant.ErrorLogEnabled
	oldCount, oldTimeout, oldRetry := constant.CountToken, constant.StreamingTimeout, common.RetryTimes
	oldMode := gin.Mode()
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	common.LogConsumeEnabled, constant.ErrorLogEnabled = true, false
	constant.CountToken, constant.StreamingTimeout, common.RetryTimes = true, 30, 2
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
		common.LogConsumeEnabled, constant.ErrorLogEnabled = oldConsume, oldErrorLog
		constant.CountToken, constant.StreamingTimeout, common.RetryTimes = oldCount, oldTimeout, oldRetry
		gin.SetMode(oldMode)
	})
	settings := model_setting.GetGlobalSettings()
	previousSettings := *settings
	settings.PassThroughRequestEnabled = false
	settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{Enabled: true, AllChannels: true, ModelPatterns: []string{"^converted-stream-accounting$"}}
	t.Cleanup(func() { *settings = previousSettings })
	quotaSettings := operation_setting.GetQuotaSetting()
	previousQuotaSettings := *quotaSettings
	quotaSettings.TrustQuotaUSD = 0
	t.Cleanup(func() { *quotaSettings = previousQuotaSettings })
	modelRatios, completionRatios := ratio_setting.ModelRatio2JSONString(), ratio_setting.CompletionRatio2JSONString()
	groupRatios, err := common.Marshal(ratio_setting.GetGroupRatioCopy())
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"converted-stream-accounting":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"converted-stream-accounting":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(modelRatios))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(completionRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(groupRatios)))
	})
	policy, policyErr := walletport.Default()
	if errors.Is(policyErr, walletport.ErrNotConfigured) {
		require.NoError(t, walletmodule.RegisterRuntime())
	} else {
		require.NoError(t, policyErr)
		require.IsType(t, walletmodule.Policy{}, policy)
	}
	factories := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(factories))
	require.NoError(t, walletmodule.RegisterFundingFactory(factories))
	if err := service.ConfigureFundingFactoryRegistry(factories); err != nil {
		require.EqualError(t, err, "service: funding factory registry already configured")
	}
	for key, price := range map[string]float64{
		deepseek.SearchToolPriceKey + ":converted-stream-accounting*":     2,
		deepseek.FetchToolPriceKey + ":converted-stream-accounting*":      3,
		dto.BuildInToolWebSearchPreview + ":converted-stream-accounting*": 1,
	} {
		operation_setting.SetToolPriceForTest(key, price)
		t.Cleanup(func() { operation_setting.DeleteToolPriceForTest(key) })
	}
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	for _, dialect := range []struct {
		kind            common.DatabaseType
		mainEnv, logEnv string
	}{
		{common.DatabaseTypeSQLite, "", ""},
		{common.DatabaseTypeMySQL, "TEST_FIXED_MYSQL_DSN", "TEST_FIXED_MYSQL_LOG_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_FIXED_POSTGRES_DSN", "TEST_FIXED_POSTGRES_LOG_DSN"},
	} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			db, logDB := convertedStreamAccountingDB(t, dialect.kind, dialect.mainEnv, dialect.logEnv)
			for _, outcome := range []string{"empty_failure", "failed", "partial_unknown", "empty_eof", "fixed_empty", "fixed_partial"} {
				t.Run("images/"+outcome, func(t *testing.T) {
					runBufferedResponsesAccountingCase(t, db, logDB, types.RelayFormatOpenAIImage, "image_"+outcome)
				})
			}

			for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
				for _, outcome := range []string{"provider_usage", "estimated_usage", "role_only", "delivery_failure", "hosted_tools", "empty_tools"} {
					t.Run(fmt.Sprintf("%s/%s", format, outcome), func(t *testing.T) {
						runConvertedStreamAccountingCase(t, db, logDB, format, outcome)
					})
				}
				for _, outcome := range []string{"failed", "bare_error", "cancelled", "failed_status", "eof", "done_without_terminal", "incomplete", "success", "empty_failure", "empty_eof", "fixed_partial", "fixed_empty", "fixed_empty_eof", "fixed_success_missing_usage", "json_failed", "json_failed_status", "json_cancelled", "json_empty_failure", "json_fixed_partial", "json_fixed_empty", "json_total_only", "json_cancelled_total_only"} {
					t.Run(fmt.Sprintf("buffered/%s/%s", format, outcome), func(t *testing.T) {
						runBufferedResponsesAccountingCase(t, db, logDB, format, outcome)
					})
				}
			}
		})
	}
}

func runConvertedStreamAccountingCase(t *testing.T, db, logDB *gorm.DB, format types.RelayFormat, outcome string) {
	t.Helper()
	const requestedModel = "converted-stream-accounting"
	const paid, bonus = 50000, 100
	toolOnly := outcome == "hosted_tools" || outcome == "empty_tools"
	refundable := outcome == "role_only" || outcome == "empty_tools"
	identity := strings.ReplaceAll(uuid.NewString(), "-", "")
	user := model.User{Username: "stream_" + identity, AffCode: identity, Quota: paid + bonus, QuotaPaid: paid, QuotaBonus: bonus, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "test-stream-" + identity, RemainQuota: paid, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	var calls atomic.Int32
	upstreamRequests := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, readErr.Error(), http.StatusBadRequest)
			return
		}
		upstreamRequests <- r.URL.Path + "\n" + string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: "+`{"type":"response.created","response":{"id":"resp_1","model":"provider-private"}}`+"\n\n")
		if outcome != "role_only" && !toolOnly {
			_, _ = fmt.Fprint(w, "data: "+`{"type":"response.output_text.delta","delta":"partial answer"}`+"\n\n")
		}
		if toolOnly {
			status := "completed"
			if outcome == "empty_tools" {
				status = "failed"
			}
			search := `{"type":"response.output_item.done","output_index":0,"item":{"id":"search-1","type":"web_search_call","status":"` + status + `","action":{"type":"search"}}}`
			// A replayed item and its cumulative terminal are the same call.
			_, _ = fmt.Fprint(w, "data: "+search+"\n\ndata: "+search+"\n\n")
			_, _ = fmt.Fprint(w, "data: "+`{"type":"response.output_item.done","output_index":1,"item":{"id":"fetch-1","type":"web_search_call","status":"`+status+`","action":{"type":"open_page"}}}`+"\n\n")
			_, _ = fmt.Fprint(w, "data: "+`{"type":"response.output_item.done","output_index":2,"item":{"id":"failed-1","type":"web_search_call","status":"failed","action":{"type":"open_page"}}}`+"\n\n")
		}
		usage := ""
		if outcome == "provider_usage" {
			usage = `,"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}`
		}
		if outcome == "delivery_failure" {
			_, _ = fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":900,"output_tokens":900,"total_tokens":1800}}}`+"\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: "+`{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","message":"provider-private failed"}`+usage+`}}`+"\n\n")
	}))
	t.Cleanup(upstream.Close)
	channel := model.Channel{Name: "stream-" + identity, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Key: "test-only", BaseURL: common.GetPointer(upstream.URL), Models: requestedModel, Group: "default", AutoBan: common.GetPointer(0)}
	if toolOnly {
		channel.Type = constant.ChannelTypeDeepSeek
	}
	path := "/v1/chat/completions"
	body := `{"model":"` + requestedModel + `","messages":[{"role":"user","content":"Please return a helpful answer to this short test request"}],"stream":true}`
	if format == types.RelayFormatClaude {
		path = "/v1/messages"
		body = `{"model":"` + requestedModel + `","messages":[{"role":"user","content":"Please return a helpful answer to this short test request"}],"max_tokens":32,"stream":true}`
	} else if format == types.RelayFormatGemini {
		path = "/v1beta/models/" + requestedModel + ":streamGenerateContent?alt=sse"
		body = `{"contents":[{"role":"user","parts":[{"text":"Please return a helpful answer to this short test request"}]}],"generationConfig":{"maxOutputTokens":32}}`
		channel.Type = constant.ChannelTypeAdvancedCustom
		channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: "/v1beta/models/{model}:streamGenerateContent", UpstreamPath: "/v1/responses", Converter: relayconvert.ConverterGeminiContentToOpenAIResponses,
		}}}})
	}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(db))
	recorder := httptest.NewRecorder()
	var writer http.ResponseWriter = recorder
	if outcome == "delivery_failure" {
		writer = &convertedStreamClosedWriter{ResponseRecorder: recorder}
	}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	for key, value := range map[constant.ContextKey]any{
		constant.ContextKeyUserId: user.Id, constant.ContextKeyUserQuota: paid + bonus,
		constant.ContextKeyUserGroup: "default", constant.ContextKeyTokenGroup: "default", constant.ContextKeyUsingGroup: "default",
		constant.ContextKeyTokenId: token.Id, constant.ContextKeyTokenKey: token.Key,
		constant.ContextKeyUserSetting: dto.UserSetting{BillingPreference: "wallet_only"},
	} {
		common.SetContextKey(c, key, value)
	}
	c.Set(common.RequestIdKey, identity)
	c.Set("username", user.Username)
	c.Set("token_quota", paid)
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channel, requestedModel))
	completion := observeNativeResponsesRefundWithin(t, db, identity, refundable, 5*time.Second, &token)

	controller.Relay(c, format)
	completion.join(t)

	require.Equal(t, int32(1), calls.Load(), "retry budget is enabled, but a handled stream must not start another upstream attempt: %s", recorder.Body.String())
	assert.Equal(t, 1, service.RequestPolicy(c).Attempts)
	assert.False(t, service.RequestPolicy(c).Successful, "settling generated usage must not mark a failed response successful")
	request := <-upstreamRequests
	wantPath := "/v1/responses\n"
	if channel.Type == constant.ChannelTypeDeepSeek {
		wantPath = "/responses\n"
	}
	assert.True(t, strings.HasPrefix(request, wantPath), request)
	assert.Contains(t, request, `"input":`)
	assert.Contains(t, request, `"stream":true`)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled))
	wire := recorder.Body.String()
	wantErrors := 1
	if outcome == "delivery_failure" {
		wantErrors = 0
	}
	assert.Equal(t, wantErrors, strings.Count(wire, `"error":`), "the outer controller must not append JSON after a handled stream failure")
	assert.NotContains(t, wire, "provider-private")
	assert.NotContains(t, wire, "[DONE]")
	assert.NotContains(t, wire, "event: message_stop")
	assert.NotContains(t, wire, `"finishReason":"STOP"`)
	assert.NotContains(t, wire, `"finish_reason":"stop"`)
	var logs []model.Log
	require.NoError(t, logDB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
	wantCharge := 0
	if refundable {
		assert.Empty(t, logs)
	} else {
		require.Len(t, logs, 1, wire)
		if outcome == "hosted_tools" {
			wantCharge = 2500 // one Search at $2/1K and one Fetch at $3/1K
			wantTools := []service.ToolSurchargeItem{{Name: deepseek.FetchToolPriceKey, Count: 1, Price: 3}, {Name: deepseek.SearchToolPriceKey, Count: 1, Price: 2}}
			if format == types.RelayFormatGemini {
				wantCharge = 1000 // generic Responses channel: two hosted searches at $1/1K
				wantTools = []service.ToolSurchargeItem{{Name: dto.BuildInToolWebSearchPreview, Count: 2, Price: 1}}
			}
			assert.Zero(t, logs[0].PromptTokens)
			assert.Zero(t, logs[0].CompletionTokens)
			var other struct {
				Tools []service.ToolSurchargeItem `json:"tool_surcharges"`
			}
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			assert.Equal(t, wantTools, other.Tools, "terminal omission and duplicate stream items must preserve exactly the completed calls")
		} else if outcome == "provider_usage" {
			wantCharge = 12
			assert.Equal(t, 10, logs[0].PromptTokens)
			assert.Equal(t, 2, logs[0].CompletionTokens)
		} else {
			assert.Positive(t, logs[0].PromptTokens)
			assert.Positive(t, logs[0].CompletionTokens)
			wantCharge = logs[0].PromptTokens + logs[0].CompletionTokens
			assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
			if outcome == "delivery_failure" {
				assert.Less(t, wantCharge, 1800, "settle the observed partial output, without reading a later upstream terminal")
			}
		}
		assert.Equal(t, wantCharge, logs[0].Quota)
		assert.Equal(t, requestedModel, logs[0].ModelName)
		assert.True(t, logs[0].IsStream)
	}
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, paid-wantCharge, user.QuotaPaid)
	assert.Equal(t, bonus, user.QuotaBonus, "an unconfigured model must not consume bonus funds")
	assert.Equal(t, paid+bonus-wantCharge, user.Quota)
	assert.Equal(t, paid-wantCharge, token.RemainQuota)
	assert.Equal(t, wantCharge, token.UsedQuota)
	var ledger []model.BalanceLedger
	require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&ledger).Error)
	require.GreaterOrEqual(t, len(ledger), 2, "real reservation must be adjusted or refunded")
	assert.Negative(t, ledger[0].Delta)
	net := 0
	for _, row := range ledger {
		assert.Equal(t, string(model.AccountPaid), row.Account)
		net += row.Delta
	}
	assert.Equal(t, -wantCharge, net)
}

type convertedStreamClosedWriter struct {
	*httptest.ResponseRecorder
}

func (w *convertedStreamClosedWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "partial answer") {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(p)
}

func (w *convertedStreamClosedWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func convertedStreamAccountingDB(t *testing.T, kind common.DatabaseType, mainEnv, logEnv string) (*gorm.DB, *gorm.DB) {
	t.Helper()
	open := func(dsn string) *gorm.DB {
		var driver gorm.Dialector = sqlite.Open(":memory:")
		switch kind {
		case common.DatabaseTypeMySQL:
			driver = mysql.Open(dsn)
		case common.DatabaseTypePostgreSQL:
			driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
		}
		db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
		return db
	}
	mainDSN, logDSN := os.Getenv(mainEnv), os.Getenv(logEnv)
	if kind != common.DatabaseTypeSQLite && (mainDSN == "" || logDSN == "") {
		t.Skipf("%s and %s are required for the separate-log database case", mainEnv, logEnv)
	}
	db, logDB := open(mainDSN), open(logDSN)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, logDB
	common.SetDatabaseTypes(kind, kind)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldType, oldLogType)
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Option{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.BalanceLedger{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	versionQuery := "select version()"
	if kind == common.DatabaseTypeSQLite {
		versionQuery = "select sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s", version)
	return db, logDB
}
