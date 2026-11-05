package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The only upstream in this fixture is an in-process HTTP server. Real PostgreSQL
// admission, dispatch journaling, quota reconciliation and response parsing run.
func setupFingerprintSiteExecution(t *testing.T) (*gorm.DB, int, *model.SystemTask, model.ModelFingerprintSiteTarget, *model.ModelFingerprintSiteRun) {
	t.Helper()
	db, userID := setupModelTestSwitchExecution(t)
	require.NoError(t, db.AutoMigrate(&model.ModelFingerprintSiteConfig{}, &model.ModelFingerprintSiteAudit{}, &model.ModelFingerprintSiteRun{}, &model.ModelFingerprintSiteProbe{}, &model.SystemTask{}, &model.SystemTaskLock{}, &model.ReferralCommission{}, &model.AgentCommissionLedger{}, &model.AgentCommissionOutbox{}))
	var channel model.Channel
	require.NoError(t, db.Where("models = ?", "gpt-4o-mini").Take(&channel).Error)
	require.NoError(t, db.Model(&channel).Update("model_mapping", `{"gpt-4o-mini":"gpt-fixture-mapped"}`).Error)
	target := model.ModelFingerprintSiteTarget{ID: uuid.NewString(), Group: "default", Model: "gpt-4o-mini", ChannelID: channel.Id, Enabled: true, MaxOutputTokens: 2048, MaxRunQuota: 10000}
	config, err := model.UpdateModelFingerprintSiteConfig(context.Background(), 0, userID, model.ModelFingerprintSiteConfig{Enabled: true, AutomaticEnabled: true, DefaultPeriodSecs: 3600, DailyBudgetQuota: 100000, Targets: []model.ModelFingerprintSiteTarget{target}})
	require.NoError(t, err)
	task := &model.SystemTask{TaskID: "fingerprint-controller-fixture", Type: model.SystemTaskTypeModelFingerprintSite, Status: model.SystemTaskStatusRunning, LockedBy: "fixture-runner"}
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, db.Create(&model.SystemTaskLock{Type: task.Type, TaskID: task.TaskID, LockedBy: task.LockedBy, LockedUntil: time.Now().Add(time.Hour).Unix()}).Error)
	run, err := model.ReserveModelFingerprintSiteRun(context.Background(), task.TaskID, task.LockedBy, target.ID, config.Version, time.Now().Unix())
	require.NoError(t, err)
	return db, userID, task, target, run
}

func TestFingerprintSitePostgresExecutionBoundaries(t *testing.T) {
	for _, mode := range []string{"success", "missing-usage", "contradictory-usage", "output-limit", "http-error", "oversize", "disabled-after-first", "shutdown-after-first"} {
		t.Run(mode, func(t *testing.T) {
			db, userID, task, target, run := setupFingerprintSiteExecution(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				assert.Equal(t, "/v1/chat/completions", r.URL.Path)
				assert.Equal(t, "Bearer synthetic-not-a-real-key", r.Header.Get("Authorization"))
				var request map[string]any
				if !assert.NoError(t, common.DecodeJson(r.Body, &request)) {
					w.WriteHeader(500)
					return
				}
				assert.Equal(t, "gpt-fixture-mapped", request["model"])
				assert.Equal(t, float64(2048), request["max_tokens"])
				assert.Equal(t, false, request["stream"])
				var probes []model.ModelFingerprintSiteProbe
				if assert.NoError(t, db.Where("run_id = ?", run.ID).Order("id").Find(&probes).Error) && assert.Len(t, probes, int(n)) {
					assert.Equal(t, "dispatching", probes[n-1].Status, "dispatch must be committed before HTTP")
					assert.NotEmpty(t, probes[n-1].RequestID)
				}
				if mode == "disabled-after-first" {
					assert.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
				}
				if mode == "http-error" {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":"synthetic"}`)
					return
				}
				if mode == "oversize" {
					fmt.Fprint(w, strings.Repeat("x", modelTestResponseLimit+1))
					return
				}
				usage := `,"usage":{"prompt_tokens":120,"completion_tokens":100,"total_tokens":220}`
				switch mode {
				case "missing-usage":
					usage = ""
				case "contradictory-usage":
					usage = `,"usage":{"prompt_tokens":120,"completion_tokens":100,"total_tokens":1}`
				case "output-limit":
					usage = `,"usage":{"prompt_tokens":120,"completion_tokens":2049,"total_tokens":2169}`
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"fixture","object":"chat.completion","model":"gpt-fixture-mapped","choices":[{"index":0,"message":{"role":"assistant","content":"0.1, 0.2, 0.3"},"finish_reason":"stop"}]%s}`, usage)
			}))
			defer upstream.Close()
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", target.ChannelID).Update("base_url", upstream.URL).Error)
			client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
			require.NoError(t, err)
			oldClient := *client
			t.Cleanup(func() { *client = oldClient })
			// Fixture-only transport: reject any accidental destination outside this server.
			transport := upstream.Client().Transport
			if transport == nil {
				transport = http.DefaultTransport
			}
			client.Transport = modelDiscoveryRoundTripper(func(req *http.Request) (*http.Response, error) {
				if req.URL.Scheme+"://"+req.URL.Host != upstream.URL {
					return nil, fmt.Errorf("unexpected fixture destination")
				}
				response, err := transport.RoundTrip(req)
				// Cancel after a response arrives, so the next probe must observe shutdown.
				if mode == "shutdown-after-first" {
					cancel()
				}
				return response, err
			})
			var userBefore model.User
			var tokenBefore model.Token
			require.NoError(t, db.First(&userBefore, userID).Error)
			require.NoError(t, db.Where("user_id = ?", userID).Take(&tokenBefore).Error)
			executionErr := executeSiteModelFingerprint(ctx, task, task.LockedBy, target, run)
			var saved model.ModelFingerprintSiteRun
			require.NoError(t, db.First(&saved, "id = ?", run.ID).Error)
			config, err := model.GetModelFingerprintSiteConfig(context.Background())
			require.NoError(t, err)
			if mode == "success" {
				require.NoError(t, executionErr)
				assert.EqualValues(t, 3, calls.Load())
				assert.Equal(t, "succeeded", saved.Status)
				assert.Equal(t, "known", saved.UsageState)
				assert.EqualValues(t, 1500, saved.ActualQuota)
				assert.Equal(t, saved.ActualQuota, config.ReservedQuota)
				assert.Equal(t, "gpt-fixture-mapped", saved.MappedModel)
				response := httptest.NewRecorder()
				publicContext, _ := gin.CreateTestContext(response)
				publicContext.Request = httptest.NewRequest(http.MethodGet, "/site", nil)
				publicContext.Set("id", userID)
				GetSiteModelFingerprints(publicContext)
				require.Equal(t, http.StatusOK, response.Code)
				var payload struct {
					Data struct {
						Items   []map[string]any `json:"items"`
						Targets []map[string]any `json:"targets"`
					} `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
				require.Len(t, payload.Data.Items, 1)
				assert.IsType(t, map[string]any{}, payload.Data.Items[0]["fingerprint_result"])
				for _, field := range []string{"channel_id", "channel_key_index", "mapped_model", "reserved_quota", "task_id"} {
					assert.NotContains(t, payload.Data.Items[0], field)
				}
				require.Len(t, payload.Data.Targets, 1)
				assert.NotContains(t, payload.Data.Targets[0], "channel_id")
				assert.NotContains(t, response.Body.String(), "synthetic-not-a-real-key")
				assert.NotContains(t, response.Body.String(), upstream.URL)

			} else {
				require.Error(t, executionErr)
				assert.EqualValues(t, 1, calls.Load(), "automatic execution never retries or proceeds after cutoff")
				if mode != "disabled-after-first" && mode != "shutdown-after-first" {
					assert.Equal(t, "unknown", saved.Status)
					assert.Equal(t, "unknown", saved.UsageState)
					assert.Equal(t, target.MaxRunQuota, config.ReservedQuota)
					assert.False(t, config.AutomaticEnabled)
				}
			}
			var userAfter model.User
			var tokenAfter model.Token
			require.NoError(t, db.First(&userAfter, userID).Error)
			require.NoError(t, db.Where("user_id = ?", userID).Take(&tokenAfter).Error)
			assert.Equal(t, userBefore.Quota, userAfter.Quota)
			assert.Equal(t, userBefore.QuotaPaid, userAfter.QuotaPaid)
			assert.Equal(t, userBefore.UsedQuota, userAfter.UsedQuota)
			assert.Equal(t, tokenBefore.RemainQuota, tokenAfter.RemainQuota)
			assert.Equal(t, tokenBefore.UsedQuota, tokenAfter.UsedQuota)
			for _, table := range []any{&model.BalanceLedger{}, &model.BillingFinalization{}, &model.Log{}, &model.ReferralCommission{}, &model.AgentCommissionLedger{}, &model.AgentCommissionOutbox{}} {
				var count int64
				require.NoError(t, db.Model(table).Count(&count).Error)
				assert.Zero(t, count, "site sampling must not create user billing, wallet or consume-log records")
			}
			var tokens int64
			require.NoError(t, db.Model(&model.Token{}).Count(&tokens).Error)
			assert.EqualValues(t, 1, tokens, "site sampling must not create an API token")
		})
	}
}

func TestFingerprintSettingsRequireBrowserIdentity(t *testing.T) {
	for _, handler := range []gin.HandlerFunc{GetModelFingerprintSettings, UpdateModelFingerprintSettings} {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{}`))
		c.Set("id", 7)
		handler(c)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Contains(t, response.Body.String(), "AUTH_SESSION_REQUIRED")
	}
}
