package controller

import (
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
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelTestConfirmationBindsOwnerSelectionVersionAndExpiry(t *testing.T) {
	selection := modelTestSelection{Model: "synthetic-model", Group: "default", MaxOutputTokens: 2048}
	ticket := modelTestConfirmation{modelTestSelection: selection, UserID: 17, Version: modelTestVersion, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	value := signModelTestConfirmation(ticket)
	_, ok := verifyModelTestConfirmation(value, 17, selection)
	require.True(t, ok)
	_, ok = verifyModelTestConfirmation(value, 18, selection)
	assert.False(t, ok)
	other := selection
	other.MaxOutputTokens = 4096
	_, ok = verifyModelTestConfirmation(value, 17, other)
	assert.False(t, ok)
	_, ok = verifyModelTestConfirmation(value+"tampered", 17, selection)
	assert.False(t, ok)
	ticket.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	_, ok = verifyModelTestConfirmation(signModelTestConfirmation(ticket), 17, selection)
	assert.False(t, ok)
	ticket.ExpiresAt = time.Now().Add(time.Minute).Unix()
	ticket.Version = "retired"
	_, ok = verifyModelTestConfirmation(signModelTestConfirmation(ticket), 17, selection)
	assert.False(t, ok)
}

func TestModelTestStaticProjectionRejectsExecutableAndNavigableContent(t *testing.T) {
	for _, body := range []string{`<svg onload="alert(1)"></svg>`, `<script>alert(1)</script><svg/>`, `<iframe src="https://private.invalid"></iframe>`, `<svg><foreignObject><div>x</div></foreignObject></svg>`, `<meta http-equiv="refresh" content="0;url=https://private.invalid"><svg/>`, `<svg><a href="https://private.invalid">x</a></svg>`, `<img src="https://private.invalid">`, `<form><input name="secret"></form>`, strings.Repeat("x", 128<<10+1)} {
		_, err := staticModelTestArtwork(body)
		assert.Error(t, err)
	}
	art, err := staticModelTestArtwork(`<html><head><style>body{background:#eef}</style></head><body><svg viewBox="0 0 360 200"><circle cx="60" cy="100" r="30"/></svg></body></html>`)
	require.NoError(t, err)
	assert.Contains(t, art, `viewBox="0 0 360 200"`)
}

func setupModelTestsPostgres(t *testing.T) (*gorm.DB, int) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires owned PostgreSQL 18.6 TEST_POSTGRES_DSN")
	}
	db, isolatedDSN := newAuditTestDatabase(t, "postgres", dsn)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMaster, oldRedis := common.IsMasterNode, common.RedisEnabled
	t.Setenv("SQL_DSN", isolatedDSN)
	common.IsMasterNode = false
	common.RedisEnabled = false
	require.NoError(t, model.InitDB())
	model.LOG_DB = model.DB
	t.Cleanup(func() {
		if conn, err := model.DB.DB(); err == nil {
			conn.Close()
		}
		model.DB, model.LOG_DB = oldDB, oldLog
		common.IsMasterNode, common.RedisEnabled = oldMaster, oldRedis
		model.InvalidatePricingCache()
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.ModelTestRun{}, &model.ModelTestGate{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.UserCallInterception{}, &model.UserCallInterceptionHit{}))
	user := model.User{Username: "synthetic-model-test-user", AffCode: "synthetic-test-aff", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 1_000_000, QuotaPaid: 1_000_000}
	require.NoError(t, db.Create(&user).Error)
	ch := model.Channel{Name: "Synthetic channel", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: "gpt-4o-mini", Group: "default", Key: "synthetic-not-a-real-key"}
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: ch.Id, Group: "default", Model: "gpt-4o-mini", Enabled: true}).Error)
	oldPrice := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o-mini":0.15}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldPrice)) })
	model.InvalidatePricingCache()
	catalogue, err := modelTestCatalogue(user.Id)
	require.NoError(t, err)
	require.Contains(t, catalogue["default"], "gpt-4o-mini")
	return db, user.Id
}

func TestModelTestPostgresRealPlaygroundRelayBillsOnceAndPreservesCancel(t *testing.T) {
	for _, mode := range []string{"success", "cancel", "upstream-failure"} {
		t.Run(mode, func(t *testing.T) {
			db, userID := setupModelTestsPostgres(t)
			require.NoError(t, db.AutoMigrate(append([]any{&model.Token{}, &model.Log{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.Task{}}, model.BonusSourceModels()...)...))
			oldMemory, oldBatch, oldLogs, oldExport, oldRetry := common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, common.RetryTimes
			oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
			common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, false
			common.RetryTimes = 3
			operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
			t.Cleanup(func() {
				waitTaskSubmissionFixtureQuiescence(t)
				common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled, common.RetryTimes = oldMemory, oldBatch, oldLogs, oldExport, oldRetry
				operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust
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
			var calls atomic.Int32
			started := make(chan struct{})
			var startOnce sync.Once
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "/v1/chat/completions", r.URL.Path)
				var input map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &input))
				assert.Equal(t, float64(2048), input["max_tokens"])
				startOnce.Do(func() { close(started) })
				if mode == "cancel" {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "upstream-failure" {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":{"message":"synthetic upstream failure","type":"server_error"}}`)
					return
				}
				fmt.Fprint(w, `{"id":"synthetic-completion","object":"chat.completion","created":1800000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"<svg viewBox=\"0 0 360 200\"><circle cx=\"60\" cy=\"80\" r=\"20\"/></svg>"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":100,"total_tokens":220}}`)
			}))
			defer upstream.Close()
			require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("base_url", upstream.URL).Error)
			model.InvalidatePricingCache()
			engine := gin.New()
			engine.Use(middleware.RequestId(), middleware.BodyStorageCleanup(), func(c *gin.Context) {
				c.Set("id", userID)
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
				c.Next()
			})
			engine.POST("/runs", PrepareModelTest, middleware.UserCallInterception(), middleware.Distribute(), ModelTestPlayground)
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			id := uuid.NewString()
			body := modelTestRequestBody(t, userID, id)
			if mode == "cancel" {
				result := make(chan string, 1)
				go func() {
					response, err := http.Post(gateway.URL+"/runs", "application/json", strings.NewReader(body))
					if err != nil {
						result <- err.Error()
						return
					}
					defer response.Body.Close()
					raw, _ := io.ReadAll(response.Body)
					result <- string(raw)
				}()
				select {
				case <-started:
				case <-time.After(10 * time.Second):
					t.Fatal("mock upstream was not reached")
				}
				_, err := model.CancelModelTestRun(userID, id)
				require.NoError(t, err)
				select {
				case response := <-result:
					assert.Contains(t, response, `"status":"cancelled"`)
				case <-time.After(10 * time.Second):
					t.Fatal("cross-node cancellation was not observed")
				}
			} else {
				for range 2 {
					response, err := http.Post(gateway.URL+"/runs", "application/json", strings.NewReader(body))
					require.NoError(t, err)
					raw, err := io.ReadAll(response.Body)
					response.Body.Close()
					require.NoError(t, err)
					require.Equal(t, 200, response.StatusCode, string(raw))
					if mode == "success" {
						assert.Contains(t, string(raw), `"status":"completed"`)
					} else {
						assert.Contains(t, string(raw), `"status":"failed"`)
					}
				}
			}
			assert.EqualValues(t, 1, calls.Load(), "paid self-test must never retry or replay upstream")
			if mode == "success" {
				var user model.User
				require.NoError(t, db.First(&user, userID).Error)
				assert.Equal(t, 999500, user.QuotaPaid)
				assert.Equal(t, 999500, user.Quota)
				var settled int64
				require.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ? AND state = ?", userID, model.BillingSettled).Count(&settled).Error)
				assert.EqualValues(t, 1, settled)
			}
		})
	}
}

func modelTestRequestBody(t *testing.T, userID int, id string) string {
	t.Helper()
	selection := modelTestSelection{Model: "gpt-4o-mini", Group: "default", MaxOutputTokens: 2048}
	ticket := modelTestConfirmation{modelTestSelection: selection, UserID: userID, Version: modelTestVersion, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	pricingHash, err := modelTestPricingHash(userID, selection)
	require.NoError(t, err)
	ticket.PricingHash = pricingHash
	body, err := common.Marshal(modelTestRequest{modelTestSelection: selection, ConfirmationToken: signModelTestConfirmation(ticket), Confirmed: true, RequestID: id})
	require.NoError(t, err)
	return string(body)
}

func TestModelTestPostgresIdempotencyOwnershipAndInterception(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	gin.SetMode(gin.TestMode)
	var calls atomic.Int32
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", userID)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Next()
	})
	engine.POST("/runs", PrepareModelTest, middleware.UserCallInterception(), func(c *gin.Context) {
		calls.Add(1)
		var input map[string]any
		require.NoError(t, common.UnmarshalBodyReusable(c, &input))
		assert.Equal(t, float64(2048), input["max_tokens"])
		assert.Equal(t, false, input["stream"])
		assert.Equal(t, "/api/user/playground/chat", c.Request.URL.Path)
		c.JSON(200, gin.H{"choices": []gin.H{{"message": gin.H{"content": `<svg viewBox="0 0 360 200"><circle cx="50" cy="60" r="20"/></svg>`}, "finish_reason": "stop"}}})
	})
	id := uuid.NewString()
	body := modelTestRequestBody(t, userID, id)
	for range 2 {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest("POST", "/runs", strings.NewReader(body)))
		require.Equal(t, 200, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"status":"completed"`)
		assert.NotContains(t, response.Body.String(), "artwork_html")
	}
	assert.EqualValues(t, 1, calls.Load())
	stored, err := model.GetModelTestRun(userID, id)
	require.NoError(t, err)
	assert.Equal(t, "renderable", stored.Assessment)
	assert.NotEmpty(t, stored.ArtworkHTML)
	_, err = model.GetModelTestRun(userID+1, id)
	assert.Error(t, err)
	require.NoError(t, db.Create(&model.UserCallInterception{UserID: userID, Enabled: true, Reply: "synthetic notice is not a test result", Revision: 1}).Error)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest("POST", "/runs", strings.NewReader(modelTestRequestBody(t, userID, uuid.NewString()))))
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"status":"intercepted"`)
	assert.NotContains(t, response.Body.String(), "synthetic notice")
	assert.EqualValues(t, 1, calls.Load())
}

func TestModelTestPostgresDistributedConcurrencyAndRateLimit(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	newRun := func(id string) *model.ModelTestRun {
		return &model.ModelTestRun{ID: id, UserID: userID, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(2 * time.Minute).Unix(), MaxOutputTokens: 2048}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { <-start; _, err := model.CreateModelTestRun(newRun(uuid.NewString())); results <- err })
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, limited := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, model.ErrModelTestLimit)
			limited++
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, limited)
	require.NoError(t, db.Model(&model.ModelTestRun{}).Where("user_id = ?", userID).Update("status", "failed").Error)
	for i := range 2 {
		run := newRun(fmt.Sprintf("synthetic-rate-%d", i))
		_, err := model.CreateModelTestRun(run)
		require.NoError(t, err)
		run.Status = "failed"
		require.NoError(t, model.FinishModelTestRun(run))
	}
	_, err := model.CreateModelTestRun(newRun("synthetic-rate-blocked"))
	assert.ErrorIs(t, err, model.ErrModelTestLimit)
}

func TestModelTestPostgresPriceChangeRequiresNewConfirmationWithoutCreatingRun(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	id := uuid.NewString()
	body := modelTestRequestBody(t, userID, id)
	previous := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previous)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"gpt-4o-mini":8}`))
	var calls atomic.Int32
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID); c.Next() })
	engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { calls.Add(1) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest("POST", "/runs", strings.NewReader(body)))
	require.Equal(t, 409, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "confirmation_price_changed")
	assert.Zero(t, calls.Load())
	var count int64
	require.NoError(t, db.Model(&model.ModelTestRun{}).Count(&count).Error)
	assert.Zero(t, count)
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	assert.Equal(t, 1_000_000, user.QuotaPaid)
}

func TestModelTestPostgresCanonicalPricingAndOriginalExpressionBody(t *testing.T) {
	_, userID := setupModelTestsPostgres(t)
	selection := modelTestSelection{Model: "gpt-4o-mini@effort:high", Group: "default", MaxOutputTokens: 2048}
	first, err := modelTestPricingHash(userID, selection)
	require.NoError(t, err)
	oldPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"gpt-4o-mini":0.02}`))
	second, err := modelTestPricingHash(userID, selection)
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	oldMode, oldExpr := settings.BillingMode, settings.BillingExpr
	t.Cleanup(func() { settings.BillingMode, settings.BillingExpr = oldMode, oldExpr })
	settings.BillingMode = map[string]string{"gpt-4o-mini": billing_setting.BillingModeTieredExpr}
	settings.BillingExpr = map[string]string{"gpt-4o-mini": `param("model") == "gpt-4o-mini@effort:high" ? 1000000 : 2000000`}
	estimate := estimateModelTestQuota(userID, selection)
	require.NotNil(t, estimate)
	assert.Equal(t, int(common.QuotaPerUnit), *estimate)
	selection.MaxOutputTokens = 1024
	assert.Error(t, validateModelTestSelection(userID, selection))
}

func TestModelTestPostgresExpiredConfirmationCanOnlyReconcileExistingOwnedRun(t *testing.T) {
	_, userID := setupModelTestsPostgres(t)
	run := &model.ModelTestRun{ID: uuid.NewString(), UserID: userID, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), MaxOutputTokens: 2048}
	_, err := model.CreateModelTestRun(run)
	require.NoError(t, err)
	selection := modelTestSelection{Model: run.Model, Group: run.Group, MaxOutputTokens: run.MaxOutputTokens}
	ticket := modelTestConfirmation{modelTestSelection: selection, UserID: userID, Version: modelTestVersion, ExpiresAt: time.Now().Add(-time.Minute).Unix()}
	body, err := common.Marshal(modelTestRequest{modelTestSelection: selection, ConfirmationToken: signModelTestConfirmation(ticket), Confirmed: true, RequestID: run.ID})
	require.NoError(t, err)
	var calls atomic.Int32
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID); c.Next() })
	engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { calls.Add(1) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest("POST", "/runs", strings.NewReader(string(body))))
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Zero(t, calls.Load())
}

func TestModelTestPostgresGlobalSlotsAndOverflowClassification(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	for i := range 4 {
		user := model.User{Username: fmt.Sprintf("synthetic-global-%d", i), AffCode: fmt.Sprintf("synthetic-global-aff-%d", i), Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default"}
		require.NoError(t, db.Create(&user).Error)
		_, err := model.CreateModelTestRun(&model.ModelTestRun{ID: uuid.NewString(), UserID: user.Id, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), MaxOutputTokens: 2048})
		require.NoError(t, err)
	}
	_, err := model.CreateModelTestRun(&model.ModelTestRun{ID: uuid.NewString(), UserID: userID, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), MaxOutputTokens: 2048})
	assert.ErrorIs(t, err, model.ErrModelTestLimit)
	require.NoError(t, db.Model(&model.ModelTestRun{}).Where("status = ?", "running").Update("status", "failed").Error)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID); c.Next() })
	engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { _, _ = c.Writer.Write([]byte(strings.Repeat("x", modelTestResponseLimit+1))) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest("POST", "/runs", strings.NewReader(modelTestRequestBody(t, userID, uuid.NewString()))))
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "response_too_large")
}
