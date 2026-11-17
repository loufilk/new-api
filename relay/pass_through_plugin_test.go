package relay

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/request_plugin_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPassThroughEgressChecksFinalChannelOverride(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	plugin, err := jsplugin.DefaultRegistry.Register(`
export const meta = {apiVersion:1,key:"final-egress",name:"Final egress",version:"1.0.0",author:{name:"Test"}};
export function egress(ctx, req) {
  const text = req.body.input || req.body.messages[0].content;
  if(text === "blocked") return {action:"reject",code:"blocked",message:"Blocked"};
  return {action:"continue",findings:[{rule:"checked",category:"egress",count:1}]};
}`, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, jsplugin.DefaultRegistry.Unregister(plugin.Meta.Key)) })
	previousInstances, err := common.Marshal(request_plugin_setting.GetInstances())
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{request_plugin_setting.InstancesOption: `[{"id":"final-egress","name":"Final egress","plugin":"final-egress","enabled":true,"global":true}]`}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{request_plugin_setting.InstancesOption: string(previousInstances)}))
	})

	for _, route := range []struct {
		name, path, body, textPath string
		format                     types.RelayFormat
		channelType, mode          int
		handler                    func(*gin.Context, *relaycommon.RelayInfo) *types.NewAPIError
	}{
		{"chat", "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"%s"}]}`, "messages.0.content", types.RelayFormatOpenAI, constant.ChannelTypeOpenAI, relayconstant.RelayModeChatCompletions, TextHelper},
		{"claude", "/v1/messages", `{"model":"m","max_tokens":128,"messages":[{"role":"user","content":"%s"}]}`, "messages.0.content", types.RelayFormatClaude, constant.ChannelTypeAnthropic, relayconstant.RelayModeChatCompletions, ClaudeHelper},
		{"responses", "/v1/responses", `{"model":"m","input":"%s"}`, "input", types.RelayFormatOpenAIResponses, constant.ChannelTypeOpenAI, relayconstant.RelayModeResponses, ResponsesHelper},
	} {
		for _, tc := range []struct {
			name, input, override string
			blocked               bool
		}{
			{"override introduces blocked content", "allowed", "blocked", true},
			{"override removes blocked content", "blocked", "allowed", false},
			{"raw blocked content", "blocked", "", true},
			{"raw allowed content", "allowed", "", false},
		} {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				captured := make(chan []byte, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					assert.NoError(t, err)
					captured <- body
					// Stop before settlement: this test owns the final request body
					// and egress decision, not the response's billing.
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"upstream received the request"}}`)
				}))
				t.Cleanup(upstream.Close)
				raw := fmt.Sprintf(route.body, tc.input)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(raw))
				c.Request.Header.Set("Content-Type", "application/json")
				t.Cleanup(func() { common.CleanupBodyStorage(c) })
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "m")
				common.SetContextKey(c, constant.ContextKeyChannelType, route.channelType)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
				common.SetContextKey(c, constant.ContextKeyChannelKey, "test")
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
				if tc.override != "" {
					common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{map[string]any{"mode": "set", "path": route.textPath, "value": tc.override}}})
				}
				info := &relaycommon.RelayInfo{OriginModelName: "m", RelayFormat: route.format, RelayMode: route.mode, RequestURLPath: route.path}
				switch route.format {
				case types.RelayFormatOpenAI:
					info.Request = &dto.GeneralOpenAIRequest{}
				case types.RelayFormatClaude:
					info.Request = &dto.ClaudeRequest{}
				case types.RelayFormatOpenAIResponses:
					info.Request = &dto.OpenAIResponsesRequest{}
				}
				require.NoError(t, common.Unmarshal([]byte(raw), info.Request))
				steps, record, apiErr := requestplugin.Ingress(c, route.format)
				require.Nil(t, apiErr)
				info.SetRequestPluginIngress(steps, record)
				apiErr = route.handler(c, info)
				require.NotNil(t, apiErr)
				require.Len(t, info.RequestPluginSteps, 1, "egress runs once on the final body")
				assert.Equal(t, requestplugin.StageEgress, info.RequestPluginSteps[0].Stage)
				if tc.blocked {
					assert.Equal(t, types.ErrorCodeRequestRejected, apiErr.GetErrorCode())
					select {
					case <-captured:
						t.Fatal("egress rejected content was sent upstream")
					default:
					}
					return
				}
				assert.NotEqual(t, types.ErrorCodeRequestRejected, apiErr.GetErrorCode())
				select {
				case actual := <-captured:
					assert.Equal(t, "allowed", gjson.GetBytes(actual, route.textPath).String())
					if tc.override == "" {
						assert.Equal(t, raw, string(actual), "unmodified pass-through requests retain their exact bytes")
					}
				default:
					t.Fatal("the allowed final body never reached upstream")
				}
			})
		}
	}
}
