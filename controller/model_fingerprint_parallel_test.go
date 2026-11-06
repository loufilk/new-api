package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelfingerprint"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type fingerprintParallelRequest struct {
	prompt        string
	model         string
	authorization string
	deadline      time.Duration
}

// All three requests must reach the transport before the test releases any
// response. A sequential collector cannot pass this barrier. The transport is
// synthetic; admission, routing, billing and PostgreSQL journals are real.
type fingerprintParallelTransport struct {
	mode    string
	entered chan fingerprintParallelRequest
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (f *fingerprintParallelTransport) unblock() { f.once.Do(func() { close(f.release) }) }

func (f *fingerprintParallelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	n := f.calls.Add(1)
	if req.URL.Host != "fingerprint-parallel.invalid" || n > 3 {
		return nil, errors.New("unexpected fingerprint dispatch")
	}
	var input struct {
		Model     string `json:"model"`
		Stream    bool   `json:"stream"`
		MaxTokens *int   `json:"max_tokens"`
		Messages  []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := common.DecodeJson(req.Body, &input); err != nil {
		return nil, err
	}
	if input.Stream || input.MaxTokens != nil || len(input.Messages) != 1 {
		return nil, errors.New("fingerprint request shape changed")
	}
	deadline, _ := req.Context().Deadline()
	f.entered <- fingerprintParallelRequest{prompt: input.Messages[0].Content, model: input.Model, authorization: req.Header.Get("Authorization"), deadline: time.Until(deadline)}
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-f.release:
	}
	if f.mode == "cancel" {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	if f.mode == "one-unknown" && n == 1 {
		return nil, errors.New("synthetic connection lost after submission")
	}
	finishReason := "stop"
	if f.mode == "one-incomplete" && n == 1 {
		finishReason = "length"
	}
	payload, err := common.Marshal(map[string]any{
		"id": fmt.Sprintf("fixture-%d", n), "object": "chat.completion", "model": input.Model,
		"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": fmt.Sprintf("sample-%d", n)}, "finish_reason": finishReason}},
		"usage":   map[string]any{"prompt_tokens": 120, "completion_tokens": 100, "total_tokens": 220},
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(payload))), Request: req}, nil
}

func installFingerprintParallelTransport(t *testing.T, mode string) *fingerprintParallelTransport {
	t.Helper()
	client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
	require.NoError(t, err)
	old := *client
	transport := &fingerprintParallelTransport{mode: mode, entered: make(chan fingerprintParallelRequest, 3), release: make(chan struct{})}
	client.Transport = transport
	t.Cleanup(func() { *client = old })
	return transport
}

func awaitFingerprintParallelDispatch(t *testing.T, transport *fingerprintParallelTransport) {
	t.Helper()
	prompts := make(map[string]bool)
	for range 3 {
		select {
		case request := <-transport.entered:
			assert.Equal(t, "gpt-fixture-mapped", request.model)
			assert.Equal(t, "Bearer synthetic-not-a-real-key", request.authorization)
			assert.Greater(t, request.deadline, 90*time.Second, "each sample needs its own full response budget")
			assert.LessOrEqual(t, request.deadline, 120*time.Second)
			assert.False(t, prompts[request.prompt], "each dispatch must contain a distinct challenge")
			prompts[request.prompt] = true
		case <-time.After(10 * time.Second):
			t.Fatal("three fingerprint requests were not simultaneously in flight")
		}
	}
}

func captureFingerprintAnalysis(t *testing.T) *[]modelfingerprint.Sample {
	t.Helper()
	var analyzed []modelfingerprint.Sample
	original := modelFingerprintScorer.analyze
	modelFingerprintScorer.analyze = func(samples []modelfingerprint.Sample, strict bool) (modelfingerprint.Result, error) {
		assert.True(t, strict)
		analyzed = slices.Clone(samples)
		return original(samples, strict)
	}
	t.Cleanup(func() { modelFingerprintScorer.analyze = original })
	return &analyzed
}

func TestModelFingerprintPostgresManualParallelCollection(t *testing.T) {
	for _, mode := range []string{"success", "one-incomplete", "one-unknown", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			db, userID := setupModelTestSwitchExecution(t)
			require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Updates(map[string]any{"base_url": "https://fingerprint-parallel.invalid", "model_mapping": `{"gpt-4o-mini":"gpt-fixture-mapped"}`}).Error)
			model.InvalidatePricingCache()
			transport := installFingerprintParallelTransport(t, mode)
			analyzed := captureFingerprintAnalysis(t)
			engine := modelTestSwitchEngine(userID, nil)
			id := uuid.NewString()
			body := modelTestRequestBody(t, userID, id)
			ctx, cancel := context.WithCancel(t.Context())
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				engine.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodPost, "/runs", strings.NewReader(body)))
			}()
			t.Cleanup(func() {
				cancel()
				transport.unblock()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("manual fingerprint workers did not stop")
				}
			})
			awaitFingerprintParallelDispatch(t, transport)
			stored, err := model.GetModelTestRun(userID, id)
			require.NoError(t, err)
			var intents []modelFingerprintSampleAudit
			require.NoError(t, common.UnmarshalJsonStr(stored.SamplesJSON, &intents))
			require.Len(t, intents, 3)
			requestIDs := make(map[string]bool)
			for _, intent := range intents {
				assert.Equal(t, "dispatching", intent.Status, "persist intent before entering transport")
				assert.NotEmpty(t, intent.RequestID)
				assert.False(t, requestIDs[intent.RequestID])
				requestIDs[intent.RequestID] = true
			}
			if mode == "cancel" {
				_, err = model.CancelModelTestRun(userID, id)
				require.NoError(t, err)
			}
			transport.unblock()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("manual fingerprint collection did not finish")
			}
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			stored, err = model.GetModelTestRun(userID, id)
			require.NoError(t, err)
			var audits []modelFingerprintSampleAudit
			require.NoError(t, common.UnmarshalJsonStr(stored.SamplesJSON, &audits))
			require.Len(t, audits, 3)
			require.Len(t, *analyzed, 3)
			var owners []model.BillingFinalization
			require.NoError(t, db.Where("user_id = ?", userID).Find(&owners).Error)
			require.Len(t, owners, 3)
			settled, unknown := 0, 0
			for _, owner := range owners {
				assert.True(t, requestIDs[owner.RequestID])
				switch owner.State {
				case model.BillingSettled:
					settled++
					assert.Equal(t, 500, owner.ActualQuota)
				case model.BillingDeliveryUnknown:
					unknown++
				default:
					t.Errorf("unexpected financial owner state: %s", owner.State)
				}
			}
			valid, incomplete := 0, 0
			for i, sample := range *analyzed {
				assert.Equal(t, intents[i].ChallengeID, sample.ChallengeID, "completion order must not reorder challenge slots")
				assert.Equal(t, intents[i].RequestID, audits[i].RequestID)
				assert.NotZero(t, audits[i].CompletedAt)
				if sample.Error == "" {
					valid++
				} else {
					incomplete++
				}
			}
			switch mode {
			case "success":
				assert.Equal(t, "completed", stored.Status)
				assert.Equal(t, 3, valid)
				assert.Equal(t, 3, settled)
			case "one-incomplete":
				assert.Equal(t, "failed", stored.Status, "a known billed but truncated response is not financial uncertainty")
				assert.Equal(t, 2, valid)
				assert.Equal(t, 1, incomplete)
				assert.Equal(t, 3, settled)
				for i, sample := range *analyzed {
					require.NotNil(t, audits[i].Usage, "retain real usage even when fingerprint content is invalid")
					if sample.Error != "" {
						assert.True(t, sample.Truncated)
						assert.Equal(t, "failed", audits[i].Status)
					}
				}
			case "one-unknown":
				assert.Equal(t, "unknown", stored.Status)
				assert.Equal(t, 2, valid, "one failure must not cancel other admitted calls")
				assert.Equal(t, 2, settled)
				assert.Equal(t, 1, unknown)
			case "cancel":
				assert.Equal(t, "cancelled", stored.Status)
				assert.Zero(t, valid)
				assert.Equal(t, 3, unknown)
			}
			assert.EqualValues(t, 3, transport.calls.Load())
			// Replaying the same confirmed request reads its history, even after
			// incomplete or unknown outcomes, without a fourth paid generation.
			replay := httptest.NewRecorder()
			engine.ServeHTTP(replay, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
			assert.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
			assert.EqualValues(t, 3, transport.calls.Load())
		})
	}
}

func TestModelFingerprintPostgresSiteParallelCollection(t *testing.T) {
	for _, mode := range []string{"success", "one-incomplete", "one-unknown", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			db, userID, task, target, run := setupFingerprintSiteExecution(t)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", target.ChannelID).Update("base_url", "https://fingerprint-parallel.invalid").Error)
			transport := installFingerprintParallelTransport(t, mode)
			analyzed := captureFingerprintAnalysis(t)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- executeSiteModelFingerprint(ctx, task, task.LockedBy, target, run) }()
			joined := false
			t.Cleanup(func() {
				cancel()
				transport.unblock()
				if joined {
					return
				}
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("site fingerprint workers did not stop")
				}
			})
			awaitFingerprintParallelDispatch(t, transport)
			var intents []model.ModelFingerprintSiteProbe
			require.NoError(t, db.Where("run_id = ?", run.ID).Find(&intents).Error)
			require.Len(t, intents, 3)
			requestIDs := make(map[string]bool)
			for _, intent := range intents {
				assert.Equal(t, "dispatching", intent.Status)
				assert.NotEmpty(t, intent.RequestID)
				assert.False(t, requestIDs[intent.RequestID])
				requestIDs[intent.RequestID] = true
			}
			if mode == "cancel" {
				cancel()
			}
			transport.unblock()
			var executionErr error
			select {
			case executionErr = <-done:
				joined = true
			case <-time.After(10 * time.Second):
				t.Fatal("site fingerprint collection did not finish")
			}
			var saved model.ModelFingerprintSiteRun
			require.NoError(t, db.First(&saved, "id = ?", run.ID).Error)
			config, err := model.GetModelFingerprintSiteConfig(t.Context())
			require.NoError(t, err)
			var probes []model.ModelFingerprintSiteProbe
			require.NoError(t, db.Where("run_id = ?", run.ID).Find(&probes).Error)
			require.Len(t, probes, 3)
			require.Len(t, *analyzed, 3)
			completed, promptTokens, completionTokens := 0, 0, 0
			for _, probe := range probes {
				assert.True(t, requestIDs[probe.RequestID])
				if probe.Status == "completed" {
					completed++
					promptTokens += probe.PromptTokens
					completionTokens += probe.CompletionTokens
				} else {
					assert.Equal(t, "dispatching", probe.Status)
				}
			}
			switch mode {
			case "success", "one-incomplete":
				require.NoError(t, executionErr)
				assert.Equal(t, "succeeded", saved.Status)
				assert.Equal(t, "known", saved.UsageState)
				assert.Equal(t, 3, completed)
				assert.True(t, config.AutomaticEnabled)
				if mode == "one-incomplete" {
					invalid := 0
					for _, sample := range *analyzed {
						if sample.Error != "" {
							invalid++
							assert.True(t, sample.Truncated)
						}
					}
					assert.Equal(t, 1, invalid)
					assert.NotEqual(t, "scored", saved.Assessment)
				}
			case "one-unknown":
				require.Error(t, executionErr)
				assert.Equal(t, "unknown", saved.Status)
				assert.Equal(t, "unknown", saved.UsageState)
				assert.Equal(t, 2, completed, "two successful responses must still be recorded")
				assert.False(t, config.AutomaticEnabled, "a successful sibling cannot clear unknown usage")
				assert.Equal(t, run.ConfigVersion+1, config.Version)
			case "cancel":
				require.Error(t, executionErr)
				assert.Equal(t, "unknown", saved.Status)
				assert.Equal(t, "unknown", saved.UsageState)
				assert.Zero(t, completed)
				assert.False(t, config.AutomaticEnabled)
			}
			assert.Equal(t, completed*120, promptTokens)
			assert.Equal(t, completed*100, completionTokens)
			assert.EqualValues(t, 3, transport.calls.Load())
			var user model.User
			require.NoError(t, db.First(&user, userID).Error)
			assert.Equal(t, 1_000_000, user.QuotaPaid)
			var owners int64
			require.NoError(t, db.Model(&model.BillingFinalization{}).Count(&owners).Error)
			assert.Zero(t, owners, "site sampling must not create user financial owners")
		})
	}
}

func TestModelFingerprintPostgresSiteRevocationAfterIntent(t *testing.T) {
	for _, mode := range []string{"http", "sdk"} {
		t.Run(mode, func(t *testing.T) {
			db, _, task, target, run := setupFingerprintSiteExecution(t)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", target.ChannelID).Update("base_url", "https://fingerprint-parallel.invalid").Error)
			if mode == "sdk" {
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", target.ChannelID).Updates(map[string]any{
					"type": constant.ChannelTypeAws,
					"key":  "SYNTHETICUNSENT|synthetic-not-a-real-secret|us-east-1",
				}).Error)
			}
			model.InvalidatePricingCache()
			transport := installFingerprintParallelTransport(t, "success")
			transport.unblock()
			var cutoff atomic.Bool
			const callback = "test:fingerprint-site-cutoff-after-intent"
			require.NoError(t, model.DB.Callback().Create().After("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table != "model_fingerprint_site_probes" || tx.Error != nil || !cutoff.CompareAndSwap(false, true) {
					return
				}
				// Commit the dispatch intent and switch revocation together. The final
				// adaptor gate must refuse HTTP, and the intent must become not_sent.
				err := tx.Session(&gorm.Session{NewDB: true}).Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error
				if err != nil {
					tx.AddError(err)
				}
			}))
			t.Cleanup(func() { require.NoError(t, model.DB.Callback().Create().Remove(callback)) })
			require.Error(t, executeSiteModelFingerprint(t.Context(), task, task.LockedBy, target, run))
			assert.True(t, cutoff.Load())
			assert.Zero(t, transport.calls.Load(), "final authorization must prevent the network send")
			var probes []model.ModelFingerprintSiteProbe
			require.NoError(t, db.Where("run_id = ?", run.ID).Find(&probes).Error)
			require.Len(t, probes, 1)
			assert.Equal(t, "not_sent", probes[0].Status)
			assert.NotZero(t, probes[0].CompletedAt)
			assert.Zero(t, probes[0].PromptTokens)
			assert.Zero(t, probes[0].CompletionTokens)
			var saved model.ModelFingerprintSiteRun
			require.NoError(t, db.First(&saved, "id = ?", run.ID).Error)
			assert.Equal(t, "failed", saved.Status)
			assert.Equal(t, "known", saved.UsageState)
			config, err := model.GetModelFingerprintSiteConfig(t.Context())
			require.NoError(t, err)
			assert.True(t, config.AutomaticEnabled, "a proven unsent request must not trigger an unknown-usage pause")
			assert.Equal(t, run.ConfigVersion, config.Version)
		})
	}
}
