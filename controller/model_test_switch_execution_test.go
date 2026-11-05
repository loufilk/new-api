package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The fixture uses real primary storage, wallet reservations and finalization;
// only the provider is a local synthetic HTTP server.
func setupModelTestSwitchExecution(t *testing.T) (*gorm.DB, int) {
	t.Helper()
	db, userID := setupModelTestsPostgres(t)
	require.NoError(t, db.AutoMigrate(append([]any{&model.TokenRoutingMetric{}, &model.Token{}, &model.Log{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.Task{}}, model.BonusSourceModels()...)...))
	oldMemory, oldBatch, oldLogs, oldExport, oldRetry := common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, common.RetryTimes
	oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
	oldAutoDisable, oldErrorLog := common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled
	common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, false
	common.RetryTimes = 3
	common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = true, true
	operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
	t.Cleanup(func() {
		waitTaskSubmissionFixtureQuiescence(t)
		common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, common.RetryTimes = oldMemory, oldBatch, oldLogs, oldExport, oldRetry
		operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust
		common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = oldAutoDisable, oldErrorLog
	})
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	if err := walletmodule.RegisterRuntime(); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}
	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	if err := service.ConfigureFundingFactoryRegistry(registry); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}
	prices := ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"gpt-4o-mini":0.001}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices)) })
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", userID).Update("setting", `{"billing_preference":"wallet_only"}`).Error)
	return db, userID
}

func modelTestSwitchEngine(userID int, beforeExecution gin.HandlerFunc) *gin.Engine {
	engine := gin.New()
	engine.Use(middleware.RequestId(), middleware.BodyStorageCleanup(), func(c *gin.Context) {
		c.Set("id", userID)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Next()
	})
	handlers := []gin.HandlerFunc{PrepareModelTest, middleware.UserCallInterception(), middleware.Distribute()}
	if beforeExecution != nil {
		handlers = append(handlers, beforeExecution)
	}
	engine.POST("/runs", append(handlers, ModelTestPlayground)...)
	engine.POST("/runs/:id/cancel", CancelModelTest)
	engine.POST("/chat", middleware.UserCallInterception(), middleware.Distribute(), Playground)
	return engine
}

func TestModelTestSwitchPostgresCutoffBeforeDispatchClosesFinancialOwner(t *testing.T) {
	for _, boundary := range []string{"before-execution", "after-execution-check", "after-reservation", "settings-failure-after-reservation"} {
		t.Run(boundary, func(t *testing.T) {
			db, userID := setupModelTestSwitchExecution(t)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer upstream.Close()
			require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("base_url", upstream.URL).Error)
			model.InvalidatePricingCache()
			var cut atomic.Bool
			var reservedBalance int
			var beforeExecution gin.HandlerFunc
			if boundary == "before-execution" {
				beforeExecution = func(c *gin.Context) {
					require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
					cut.Store(true)
				}
			} else if boundary == "after-execution-check" {
				callback := "test:model-test-switch-before-billing"
				require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table != "options" || tx.Error != nil || cut.Load() {
						return
					}
					var running int64
					if !assert.NoError(t, db.Model(&model.ModelTestRun{}).Where("user_id = ? AND status = ?", userID, "running").Count(&running).Error) || running == 0 {
						return
					}
					// The execution check has already fetched true. Closing now
					// must be caught before the wallet reservation, not only by
					// the later final-dispatch check.
					if cut.CompareAndSwap(false, true) {
						assert.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
			} else {
				// This hook controls only the database boundary. It changes the switch
				// only after a separate connection observes a committed reservation. The
				// real final-dispatch check, refusal and refund execute unchanged.
				callback := "test:model-test-switch-after-held"
				require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table != "options" || cut.Load() {
						return
					}
					var held int64
					if !assert.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ? AND state = ?", userID, model.BillingHeld).Count(&held).Error) || held == 0 {
						return
					}
					if !cut.CompareAndSwap(false, true) {
						return
					}
					var user model.User
					assert.NoError(t, db.First(&user, userID).Error)
					reservedBalance = user.QuotaPaid
					if boundary == "after-reservation" {
						assert.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
					} else {
						assert.NoError(t, db.Migrator().RenameTable("options", "switch_unavailable_options"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
			}
			engine := modelTestSwitchEngine(userID, beforeExecution)
			id := uuid.NewString()
			body := modelTestRequestBody(t, userID, id)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
			require.True(t, cut.Load(), "the intended boundary was actually reached")
			require.Equal(t, 200, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"status":"failed"`)
			code := "model_tests_disabled"
			if boundary == "settings-failure-after-reservation" {
				code = "model_test_settings_unavailable"
				require.NoError(t, db.Migrator().RenameTable("switch_unavailable_options", "options"))
			}
			assert.Contains(t, response.Body.String(), code)
			assert.Zero(t, calls.Load(), "no request crossed the final upstream boundary")
			waitTaskSubmissionFixtureQuiescence(t)
			var channel model.Channel
			require.NoError(t, db.Where("models = ?", "gpt-4o-mini").Take(&channel).Error)
			assert.Equal(t, common.ChannelStatusEnabled, channel.Status, "local feature denial must not disable or notify about a healthy upstream")
			var errorLogs int64
			require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeError).Count(&errorLogs).Error)
			assert.Zero(t, errorLogs, "feature cutoff is not an upstream fault log")
			run, err := model.GetModelTestRun(userID, id)
			require.NoError(t, err)
			assert.Equal(t, "failed", run.Status)
			assert.NotZero(t, run.CompletedAt)
			var user model.User
			require.NoError(t, db.First(&user, userID).Error)
			assert.Equal(t, 1_000_000, user.QuotaPaid)
			assert.Equal(t, user.QuotaPaid, user.Quota)
			var owners []model.BillingFinalization
			require.NoError(t, db.Where("user_id = ?", userID).Find(&owners).Error)
			var ledger []model.BalanceLedger
			require.NoError(t, db.Where("user_id = ?", userID).Order("id").Find(&ledger).Error)
			if boundary == "before-execution" || boundary == "after-execution-check" {
				assert.Empty(t, owners)
				assert.Empty(t, ledger, "execution refusal cannot pre-consume")
			} else {
				assert.Equal(t, 999500, reservedBalance, "real committed debit existed before switch cutoff")
				require.Len(t, owners, 1)
				assert.Equal(t, model.BillingRefunded, owners[0].State)
				assert.Equal(t, run.RequestID, owners[0].RequestID)
				require.Len(t, ledger, 2)
				assert.Equal(t, model.LedgerReasonBillingPreConsume, ledger[0].Reason)
				assert.Equal(t, -500, ledger[0].Delta)
				assert.Equal(t, model.LedgerReasonBillingRefund, ledger[1].Reason)
				assert.Equal(t, 500, ledger[1].Delta)
			}
			// Replay reconciles the owned terminal row, even while disabled; it must
			// not dispatch, reserve again or refund the same hold a second time.
			replay := httptest.NewRecorder()
			engine.ServeHTTP(replay, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
			require.Equal(t, 200, replay.Code, replay.Body.String())
			assert.Zero(t, calls.Load())
			var ledgerAfter int64
			require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ?", userID).Count(&ledgerAfter).Error)
			assert.EqualValues(t, len(ledger), ledgerAfter)
		})
	}
}

func TestModelTestSwitchPostgresDisableAfterSendPreservesSettlementAndCancellation(t *testing.T) {
	for _, outcome := range []string{"settle", "cancel", "disable-after-admission-before-send"} {
		t.Run(outcome, func(t *testing.T) {
			db, userID := setupModelTestSwitchExecution(t)
			var admittedCut atomic.Bool
			if outcome == "disable-after-admission-before-send" {
				callback := "test:model-test-switch-after-admission"
				require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table != "options" || tx.Error != nil || admittedCut.Load() {
						return
					}
					var held int64
					if !assert.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ? AND state = ?", userID, model.BillingHeld).Count(&held).Error) || held == 0 {
						return
					}
					// The final authority read already obtained true, but no
					// HTTP request has left this goroutine yet. The admission
					// precedes the disable and may still send and settle once.
					if admittedCut.CompareAndSwap(false, true) {
						assert.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
			}
			var calls atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce, startedOnce sync.Once
			releaseUpstream := func() { releaseOnce.Do(func() { close(release) }) }
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if outcome == "disable-after-admission-before-send" {
					assert.True(t, admittedCut.Load(), "the disable committed before the provider received this admitted request")
				}
				startedOnce.Do(func() { close(started) })
				select {
				case <-r.Context().Done():
					return
				case <-release:
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"synthetic-completion","object":"chat.completion","created":1800000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"<svg viewBox=\"0 0 360 200\"><circle cx=\"60\" cy=\"80\" r=\"20\"/></svg>"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":100,"total_tokens":220}}`)
			}))
			defer upstream.Close()
			defer releaseUpstream()
			require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("base_url", upstream.URL).Error)
			model.InvalidatePricingCache()
			engine := modelTestSwitchEngine(userID, nil)
			id := uuid.NewString()
			body := modelTestRequestBody(t, userID, id)
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
				finished <- response
			}()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("mock upstream was not reached")
			}
			require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
			if outcome == "cancel" {
				for range 2 {
					response := httptest.NewRecorder()
					engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs/"+id+"/cancel", nil))
					require.Equal(t, 200, response.Code, response.Body.String())
					assert.Contains(t, response.Body.String(), `"cancel_requested":true`)
				}
			} else {
				releaseUpstream()
			}
			var response *httptest.ResponseRecorder
			select {
			case response = <-finished:
			case <-time.After(10 * time.Second):
				t.Fatal("model test did not finish")
			}
			waitTaskSubmissionFixtureQuiescence(t)
			require.Equal(t, 200, response.Code, response.Body.String())
			assert.EqualValues(t, 1, calls.Load())
			run, err := model.GetModelTestRun(userID, id)
			require.NoError(t, err)
			var owner model.BillingFinalization
			require.NoError(t, db.Where("request_id = ?", run.RequestID).Take(&owner).Error)
			var user model.User
			require.NoError(t, db.First(&user, userID).Error)
			assert.Equal(t, 999500, user.QuotaPaid)
			assert.Equal(t, user.QuotaPaid, user.Quota)
			if outcome != "cancel" {
				assert.Equal(t, "failed", run.Status)
				assert.Equal(t, "model_tests_disabled", run.ErrorCode)
				assert.Equal(t, model.BillingSettled, owner.State)
				assert.Equal(t, 500, owner.ActualQuota)
			} else {
				assert.Equal(t, "cancelled", run.Status)
				assert.True(t, run.CancelRequested)
				assert.Equal(t, model.BillingDeliveryUnknown, owner.State, "cancel after sending cannot prove upstream did no billable work")
				assert.Equal(t, "upstream_delivery_unknown", owner.LastErrorCode)
				assert.Equal(t, 500, owner.SourceHeld)
			}
			replay := httptest.NewRecorder()
			engine.ServeHTTP(replay, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
			require.Equal(t, 200, replay.Code, replay.Body.String())
			assert.EqualValues(t, 1, calls.Load())
			var refundCount, ownerCount int64
			require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ? AND reason = ?", userID, model.LedgerReasonBillingRefund).Count(&refundCount).Error)
			require.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ?", userID).Count(&ownerCount).Error)
			assert.Zero(t, refundCount, "closing the feature or repeating cancel never invents a refund")
			assert.EqualValues(t, 1, ownerCount)
		})
	}
}
