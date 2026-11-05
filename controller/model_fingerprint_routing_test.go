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
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFingerprintSitePostgresRoutesGroupModelAcrossProviders(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeNewAPI, constant.ChannelTypeAnthropic, constant.ChannelTypeGemini, constant.ChannelTypeCohere, constant.ChannelTypeCline, constant.ChannelTypeOllama, constant.ChannelTypeCoze} {
		t.Run(fmt.Sprint(channelType), func(t *testing.T) {
			db, userID, task, target, run := setupFingerprintSiteExecution(t)
			selectedID := target.ChannelID
			target.ChannelID = 0
			config, err := model.GetModelFingerprintSiteConfig(context.Background())
			require.NoError(t, err)
			if channelType == constant.ChannelTypeNewAPI {
				// Exercise the actual settings handler with an enabled zero-budget
				// target before admission, not just model-level normalization.
				body, marshalErr := common.Marshal(map[string]any{"enabled": true, "automatic_enabled": true, "expected_version": config.Version, "default_period_secs": 3600, "daily_budget_quota": 0, "targets": config.Targets})
				require.NoError(t, marshalErr)
				response := securityEnrollmentRequest(http.MethodPut, "/settings", string(body), "", service.AuthIdentity{UserID: userID, SessionID: "fixture-session", UserAuthVersion: 1, SessionVersion: 1}, UpdateModelFingerprintSettings)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.NoError(t, db.Delete(run).Error)
				config, err = model.GetModelFingerprintSiteConfig(context.Background())
				require.NoError(t, err)
				run, err = model.ReserveModelFingerprintSiteRun(context.Background(), task.TaskID, task.LockedBy, target.ID, config.Version, time.Now().Unix())
				require.NoError(t, err)
			}
			// A pre-upgrade configuration must not restore a channel pin, output
			// cap, or budget even before its next settings save.
			config.Targets[0].ChannelID = 999
			config.Targets[0].MaxOutputTokens = 2048
			config.Targets[0].MaxRunQuota = 1
			encoded, err := common.Marshal(config.Targets)
			require.NoError(t, err)
			require.NoError(t, db.Model(&model.ModelFingerprintSiteConfig{}).Where("id = ?", 1).Updates(map[string]any{"targets_json": string(encoded), "daily_budget_quota": 1, "reserved_quota": 999}).Error)
			require.NoError(t, db.Model(run).Update("channel_id", 0).Error)
			priority := int64(100)
			require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", selectedID).Update("priority", priority).Error)
			lowerPriority := int64(0)
			fallback := model.Channel{Name: "not-selected", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: target.Model, Group: target.Group, Key: "unused-synthetic-key", BaseURL: common.GetPointer("https://must-not-be-called.invalid")}
			require.NoError(t, db.Create(&fallback).Error)
			require.NoError(t, db.Create(&model.Ability{ChannelId: fallback.Id, Group: target.Group, Model: target.Model, Enabled: true, Priority: &lowerPriority}).Error)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if channelType == constant.ChannelTypeCoze && r.Method == http.MethodGet {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/v3/chat/retrieve":
						fmt.Fprint(w, `{"code":0,"data":{"id":"fixture-chat","conversation_id":"fixture-conversation","status":"completed","usage":{"input_count":120,"output_count":100,"token_count":220}}}`)
					case "/v3/chat/message/list":
						fmt.Fprint(w, `{"code":0,"data":[{"type":"answer","role":"assistant","content":"0.1, 0.2, 0.3","content_type":"text"}]}`)
					default:
						t.Errorf("unexpected Coze polling path %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
					return
				}
				calls.Add(1)
				var request map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &request))
				w.Header().Set("Content-Type", "application/json")
				switch channelType {
				case constant.ChannelTypeAnthropic:
					assert.Equal(t, "/v1/messages", r.URL.Path)
					assert.Equal(t, "synthetic-not-a-real-key", r.Header.Get("x-api-key"))
					assert.Greater(t, request["max_tokens"].(float64), float64(0), "Anthropic keeps its ordinary required default")
					fmt.Fprint(w, `{"id":"fixture","type":"message","role":"assistant","model":"gpt-fixture-mapped","content":[{"type":"text","text":"0.1, 0.2, 0.3"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"cache_read_input_tokens":20,"output_tokens":100}}`)
				case constant.ChannelTypeGemini:
					assert.True(t, strings.HasSuffix(r.URL.Path, "/models/gpt-fixture-mapped:generateContent"), r.URL.Path)
					assert.Equal(t, "synthetic-not-a-real-key", r.Header.Get("x-goog-api-key"))
					if generation, ok := request["generationConfig"].(map[string]any); ok {
						assert.NotContains(t, generation, "maxOutputTokens")
					}
					fmt.Fprint(w, `{"modelVersion":"gpt-fixture-mapped","candidates":[{"content":{"role":"model","parts":[{"text":"0.1, 0.2, 0.3"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":80,"thoughtsTokenCount":20,"totalTokenCount":220}}`)
				case constant.ChannelTypeCoze:
					assert.Equal(t, "/v3/chat", r.URL.Path)
					assert.NotContains(t, request, "max_tokens")
					fmt.Fprint(w, `{"code":0,"data":{"id":"fixture-chat","conversation_id":"fixture-conversation","status":"in_progress"}}`)
				case constant.ChannelTypeCohere:
					assert.Equal(t, "/v1/chat", r.URL.Path)
					assert.EqualValues(t, 4000, request["max_tokens"], "Cohere retains its ordinary adaptor default")
					fmt.Fprint(w, `{"response_id":"fixture","text":"0.1, 0.2, 0.3","finish_reason":"COMPLETE","meta":{"billed_units":{"input_tokens":120,"output_tokens":100}}}`)
				case constant.ChannelTypeCline:
					assert.Equal(t, "/v1/chat/completions", r.URL.Path)
					assert.NotContains(t, request, "max_tokens")
					fmt.Fprint(w, `{"success":true,"data":{"id":"fixture","model":"gpt-fixture-mapped","choices":[{"message":{"role":"assistant","content":"0.1, 0.2, 0.3"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":100,"total_tokens":220}}}`)
				case constant.ChannelTypeOllama:
					assert.Equal(t, "/api/chat", r.URL.Path)
					if options, ok := request["options"].(map[string]any); ok {
						assert.NotContains(t, options, "num_predict")
					}
					fmt.Fprint(w, `{"model":"gpt-fixture-mapped","message":{"role":"assistant","content":"0.1, 0.2, 0.3"},"done":true,"done_reason":"stop","prompt_eval_count":120,"eval_count":100}`)
				default:
					assert.Equal(t, "/v1/chat/completions", r.URL.Path)
					assert.NotContains(t, request, "max_tokens")
					fmt.Fprint(w, `{"id":"fixture","model":"gpt-fixture-mapped","choices":[{"message":{"role":"assistant","content":"0.1, 0.2, 0.3"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":100,"total_tokens":220}}`)
				}
			}))
			defer upstream.Close()
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", selectedID).Updates(map[string]any{"base_url": upstream.URL, "type": channelType, "priority": priority}).Error)
			model.InvalidatePricingCache()
			catalogue, err := fingerprintSiteCatalogue()
			require.NoError(t, err)
			assert.Contains(t, catalogue.ModelsByGroup[target.Group], target.Model)
			personal, err := modelTestCatalogue(userID)
			require.NoError(t, err)
			assert.Contains(t, personal[target.Group], target.Model)
			client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
			require.NoError(t, err)
			oldClient := *client
			t.Cleanup(func() { *client = oldClient })
			client.Transport = modelDiscoveryRoundTripper(func(req *http.Request) (*http.Response, error) {
				if req.URL.Scheme+"://"+req.URL.Host != upstream.URL {
					return nil, fmt.Errorf("unexpected fixture destination")
				}
				return http.DefaultTransport.RoundTrip(req)
			})
			require.NoError(t, executeSiteModelFingerprint(context.Background(), task, task.LockedBy, target, run))
			assert.EqualValues(t, 3, calls.Load(), "one selected channel serves all three probes")
			var saved model.ModelFingerprintSiteRun
			require.NoError(t, db.First(&saved, "id = ?", run.ID).Error)
			assert.Equal(t, selectedID, saved.ChannelID, "normal routing honors priority instead of a configured channel pin")
			assert.Equal(t, "known", saved.UsageState)
			assert.Equal(t, "succeeded", saved.Status)
			storedConfig, err := model.GetModelFingerprintSiteConfig(context.Background())
			require.NoError(t, err)
			assert.Zero(t, storedConfig.DailyBudgetQuota)
			assert.Zero(t, storedConfig.Targets[0].MaxOutputTokens)
			assert.Zero(t, storedConfig.Targets[0].MaxRunQuota)
			assert.EqualValues(t, 999, storedConfig.ReservedQuota, "legacy ledger entries are never refunded or changed")
			var probes []model.ModelFingerprintSiteProbe
			require.NoError(t, db.Where("run_id = ?", run.ID).Find(&probes).Error)
			require.Len(t, probes, 3)
			for _, probe := range probes {
				assert.Equal(t, 120, probe.PromptTokens)
				assert.Equal(t, 100, probe.CompletionTokens)
			}
		})
	}
}
