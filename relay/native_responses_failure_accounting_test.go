package relay_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/pkg/walletport"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The native HTTP handler and WebSocket event accumulator share settlement.
// Exercise that boundary with real reservations, wallet/token transactions and
// a separate consume-log database, including successful missing-usage controls.
func TestNativeResponsesFailureFixedAccountingDatabaseMatrix(t *testing.T) {
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	oldConsume, oldCount, oldTimeout := common.LogConsumeEnabled, constant.CountToken, constant.StreamingTimeout
	oldQuota, oldMode := common.QuotaPerUnit, gin.Mode()
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	common.LogConsumeEnabled, constant.CountToken, constant.StreamingTimeout = true, true, 30
	common.QuotaPerUnit = 500000
	gin.SetMode(gin.TestMode)
	settings := model_setting.GetGlobalSettings()
	oldPassThrough := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
		common.LogConsumeEnabled, constant.CountToken, constant.StreamingTimeout = oldConsume, oldCount, oldTimeout
		common.QuotaPerUnit = oldQuota
		gin.SetMode(oldMode)
		settings.PassThroughRequestEnabled = oldPassThrough
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
	const toolPriceKey = dto.BuildInToolWebSearchPreview + ":native-responses-fixed*"
	operation_setting.SetToolPriceForTest(toolPriceKey, 1)
	t.Cleanup(func() { operation_setting.DeleteToolPriceForTest(toolPriceKey) })

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
			for _, transport := range []string{"http_sse", "websocket_accumulator"} {
				for _, tc := range []struct {
					name      string
					events    []string
					wantQuota int
				}{
					{"leading failed", []string{`{"type":"response.failed","response":{"status":"failed"}}`}, 0},
					{"leading error", []string{`{"type":"error","code":"server_error","message":"failed"}`}, 0},
					{"error request echo is not output", []string{`{"type":"error","text":"echoed request","arguments":"echoed arguments","input":"echoed input"}`}, 0},
					{"created then response error", []string{`{"type":"response.created"}`, `{"type":"response.error","message":"failed"}`}, 0},
					{"completed with failed status", []string{`{"type":"response.completed","response":{"status":"failed"}}`}, 0},
					{"done with cancelled status", []string{`{"type":"response.done","response":{"status":"cancelled"}}`}, 0},
					{"partial text failure", []string{`{"type":"response.output_text.delta","delta":"partial answer"}`, `{"type":"response.failed"}`}, 5000},
					{"reported usage failure", []string{`{"type":"response.failed","response":{"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`}, 5000},
					{"cached usage failure", []string{`{"type":"response.failed","response":{"usage":{"input_tokens_details":{"cached_tokens":3}}}}`}, 5000},
					{"completed tool failure", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"search-1","type":"web_search_call","status":"completed"}}`, `{"type":"response.failed"}`}, 5500},
					{"completed tool cancellation", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"search-1","type":"web_search_call","status":"completed"}}`, `{"type":"response.cancelled"}`}, 5500},
					{"done only function arguments failure", []string{`{"type":"response.output_item.done","item":{"id":"fn-1","type":"function_call","name":"unpriced_fn","arguments":"{}"}}`, `{"type":"response.failed"}`}, 5000},
					{"completed function without arguments failure", []string{`{"type":"response.output_item.done","item":{"id":"fn-1","type":"function_call","name":"unpriced_fn","status":"completed","arguments":""}}`, `{"type":"response.failed"}`}, 5000},
					{"completed computer action failure", []string{`{"type":"response.output_item.done","item":{"id":"computer-1","type":"computer_call","status":"completed","action":{"type":"click","x":1,"y":2}}}`, `{"type":"response.failed"}`}, 5000},
					{"partial computer action failure", []string{`{"type":"response.output_item.added","item":{"id":"computer-1","type":"computer_call","status":"in_progress","action":{"type":"click","x":1,"y":2}}}`, `{"type":"response.failed"}`}, 5000},
					{"partial patch operation failure", []string{`{"type":"response.output_item.added","item":{"id":"patch-1","type":"apply_patch_call","status":"in_progress","operation":{"type":"update_file","path":"test.txt","diff":"generated patch"}}}`, `{"type":"response.failed"}`}, 5000},
					{"empty action shell is not output", []string{`{"type":"response.output_item.added","item":{"id":"computer-1","type":"computer_call","status":"in_progress","action":{}}}`, `{"type":"response.failed"}`}, 0},
					{"failed empty tool is not output", []string{`{"type":"response.output_item.done","item":{"id":"fn-1","type":"function_call","name":"unpriced_fn","status":"failed","arguments":""}}`, `{"type":"response.failed"}`}, 0},
					{"in progress empty tool is not output", []string{`{"type":"response.output_item.added","item":{"id":"fn-1","type":"function_call","name":"unpriced_fn","status":"in_progress","arguments":""}}`, `{"type":"response.failed"}`}, 0},
					{"created response tool shell is not completed", []string{`{"type":"response.created","response":{"status":"in_progress","output":[{"id":"fn-1","type":"function_call","name":"unpriced_fn"}]}}`, `{"type":"response.failed"}`}, 0},
					{"custom tool input delta failure", []string{`{"type":"response.custom_tool_call_input.delta","delta":"generated tool input"}`, `{"type":"response.failed"}`}, 5000},
					{"mcp arguments done failure", []string{`{"type":"response.mcp_call_arguments.done","arguments":"{}"}`, `{"type":"response.failed"}`}, 5000},
					{"code interpreter delta failure", []string{`{"type":"response.code_interpreter_call_code.delta","delta":"print(1)"}`, `{"type":"response.failed"}`}, 5000},
					{"code interpreter done failure", []string{`{"type":"response.code_interpreter_call_code.done","code":"print(1)"}`, `{"type":"response.failed"}`}, 5000},
					{"generated audio delta failure", []string{`{"type":"response.audio.delta","delta":"YXVkaW8="}`, `{"type":"response.failed"}`}, 5000},
					{"audio transcript delta failure", []string{`{"type":"response.audio_transcript.delta","delta":"partial transcript"}`, `{"type":"response.failed"}`}, 5000},
					{"terminal function arguments failure", []string{`{"type":"response.failed","response":{"output":[{"type":"function_call","name":"unpriced_fn","arguments":"{}"}]}}`}, 5000},
					{"terminal reasoning failure", []string{`{"type":"response.failed","response":{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"partial reasoning"}]}]}}`}, 5000},
					{"encrypted reasoning failure", []string{`{"type":"response.failed","response":{"output":[{"type":"reasoning","summary":[],"encrypted_content":"opaque-generated-reasoning"}]}}`}, 5000},
					{"terminal refusal failure", []string{`{"type":"response.failed","response":{"output":[{"type":"message","content":[{"type":"refusal","refusal":"I cannot answer"}]}]}}`}, 5000},
					{"done only message failure", []string{`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"partial answer"}]}}`, `{"type":"response.failed"}`}, 5000},
					{"successful missing usage", []string{`{"type":"response.completed","response":{"status":"completed"}}`}, 5000},
					{"created only disconnect", []string{`{"type":"response.created","response":{"status":"in_progress"}}`}, 5000},
					{"successful nil usage dispatch", nil, 5000},
				} {
					t.Run(transport+"/"+tc.name, func(t *testing.T) {
						c, info, user, token := nativeResponsesBillingReservation(t, db)
						var usage *dto.Usage
						if len(tc.events) > 0 {
							if transport == "http_sse" {
								body := "data: " + strings.Join(tc.events, "\n\ndata: ") + "\n\n"
								resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
								var apiErr *types.NewAPIError
								usage, apiErr = openai.OaiResponsesStreamHandler(c, info, resp)
								if tc.name != "successful missing usage" {
									assert.NotNil(t, apiErr)
									assert.True(t, types.IsSkipRetryError(apiErr))
								}
							} else {
								accumulator := service.NewResponsesUsageAccumulator(info)
								for _, raw := range tc.events {
									var event dto.ResponsesStreamResponse
									require.NoError(t, common.UnmarshalJsonStr(raw, &event))
									accumulator.Observe(&event, []byte(raw))
								}
								usage = accumulator.Finish()
								assert.Equal(t, usage, accumulator.Finish(), "finishing a call twice must not add tool usage")
							}
						}
						relay.ConsumeResponsesQuota(c, info, usage)
						assertNativeResponsesBillingBalance(t, db, user, token, tc.wantQuota)
						var logs []model.Log
						require.NoError(t, logDB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
						if tc.wantQuota == 0 {
							assert.Empty(t, logs, "an empty explicit failure must not record a successful fixed-fee consumption")
						} else {
							require.Len(t, logs, 1)
							assert.Equal(t, tc.wantQuota, logs[0].Quota)
						}
						var ledger []model.BalanceLedger
						require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&ledger).Error)
						require.NotEmpty(t, ledger, "the test must include a real wallet reservation")
						assert.Equal(t, -5000, ledger[0].Delta)
						net := 0
						for _, row := range ledger {
							assert.Equal(t, string(model.AccountPaid), row.Account)
							net += row.Delta
						}
						assert.Equal(t, -tc.wantQuota, net)
						require.NoError(t, info.Billing.Settle(tc.wantQuota))
						info.Billing.Refund(c)
						assertNativeResponsesBillingBalance(t, db, user, token, tc.wantQuota)
						var ledgerAfter int64
						require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ?", user.Id).Count(&ledgerAfter).Error)
						assert.Equal(t, int64(len(ledger)), ledgerAfter, "settlement replay and outer refund must not move money twice")
					})
				}
			}
			for _, status := range []string{"failed", "error", "cancelled", "canceled"} {
				for _, tc := range []struct {
					name, fields string
					wantQuota    int
				}{
					{"empty", `"output":[]`, 0},
					{"echoed request input", `"input":"echoed request","output":[]`, 0},
					{"partial usage", `"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}`, 5000},
					{"completed tool", `"output":[{"id":"search-1","type":"web_search_call","status":"completed"}]`, 5500},
					{"unpriced arguments", `"output":[{"type":"function_call","name":"unpriced_fn","arguments":"{}"}]`, 5000},
				} {
					t.Run("http_nonstream/"+status+"/"+tc.name, func(t *testing.T) {
						body := `{"id":"resp_failed","status":"` + status + `","error":{"code":"server_error","message":"private provider failure"},` + tc.fields + `}`
						runNativeResponsesRequestBilling(t, db, logDB, body, false, tc.wantQuota, true, constant.ChannelTypeOpenAI)
					})
				}
			}
			t.Run("http_nonstream/success missing usage", func(t *testing.T) {
				runNativeResponsesRequestBilling(t, db, logDB, `{"status":"completed","output":[]}`, false, 5000, false, constant.ChannelTypeOpenAI)
			})
			for _, tc := range []struct {
				name, event string
				wantQuota   int
			}{
				{"empty failed", `{"type":"response.failed","response":{"status":"failed"}}`, 0},
				{"failed partial usage", `{"type":"response.failed","response":{"status":"failed","usage":{"input_tokens":10,"output_tokens":2}}}`, 5000},
				{"done with cancelled status", `{"type":"response.done","response":{"status":"cancelled"}}`, 0},
			} {
				t.Run("http_request/"+tc.name, func(t *testing.T) {
					runNativeResponsesRequestBilling(t, db, logDB, "data: "+tc.event+"\n\n", true, tc.wantQuota, true, constant.ChannelTypeOpenAI)
				})
			}
			for _, tc := range []struct{ name, body string }{
				{"empty candidates", `{"candidates":[],"usageMetadata":{"totalTokenCount":0}}`},
				{"blocked prompt", `{"candidates":[],"promptFeedback":{"blockReason":"SAFETY"}}`},
			} {
				t.Run("http_gemini/"+tc.name, func(t *testing.T) {
					runNativeResponsesRequestBilling(t, db, logDB, tc.body, false, 0, true, constant.ChannelTypeGemini)
				})
			}
			t.Run("zero settlement transaction failure recovers reserve", func(t *testing.T) {
				c, info, user, token := nativeResponsesBillingReservation(t, db)
				info.StreamStatus.MarkFailed("server_error", "", 0)
				var failed atomic.Bool
				callback := "native_zero_settlement_" + info.RequestId
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" && !failed.Swap(true) {
						tx.AddError(errors.New("injected token quota transaction failure"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
				completion := observeNativeResponsesRefund(t, db, info.RequestId, true)
				relay.ConsumeResponsesQuota(c, info, nil)
				require.True(t, failed.Load(), "the real wallet/token settlement must encounter the injected rollback")
				completion.join(t)
				assertNativeResponsesBillingBalance(t, db, user, token, 0)
				info.Billing.Refund(c)
				assertNativeResponsesBillingBalance(t, db, user, token, 0)
				var net int
				require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ?", user.Id).Select("SUM(delta)").Scan(&net).Error)
				assert.Zero(t, net, "the failed adjustment must roll back before the ordinary refund")
			})
		})
	}
}

func runNativeResponsesRequestBilling(t *testing.T, db, logDB *gorm.DB, body string, stream bool, wantQuota int, wantError bool, channelType int) {
	t.Helper()
	c, info, user, token := nativeResponsesBillingReservation(t, db)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		contentType := "application/json"
		if stream {
			contentType = "text/event-stream"
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)
	info.IsStream = stream
	if !stream {
		info.StreamStatus = nil
	}
	info.RelayMode = relayconstant.RelayModeResponses
	info.RequestURLPath = "/v1/responses"
	info.Request = &dto.OpenAIResponsesRequest{Model: info.OriginModelName, Input: []byte(`"hi"`), Stream: &stream}
	for key, value := range map[constant.ContextKey]any{
		constant.ContextKeyChannelId: info.ChannelId, constant.ContextKeyChannelType: channelType,
		constant.ContextKeyChannelBaseUrl: upstream.URL, constant.ContextKeyChannelKey: "test-only",
		constant.ContextKeyOriginalModel: info.OriginModelName,
	} {
		common.SetContextKey(c, key, value)
	}
	completion := observeNativeResponsesRefund(t, db, info.RequestId, wantQuota == 0)
	apiErr := relay.ResponsesHelper(c, info)
	if wantError {
		require.NotNil(t, apiErr)
		if channelType == constant.ChannelTypeOpenAI {
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiErr))
		} else {
			assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, apiErr.StatusCode)
		}
	} else {
		require.Nil(t, apiErr)
	}
	relay.RefundFailedRequestBilling(c, info, apiErr)
	completion.join(t)
	assert.Equal(t, int32(1), calls.Load())
	assertNativeResponsesBillingBalance(t, db, user, token, wantQuota)
	if wantError {
		assert.False(t, service.RequestPolicy(c).Successful, "partial settlement must not mark a failed response successful")
	}
	var logs []model.Log
	require.NoError(t, logDB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
	if wantQuota == 0 {
		assert.Empty(t, logs)
	} else {
		require.Len(t, logs, 1)
		assert.Equal(t, wantQuota, logs[0].Quota)
		assert.Equal(t, stream, logs[0].IsStream)
	}
}

func nativeResponsesBillingReservation(t *testing.T, db *gorm.DB) (*gin.Context, *relaycommon.RelayInfo, model.User, model.Token) {
	t.Helper()
	identity := strings.ReplaceAll(uuid.NewString(), "-", "")
	user := model.User{Username: "native_" + identity, AffCode: identity, Quota: 50100, QuotaPaid: 50000, QuotaBonus: 100, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "native-stream-" + identity, RemainQuota: 50000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "native-" + identity, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "test-only"}
	require.NoError(t, db.Create(&channel).Error)
	const expression = `tier("request", fixed(0.01))`
	request := &billingexpr.RequestInput{Body: []byte(`{}`)}
	cost, trace, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{P: 8, Len: 8}, *request)
	require.NoError(t, err)
	reservation, err := billingexpr.QuotaRoundStrict(cost / 1_000_000 * common.QuotaPerUnit)
	require.NoError(t, err)
	require.Equal(t, 5000, reservation)
	info := &relaycommon.RelayInfo{
		RequestId: identity, UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
		UserQuota:       user.Quota,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"},
		OriginModelName: "native-responses-fixed", UsingGroup: "default", UserGroup: "default",
		UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningEnabled: new(bool)}, ForcePreConsume: true,
		StartTime: time.Now(), IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAIResponses,
		StreamStatus:          relaycommon.NewStreamStatus(),
		PriceData:             hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), QuotaPerUnit: common.QuotaPerUnit, GroupRatio: 1, EstimatedTier: trace.MatchedTier, EstimatedBillingUnit: trace.BillingUnit, EstimatedFixedPrice: trace.FixedPrice, EstimatedQuotaAfterGroup: reservation},
		BillingRequestInput:   request,
	}
	info.SetEstimatePromptTokens(8)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, identity)
	require.Nil(t, service.PreConsumeBilling(c, reservation, info))
	require.NotNil(t, info.Billing)
	return c, info, user, token
}

func assertNativeResponsesBillingBalance(t *testing.T, db *gorm.DB, user model.User, token model.Token, charge int) {
	t.Helper()
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 50000-charge, user.QuotaPaid)
	assert.Equal(t, 100, user.QuotaBonus)
	assert.Zero(t, user.QuotaReward)
	assert.Equal(t, 50100-charge, user.Quota)
	assert.Equal(t, 50000-charge, token.RemainQuota)
	assert.Equal(t, charge, token.UsedQuota)
}

// A token UPDATE callback runs before the outer transaction and post-commit
// cache work finish. Observe the real driver's result and the request-owned
// refund context instead; unrelated default-pool workers are not join evidence.
type nativeResponsesRefundCompletion struct {
	requestID  string
	db         *gorm.DB
	expect     bool
	budget     time.Duration
	deadline   time.Time
	token      *model.Token
	driverWait chan *nativeResponsesRefundTransaction
	mu         sync.Mutex
	records    map[context.Context]*nativeResponsesRefundTransaction
	next       string
	admitted   bool
	invalid    error
	refund     chan *nativeResponsesRefundTransaction
	metadata   chan *nativeResponsesRefundTransaction
	once       sync.Once
	joined     bool
	err        error
}

type nativeResponsesRefundPool struct {
	*sql.DB
	completion *nativeResponsesRefundCompletion
}

type nativeResponsesRefundTransaction struct {
	*sql.Tx
	completion   *nativeResponsesRefundCompletion
	ctx          context.Context
	role         string
	finished     chan struct{}
	once         sync.Once
	committed    bool
	matchesToken bool
	tokenDelta   int
	err          error
}

func (p *nativeResponsesRefundPool) GetDBConn() (*sql.DB, error) { return p.DB, nil }

func (p *nativeResponsesRefundPool) BeginTx(ctx context.Context, options *sql.TxOptions) (gorm.ConnPool, error) {
	r := &nativeResponsesRefundTransaction{completion: p.completion, ctx: ctx, finished: make(chan struct{})}
	p.completion.mu.Lock()
	p.completion.records[ctx] = r
	p.completion.mu.Unlock()
	var err error
	r.Tx, err = p.DB.BeginTx(ctx, options)
	if err != nil {
		r.finish(false, err)
		return nil, err
	}
	return r, nil
}

func (r *nativeResponsesRefundTransaction) Commit() error {
	err := r.Tx.Commit()
	r.finish(err == nil, err)
	return err
}

func (r *nativeResponsesRefundTransaction) Rollback() error {
	err := r.Tx.Rollback()
	r.finish(false, err)
	return err
}

func (r *nativeResponsesRefundTransaction) finish(committed bool, err error) {
	r.once.Do(func() {
		m := r.completion
		m.mu.Lock()
		r.committed, r.err = committed, err
		if r.role == "admission" && committed {
			m.admitted, m.next = true, "refund"
		} else if r.role == "refund" && !committed {
			m.next = "metadata"
		}
		m.mu.Unlock()
		close(r.finished)
	})
}

func observeNativeResponsesRefund(t *testing.T, db *gorm.DB, requestID string, expect bool) *nativeResponsesRefundCompletion {
	t.Helper()
	return observeNativeResponsesRefundWithin(t, db, requestID, expect, 8*time.Second, nil)
}

func observeNativeResponsesRefundWithin(t *testing.T, db *gorm.DB, requestID string, expect bool, budget time.Duration, token *model.Token) *nativeResponsesRefundCompletion {
	t.Helper()
	require.Positive(t, budget, "refund completion budget must be positive")
	if token != nil {
		copy := *token
		token = &copy
	}
	driver, err := db.DB()
	require.NoError(t, err)
	m := &nativeResponsesRefundCompletion{requestID: requestID, db: db, expect: expect, budget: budget, token: token,
		records: make(map[context.Context]*nativeResponsesRefundTransaction),
		refund:  make(chan *nativeResponsesRefundTransaction, 1), metadata: make(chan *nativeResponsesRefundTransaction, 1)}
	previous := model.DB
	// A non-nil Context clones Statement as well as Config; do not mutate the
	// original DB's connection pool or change its actual driver/transactions.
	observed := previous.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	pool := &nativeResponsesRefundPool{DB: driver, completion: m}
	observed.ConnPool, observed.Statement.ConnPool = pool, pool
	model.DB = observed
	callback := "native_refund_completion_" + requestID
	require.NoError(t, db.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
		if m.token != nil {
			if delta, ok := nativeResponsesTokenQuotaDelta(tx, m.token.Id); ok && delta > 0 {
				m.mu.Lock()
				if r := m.records[tx.Statement.Context]; r != nil && r.role == "refund" && r.matchesToken {
					r.tokenDelta = delta
				}
				m.mu.Unlock()
			}
		}
		owner, ok := tx.Statement.Model.(*model.BillingFinalization)
		values, mapped := tx.Statement.Dest.(map[string]any)
		if !ok || owner.RequestID != requestID || !mapped || values["state"] != model.BillingRefundReady || tx.Error != nil || tx.RowsAffected != 1 {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if r := m.records[tx.Statement.Context]; r != nil {
			r.role = "admission"
		} else {
			m.invalid = errors.New("refund admission bypassed the observed real transaction")
		}
	}))
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*model.BillingFinalization); !ok {
			return
		}
		matches := false
		for _, value := range tx.Statement.Vars {
			if key, ok := value.(string); ok && key == requestID {
				matches = true
			}
		}
		if !matches {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		r := m.records[tx.Statement.Context]
		if m.token != nil && r != nil && tx.Error == nil && tx.RowsAffected == 1 {
			owner, ok := tx.Statement.Dest.(*model.BillingFinalization)
			r.matchesToken = ok && owner.RequestID == requestID && owner.UserID == m.token.UserId && owner.TokenID == m.token.Id && owner.TokenFingerprint == model.BillingTokenFingerprint(m.token.Key)
		}
		if m.next == "" {
			return
		}
		if r == nil {
			m.invalid = errors.New("refund request query bypassed the observed real transaction")
			return
		}
		r.role, m.next = m.next, ""
		if r.role == "refund" {
			m.refund <- r
		} else {
			m.metadata <- r
		}
	}))
	// Registered after the fault callback and DB fixture: even Fatal paths join
	// before removing callbacks, restoring model.DB, closing DBs or globals.
	t.Cleanup(func() {
		m.join(t)
		require.NoError(t, db.Callback().Query().Remove(callback))
		require.NoError(t, db.Callback().Update().Remove(callback))
		model.DB = previous
	})
	return m
}

func (m *nativeResponsesRefundCompletion) wait() (bool, error) {
	m.mu.Lock()
	admitted, invalid := m.admitted, m.invalid
	m.mu.Unlock()
	if invalid != nil || m.expect && !admitted {
		return false, fmt.Errorf("admission: expected=%t admitted=%t observation=%v", m.expect, admitted, invalid)
	}
	if !admitted {
		return true, nil // Synchronous settlement has already returned.
	}
	// Production has a 5 s refund transaction, at most 500 ms post-commit
	// compensation, then a separate 1 s error-metadata transaction. No sleep or
	// SQL-visible balance is treated as callback completion.
	deadline := m.deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(m.budget)
	}
	budget, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	phase := "refund"
	for channel := m.refund; ; channel = m.metadata {
		var r *nativeResponsesRefundTransaction
		select {
		case r = <-channel:
		case <-budget.Done():
			return false, fmt.Errorf("%s transaction observation: %w", phase, budget.Err())
		}
		if m.driverWait != nil && phase == "refund" {
			m.driverWait <- r // The barrier test observes this exact driver wait, not goroutine startup.
		}
		select {
		case <-r.finished:
		case <-budget.Done():
			return false, fmt.Errorf("%s real driver completion: %w", phase, budget.Err())
		}
		if phase == "refund" && !r.committed {
			// The original context is cancelled BEFORE retry metadata is written.
			// Joining it alone would leave another transaction using this fixture.
			phase = "metadata"
			continue
		}
		select {
		case <-r.ctx.Done():
		case <-budget.Done():
			return false, fmt.Errorf("%s post-commit tail: %w", phase, budget.Err())
		}
		if !errors.Is(r.ctx.Err(), context.Canceled) {
			return false, fmt.Errorf("%s deadline is not an owned tail completion: %v", phase, r.ctx.Err())
		}
		m.mu.Lock()
		invalid = m.invalid
		m.mu.Unlock()
		if invalid != nil {
			return false, fmt.Errorf("%s observation invalidated: %w", phase, invalid)
		}
		if phase == "metadata" {
			return true, fmt.Errorf("refund transaction failed; its error-metadata tail finished (commit=%t error=%v)", r.committed, r.err)
		}
		var owner model.BillingFinalization
		if err := m.db.WithContext(budget).Where("request_id = ?", m.requestID).First(&owner).Error; err != nil {
			return true, err
		}
		if m.token != nil && (!r.matchesToken || r.tokenDelta <= 0 || r.tokenDelta != owner.TokenHeld || owner.TokenID != m.token.Id || owner.UserID != m.token.UserId || owner.TokenFingerprint != model.BillingTokenFingerprint(m.token.Key)) {
			return true, errors.New("completed refund did not credit the original request token")
		}
		if owner.State != model.BillingRefunded {
			return true, fmt.Errorf("completed transaction did not refund this owner: %s", owner.State)
		}
		return true, nil
	}
}

func (m *nativeResponsesRefundCompletion) join(t *testing.T) {
	t.Helper()
	m.once.Do(func() { m.joined, m.err = m.wait() })
	if !m.joined {
		// Continuing t.Cleanup after an unproved join would reproduce the race.
		// Fail ONLY this owned Go test process; the outer native runner observes
		// nonzero/missing terminal events and still performs its SQL cleanup.
		fmt.Fprintf(os.Stderr, "native-responses refund worker unjoined: request=%s error=%v\n", m.requestID, m.err)
		os.Exit(1)
	}
	assert.NoError(t, m.err)
}

// Match the original token's actual UPDATE, including the signed bound value.
// The caller must additionally bind this statement to its request transaction.
func nativeResponsesTokenQuotaDelta(tx *gorm.DB, tokenID int) (int, bool) {
	if tx.Error != nil || tx.RowsAffected != 1 || tx.Statement.Table != "tokens" {
		return 0, false
	}
	updates, ok := tx.Statement.Dest.(map[string]any)
	if !ok {
		return 0, false
	}
	expression, ok := updates["remain_quota"].(clause.Expr)
	if !ok || expression.SQL != "remain_quota + ?" || len(expression.Vars) != 1 {
		return 0, false
	}
	delta, ok := expression.Vars[0].(int)
	if !ok {
		return 0, false
	}
	where, ok := tx.Statement.Clauses["WHERE"].Expression.(clause.Where)
	if !ok {
		return 0, false
	}
	for _, condition := range where.Exprs {
		if id, ok := condition.(clause.Expr); ok && id.SQL == "id = ?" && len(id.Vars) == 1 && id.Vars[0] == tokenID {
			return delta, true
		}
	}
	return 0, false
}
