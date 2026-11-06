package controller

import (
	"context"
	"fmt"
	"io"
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
	return setupModelTestSwitchAccounting(t, db, userID)
}

func setupModelTestSwitchAccounting(t *testing.T, db *gorm.DB, userID int) (*gorm.DB, int) {
	t.Helper()
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
			var cutoffMu sync.Mutex
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
					if tx.Statement.Table != "options" || tx.Error != nil {
						return
					}
					cutoffMu.Lock()
					defer cutoffMu.Unlock()
					if cut.Load() {
						return
					}
					var running int64
					if !assert.NoError(t, db.Model(&model.ModelTestRun{}).Where("user_id = ? AND status = ?", userID, "running").Count(&running).Error) || running == 0 {
						return
					}
					// The execution check has already fetched true. Closing now
					// must be caught before the wallet reservation, not only by
					// the later final-dispatch check.
					assert.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
					cut.Store(true)
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
			} else {
				// This hook controls only the database boundary. It changes the switch
				// only after a separate connection observes a committed reservation. The
				// real final-dispatch check, refusal and refund execute unchanged.
				callback := "test:model-test-switch-after-held"
				require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table != "options" {
						return
					}
					// Parallel admission reads wait until the cutoff commits. Seeing
					// a flag before its UPDATE would let another probe read true.
					cutoffMu.Lock()
					defer cutoffMu.Unlock()
					if cut.Load() {
						return
					}
					var held int64
					if !assert.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ? AND state = ?", userID, model.BillingHeld).Count(&held).Error) || held == 0 {
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
					cut.Store(true)
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
			}
			engine := modelTestSwitchEngine(userID, beforeExecution)
			id := uuid.NewString()
			body := modelTestRequestBody(t, userID, id)
			newBody := modelTestRequestBody(t, userID, uuid.NewString())
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
			require.True(t, cut.Load(), "the intended boundary was actually reached")
			require.Equal(t, 200, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"status":"failed"`)
			code := "model_tests_disabled"
			denialStatus := http.StatusForbidden
			if boundary == "settings-failure-after-reservation" {
				code = "model_test_settings_unavailable"
				denialStatus = http.StatusServiceUnavailable
			}
			assert.Contains(t, response.Body.String(), code)
			denied := httptest.NewRecorder()
			engine.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(newBody)))
			require.Equal(t, denialStatus, denied.Code, denied.Body.String())
			assert.Contains(t, denied.Body.String(), code)
			if boundary == "settings-failure-after-reservation" {
				require.NoError(t, db.Migrator().RenameTable("switch_unavailable_options", "options"))
			}
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
			var token model.Token
			require.NoError(t, db.Where("user_id = ?", userID).First(&token).Error)
			assert.Equal(t, 1_000_000, token.RemainQuota)
			var owners []model.BillingFinalization
			require.NoError(t, db.Where("user_id = ?", userID).Find(&owners).Error)
			var ledger []model.BalanceLedger
			require.NoError(t, db.Where("user_id = ?", userID).Order("id").Find(&ledger).Error)
			if boundary == "before-execution" || boundary == "after-execution-check" {
				assert.Empty(t, owners)
				assert.Empty(t, ledger, "execution refusal cannot pre-consume")
			} else {
				assert.Less(t, reservedBalance, 1_000_000, "real committed debit existed before switch cutoff")
				assert.GreaterOrEqual(t, reservedBalance, 998500)
				assert.Zero(t, (1_000_000-reservedBalance)%500)
				require.NotEmpty(t, owners)
				require.LessOrEqual(t, len(owners), 3)
				var audits []modelFingerprintSampleAudit
				require.NoError(t, common.UnmarshalJsonStr(run.SamplesJSON, &audits))
				require.Len(t, audits, 3)
				auditIDs := make(map[string]bool, len(audits))
				for _, audit := range audits {
					require.NotEmpty(t, audit.RequestID)
					assert.False(t, auditIDs[audit.RequestID], "each probe has its own financial identity")
					auditIDs[audit.RequestID] = true
				}
				assert.Contains(t, auditIDs, run.RequestID)
				require.Len(t, ledger, 2*len(owners))
				for _, owner := range owners {
					assert.Contains(t, auditIDs, owner.RequestID)
					assert.Equal(t, model.BillingRefunded, owner.State)
					assert.Equal(t, 500, owner.SourceHeld)
					assert.Equal(t, 500, owner.TokenHeld)
					var reasons []string
					var deltas []int
					for _, row := range ledger {
						if row.RequestId == owner.RequestID {
							reasons = append(reasons, row.Reason)
							deltas = append(deltas, row.Delta)
						}
					}
					assert.Equal(t, []string{model.LedgerReasonBillingPreConsume, model.LedgerReasonBillingRefund}, reasons)
					assert.Equal(t, []int{-500, 500}, deltas, "each undispatched hold is refunded exactly once")
				}
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
				client := service.GetHttpClient()
				transport := client.Transport
				require.NotNil(t, transport)
				t.Cleanup(func() { client.Transport = transport })
				var cutoff sync.Once
				var cutoffErr error
				client.Transport = modelDiscoveryRoundTripper(func(request *http.Request) (*http.Response, error) {
					// RoundTrip is entered after the real final authority check,
					// but the provider has not received this request yet. A held
					// row alone cannot identify that boundary with parallel probes.
					cutoff.Do(func() {
						cutoffErr = db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error
						admittedCut.Store(cutoffErr == nil)
					})
					if cutoffErr != nil {
						return nil, cutoffErr
					}
					return transport.RoundTrip(request)
				})
			}
			var calls atomic.Int32
			started, cancelled := make(chan struct{}, 3), make(chan struct{}, 3)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseUpstream := func() { releaseOnce.Do(func() { close(release) }) }
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !assert.LessOrEqual(t, calls.Add(1), int32(3), "each challenge is dispatched at most once") {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				// HTTP/1 starts watching for a disconnected client after the
				// request body reaches EOF. A provider has consumed its prompt
				// before waiting to generate the response.
				_, readErr := io.Copy(io.Discard, r.Body)
				closeErr := r.Body.Close()
				if !assert.NoError(t, readErr) || !assert.NoError(t, closeErr) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if outcome == "disable-after-admission-before-send" {
					assert.True(t, admittedCut.Load(), "the disable committed before the provider received this admitted request")
				}
				started <- struct{}{}
				select {
				case <-r.Context().Done():
					cancelled <- struct{}{}
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
			newBody := modelTestRequestBody(t, userID, uuid.NewString())
			finished := make(chan *httptest.ResponseRecorder, 1)
			done := make(chan struct{})
			requestContext, cancel := context.WithCancel(t.Context())
			defer func() {
				cancel()
				releaseUpstream()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("model-test request did not stop before fixture cleanup")
				}
			}()
			go func() {
				defer close(done)
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, httptest.NewRequestWithContext(requestContext, http.MethodPost, "/runs", strings.NewReader(body)))
				finished <- response
			}()
			admissionsToWait := 3
			if outcome == "disable-after-admission-before-send" {
				admissionsToWait = 1
			}
			for range admissionsToWait {
				select {
				case <-started:
				case <-time.After(10 * time.Second):
					t.Fatal("expected probes were not simultaneously in flight")
				}
			}
			require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
			denied := httptest.NewRecorder()
			engine.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(newBody)))
			require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
			assert.Contains(t, denied.Body.String(), "model_tests_disabled")
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
			if outcome == "cancel" {
				for range 3 {
					select {
					case <-cancelled:
					case <-time.After(10 * time.Second):
						t.Fatal("cancellation did not reach every admitted probe")
					}
				}
			}
			waitTaskSubmissionFixtureQuiescence(t)
			require.Equal(t, 200, response.Code, response.Body.String())
			sent := int(calls.Load())
			if outcome == "disable-after-admission-before-send" {
				require.GreaterOrEqual(t, sent, 1)
				require.LessOrEqual(t, sent, 3)
			} else {
				require.Equal(t, 3, sent)
			}
			run, err := model.GetModelTestRun(userID, id)
			require.NoError(t, err)
			var audits []modelFingerprintSampleAudit
			require.NoError(t, common.UnmarshalJsonStr(run.SamplesJSON, &audits))
			require.Len(t, audits, 3)
			auditsByID := make(map[string]modelFingerprintSampleAudit, len(audits))
			for _, audit := range audits {
				require.NotEmpty(t, audit.RequestID)
				assert.NotContains(t, auditsByID, audit.RequestID, "financial owners cannot be shared across probes")
				auditsByID[audit.RequestID] = audit
			}
			assert.Contains(t, auditsByID, run.RequestID)
			var owners []model.BillingFinalization
			require.NoError(t, db.Where("user_id = ?", userID).Find(&owners).Error)
			require.GreaterOrEqual(t, len(owners), sent)
			require.LessOrEqual(t, len(owners), 3)
			var user model.User
			require.NoError(t, db.First(&user, userID).Error)
			assert.Equal(t, 1_000_000-500*sent, user.QuotaPaid)
			assert.Equal(t, user.QuotaPaid, user.Quota)
			var token model.Token
			require.NoError(t, db.Where("user_id = ?", userID).First(&token).Error)
			assert.Equal(t, user.QuotaPaid, token.RemainQuota)
			if outcome == "cancel" {
				assert.Equal(t, "cancelled", run.Status)
				assert.True(t, run.CancelRequested)
			} else if sent == 3 {
				assert.Equal(t, "completed", run.Status, "revocation does not invalidate already admitted delivery")
				assert.Empty(t, run.ErrorCode)
			} else {
				assert.Equal(t, "failed", run.Status)
				assert.Equal(t, "model_tests_disabled", run.ErrorCode)
			}
			completed, unknown, refunded := 0, 0, 0
			for _, owner := range owners {
				audit, found := auditsByID[owner.RequestID]
				require.True(t, found, "every financial owner belongs to an audited probe")
				assert.Equal(t, 500, owner.SourceHeld)
				assert.Equal(t, 500, owner.TokenHeld)
				switch owner.State {
				case model.BillingSettled:
					completed++
					assert.Equal(t, 500, owner.ActualQuota)
					assert.Equal(t, "completed", audit.Status)
				case model.BillingDeliveryUnknown:
					unknown++
					assert.Equal(t, "upstream_delivery_unknown", owner.LastErrorCode)
					assert.Zero(t, owner.ActualQuota)
					assert.Equal(t, "unknown", audit.Status)
				case model.BillingRefunded:
					refunded++
					assert.Zero(t, owner.ActualQuota)
					assert.Equal(t, "failed", audit.Status)
				default:
					t.Errorf("probe %s has unresolved financial state %s", owner.RequestID, owner.State)
				}
			}
			if outcome == "cancel" {
				assert.Equal(t, sent, unknown, "cancelled dispatched probes each preserve their own hold")
				assert.Zero(t, completed)
			} else {
				assert.Equal(t, sent, completed)
				assert.Zero(t, unknown)
			}
			assert.Equal(t, len(owners)-sent, refunded, "only undispatched probes refund")
			var ledger []model.BalanceLedger
			require.NoError(t, db.Where("user_id = ?", userID).Order("id").Find(&ledger).Error)
			require.Len(t, ledger, len(owners)+refunded)
			ledgerDelta := 0
			for _, row := range ledger {
				assert.Contains(t, auditsByID, row.RequestId)
				ledgerDelta += row.Delta
			}
			assert.Equal(t, -500*sent, ledgerDelta)
			replay := httptest.NewRecorder()
			engine.ServeHTTP(replay, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
			require.Equal(t, 200, replay.Code, replay.Body.String())
			assert.EqualValues(t, sent, calls.Load())
			var refundCount, ownerCount int64
			require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ? AND reason = ?", userID, model.LedgerReasonBillingRefund).Count(&refundCount).Error)
			require.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ?", userID).Count(&ownerCount).Error)
			assert.EqualValues(t, refunded, refundCount, "replay and repeated cancellation never refund dispatched work")
			assert.EqualValues(t, len(owners), ownerCount)
		})
	}
}
