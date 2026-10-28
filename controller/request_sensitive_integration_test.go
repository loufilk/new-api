package controller

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A shared-model channel can choose a different decoder from the initial
// candidate. Check that provider's normalized prompt before its driver hook,
// including a vendor field that the raw-body text extractor does not know.
func TestTaskSensitivePolicyChecksSelectedPluginNormalizationBeforeDriver(t *testing.T) {
	require.NoError(t, i18n.Init())
	enabled, promptEnabled := setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled
	words, exemptions := setting.SensitiveWordsToString(), setting.SensitiveCheckExemptGroupsToString()
	autoDisable, disableCodes := common.AutomaticDisableChannelEnabled, operation_setting.AutomaticDisableStatusCodesToString()
	t.Cleanup(func() {
		setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = enabled, promptEnabled
		setting.SensitiveWordsFromString(words)
		setting.LoadSensitiveCheckExemptGroups(exemptions)
		common.AutomaticDisableChannelEnabled = autoDisable
		require.NoError(t, operation_setting.AutomaticDisableStatusCodesFromString(disableCodes))
	})
	setting.CheckSensitiveOnPromptEnabled = true
	setting.SensitiveWordsFromString("blocked")
	setting.LoadSensitiveCheckExemptGroups("")
	common.AutomaticDisableChannelEnabled = true
	require.NoError(t, operation_setting.AutomaticDisableStatusCodesFromString("400"))
	const source = `
export const meta = {apiVersion:1,key:"KEY",name:"KEY",version:"1.0.0",author:{name:"Test"},models:["policy-shared-model"],fetchMode:"per_task",protocols:["openai_video"]};
export const protocols = {openai_video:{
  decodeRequest:function(ctx){
    const prompt = ctx.body.kind === "json" ? ctx.body.value.FIELD : ctx.body.fields.FIELD[0];
    return {kind:"submit",model:ctx.model,requestBody:{prompt:prompt}};
  },
  render:function(){return {};}
}};
export function buildSubmitRequest(ctx){throw new Error("driver hook reached with " + ctx.requestBody.prompt);}
export function parseSubmitResponse(){return {taskId:"unexpected"};}
export function buildQueryRequest(){return {url:"https://provider.example/task"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export function listArtifacts(){return [];}
export function buildContentRequest(){throw new Error("artifact_not_found");}
`
	for _, spec := range []struct{ key, field string }{{"policy-alpha", "alpha_prompt"}, {"policy-beta", "vendor_prompt"}} {
		_, err := pluginruntime.DefaultRegistry.Register(strings.NewReplacer("KEY", spec.key, "FIELD", spec.field).Replace(source), pluginruntime.Options{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, pluginruntime.DefaultRegistry.Unregister(spec.key)) })
	}
	baseURL := "https://provider.example"
	selected := &model.Channel{Id: 902, Type: constant.ChannelTypeTaskPlugin, Name: "second provider", Key: "test-key", BaseURL: &baseURL, Status: common.ChannelStatusEnabled, Group: "default", Models: "policy-shared-model"}
	selected.SetSetting(dto.ChannelSettings{TaskPluginKey: "policy-beta"})
	for _, format := range []string{"json", "multipart"} {
		for _, tc := range []struct {
			name, initialPrompt, selectedPrompt string
			checkEnabled, blocked               bool
		}{
			{"selected provider prompt is blocked", "safe", "blocked", true, true},
			{"discarded provider prompt does not block", "blocked", "safe", true, false},
			{"disabled policy still permits the driver", "safe", "blocked", false, false},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				setting.CheckSensitiveEnabled = tc.checkEnabled
				body := []byte(fmt.Sprintf(`{"model":"policy-shared-model","alpha_prompt":%q,"vendor_prompt":%q}`, tc.initialPrompt, tc.selectedPrompt))
				contentType := "application/json"
				if format == "multipart" {
					var buffer bytes.Buffer
					writer := multipart.NewWriter(&buffer)
					for _, field := range []struct{ key, value string }{{"model", "policy-shared-model"}, {"alpha_prompt", tc.initialPrompt}, {"vendor_prompt", tc.selectedPrompt}} {
						require.NoError(t, writer.WriteField(field.key, field.value))
					}
					file, err := writer.CreateFormFile("input_reference", "reference.png")
					require.NoError(t, err)
					_, err = file.Write([]byte("uploaded-image"))
					require.NoError(t, err)
					require.NoError(t, writer.Close())
					body, contentType = buffer.Bytes(), writer.FormDataContentType()
				}
				called := false
				router := gin.New()
				router.POST("/v1/videos", middleware.PinTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), func(c *gin.Context) {
					called = true
					defer common.CleanupBodyStorage(c)
					assert.Equal(t, "policy-alpha", c.GetString("task_plugin_key"))
					assert.Equal(t, map[string]any{"prompt": tc.initialPrompt}, c.MustGet("task_request"))
					common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
					common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
					common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
					require.Nil(t, middleware.SetupContextForSelectedChannel(c, selected, "policy-shared-model"))
					assert.Equal(t, "policy-beta", c.GetString("task_plugin_key"))
					info := taskSubmissionRelayInfo(nil)
					info.OriginModelName = "policy-shared-model"
					_, taskErr := relay.RelayTaskSubmit(c, info)
					require.NotNil(t, taskErr)
					if tc.blocked {
						assert.Equal(t, "sensitive_words_detected", taskErr.Code, taskErr.Message)
						assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
						assert.True(t, taskErr.LocalError)
						assert.NotContains(t, taskErr.Message, "driver hook reached")
						apiErr := taskSubmissionAPIError(taskErr)
						assert.Equal(t, types.ErrorTypeNewAPIError, apiErr.GetErrorType())
						assert.Equal(t, types.ErrorCodeSensitiveWordsDetected, apiErr.GetErrorCode())
						assert.False(t, service.ShouldDisableChannel(apiErr), "local rejection must not match upstream auto-disable rules")
						decision := decideTaskRetry(c, taskErr, 3)
						assert.Equal(t, "stop", decision.Action)
						service.RecordPolicyFailure(c, selected.Id, apiErr, decision)
						events := service.RequestPolicy(c).Events()
						require.NotEmpty(t, events)
						assert.Equal(t, "local", events[len(events)-1].ErrorSource)
						assert.Equal(t, "unchanged", events[len(events)-1].Health)
					} else {
						assert.Equal(t, "plugin_request_invalid", taskErr.Code)
						assert.Contains(t, taskErr.Message, "driver hook reached with "+tc.selectedPrompt)
					}
					assert.Nil(t, info.Billing, "rejection precedes a billing reservation")
					storage, err := common.GetBodyStorage(c)
					require.NoError(t, err)
					preserved, err := storage.Bytes()
					require.NoError(t, err)
					assert.Equal(t, body, preserved)
					if format == "multipart" {
						form, err := common.ParseMultipartFormReusable(c)
						require.NoError(t, err)
						defer form.RemoveAll()
						require.Len(t, form.File["input_reference"], 1)
						file, err := form.File["input_reference"][0].Open()
						require.NoError(t, err)
						defer file.Close()
						image, err := io.ReadAll(file)
						require.NoError(t, err)
						assert.Equal(t, "uploaded-image", string(image))
					}
					c.Status(http.StatusNoContent)
				})
				request := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
				request.Header.Set("Content-Type", contentType)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				assert.True(t, called, recorder.Body.String())
				assert.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
			})
		}
	}
}

// Exercise the production auth, distributor, HTTP/WS relay, and billing path.
// The exempt attempt may reserve funds, but switching to a checked group must
// reject before a second upstream call or a higher-group reserve top-up.
func TestRequestSensitiveHTTPAndWebSocketBeforeBilling(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, transport := range []string{"http", "websocket"} {
		scenarios := []string{"default cannot spoof group", "authorized exemption", "auto cross-group retry", "auto token scope", "unauthorized group"}
		if transport == "http" {
			scenarios = append(scenarios, "fixed channel mismatch")
		}
		for _, scenario := range scenarios {
			t.Run(transport+"/"+scenario, func(t *testing.T) {
				var upstreamCalls, failedCalls atomic.Int64
				fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(ws *websocket.Conn, _ *http.Request) {
					if _, _, err := ws.ReadMessage(); !assert.NoError(t, err) {
						return
					}
					upstreamCalls.Add(1)
					if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"checked","status":"completed","model":"ws-billing","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))) {
						return
					}
					_, _, _ = ws.ReadMessage()
				})
				fixture.httpUpstream = func(w http.ResponseWriter, _ *http.Request) {
					upstreamCalls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"checked","object":"response","status":"completed","model":"ws-billing","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
				}
				// Re-authentication must read the per-scenario token write, not
				// the token cached by the fixture's initial WS handshake.
				common.RedisEnabled = false
				enabled, promptEnabled := setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled
				words, exemptions := setting.SensitiveWordsToString(), setting.SensitiveCheckExemptGroupsToString()
				auto, usable, ratios := setting.AutoGroups2JsonString(), setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString()
				retries := common.RetryTimes
				t.Cleanup(func() {
					setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = enabled, promptEnabled
					setting.SensitiveWordsFromString(words)
					setting.LoadSensitiveCheckExemptGroups(exemptions)
					require.NoError(t, setting.UpdateAutoGroupsByJsonString(auto))
					require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(usable))
					require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(ratios))
					common.RetryTimes = retries
				})
				setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = true, true
				setting.SensitiveWordsFromString("blocked")
				setting.LoadSensitiveCheckExemptGroups("破甲")
				require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["破甲","default"]`))
				require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","破甲":"Exempt","auto":"Auto"}`))
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":3,"破甲":1}`))
				common.RetryTimes = 1

				tokenUpdates := map[string]any{"group": "default"}
				switch scenario {
				case "authorized exemption":
					tokenUpdates["group"] = "破甲"
					require.NoError(t, model.DB.Model(fixture.channel).Update("group", "破甲").Error)
					require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", fixture.channel.Id).Update("group", "破甲").Error)
				case "auto cross-group retry", "auto token scope":
					tokenUpdates["group"], tokenUpdates["cross_group_retry"] = "auto", true
					if scenario == "auto token scope" {
						tokenUpdates["auto_groups"] = `["default"]`
					}
					failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						failedCalls.Add(1)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"error":{"message":"unavailable","type":"upstream_error"}}`)
					}))
					t.Cleanup(failing.Close)
					channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "exempt-attempt", Key: "test-key", BaseURL: &failing.URL, Status: common.ChannelStatusEnabled, Group: "破甲", Models: "ws-billing"}
					channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: true})
					require.NoError(t, model.DB.Create(channel).Error)
					require.NoError(t, model.DB.Create(&model.Ability{ChannelId: channel.Id, Group: "破甲", Model: "ws-billing", Enabled: true}).Error)
					t.Cleanup(func() {
						require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).Delete(&model.Ability{}).Error)
						require.NoError(t, model.DB.Delete(channel).Error)
					})
				case "unauthorized group":
					tokenUpdates["group"] = "破甲"
					require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
				case "fixed channel mismatch":
					tokenUpdates["group"] = "破甲"
					require.NoError(t, model.DB.Model(fixture.user).Update("role", common.RoleAdminUser).Error)
				}
				require.NoError(t, model.DB.Model(fixture.token).Updates(tokenUpdates).Error)
				allowed := scenario == "authorized exemption"
				if transport == "http" {
					request, err := http.NewRequest(http.MethodPost, fixture.gatewayURL+"/v1/responses?group=破甲",
						strings.NewReader(`{"model":"ws-billing","input":"blocked","group":"破甲"}`))
					require.NoError(t, err)
					authorization := "Bearer sk-" + fixture.token.Key
					if scenario == "fixed channel mismatch" {
						authorization += fmt.Sprintf("-%d", fixture.channel.Id)
					}
					request.Header.Set("Authorization", authorization)
					request.Header.Set("Content-Type", "application/json")
					response, err := http.DefaultClient.Do(request)
					require.NoError(t, err)
					body, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					if allowed {
						assert.Equal(t, http.StatusOK, response.StatusCode, string(body))
					} else if scenario == "unauthorized group" {
						assert.Equal(t, http.StatusForbidden, response.StatusCode, string(body))
					} else {
						assert.Equal(t, http.StatusBadRequest, response.StatusCode, string(body))
						assert.Contains(t, string(body), "sensitive_words_detected")
					}
					if scenario != "unauthorized group" {
						select {
						case <-fixture.httpDone:
						case <-time.After(3 * time.Second):
							t.Fatal("HTTP request did not finish")
						}
					}
				} else {
					require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"blocked","group":"破甲"}`)))
					event := readResponsesWSTestEvent(t, fixture.client)
					if allowed {
						assert.Equal(t, "response.completed", event["type"])
					} else {
						assert.Equal(t, "error", event["type"])
						if scenario != "unauthorized group" {
							assert.Contains(t, fmt.Sprint(event), "sensitive_words_detected")
						}
					}
				}
				fixture.closeAndWait(t)
				if allowed {
					assert.Equal(t, int64(1), upstreamCalls.Load())
					assertResponsesWSAccounting(t, fixture, []int{1000})
				} else {
					assert.Zero(t, upstreamCalls.Load(), "checked route cannot reach upstream")
					assertResponsesWSAccounting(t, fixture, nil)
				}
				if scenario == "auto cross-group retry" {
					assert.Equal(t, int64(2), failedCalls.Load(), "the exempt group exhausts its initial attempt and one retry before fallback is blocked")
				} else {
					assert.Zero(t, failedCalls.Load())
				}
				var reserves int64
				require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Where("user_id = ? AND delta < 0", fixture.user.Id).Count(&reserves).Error)
				if allowed || scenario == "auto cross-group retry" {
					assert.Equal(t, int64(1), reserves, "no higher-group precharge occurs after the checked fallback")
				} else {
					assert.Zero(t, reserves, "rejection must precede any precharge, not merely refund it")
				}
			})
		}
	}
}
