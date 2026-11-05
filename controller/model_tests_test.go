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
	selection := modelTestSelection{Model: "synthetic-model", Group: "default", MaxOutputTokens: 0}
	ticket := modelTestConfirmation{OutputUnbounded: true, modelTestSelection: selection, UserID: 17, Version: modelTestVersion, ExpiresAt: time.Now().Add(time.Minute).Unix()}
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
	setupFingerprintScoringFixture(t)
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
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.ModelTestRun{}, &model.ModelTestGate{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.UserCallInterception{}, &model.UserCallInterceptionHit{}))
	// Existing execution scenarios explicitly opt in; switch tests remove/disable it.
	require.NoError(t, db.Create(&model.Option{Key: model.ModelTestEnabledKey, Value: "true"}).Error)
	user := model.User{Username: "synthetic-model-test-user", AffCode: "synthetic-test-aff", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 1_000_000, QuotaPaid: 1_000_000}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.Token{UserId: user.Id, Name: "Test token", Key: "synthetic-user-token", Status: common.TokenStatusEnabled, Group: "default", ExpiredTime: -1, RemainQuota: 1000000}).Error)
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
			require.NoError(t, db.AutoMigrate(append([]any{&model.TokenRoutingMetric{}, &model.Token{}, &model.Log{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.Task{}}, model.BonusSourceModels()...)...))
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
				assert.NotContains(t, input, "max_tokens")
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
						assert.Contains(t, string(raw), `"status":"unknown"`)
						assert.Contains(t, string(raw), "upstream_outcome_uncertain")
					}
				}
			}
			if mode == "success" {
				assert.EqualValues(t, 3, calls.Load())
			} else {
				assert.EqualValues(t, 1, calls.Load(), "failed calls must never retry")
			}
			if mode == "success" {
				var user model.User
				require.NoError(t, db.First(&user, userID).Error)
				assert.Equal(t, 998500, user.QuotaPaid)
				assert.Equal(t, 998500, user.Quota)
				var settled int64
				require.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ? AND state = ?", userID, model.BillingSettled).Count(&settled).Error)
				assert.EqualValues(t, 3, settled)
			}
		})
	}
}

func modelTestRequestBody(t *testing.T, userID int, id string) string {
	t.Helper()
	selection := modelTestSelection{Model: "gpt-4o-mini", Group: "default", MaxOutputTokens: 0}
	var token model.Token
	require.NoError(t, model.DB.Where("user_id = ?", userID).First(&token).Error)
	selection.TokenID = token.Id
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/runs", nil)
	ctx.Set("id", userID)
	_, identity, identityErr := selectedModelTestToken(ctx, selection)
	require.NoError(t, identityErr)
	challenges, challengeErr := modelFingerprintScorer.challenges()
	require.NoError(t, challengeErr)
	ticket := modelTestConfirmation{OutputUnbounded: true, EstimatedQuota: estimateModelTestQuota(userID, selection, challenges), Challenges: challenges, TokenIdentity: identity, Nonce: uuid.NewString(), modelTestSelection: selection, UserID: userID, Version: modelTestVersion, ExpiresAt: time.Now().Add(time.Minute).Unix()}
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
		assert.NotContains(t, input, "max_tokens")
		assert.Equal(t, false, input["stream"])
		assert.Equal(t, "/v1/chat/completions", c.Request.URL.Path)
		c.JSON(200, gin.H{"choices": []gin.H{{"message": gin.H{"content": `<svg viewBox="0 0 360 200"><circle cx="50" cy="60" r="20"/></svg>`}, "finish_reason": "stop"}}})
	})
	id := uuid.NewString()
	body := modelTestRequestBody(t, userID, id)
	for range 2 {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest("POST", "/runs", strings.NewReader(body)))
		require.Equal(t, 200, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"status":"failed"`)
		assert.NotContains(t, response.Body.String(), "artwork_html")
	}
	assert.EqualValues(t, 1, calls.Load())
	stored, err := model.GetModelTestRun(userID, id)
	require.NoError(t, err)
	assert.Equal(t, "unassessed", stored.Assessment)
	assert.Empty(t, stored.ArtworkHTML)
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
		return &model.ModelTestRun{ID: id, UserID: userID, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(2 * time.Minute).Unix(), MaxOutputTokens: 0}
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

func TestModelTestPostgresCanonicalFixedPricingAndUnknownExpressionCost(t *testing.T) {
	_, userID := setupModelTestsPostgres(t)
	selection := modelTestSelection{Model: "gpt-4o-mini@effort:high", Group: "default", MaxOutputTokens: 0}
	first, err := modelTestPricingHash(userID, selection)
	require.NoError(t, err)
	oldPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"gpt-4o-mini":0.02}`))
	second, err := modelTestPricingHash(userID, selection)
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	fixedChallenges, err := modelFingerprintScorer.challenges()
	require.NoError(t, err)
	fixedEstimate := estimateModelTestQuota(userID, selection, fixedChallenges)
	require.NotNil(t, fixedEstimate)
	assert.Equal(t, common.QuotaRound(3*0.02*common.QuotaPerUnit), *fixedEstimate)
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	oldMode, oldExpr := settings.BillingMode, settings.BillingExpr
	t.Cleanup(func() { settings.BillingMode, settings.BillingExpr = oldMode, oldExpr })
	settings.BillingMode = map[string]string{"gpt-4o-mini": billing_setting.BillingModeTieredExpr}
	settings.BillingExpr = map[string]string{"gpt-4o-mini": `param("model") == "gpt-4o-mini@effort:high" ? 1000000 : 2000000`}
	challenges, challengeErr := modelFingerprintScorer.challenges()
	require.NoError(t, challengeErr)
	estimate := estimateModelTestQuota(userID, selection, challenges)
	assert.Nil(t, estimate, "token expression costs are unknown without an output bound")
	selection.MaxOutputTokens = 1024
	assert.Error(t, validateModelTestSelection(userID, selection))
}

func TestModelTestPostgresExpiredConfirmationCanOnlyReconcileExistingOwnedRun(t *testing.T) {
	_, userID := setupModelTestsPostgres(t)
	run := &model.ModelTestRun{ID: uuid.NewString(), UserID: userID, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), MaxOutputTokens: 0}
	_, err := model.CreateModelTestRun(run)
	require.NoError(t, err)
	selection := modelTestSelection{Model: run.Model, Group: run.Group, MaxOutputTokens: run.MaxOutputTokens}
	ticket := modelTestConfirmation{OutputUnbounded: true, modelTestSelection: selection, UserID: userID, Version: modelTestVersion, ExpiresAt: time.Now().Add(-time.Minute).Unix()}
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
		_, err := model.CreateModelTestRun(&model.ModelTestRun{ID: uuid.NewString(), UserID: user.Id, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), MaxOutputTokens: 0})
		require.NoError(t, err)
	}
	_, err := model.CreateModelTestRun(&model.ModelTestRun{ID: uuid.NewString(), UserID: userID, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), MaxOutputTokens: 0})
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

func TestModelTestConfirmationBindsTokenAndGroup(t *testing.T) {
	selected := modelTestSelection{TokenID: 42, Model: "synthetic", Group: "default", MaxOutputTokens: 0}
	ticket := modelTestConfirmation{OutputUnbounded: true, modelTestSelection: selected, UserID: 7, Version: modelTestVersion, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	signed := signModelTestConfirmation(ticket)
	for _, mutation := range []modelTestSelection{{TokenID: 43, Model: "synthetic", Group: "default", MaxOutputTokens: 0}, {TokenID: 42, Model: "synthetic", Group: "renamed", MaxOutputTokens: 0}, {TokenID: 42, Model: "other", Group: "default", MaxOutputTokens: 0}} {
		_, ok := verifyModelTestConfirmation(signed, 7, mutation)
		assert.False(t, ok)
	}
}

func TestModelTestTokenRejectsOwnerStatusExpiryBudgetAndAutomaticRouting(t *testing.T) {
	base := model.Token{Id: 4, UserId: 7, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100, Group: "default"}
	cases := []struct {
		name   string
		change func(*model.Token)
	}{
		{"other-owner", func(t *model.Token) { t.UserId = 8 }},
		{"disabled", func(t *model.Token) { t.Status = common.TokenStatusDisabled }},
		{"expired", func(t *model.Token) { t.ExpiredTime = time.Now().Unix() - 1 }},
		{"quota", func(t *model.Token) { t.RemainQuota = 0 }},
		{"auto", func(t *model.Token) { t.Group = "auto" }},
		{"smart", func(t *model.Token) { t.SmartRouting = true }},
		{"different-group", func(t *model.Token) { t.Group = "other" }},
		{"model-limit", func(t *model.Token) { t.ModelLimitsEnabled = true; t.ModelLimits = "other" }},
		{"ip-limit", func(t *model.Token) { ip := "192.0.2.1"; t.AllowIps = &ip }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := base
			tc.change(&token)
			assert.False(t, modelTestTokenAllowed(&token, 7, "default", "default", "synthetic", "198.51.100.1"))
		})
	}
}

func TestModelTestPostgresConfirmationCannotStartAnotherRequest(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	confirmation := "synthetic-confirmation-digest"
	first := &model.ModelTestRun{ID: uuid.NewString(), UserID: userID, TokenID: 1, Source: "user", ConfirmationID: &confirmation, Model: "synthetic", Group: "default", BenchmarkID: modelTestVersion, Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix()}
	_, err := model.CreateModelTestRun(first)
	require.NoError(t, err)
	require.NoError(t, db.Model(first).Update("status", "unknown").Error)
	second := *first
	second.ID = uuid.NewString()
	second.Status = "running"
	_, err = model.CreateModelTestRun(&second)
	require.ErrorIs(t, err, model.ErrModelTestConflict)
	replay := *first
	replayed, err := model.CreateModelTestRun(&replay)
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, "unknown", replay.Status)
}

func TestModelFingerprintPostgresChangedTokenCannotConsumeQuote(t *testing.T) {
	for _, mutation := range []struct {
		name   string
		fields map[string]any
	}{
		{"key", map[string]any{"key": "rotated-synthetic-key"}},
		{"owner", map[string]any{"user_id": 999999}},
		{"group", map[string]any{"group": "other"}},
		{"disabled", map[string]any{"status": common.TokenStatusDisabled}},
		{"expired", map[string]any{"expired_time": int64(1)}},
		{"quota", map[string]any{"remain_quota": 0}},
		{"model-limits", map[string]any{"model_limits_enabled": true, "model_limits": "other"}},
		{"ip", map[string]any{"allow_ips": "192.0.2.123"}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			db, userID := setupModelTestsPostgres(t)
			id := uuid.NewString()
			body := modelTestRequestBody(t, userID, id)
			require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", userID).Updates(mutation.fields).Error)
			var calls atomic.Int32
			engine := gin.New()
			engine.Use(func(c *gin.Context) { c.Set("id", userID); c.Next() })
			engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { calls.Add(1) })
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
			assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), "confirmation_token_changed")
			assert.Zero(t, calls.Load())
			var count int64
			require.NoError(t, db.Model(&model.ModelTestRun{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestModelFingerprintPostgresTokenCatalogueUsesProductionModelLimits(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", userID).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "gpt-4o-mini"}).Error)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ctx.Set("id", userID)
	catalogue := map[string][]string{"default": {"gpt-4o-mini", "gpt-4o-mini@effort:high", "other"}}
	tokens, err := modelTestTokens(ctx, catalogue)
	require.NoError(t, err)
	require.Len(t, tokens["default"], 1)
	expected := make([]string, 0)
	for _, name := range catalogue["default"] {
		if middleware.TokenModelLimitAllows(map[string]bool{"gpt-4o-mini": true}, name) {
			expected = append(expected, name)
		}
	}
	assert.Equal(t, expected, tokens["default"][0].EligibleModels)
	encoded, err := common.Marshal(tokens)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "synthetic-user-token")
	assert.NotContains(t, string(encoded), `"key"`)
}
