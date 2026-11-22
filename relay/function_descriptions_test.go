package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestFunctionDescriptionCompatibilityReachesMockUpstream(t *testing.T) {
	for _, route := range []string{"chat-to-responses", "responses", "responses-pass-through"} {
		t.Run(route, func(t *testing.T) {
			captured := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, "/v1/responses", r.URL.Path)
				captured <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"resp_mock","object":"response","status":"completed","model":"gpt-test","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`))
			}))
			defer server.Close()
			override := map[string]any{"operations": []any{
				map[string]any{"mode": "ensure_function_descriptions", "value": "Client-provided function."},
			}}
			if route == "chat-to-responses" {
				override["operations"] = append(override["operations"].([]any), map[string]any{
					"mode": "append", "path": "messages.0.content", "value": "-once",
				})
			}
			info := &relaycommon.RelayInfo{
				RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI,
				OriginModelName: "gpt-test", RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI},
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: server.URL,
					ApiKey: "synthetic-test-key", UpstreamModelName: "gpt-test", ParamOverride: override,
				},
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if route == "chat-to-responses" {
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.Unmarshal([]byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"missing"}},{"type":"function","function":{"name":"null","description":null}},{"type":"function","function":{"name":"kept","description":"Original description"}},{"type":"function","function":{"name":"empty","description":""}}],"functions":[{"name":"legacy"}]}`), &request))
				adaptor := &openaichannel.Adaptor{}
				adaptor.Init(info)
				_, apiErr := textRequestViaResponses(c, info, adaptor, &request)
				require.Nil(t, apiErr)
			} else {
				raw := `{"model":"gpt-test","input":"hello","tools":[{"type":"function","name":"missing"},{"type":"web_search"},{"type":"function","name":"kept","description":"Original description"},{"type":"function","name":"null","description":null},{"type":"function","name":"empty","description":""},{"type":"image_generation"}]}`
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(raw))
				t.Cleanup(func() { common.CleanupBodyStorage(c) })
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-test")
				common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
				common.SetContextKey(c, constant.ContextKeyChannelKey, "synthetic-test-key")
				common.SetContextKey(c, constant.ContextKeyChannelParamOverride, override)
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: route == "responses-pass-through"})
				var request dto.OpenAIResponsesRequest
				require.NoError(t, common.Unmarshal([]byte(raw), &request))
				info = relaycommon.GenRelayInfoResponses(c, &request)
				adaptor, body, closer, apiErr := PrepareResponsesRequest(c, info, &request)
				require.Nil(t, apiErr)
				defer closer.Close()
				response, err := adaptor.DoRequest(c, info, body)
				require.NoError(t, err)
				require.NoError(t, response.(*http.Response).Body.Close())
			}
			body := <-captured
			tools := gjson.GetBytes(body, "tools").Array()
			require.NotEmpty(t, tools)
			for _, tool := range tools {
				if tool.Get("type").String() != "function" {
					assert.False(t, tool.Get("description").Exists(), "builtin tools must not acquire a description")
					continue
				}
				want := "Client-provided function."
				if tool.Get("name").String() == "kept" {
					want = "Original description"
				}
				assert.Equal(t, want, tool.Get("description").String())
				assert.False(t, tool.Get("function").Exists(), "Responses uses flattened functions")
			}
			if route == "chat-to-responses" {
				assert.Equal(t, "hello-once", gjson.GetBytes(body, "input.0.content").String(), "the entire override list must not be repeated after conversion")
				assert.Len(t, tools, 5, "legacy and modern function definitions must both survive")
			} else {
				assert.Len(t, tools, 6)
			}
		})
	}
}

func TestChatToResponsesDescriptionRulesAndUpstreamErrors(t *testing.T) {
	for _, tc := range []struct {
		name            string
		operation       map[string]any
		wantDescription string
		status          int
		upstreamStatus  int
		response        string
	}{
		{name: "empty chat fallback is removed by conversion", operation: map[string]any{"mode": "set", "path": "tools.*.function.description", "value": "", "keep_origin": true}, status: 400, response: `{"error":{"message":"Missing required parameter: 'tools[0].description'.","type":"invalid_request_error","param":"tools[0].description"}}`},
		{name: "responses-only rule runs before chat conversion", operation: map[string]any{"mode": "set", "path": "tools.*.description", "value": "Function.", "keep_origin": true}, status: 400, response: `{"error":{"message":"Missing required parameter: 'tools[0].description'.","type":"invalid_request_error"}}`},
		{name: "compatible description does not hide unrelated 400", operation: map[string]any{"mode": "ensure_function_descriptions", "value": "Function."}, wantDescription: "Function.", status: 400, response: `{"error":{"message":"Invalid function parameters","type":"invalid_request_error"}}`},
		{name: "compatible description does not hide non-json 500", operation: map[string]any{"mode": "ensure_function_descriptions", "value": "Function."}, wantDescription: "Function.", status: 500, response: `error: synthetic upstream failure`},
		{name: "non-json success body is a separate decode failure", operation: map[string]any{"mode": "ensure_function_descriptions", "value": "Function."}, wantDescription: "Function.", upstreamStatus: 200, status: 500, response: `error: synthetic upstream failure`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				captured <- body
				status := tc.upstreamStatus
				if status == 0 {
					status = tc.status
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI, OriginModelName: "gpt-test", ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: server.URL, ApiKey: "synthetic-test-key", UpstreamModelName: "gpt-test", ParamOverride: map[string]any{"operations": []any{tc.operation}},
			}}
			adaptor := &openaichannel.Adaptor{}
			adaptor.Init(info)
			request := &dto.GeneralOpenAIRequest{Model: "gpt-test", Messages: []dto.Message{{Role: "user", Content: "hello"}}, Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup"}}}}
			_, apiErr := textRequestViaResponses(c, info, adaptor, request)
			require.NotNil(t, apiErr)
			assert.Equal(t, tc.status, apiErr.StatusCode)
			if tc.upstreamStatus == 200 {
				assert.Contains(t, apiErr.Error(), "invalid character 'e'")
			}
			select {
			case body := <-captured:
				assert.Equal(t, tc.wantDescription, gjson.GetBytes(body, "tools.0.description").String())
				assert.Equal(t, tc.wantDescription != "", gjson.GetBytes(body, "tools.0.description").Exists())
			default:
				t.Fatal("request never reached mock upstream")
			}
		})
	}
}

func TestFunctionDescriptionCompatibilityDirectChatAndPassThrough(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	oldPolicy, oldPassThrough := settings.ChatCompletionsToResponsesPolicy, settings.PassThroughRequestEnabled
	settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{}
	settings.PassThroughRequestEnabled = false
	t.Cleanup(func() {
		settings.ChatCompletionsToResponsesPolicy, settings.PassThroughRequestEnabled = oldPolicy, oldPassThrough
	})
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "typed chat", true: "raw chat"}[passthrough], func(t *testing.T) {
			captured := make(chan []byte, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, "/v1/chat/completions", r.URL.Path)
				captured <- body
				// Stop before billing: this synthetic upstream never generates output.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"synthetic unrelated rejection","type":"invalid_request_error"}}`)
			}))
			defer upstream.Close()
			raw := `{"model":"gpt-test","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"missing"}},{"type":"function","function":{"name":"empty","description":""}},{"type":"function","function":{"name":"null","description":null}},{"type":"function","function":{"name":"kept","description":"Original"}}],"functions":[{"name":"legacy"}]}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(raw))
			c.Request.Header.Set("Content-Type", "application/json")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "synthetic-test-key")
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passthrough})
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{
				map[string]any{"mode": "ensure_function_descriptions", "value": "Function."},
			}})
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.Unmarshal([]byte(raw), &request))
			info := &relaycommon.RelayInfo{Request: &request, RelayFormat: types.RelayFormatOpenAI,
				RelayMode: relayconstant.RelayModeChatCompletions, RequestURLPath: c.Request.URL.Path, OriginModelName: "gpt-test"}
			apiErr := TextHelper(c, info)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Contains(t, apiErr.Error(), "synthetic unrelated rejection")
			select {
			case body := <-captured:
				assert.Equal(t, "Function.", gjson.GetBytes(body, "tools.0.function.description").String())
				assert.Equal(t, "Function.", gjson.GetBytes(body, "tools.1.function.description").String())
				assert.Equal(t, "Function.", gjson.GetBytes(body, "tools.2.function.description").String())
				assert.Equal(t, "Original", gjson.GetBytes(body, "tools.3.function.description").String())
				assert.Equal(t, "Function.", gjson.GetBytes(body, "functions.0.description").String())
			default:
				t.Fatal("request never reached mock upstream")
			}
		})
	}
}
