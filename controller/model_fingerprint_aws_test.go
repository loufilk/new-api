package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	awsadaptor "github.com/QuantumNous/new-api/relay/channel/aws"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The actual AWS SDK signs synthetic credentials, but its shared relay client's
// transport can reach only this local server. No AWS network access is possible.
func installFingerprintAWSFixtureTransport(t *testing.T, upstream *httptest.Server) {
	t.Helper()
	local, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
	require.NoError(t, err)
	oldClient := *client
	t.Cleanup(func() { *client = oldClient })
	transport := upstream.Client().Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = modelDiscoveryRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "bedrock-runtime.us-east-1.amazonaws.com" {
			return nil, fmt.Errorf("unexpected AWS fixture destination")
		}
		localRequest := req.Clone(req.Context())
		localRequest.URL.Scheme, localRequest.URL.Host = local.Scheme, local.Host
		localRequest.Host = local.Host
		return transport.RoundTrip(localRequest)
	})
}

func TestFingerprintSitePostgresAWSUsesSDKResponseAndRealUsage(t *testing.T) {
	for _, mode := range []string{"claude", "nova", "http-error", "missing-usage"} {
		t.Run(mode, func(t *testing.T) {
			db, _, task, target, run := setupFingerprintSiteExecution(t)
			selectedID := target.ChannelID
			target.ChannelID = 0
			mappedModel := "gpt-fixture-mapped"
			invokeModel := mappedModel
			if mode == "nova" {
				mappedModel = "amazon.nova-micro-v1:0"
				invokeModel = "us.amazon.nova-micro-v1:0"
			}
			mapping, err := common.Marshal(map[string]string{target.Model: mappedModel})
			require.NoError(t, err)
			channelInfo := model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling}
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", selectedID).Updates(map[string]any{
				"type": constant.ChannelTypeAws, "model_mapping": string(mapping), "channel_info": channelInfo,
				"key": "SYNTHETICFIRST|synthetic-not-a-real-secret|us-east-1\nSYNTHETICSECOND|synthetic-second-secret|us-east-1",
			}).Error)
			model.InvalidatePricingCache()
			catalogue, err := fingerprintSiteCatalogue()
			require.NoError(t, err)
			assert.Contains(t, catalogue.ModelsByGroup[target.Group], target.Model, "AWS follows the same group/model catalogue")
			challenges, err := modelFingerprintScorer.challenges()
			require.NoError(t, err)
			require.Len(t, challenges, 3)
			var calls atomic.Int32
			var callsMu sync.Mutex
			callsByChallenge := make(map[string]int, len(challenges))
			allStarted := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if n == 3 {
					close(allStarted)
				}
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/model/"+invokeModel+"/invoke", r.URL.Path)
				assert.Contains(t, r.Header.Get("Authorization"), "Credential=SYNTHETICFIRST/", "all probes retain the first selected credential slot")
				assert.NotContains(t, r.Header.Get("Authorization"), "SYNTHETICSECOND")
				var request map[string]any
				if !assert.NoError(t, common.DecodeJson(r.Body, &request)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				assert.NotContains(t, request, "model")
				if mode == "nova" {
					assert.NotContains(t, request, "inferenceConfig", "fingerprints do not inject output limits")
				} else {
					assert.Equal(t, "bedrock-2023-05-31", request["anthropic_version"])
					assert.Greater(t, request["max_tokens"], float64(0), "Claude keeps its ordinary required provider default")
				}
				messages, err := common.Marshal(request["messages"])
				if !assert.NoError(t, err) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var challengeID string
				for _, challenge := range challenges {
					if strings.Contains(string(messages), challenge.Prompt) {
						challengeID = challenge.ID
						break
					}
				}
				if !assert.NotEmpty(t, challengeID, "the SDK request retains one of the three fixed challenges") {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				callsMu.Lock()
				callsByChallenge[challengeID]++
				callsMu.Unlock()
				var probe model.ModelFingerprintSiteProbe
				if assert.NoError(t, db.Where("run_id = ? AND probe_id = ?", run.ID, challengeID).Take(&probe).Error) {
					assert.Equal(t, "dispatching", probe.Status, "this probe's durable intent precedes its SDK request")
					assert.NotEmpty(t, probe.RequestID)
				}
				// Each sample must reach the provider before any response is
				// released, including failures that the SDK must not retry.
				select {
				case <-allStarted:
				case <-r.Context().Done():
					return
				case <-time.After(10 * time.Second):
					t.Error("three AWS samples did not reach the provider concurrently")
					w.WriteHeader(http.StatusGatewayTimeout)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "http-error" {
					w.Header().Set("X-Amzn-Errortype", "ServiceUnavailableException")
					w.WriteHeader(http.StatusServiceUnavailable)
					fmt.Fprint(w, `{"message":"synthetic upstream failure"}`)
					return
				}
				if mode == "nova" {
					fmt.Fprint(w, `{"output":{"message":{"role":"assistant","content":[{"text":"0.1, 0.2, 0.3"}]}},"usage":{"inputTokens":150,"outputTokens":100,"totalTokens":250}}`)
					return
				}
				usage := `,"usage":{"input_tokens":100,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"output_tokens":100}`
				if mode == "missing-usage" {
					usage = ""
				}
				fmt.Fprintf(w, `{"id":"fixture","type":"message","role":"assistant","model":"gpt-fixture-mapped","content":[{"type":"text","text":"0.1, 0.2, 0.3"}],"stop_reason":"end_turn"%s}`, usage)
			}))
			defer upstream.Close()
			installFingerprintAWSFixtureTransport(t, upstream)
			executionErr := executeSiteModelFingerprint(context.Background(), task, task.LockedBy, target, run)
			var saved model.ModelFingerprintSiteRun
			require.NoError(t, db.First(&saved, "id = ?", run.ID).Error)
			assert.Equal(t, selectedID, saved.ChannelID)
			assert.Zero(t, saved.ChannelKeyIndex)
			assert.Zero(t, saved.ActualQuota)
			var probes []model.ModelFingerprintSiteProbe
			require.NoError(t, db.Where("run_id = ?", run.ID).Order("id").Find(&probes).Error)
			assert.EqualValues(t, 3, calls.Load(), "three planned samples each get one SDK invocation")
			callsMu.Lock()
			for _, challenge := range challenges {
				assert.Equal(t, 1, callsByChallenge[challenge.ID], "AWS SDK must not retry challenge %s", challenge.ID)
			}
			callsMu.Unlock()
			require.Len(t, probes, 3)
			requestIDs := make(map[string]bool, len(probes))
			for _, probe := range probes {
				require.NotEmpty(t, probe.RequestID)
				assert.False(t, requestIDs[probe.RequestID], "parallel probes retain distinct journal identities")
				requestIDs[probe.RequestID] = true
			}
			if mode == "http-error" || mode == "missing-usage" {
				require.Error(t, executionErr)
				assert.Equal(t, "unknown", saved.Status)
				assert.Equal(t, "unknown", saved.UsageState)
				for _, probe := range probes {
					assert.Equal(t, "dispatching", probe.Status, "an unknown SDK outcome cannot be marked completed")
					assert.Zero(t, probe.PromptTokens)
					assert.Zero(t, probe.CompletionTokens)
				}
				config, err := model.GetModelFingerprintSiteConfig(t.Context())
				require.NoError(t, err)
				assert.False(t, config.AutomaticEnabled, "unknown usage pauses subsequent automatic runs")
				return
			}
			require.NoError(t, executionErr)
			assert.Equal(t, "succeeded", saved.Status)
			assert.Equal(t, "known", saved.UsageState)
			assert.Equal(t, mappedModel, saved.MappedModel)
			for _, probe := range probes {
				assert.Equal(t, "completed", probe.Status)
				assert.Equal(t, 150, probe.PromptTokens, "native cache read/write tokens are preserved, not replaced by estimates")
				assert.Equal(t, 100, probe.CompletionTokens)
			}
			var channel model.Channel
			require.NoError(t, db.First(&channel, selectedID).Error)
			assert.Equal(t, 1, channel.ChannelInfo.MultiKeyPollingIndex, "credential selection advances once for the entire run")
		})
	}
}

func TestFingerprintPostgresAWSCutoffBetweenPreparationAndInvoke(t *testing.T) {
	for _, provider := range []string{"claude", "claude-stream", "nova"} {
		t.Run(provider, func(t *testing.T) {
			db, _, _, _, _ := setupFingerprintSiteExecution(t)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Amzn-Errortype", "ValidationException")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"message":"synthetic ordinary request reached upstream"}`)
			}))
			defer upstream.Close()
			installFingerprintAWSFixtureTransport(t, upstream)
			for _, cutoff := range []string{"disabled", "cancelled", "ordinary-disabled"} {
				t.Run(cutoff, func(t *testing.T) {
					require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "true").Error)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", nil)
					common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, cutoff != "ordinary-disabled")
					mappedModel := "gpt-fixture-mapped"
					if provider == "nova" {
						mappedModel = "amazon.nova-micro-v1:0"
					}
					info := &relaycommon.RelayInfo{StartTime: time.Now(), RelayFormat: types.RelayFormatOpenAI, IsStream: provider == "claude-stream", OriginModelName: mappedModel, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "SYNTHETICFIRST|synthetic-not-a-real-secret|us-east-1", UpstreamModelName: mappedModel}}
					adaptor := &awsadaptor.Adaptor{IsNova: provider == "nova"}
					before := calls.Load()
					response, err := adaptor.DoRequest(c, info, strings.NewReader(`{"messages":[{"role":"user","content":[{"type":"text","text":"synthetic"}]}],"max_tokens":100}`))
					require.NoError(t, err)
					assert.Nil(t, response, "AWS DoRequest prepares SDK state without an HTTP response")
					assert.Equal(t, before, calls.Load())
					if cutoff == "cancelled" {
						cancel()
					} else {
						require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
					}
					_, apiErr := adaptor.DoResponse(c, nil, info)
					require.NotNil(t, apiErr)
					if cutoff == "ordinary-disabled" {
						assert.Equal(t, before+1, calls.Load(), "ordinary relay does not depend on model-test settings")
						assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
						return
					}
					assert.Equal(t, before, calls.Load(), "no SDK request after the final dispatch cutoff")
					assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
					assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
					assert.True(t, types.IsSkipRetryError(apiErr))
					if cutoff == "disabled" {
						assert.Equal(t, "model_tests_disabled", c.GetString("model_test_error_code"))
					}
				})
			}
		})
	}
}
