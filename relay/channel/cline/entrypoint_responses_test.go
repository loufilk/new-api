package cline_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const responsesChatCompletionFixture = `{
	"success": true,
	"data": {
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"created": 123,
		"model": "cline-free/upstream-model",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "hello from cline",
				"tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"a.txt\"}"}}]
			},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
	}
}`

const responsesChatStreamFixture = `data: {"success":true,"data":{"id":"chatcmpl-1","object":"chat.completion.chunk","created":123,"model":"cline-free/upstream-model","choices":[{"index":0,"delta":{"role":"assistant"}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"hello "}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"world"}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]},"finish_reason":"tool_calls"}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}}

data: [DONE]

`

func newResponsesRequest(requestedModel string) *dto.OpenAIResponsesRequest {
	return &dto.OpenAIResponsesRequest{
		Model: requestedModel,
		Input: []byte(`"hello"`),
	}
}

func newResponsesStreamRequest(requestedModel string) *dto.OpenAIResponsesRequest {
	stream := true
	request := newResponsesRequest(requestedModel)
	request.Stream = &stream
	return request
}

func TestClineResponsesRequestConversionStringInput(t *testing.T) {
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-free/deepseek-v4.1-flash", "http://127.0.0.1:1")
	request := newResponsesRequest(env.info.OriginModelName)
	request.Instructions = []byte(`"be brief"`)
	maxOutput := uint(128)
	request.MaxOutputTokens = &maxOutput

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)

	assert.Equal(t, env.info.OriginModelName, gjson.GetBytes(body, "model").String())
	assert.Equal(t, "system", gjson.GetBytes(body, "messages.0.role").String())
	assert.Equal(t, "be brief", gjson.GetBytes(body, "messages.0.content").String())
	assert.Equal(t, "user", gjson.GetBytes(body, "messages.1.role").String())
	assert.Equal(t, "hello", gjson.GetBytes(body, "messages.1.content").String())
	assert.Equal(t, int64(128), gjson.GetBytes(body, "max_completion_tokens").Int())
	assert.True(t, env.info.SupportStreamOptions)
	require.Equal(t, []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI}, env.info.RequestConversionChain)
}

func TestClineResponsesRequestConversionMessageArrayWithTools(t *testing.T) {
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-pass/kimi-k3", "http://127.0.0.1:1")
	request := &dto.OpenAIResponsesRequest{
		Model: env.info.OriginModelName,
		Input: []byte(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"read the file"}]},
			{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.txt\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"file contents"}
		]`),
		Tools: []byte(`[{"type":"function","name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}]`),
	}

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)

	assert.Equal(t, "read the file", gjson.GetBytes(body, "messages.0.content").String())
	assert.Equal(t, "assistant", gjson.GetBytes(body, "messages.1.role").String())
	assert.Equal(t, "read_file", gjson.GetBytes(body, "messages.1.tool_calls.0.function.name").String())
	assert.Equal(t, "call_1", gjson.GetBytes(body, "messages.1.tool_calls.0.id").String())
	assert.Equal(t, `{"path":"a.txt"}`, gjson.GetBytes(body, "messages.1.tool_calls.0.function.arguments").String())
	assert.Equal(t, "tool", gjson.GetBytes(body, "messages.2.role").String())
	assert.Equal(t, "call_1", gjson.GetBytes(body, "messages.2.tool_call_id").String())
	assert.Equal(t, "read_file", gjson.GetBytes(body, "tools.0.function.name").String())
	assert.Equal(t, "Read a file", gjson.GetBytes(body, "tools.0.function.description").String())
}

func TestClineResponsesReasoningStaysWithAssistantToolCalls(t *testing.T) {
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-pass/kimi-k3", "http://127.0.0.1:1")
	request := &dto.OpenAIResponsesRequest{
		Model: env.info.OriginModelName,
		Input: []byte(`[
			{"role":"user","content":"check both files"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"inspect both files"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"checking files"}]},
			{"type":"function_call","call_id":"a","name":"read_file","arguments":"{}"},
			{"type":"reasoning","content":[{"type":"reasoning_text","text":"also inspect config"}],"summary":[{"type":"summary_text","text":"do not duplicate"}]},
			{"type":"function_call","call_id":"b","name":"read_file","arguments":"{}"},
			{"type":"function_call_output","call_id":"a","output":"first"},
			{"type":"function_call_output","call_id":"b","output":"second"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"both results checked"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]},
			{"role":"user","content":"continue"}
		]`),
	}
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(body, "messages").Array(), 6)
	assert.Equal(t, "inspect both files\n\nalso inspect config", gjson.GetBytes(body, "messages.1.reasoning_content").String())
	assert.Equal(t, "checking files", gjson.GetBytes(body, "messages.1.content").String())
	require.Len(t, gjson.GetBytes(body, "messages.1.tool_calls").Array(), 2)
	assert.Equal(t, "a", gjson.GetBytes(body, "messages.1.tool_calls.0.id").String())
	assert.Equal(t, "b", gjson.GetBytes(body, "messages.1.tool_calls.1.id").String())
	assert.Equal(t, "first", gjson.GetBytes(body, "messages.2.content").String())
	assert.Equal(t, "second", gjson.GetBytes(body, "messages.3.content").String())
	assert.Equal(t, "both results checked", gjson.GetBytes(body, "messages.4.reasoning_content").String())
	assert.Equal(t, "done", gjson.GetBytes(body, "messages.4.content").String())
	assert.Equal(t, "continue", gjson.GetBytes(body, "messages.5.content").String())
}

func TestClineResponsesReasoningThreeRoundReplay(t *testing.T) {
	// Replay the actual returned output, as a Responses client does. The first
	// batch mixes a function tool and a custom tool; the next uses their results.
	messages := []string{
		`{"role":"assistant","reasoning_content":"inspect both files","content":"checking files","tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"read_file","arguments":"{}"}},{"index":1,"id":"b","type":"function","function":{"name":"exec","arguments":"{\"input\":\"ls\"}"}}]}`,
		`{"role":"assistant","reasoning_content":"both results checked","content":"checking difference","tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"diff\"}"}}]}`,
		`{"role":"assistant","reasoning_content":"all results checked","content":"finished"}`,
	}
	for _, model := range []string{"cline-pass/kimi-k3", "cline-free/deepseek-v4.1-flash", "cline-free/qwen3.8-max", "cline-free/glm-5.3"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
					toolCount := len(gjson.GetBytes(body, `messages.#(role=="tool")#`).Array())
					round := 0
					if toolCount == 2 {
						round = 1
					} else if toolCount == 3 {
						round = 2
					}
					finish := "tool_calls"
					if round == 2 {
						finish = "stop"
					}
					if !stream {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"success":true,"data":{"id":"chat","model":"upstream","choices":[{"index":0,"message":%s,"finish_reason":%q}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}}`, messages[round], finish)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"success\":true,\"data\":{\"id\":\"chat\",\"choices\":[{\"index\":0,\"delta\":%s}]}}\n\n", messages[round])
					fmt.Fprintf(w, "data: {\"success\":true,\"data\":{\"id\":\"chat\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}}\n\n", finish)
					fmt.Fprint(w, "data: {\"success\":true,\"data\":{\"id\":\"chat\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"total_tokens\":14}}}\n\ndata: [DONE]\n\n")
				})
				history := []json.RawMessage{json.RawMessage(`{"role":"user","content":"check both files"}`)}
				for round := range 3 {
					env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, stream, model, upstream.URL+"/v1")
					input, err := common.Marshal(history)
					require.NoError(t, err)
					request := &dto.OpenAIResponsesRequest{
						Model: model, Stream: &stream, Input: input,
						Tools: []byte(`[{"type":"function","name":"read_file","parameters":{"type":"object"}},{"type":"custom","name":"exec","format":{"type":"text"}}]`),
					}
					converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
					require.NoError(t, err)
					resp := env.send(t, converted)
					_, apiErr := env.receive(t, resp)
					require.Nil(t, apiErr)
					wire := (<-received).body
					assert.Equal(t, []int{1, 4, 6}[round], len(gjson.GetBytes(wire, "messages").Array()))
					if round > 0 {
						assert.Equal(t, "inspect both files", gjson.GetBytes(wire, "messages.1.reasoning_content").String())
						assert.Equal(t, "checking files", gjson.GetBytes(wire, "messages.1.content").String())
						require.Len(t, gjson.GetBytes(wire, "messages.1.tool_calls").Array(), 2)
						assert.Equal(t, "a", gjson.GetBytes(wire, "messages.2.tool_call_id").String())
						assert.Equal(t, "file contents", gjson.GetBytes(wire, "messages.2.content").String())
						assert.Equal(t, "b", gjson.GetBytes(wire, "messages.3.tool_call_id").String())
						assert.Equal(t, "directory listing", gjson.GetBytes(wire, "messages.3.content").String())
						assert.JSONEq(t, `{"input":"ls"}`, gjson.GetBytes(wire, "messages.1.tool_calls.1.function.arguments").String())
					}
					if round == 2 {
						assert.Equal(t, "both results checked", gjson.GetBytes(wire, "messages.4.reasoning_content").String())
						assert.Equal(t, "c", gjson.GetBytes(wire, "messages.4.tool_calls.0.id").String())
						assert.Equal(t, "difference found", gjson.GetBytes(wire, "messages.5.content").String())
					}
					output := gjson.Get(env.recorder.Body.String(), "output")
					if stream {
						payloads := dataPayloads(env.recorder.Body.String())
						completed := findEventPayload(payloads, "response.completed")
						require.NotEmpty(t, completed)
						assert.Equal(t, 1, countEvents(streamEventTypes(env.recorder.Body.String()), "response.completed"))
						output = gjson.Get(completed, "response.output")
					}
					require.True(t, output.IsArray())
					var items []json.RawMessage
					require.NoError(t, common.Unmarshal([]byte(output.Raw), &items))
					history = append(history, items...)
					switch round {
					case 0:
						assert.Equal(t, "ls", output.Get(`#(type=="custom_tool_call").input`).String())
						history = append(history,
							json.RawMessage(`{"type":"function_call_output","call_id":"a","output":"file contents"}`),
							json.RawMessage(`{"type":"custom_tool_call_output","call_id":"b","output":"directory listing"}`))
					case 1:
						history = append(history, json.RawMessage(`{"type":"function_call_output","call_id":"c","output":"difference found"}`))
					case 2:
						assert.Equal(t, "finished", output.Get(`#(type=="message").content.0.text`).String())
					}
				}
			})
		}
	}
}

func TestClineResponsesRejectsUnrepresentableHistory(t *testing.T) {
	for _, item := range []string{
		`{"type":"reasoning","encrypted_content":"private-opaque-value","summary":[]}`,
		`{"type":"reasoning","encrypted_content":"private-opaque-value","summary":[{"type":"summary_text","text":"summary cannot restore opaque state"}]}`,
		`{"type":"compaction","encrypted_content":"private-opaque-value"}`,
		`{"type":"item_reference","id":"private-opaque-value"}`,
		`{"type":"agent_message","content":"private-opaque-value"}`,
	} {
		t.Run(gjson.Get(item, "type").String(), func(t *testing.T) {
			env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-pass/kimi-k3", "http://127.0.0.1:1")
			request := &dto.OpenAIResponsesRequest{Model: env.info.OriginModelName, Input: []byte("[" + item + "]")}
			converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
			require.Error(t, err)
			assert.Nil(t, converted)
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.Equal(t, types.ErrorCode("responses_history_unsupported"), apiErr.GetErrorCode())
			assert.NotContains(t, err.Error(), "private-opaque-value")
		})
	}
}

func TestClineResponsesReasoningDoesNotLeakToNonClineRetry(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeOpenRouter, constant.ChannelTypeAdvancedCustom} {
		t.Run(fmt.Sprintf("channel=%d", channelType), func(t *testing.T) {
			env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "test-model", "http://127.0.0.1:1")
			request := dto.OpenAIResponsesRequest{Model: "test-model", Input: []byte(`[{"role":"user","content":"inspect"},{"type":"reasoning","content":[{"type":"reasoning_text","text":"private reasoning"}]},{"role":"assistant","content":"checking"}]`)}
			converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, request)
			require.NoError(t, err)
			clineChat, ok := converted.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			require.Len(t, clineChat.Messages, 2)
			assert.Equal(t, "private reasoning", clineChat.Messages[1].GetReasoningContent())

			// A retry reuses RelayInfo but rebuilds channel metadata. The Cline
			// dialect opt-in must not survive into an unrelated Chat bridge.
			common.SetContextKey(env.context, constant.ContextKeyChannelType, channelType)
			env.info.InitChannelMeta(env.context)
			result, err := service.ConvertRequestByID(env.context, env.info, relayconvert.ConverterOpenAIResponsesToOpenAIChat, request)
			require.NoError(t, err)
			chat, ok := result.Value.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			require.Len(t, chat.Messages, 2)
			assert.Equal(t, "checking", chat.Messages[1].Content)
			assert.Nil(t, chat.Messages[1].ReasoningContent)
			assert.Empty(t, result.Diagnostics)
		})
	}
}

func TestClineResponsesRequestMaxOutputTokensExplicitZero(t *testing.T) {
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-free/deepseek-v4.1-flash", "http://127.0.0.1:1")
	request := newResponsesRequest(env.info.OriginModelName)
	zero := uint(0)
	request.MaxOutputTokens = &zero

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(body, "max_completion_tokens").Exists(), "explicit zero must survive conversion")
	assert.Equal(t, int64(0), gjson.GetBytes(body, "max_completion_tokens").Int())
}

func TestClineResponsesRequestMaxOutputTokensBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "within bound", value: "4096", valid: true},
		{name: "zero", value: "0", valid: true},
		{name: "above int32 half", value: "4294967295"},
		{name: "wrapped negative uint", value: "18446744073686646784"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := `{"model":"cline-free/deepseek-v4.1-flash","input":"hello","max_output_tokens":` + tc.value + `}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			request, err := helper.GetAndValidateResponsesRequest(c)
			if tc.valid {
				require.NoError(t, err)
				require.NotNil(t, request)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestClineResponsesJSONThroughAdaptor(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responsesChatCompletionFixture))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesRequest(env.info.OriginModelName)
	env.mapModel(t, request, "cline-free/upstream-model")

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 12, usage.(*dto.Usage).TotalTokens)

	upstreamRequest := <-received
	assert.Equal(t, "/api/v1/chat/completions", upstreamRequest.path)
	assert.Equal(t, "cline-free/upstream-model", gjson.GetBytes(upstreamRequest.body, "model").String())

	body := env.recorder.Body.String()
	assert.Equal(t, "completed", gjson.Get(body, "status").String())
	assert.Equal(t, "cline-free/deepseek-v4.1-flash", gjson.Get(body, "model").String())
	assert.Equal(t, "hello from cline", gjson.Get(body, `output.#(type=="message").content.0.text`).String())
	assert.Equal(t, "function_call", gjson.Get(body, `output.#(type=="function_call").type`).String())
	assert.Equal(t, "read_file", gjson.Get(body, `output.#(type=="function_call").name`).String())
	assert.Equal(t, `{"path":"a.txt"}`, gjson.Get(body, `output.#(type=="function_call").arguments`).String())
	assert.Equal(t, int64(12), gjson.Get(body, "usage.total_tokens").Int())
}

func TestClineResponsesSSEThroughAdaptor(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesChatStreamFixture))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesStreamRequest(env.info.OriginModelName)
	env.mapModel(t, request, "cline-free/upstream-model")

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.(*dto.Usage).TotalTokens)

	upstreamRequest := <-received
	assert.True(t, gjson.GetBytes(upstreamRequest.body, "stream").Bool())
	assert.True(t, gjson.GetBytes(upstreamRequest.body, "stream_options.include_usage").Bool())

	body := env.recorder.Body.String()
	events := streamEventTypes(body)
	require.NotEmpty(t, events)
	assert.Equal(t, "response.created", events[0])
	assert.Equal(t, "response.completed", events[len(events)-1])
	assert.Equal(t, 1, countEvents(events, "response.completed"))
	assert.Equal(t, 0, countEvents(events, "response.failed"))

	payloads := dataPayloads(body)
	var text strings.Builder
	var arguments strings.Builder
	for _, payload := range payloads {
		if !gjson.Valid(payload) {
			continue
		}
		switch gjson.Get(payload, "type").String() {
		case "response.output_text.delta":
			text.WriteString(gjson.Get(payload, "delta").String())
		case "response.function_call_arguments.delta":
			arguments.WriteString(gjson.Get(payload, "delta").String())
		}
	}
	assert.Equal(t, "hello world", text.String())
	assert.Equal(t, `{"path":"a.txt"}`, arguments.String())

	completed := findEventPayload(payloads, "response.completed")
	require.NotEmpty(t, completed)
	assert.Equal(t, "completed", gjson.Get(completed, "response.status").String())
	assert.Equal(t, "cline-free/deepseek-v4.1-flash", gjson.Get(completed, "response.model").String())
	assert.Equal(t, int64(7), gjson.Get(completed, "response.usage.total_tokens").Int())
}

func TestClineResponsesRoleThenErrorHasNoBillableUsage(t *testing.T) {
	stream := "data: " + `{"id":"chat-1","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}` + "\n\n" +
		"data: " + `{"error":{"type":"invalid_request_error","message":"user message must have content","param":"messages.29.content"}}` + "\n\n"
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "test-model", upstream.URL)
	env.info.SetEstimatePromptTokens(30308)
	request := newResponsesStreamRequest(env.info.OriginModelName)
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	usage, apiErr := env.receive(t, env.send(t, converted))
	require.NotNil(t, apiErr)
	assert.Nil(t, usage, "role-only prelude and failure must not estimate or settle prompt tokens")
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.False(t, service.ShouldRetryRelayError(env.context, apiErr, 2))
	assert.True(t, common.GetContextKeyBool(env.context, constant.ContextKeyResponsesStreamErrorHandled))
	assert.True(t, env.info.StreamStatus.ResponseFailed())
	assert.Equal(t, 1, countEvents(streamEventTypes(env.recorder.Body.String()), "response.failed"))
	assert.NotContains(t, env.recorder.Body.String(), "response.completed")
	assert.Len(t, received, 1)
}

func TestClineResponsesFailurePreservesObservedUsage(t *testing.T) {
	stream := "data: " + `{"choices":[{"index":0,"delta":{"content":"partial"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}` + "\n\n" +
		"data: " + `{"error":{"type":"server_error","message":"upstream generation failed"}}` + "\n\n"
	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "test-model", upstream.URL)
	env.info.SetEstimatePromptTokens(30308)
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *newResponsesStreamRequest("test-model"))
	require.NoError(t, err)
	rawUsage, apiErr := env.receive(t, env.send(t, converted))
	require.NotNil(t, apiErr)
	usage, ok := rawUsage.(*dto.Usage)
	require.True(t, ok)
	require.NotNil(t, usage)
	assert.Equal(t, 10, usage.PromptTokens)
	assert.Equal(t, 2, usage.CompletionTokens)
	assert.Equal(t, 12, usage.TotalTokens)
	assert.False(t, service.ShouldRetryRelayError(env.context, apiErr, 2))
	assert.NotContains(t, env.recorder.Body.String(), "response.completed")
}

func TestClineResponsesLeadingStreamErrorReturnsBeforeOutput(t *testing.T) {
	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"success\":false,\"error\":\"responses not enabled for free tier\"}\n\n"))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesStreamRequest(env.info.OriginModelName)
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "responses not enabled for free tier")
	assert.Empty(t, env.recorder.Body.String())
}

func TestClineResponsesMidStreamErrorEmitsFailedTerminalAndKeepsDiagnosticsInternal(t *testing.T) {
	stream := "data: " + `{"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"partial"}}]}}` + "\n\n" +
		"data: " + `{"success":false,"error":{"message":"internal account id 42 rejected"}}` + "\n\n"

	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesStreamRequest(env.info.OriginModelName)
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	_, apiErr := env.receive(t, resp)
	require.NotNil(t, apiErr)
	assert.True(t, types.IsSkipRetryError(apiErr))

	body := env.recorder.Body.String()
	events := streamEventTypes(body)
	require.Contains(t, events, "response.failed")
	assert.Equal(t, 0, countEvents(events, "response.completed"), "a partial error must not become a fake completion")
	assert.Equal(t, 1, countEvents(events, "response.failed"))
	assert.Contains(t, body, "upstream request failed")
	assert.NotContains(t, body, "internal account id 42 rejected")
	require.NotNil(t, env.info.StreamStatus)
	assert.True(t, env.info.StreamStatus.ResponseFailed())
	require.NotEmpty(t, env.info.StreamStatus.Errors)
	assert.Contains(t, env.info.StreamStatus.Errors[0].Message, "internal account id 42 rejected",
		"upstream diagnostics stay available internally and in logs")
}

func TestClineResponsesMidStreamErrorKeepsDiagnosticsForChannelTest(t *testing.T) {
	stream := "data: " + `{"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"partial"}}]}}` + "\n\n" +
		"data: " + `{"success":false,"error":{"message":"internal account id 42 rejected"}}` + "\n\n"

	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	common.SetContextKey(env.context, constant.ContextKeyChannelTest, true)
	request := newResponsesStreamRequest(env.info.OriginModelName)
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	_, apiErr := env.receive(t, resp)
	require.NotNil(t, apiErr)

	body := env.recorder.Body.String()
	assert.Contains(t, body, "response.failed")
	assert.Contains(t, body, "internal account id 42 rejected",
		"the administrator channel test keeps upstream diagnostics")
}

func TestClineResponsesCompactIsRejectedBeforeUpstream(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponsesCompaction, relayconstant.RelayModeResponsesCompact, false, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesRequest(env.info.OriginModelName)
	body, err := common.Marshal(request)
	require.NoError(t, err)
	_, requestErr := env.adaptor.DoRequest(env.context, env.info, strings.NewReader(string(body)))
	require.Error(t, requestErr)
	apiErr, ok := requestErr.(*types.NewAPIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCodeInvalidApiType, apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Empty(t, received, "unsupported compact requests must never reach a Cline endpoint")
}

func countEvents(events []string, target string) int {
	count := 0
	for _, event := range events {
		if event == target {
			count++
		}
	}
	return count
}

func findEventPayload(payloads []string, eventType string) string {
	for _, payload := range payloads {
		if gjson.Valid(payload) && gjson.Get(payload, "type").String() == eventType {
			return payload
		}
	}
	return ""
}
