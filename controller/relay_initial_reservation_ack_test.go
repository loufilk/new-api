package controller

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var errInitialReservationACK = errors.New("synthetic initial reservation COMMIT acknowledgement lost")

// Fault the actual driver COMMIT, not a GORM callback before the transaction.
// Later transactions (refund admission and the existing worker) use real SQL.
type initialReservationACKPool struct {
	*sql.DB
	rollback       bool
	commits        atomic.Int32
	firstCommitted atomic.Bool
}

type initialReservationACKTx struct {
	*sql.Tx
	pool *initialReservationACKPool
}

func (p *initialReservationACKPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &initialReservationACKTx{Tx: tx, pool: p}, nil
}

func (tx *initialReservationACKTx) Commit() error {
	first := tx.pool.commits.Add(1) == 1
	if first && tx.pool.rollback {
		if err := tx.Tx.Rollback(); err != nil {
			return err
		}
		return errInitialReservationACK
	}
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if first {
		tx.pool.firstCommitted.Store(true)
		return errInitialReservationACK
	}
	return nil
}

type initialReservationHTTPFixture struct {
	db            *gorm.DB
	user          model.User
	token         model.Token
	channel       model.Channel
	source        string
	requestID     string
	modelName     string
	upstreamCalls atomic.Int32
}

func newInitialReservationHTTPFixture(t *testing.T, source string) *initialReservationHTTPFixture {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not configured; this regression requires isolated PostgreSQL")
	}
	require.Zero(t, gopool.WorkerCount(), "no previous fixture may retain a worker")
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.Model{}, &model.Ability{}, &model.Vendor{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
	var version string
	require.NoError(t, db.Raw("SHOW server_version").Scan(&version).Error)
	t.Logf("initial reservation HTTP database: PostgreSQL %s", version)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	oldCount := constant.CountToken
	oldRetries := common.RetryTimes
	oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
	oldPrices, oldGroups := ratio_setting.ModelPrice2JSONString(), ratio_setting.GroupRatio2JSONString()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	// Quoted-column caches and i18n's once-owned bundle are private process
	// state. Only the isolated same-binary leaf may initialize them.
	require.Equal(t, t.Name(), os.Getenv("PAYMENT_STALE_FIXTURE_CHILD"))
	t.Setenv("LOG_SQL_DSN", "")
	oldMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = oldMaster
	require.NoError(t, i18n.Init())
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, false, false
	constant.CountToken = false
	common.RetryTimes = 2
	operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
		constant.CountToken = oldCount
		common.RetryTimes = oldRetries
		operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
		model.InvalidatePricingCache()
		model.InvalidateModelBonusEligibleCache()
	})
	if err := walletmodule.RegisterRuntime(); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}
	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	if err := service.ConfigureFundingFactoryRegistry(registry); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}
	f := &initialReservationHTTPFixture{db: db, source: source, requestID: "http-initial-ack-" + source, modelName: "initial-ack-http-model"}
	f.user = model.User{Username: "initial-ack-user", AffCode: "initial-ack-user", Group: "default", Quota: 1000, QuotaPaid: 400, QuotaBonus: 300, QuotaReward: 300}
	require.NoError(t, db.Create(&f.user).Error)
	f.token = model.Token{UserId: f.user.Id, Key: "synthetic-initial-ack-original-token", Name: "initial-ack", Status: common.TokenStatusEnabled, RemainQuota: 1000}
	require.NoError(t, db.Create(&f.token).Error)
	require.NoError(t, db.Create(&model.Model{ModelName: f.modelName, Status: 1, BonusEligible: common.GetPointer(true)}).Error)
	model.InvalidatePricingCache()
	model.InvalidateModelBonusEligibleCache()
	prices, err := common.Marshal(map[string]float64{f.modelName: 800 / common.QuotaPerUnit})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(prices)))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	if source == service.BillingSourceSubscription {
		plan := model.SubscriptionPlan{Title: "initial ACK period", TotalAmount: 1000, QuotaResetPeriod: model.SubscriptionResetNever}
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&plan).Error)
		now := model.GetDBTimestamp()
		require.NoError(t, db.Create(&model.UserSubscription{UserId: f.user.Id, PlanId: plan.Id, AmountTotal: 1000, QuotaPeriod: 7, Status: "active", StartTime: now - 10, EndTime: now + 3600}).Error)
		t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"should-not-dispatch","choices":[],"usage":{"total_tokens":1}}`)
	}))
	t.Cleanup(upstream.Close)
	f.channel = model.Channel{Name: "initial-ack-upstream", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "synthetic-upstream-key", BaseURL: common.GetPointer(upstream.URL), Models: f.modelName, Group: "default"}
	require.NoError(t, db.Create(&f.channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: f.modelName, ChannelId: f.channel.Id, Enabled: true}).Error)
	t.Cleanup(func() { waitTaskSubmissionFixtureQuiescence(t) })
	return f
}

func (f *initialReservationHTTPFixture) context(c *gin.Context) {
	c.Set("token_quota", 1000)
	c.Set("token_name", f.token.Name)
	common.SetContextKey(c, constant.ContextKeyUserId, f.user.Id)
	common.SetContextKey(c, constant.ContextKeyUserQuota, 1000)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenId, f.token.Id)
	common.SetContextKey(c, constant.ContextKeyTokenKey, f.token.Key)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, f.modelName)
	common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: f.source + "_only", QuotaWarningEnabled: common.GetPointer(false)})
}

func (f *initialReservationHTTPFixture) fault(t *testing.T, rollback bool) *initialReservationACKPool {
	t.Helper()
	sqlDB, err := f.db.DB()
	require.NoError(t, err)
	pool := &initialReservationACKPool{DB: sqlDB, rollback: rollback}
	wrapped := f.db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	model.DB = wrapped
	return pool
}

func (f *initialReservationHTTPFixture) requireRestored(t *testing.T, refunded bool) {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, f.db.First(&user, f.user.Id).Error)
	require.NoError(t, f.db.First(&token, f.token.Id).Error)
	require.Equal(t, []int{1000, 400, 300, 300, 0, 0}, []int{user.Quota, user.QuotaPaid, user.QuotaBonus, user.QuotaReward, user.UsedQuota, user.RequestCount})
	require.Equal(t, []int{1000, 0}, []int{token.RemainQuota, token.UsedQuota})
	var owners []model.BillingFinalization
	require.NoError(t, f.db.Where("request_id = ?", f.requestID).Find(&owners).Error)
	if !refunded {
		require.Empty(t, owners, "a rejected COMMIT must not fabricate a refund owner")
		return
	}
	require.Len(t, owners, 1)
	owner := owners[0]
	require.Equal(t, model.BillingRefunded, owner.State)
	require.Equal(t, 800, owner.SourceHeld)
	require.Equal(t, 800, owner.TokenHeld)
	require.Equal(t, f.token.Id, owner.TokenID)
	require.Equal(t, model.BillingTokenFingerprint(f.token.Key), owner.TokenFingerprint)
	if f.source == service.BillingSourceWallet {
		require.Equal(t, []int{200, 300, 300}, []int{owner.Paid, owner.Bonus, owner.Reward})
		var rows []model.BalanceLedger
		require.NoError(t, f.db.Order("id").Find(&rows).Error)
		require.Len(t, rows, 6, "one three-account debit plus one original-source refund")
	} else {
		var allowance model.UserSubscription
		require.NoError(t, f.db.First(&allowance, owner.SubscriptionID).Error)
		require.EqualValues(t, 7, owner.SubscriptionPeriod)
		require.EqualValues(t, 7, allowance.QuotaPeriod)
		require.Zero(t, allowance.AmountUsed)
		var receipt model.SubscriptionPreConsumeRecord
		require.NoError(t, f.db.Where("request_id = ?", f.requestID).First(&receipt).Error)
		require.Equal(t, "refunded", receipt.Status)
	}
}

func TestRelayInitialReservationCommitACKRecoveryHTTP(t *testing.T) {
	for _, source := range []string{service.BillingSourceWallet, service.BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			sourceNode := t.Name()
			for _, rollback := range []bool{false, true} {
				t.Run(fmt.Sprintf("rollback=%t", rollback), func(t *testing.T) {
					if os.Getenv("TEST_POSTGRES_DSN") == "" {
						t.Skip("TEST_POSTGRES_DSN not configured; isolated PostgreSQL required")
					}
					if runStalePaymentFixtureChild(t, sourceNode) {
						return
					}
					f := newInitialReservationHTTPFixture(t, source)
					pool := f.fault(t, rollback)
					engine := gin.New()
					engine.Use(middleware.RequestId())
					engine.POST("/v1/chat/completions", func(c *gin.Context) {
						f.context(c)
						if apiErr := middleware.SetupContextForSelectedChannel(c, &f.channel, f.modelName); apiErr != nil {
							c.JSON(http.StatusInternalServerError, gin.H{"setup_error": apiErr.Error()})
							return
						}
						Relay(c, types.RelayFormatOpenAI)
					})
					gateway := httptest.NewServer(engine)
					defer gateway.Close()
					client := &http.Client{Timeout: 15 * time.Second}
					request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"`+f.modelName+`","messages":[{"role":"user","content":"hello"}]}`))
					require.NoError(t, err)
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set(common.RequestIdKey, "untrusted-client-supplied-id")
					response, err := client.Do(request)
					require.NoError(t, err)
					f.requestID = response.Header.Get(common.RequestIdKey)
					require.NotEmpty(t, f.requestID)
					require.NotEqual(t, "untrusted-client-supplied-id", f.requestID)
					body, err := io.ReadAll(response.Body)
					response.Body.Close()
					require.NoError(t, err)
					require.Equal(t, !rollback, pool.firstCommitted.Load())
					require.Positive(t, pool.commits.Load(), "the driver COMMIT boundary must be reached even for actual rollback")
					var result struct {
						Error types.OpenAIError `json:"error"`
					}
					require.NoError(t, common.Unmarshal(body, &result))
					assert.Equal(t, http.StatusInternalServerError, response.StatusCode, string(body))
					assert.Equal(t, string(types.ErrorCodeUpdateDataError), result.Error.Code)
					assert.Equal(t, "new_api_error", result.Error.Type)
					assert.Equal(t, common.MessageWithRequestId("upstream request failed", f.requestID), result.Error.Message)
					assert.NotContains(t, string(body), errInitialReservationACK.Error(), "normal Relay keeps its existing public error redaction")
					assert.Zero(t, f.upstreamCalls.Load(), "failed initial preconsume cannot dispatch upstream")
					// This joins the actual recovery worker scheduled by the failed
					// HTTP call. No same-request NewBillingSession/Reserve is retried.
					waitTaskSubmissionFixtureQuiescence(t)
					f.requireRestored(t, !rollback)
					if !rollback {
						applied, err := model.ApplyBillingRefund(t.Context(), f.requestID)
						require.NoError(t, err)
						require.False(t, applied, "a repeated worker pass cannot refund twice")
						f.requireRestored(t, true)
					}
				})
			}
		})
	}
}

func TestTaskInitialReservationCommitACKRecoveryHTTP(t *testing.T) {
	for _, source := range []string{service.BillingSourceWallet, service.BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			if os.Getenv("TEST_POSTGRES_DSN") == "" {
				t.Skip("TEST_POSTGRES_DSN not configured; isolated PostgreSQL required")
			}
			if runStalePaymentFixtureChild(t) {
				return
			}
			f := newInitialReservationHTTPFixture(t, source)
			f.channel.Type = constant.ChannelTypeTaskPlugin
			require.NoError(t, f.db.Save(&f.channel).Error)
			// Match the selected plugin context supplied by the production route
			// middleware. The controller and RelayTaskSubmit remain unstubbed.
			plugin, err := pluginruntime.CompilePlugin(`
export const meta={apiVersion:1,key:"initial-ack-task",name:"Initial ACK task",version:"1.0.0",author:{name:"Test"},models:["initial-ack-http-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/jobs",body:ctx.requestBody};}
export function parseSubmitResponse(){return {taskId:"must-not-be-created"};}
export function buildQueryRequest(){return {};}
export function parseTaskResult(){return {status:"UNKNOWN"};}
`, pluginruntime.Options{})
			require.NoError(t, err)
			pool := f.fault(t, false)
			engine := gin.New()
			engine.Use(middleware.RequestId())
			engine.POST("/v1/videos", func(c *gin.Context) {
				f.context(c)
				if apiErr := middleware.SetupContextForSelectedChannel(c, &f.channel, f.modelName); apiErr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"setup_error": apiErr.Error()})
					return
				}
				c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
				c.Set("task_request", map[string]any{"model": f.modelName, "prompt": "hello"})
				RelayTask(c)
			})
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			client := &http.Client{Timeout: 15 * time.Second}
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/videos", strings.NewReader(`{"model":"`+f.modelName+`","prompt":"hello"}`))
			require.NoError(t, err)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(common.RequestIdKey, "untrusted-client-supplied-id")
			response, err := client.Do(request)
			require.NoError(t, err)
			f.requestID = response.Header.Get(common.RequestIdKey)
			require.NotEmpty(t, f.requestID)
			require.NotEqual(t, "untrusted-client-supplied-id", f.requestID)
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			require.NoError(t, err)
			require.True(t, pool.firstCommitted.Load())
			var result struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(body, &result))
			assert.Equal(t, http.StatusInternalServerError, response.StatusCode, string(body))
			assert.Equal(t, string(types.ErrorCodeUpdateDataError), result.Code)
			// The existing generic Task presenter preserves this message; unlike
			// normal Relay it does not promise public error redaction. This test
			// must not silently substitute the native route's different contract.
			assert.Equal(t, errInitialReservationACK.Error(), result.Message)
			assert.Zero(t, f.upstreamCalls.Load())
			waitTaskSubmissionFixtureQuiescence(t)
			f.requireRestored(t, true)
			var tasks int64
			require.NoError(t, f.db.Model(&model.Task{}).Count(&tasks).Error)
			require.Zero(t, tasks, "failed preconsume must not create a provider task")
		})
	}
}

func TestTaskBillingErrorPreservesRetryPolicy(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	original := errors.New("billing database failure")
	for _, skip := range []bool{false, true} {
		options := []types.NewAPIErrorOptions{}
		if skip {
			options = append(options, types.ErrOptionWithSkipRetry())
		}
		apiErr := types.NewErrorWithStatusCode(original, types.ErrorCodeUpdateDataError, http.StatusInternalServerError, options...)
		taskErr := service.TaskErrorFromAPIError(apiErr)
		require.Equal(t, skip, taskErr.NoRetry)
		require.ErrorIs(t, taskErr.Error, original)
		require.Equal(t, original.Error(), taskErr.Message)
		decision := decideTaskRetry(c, taskErr, 2)
		if skip {
			require.Equal(t, "stop", decision.Action)
			require.Equal(t, "no_retry", decision.Reason, "a failed preconsume is not an accepted upstream task")
		} else {
			require.Equal(t, "retry", decision.Action, "ordinary retryable errors must not become universally non-retryable")
		}
	}
}
