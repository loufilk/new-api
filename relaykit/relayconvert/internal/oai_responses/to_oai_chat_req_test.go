package oairesponses

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/internal/convdiag"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesReasoningReplayPlaintextAndSummary(t *testing.T) {
	for _, tc := range []struct {
		name, item, want string
		summary          bool
	}{
		{"plaintext takes precedence", `{"content":[{"type":"reasoning_text","text":"full reasoning"}],"summary":[{"type":"summary_text","text":"summary"}]}`, "full reasoning", false},
		{"legacy summary", `{"content":null,"summary":[{"type":"summary_text","text":"first"},{"type":"summary_text","text":"second"}]}`, "first\n\nsecond", true},
		{"empty content falls back to summary", `{"content":[{"type":"reasoning_text","text":""}],"summary":[{"type":"summary_text","text":"fallback"}]}`, "fallback", true},
		{"empty placeholder", `{"encrypted_content":"","summary":[]}`, "", false},
		{"null placeholder", `{"encrypted_content":null,"content":null,"summary":null}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var item map[string]any
			require.NoError(t, kitutil.Unmarshal([]byte(tc.item), &item))
			item["type"] = "reasoning"
			input := mustRawMessage(t, []any{map[string]any{"role": "user", "content": "hello"}, item, map[string]any{"role": "assistant", "content": "answer"}})
			ctx, diagnostics := convdiag.WithCollector(context.Background())
			got, err := ResponsesRequestToChatCompletionsRequestWithOptions(ctx, &dto.OpenAIResponsesRequest{Model: "test", Input: input}, &convmeta.Options{ReplayResponsesReasoning: true})
			require.NoError(t, err)
			require.Len(t, got.Messages, 2)
			assert.Equal(t, "assistant", got.Messages[1].Role)
			assert.Equal(t, "answer", got.Messages[1].Content)
			assert.Equal(t, tc.want, got.Messages[1].GetReasoningContent())
			if tc.summary {
				require.Len(t, diagnostics.Diagnostics(), 1)
				assert.Equal(t, "reasoning_summary_replayed", diagnostics.Diagnostics()[0].Code)
				assert.NotContains(t, diagnostics.Diagnostics()[0].Message, tc.want)
			} else {
				assert.Empty(t, diagnostics.Diagnostics())
			}
		})
	}
}

func TestResponsesReasoningReplayRejectsMalformedParts(t *testing.T) {
	for _, item := range []string{
		`{"type":"reasoning","content":"private-value"}`,
		`{"type":"reasoning","content":[null]}`,
		`{"type":"reasoning","content":[{"type":"output_text","text":"private-value"}]}`,
		`{"type":"reasoning","summary":[{"type":"summary_text","text":123}]}`,
		`{"type":"reasoning","encrypted_content":{"private-value":true}}`,
		`{"type":"reasoning","encrypted_content":"private-value","content":[{"type":"reasoning_text","text":"plaintext cannot replace encrypted state"}]}`,
		`{"role":"assistant","content":{"text":"private-value"}}`,
	} {
		t.Run(item, func(t *testing.T) {
			_, err := ResponsesRequestToChatCompletionsRequestWithOptions(context.Background(), &dto.OpenAIResponsesRequest{Model: "test", Input: []byte("[" + item + "]")}, &convmeta.Options{ReplayResponsesReasoning: true})
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, 400, apiErr.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.NotContains(t, err.Error(), "private-value")
		})
	}
}

func TestResponsesReasoningReplayKeepsAssistantPartsAndToolBatch(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequestWithOptions(context.Background(), &dto.OpenAIResponsesRequest{
		Model: "test",
		Input: []byte(`[
			{"role":"user","content":"inspect"},
			{"type":"reasoning","content":[{"type":"reasoning_text","text":"plan"}]},
			{"role":"assistant","content":"first"},
			{"type":"function_call","call_id":"a","name":"read","arguments":"{}"},
			{"role":"assistant","content":[{"type":"output_text","text":"second"},{"type":"refusal","refusal":"unavailable"}]},
			{"role":"assistant","content":"third"},
			{"role":"assistant","content":[{"type":"refusal","refusal":"still unavailable"}]},
			{"type":"function_call","call_id":"b","name":"read","arguments":"{}"},
			{"type":"function_call_output","call_id":"a","output":[{"type":"input_image","image_url":"https://example.com/a.png"}]},
			{"type":"reasoning","summary":[]},
			{"type":"function_call_output","call_id":"b","output":"second result"},
			{"type":"reasoning","content":[{"type":"reasoning_text","text":"new plan"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"next"}
		]`),
	}, &convmeta.Options{ReplayResponsesReasoning: true})
	require.NoError(t, err)
	require.Len(t, got.Messages, 7)
	assert.Equal(t, "plan", got.Messages[1].GetReasoningContent())
	assert.Len(t, got.Messages[1].ParseToolCalls(), 2)
	encoded, err := kitutil.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"type":"text","text":"first"},{"type":"text","text":"second"},{"type":"refusal","refusal":"unavailable"},{"type":"text","text":"third"},{"type":"refusal","refusal":"still unavailable"}]`, gjson.GetBytes(encoded, "messages.1.content").Raw)
	assert.Equal(t, "a", got.Messages[2].ToolCallId)
	assert.Equal(t, "b", got.Messages[3].ToolCallId)
	assert.Equal(t, "user", got.Messages[4].Role)
	assert.Equal(t, "https://example.com/a.png", gjson.GetBytes(encoded, "messages.4.content.0.image_url.url").String())
	assert.Equal(t, "new plan", got.Messages[5].GetReasoningContent())
	assert.Equal(t, "done", got.Messages[5].Content)
	assert.Empty(t, got.Messages[6].GetReasoningContent())
}

func TestResponsesReasoningReplayRejectsReasoningBetweenParallelResults(t *testing.T) {
	_, err := ResponsesRequestToChatCompletionsRequestWithOptions(context.Background(), &dto.OpenAIResponsesRequest{
		Model: "test",
		Input: []byte(`[
			{"role":"user","content":"inspect"},
			{"type":"function_call","call_id":"a","name":"read","arguments":"{}"},
			{"type":"function_call","call_id":"b","name":"read","arguments":"{}"},
			{"type":"function_call_output","call_id":"a","output":"first"},
			{"type":"reasoning","content":[{"type":"reasoning_text","text":"private partial result analysis"}]},
			{"type":"function_call_output","call_id":"b","output":"second"}
		]`),
	}, &convmeta.Options{ReplayResponsesReasoning: true})
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 400, apiErr.StatusCode)
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.NotContains(t, err.Error(), "private partial result analysis")
}

func TestResponsesReasoningReplayPreservesParallelResultOrder(t *testing.T) {
	for _, order := range [][2]string{{"a", "b"}, {"b", "a"}} {
		t.Run(order[0]+" then "+order[1], func(t *testing.T) {
			got, err := ResponsesRequestToChatCompletionsRequestWithOptions(context.Background(), &dto.OpenAIResponsesRequest{
				Model: "test",
				Input: []byte(fmt.Sprintf(`[
					{"role":"user","content":"inspect"},
					{"type":"reasoning","content":[{"type":"reasoning_text","text":"plan"}]},
					{"type":"function_call","call_id":"a","name":"read","arguments":"{}"},
					{"type":"function_call","call_id":"b","name":"read","arguments":"{}"},
					{"type":"function_call_output","call_id":%q,"output":"first received result"},
					{"type":"reasoning","content":[],"summary":[]},
					{"type":"function_call_output","call_id":%q,"output":"second received result"},
					{"type":"reasoning","content":[{"type":"reasoning_text","text":"both results inspected"}]},
					{"role":"assistant","content":"done"}
				]`, order[0], order[1])),
			}, &convmeta.Options{ReplayResponsesReasoning: true})
			require.NoError(t, err)
			require.Len(t, got.Messages, 5)
			assert.Equal(t, "plan", got.Messages[1].GetReasoningContent())
			calls := got.Messages[1].ParseToolCalls()
			require.Len(t, calls, 2)
			assert.Equal(t, "a", calls[0].ID)
			assert.Equal(t, "b", calls[1].ID)
			assert.Equal(t, dto.Message{Role: "tool", ToolCallId: order[0], Content: "first received result"}, got.Messages[2])
			assert.Equal(t, dto.Message{Role: "tool", ToolCallId: order[1], Content: "second received result"}, got.Messages[3])
			assert.Equal(t, "both results inspected", got.Messages[4].GetReasoningContent())
			assert.Equal(t, "done", got.Messages[4].Content)
		})
	}
}

func TestResponsesReasoningHistoryDoesNotCreateUserMessages(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model: "test-model",
		Input: json.RawMessage(`[
			{"role":"user","content":"check both files"},
			{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]},
			{"type":"function_call","call_id":"a","name":"read_file","arguments":"{}"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"checking"}]},
			{"type":"function_call","call_id":"b","name":"read_file","arguments":"{}"},
			{"type":"function_call_output","call_id":"a","output":"first"},
			{"type":"function_call_output","call_id":"b","output":"second"},
			{"type":"reasoning","summary":[]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]},
			{"role":"user","content":"continue"}
		]`),
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 6)
	assert.Equal(t, dto.Message{Role: "user", Content: "check both files"}, got.Messages[0])
	assert.Equal(t, "assistant", got.Messages[1].Role)
	require.Len(t, got.Messages[1].ParseToolCalls(), 2)
	assert.Equal(t, "a", got.Messages[1].ParseToolCalls()[0].ID)
	assert.Equal(t, "b", got.Messages[1].ParseToolCalls()[1].ID)
	assert.Equal(t, dto.Message{Role: "tool", ToolCallId: "a", Content: "first"}, got.Messages[2])
	assert.Equal(t, dto.Message{Role: "tool", ToolCallId: "b", Content: "second"}, got.Messages[3])
	assert.Equal(t, dto.Message{Role: "assistant", Content: "done"}, got.Messages[4])
	assert.Equal(t, dto.Message{Role: "user", Content: "continue"}, got.Messages[5])
}

func TestResponsesRequestToChatCompletionsRequestInstructionsAndScalarInput(t *testing.T) {
	stream := true
	temperature := 0.0
	topP := 0.9
	maxOutputTokens := uint(128)
	parallelToolCalls := true

	got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model:                "gpt-test",
		Instructions:         mustRawMessage(t, "system rules"),
		Input:                mustRawMessage(t, "hello"),
		Stream:               &stream,
		StreamOptions:        &dto.StreamOptions{IncludeUsage: true},
		MaxOutputTokens:      &maxOutputTokens,
		Temperature:          &temperature,
		TopP:                 &topP,
		User:                 mustRawMessage(t, "user-1"),
		Store:                mustRawMessage(t, false),
		Metadata:             mustRawMessage(t, map[string]any{"trace": "abc"}),
		ParallelToolCalls:    mustRawMessage(t, parallelToolCalls),
		PromptCacheKey:       mustRawMessage(t, "cache-key"),
		PromptCacheRetention: mustRawMessage(t, "24h"),
		Reasoning:            &dto.Reasoning{Effort: "medium"},
	})
	require.NoError(t, err)

	assert.Equal(t, "gpt-test", got.Model)
	require.Len(t, got.Messages, 2)
	assert.Equal(t, dto.Message{Role: "system", Content: "system rules"}, got.Messages[0])
	assert.Equal(t, dto.Message{Role: "user", Content: "hello"}, got.Messages[1])
	assert.Same(t, &stream, got.Stream)
	require.NotNil(t, got.StreamOptions)
	assert.True(t, got.StreamOptions.IncludeUsage)
	assert.Equal(t, maxOutputTokens, lo.FromPtr(got.MaxCompletionTokens))
	assert.Equal(t, 0.0, lo.FromPtr(got.Temperature))
	assert.Equal(t, 0.9, lo.FromPtr(got.TopP))
	assert.True(t, lo.FromPtr(got.ParallelTooCalls))
	assert.Equal(t, "cache-key", got.PromptCacheKey)
	assert.Equal(t, "medium", got.ReasoningEffort)
	assert.Equal(t, `"user-1"`, string(got.User))
	assert.Equal(t, `false`, string(got.Store))
	assert.Equal(t, "abc", gjson.GetBytes(got.Metadata, "trace").String())
}

func TestResponsesRequestToChatCompletionsRequestPreservesQwenThinkingBudget(t *testing.T) {
	tests := []struct {
		name   string
		budget json.RawMessage
		want   int64
	}{
		{name: "positive budget", budget: json.RawMessage(`128`), want: 128},
		{name: "zero budget", budget: json.RawMessage(`0`), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
				Model:          "qwen-plus",
				Input:          mustRawMessage(t, "hello"),
				EnableThinking: json.RawMessage(`true`),
				ThinkingBudget: tt.budget,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.budget, got.ThinkingBudget)

			encoded, err := kitutil.Marshal(got)
			require.NoError(t, err)

			assert.True(t, gjson.GetBytes(encoded, "enable_thinking").Bool())
			value := gjson.GetBytes(encoded, "thinking_budget")
			assert.True(t, value.Exists())
			assert.Equal(t, tt.want, value.Int())
		})
	}
}

func TestResponsesRequestToChatCompletionsRequestMultimodalInput(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "look"},
					{"type": "input_image", "image_url": "https://example.test/a.png", "detail": "low"},
					{"type": "input_image", "image_url": map[string]any{"url": "https://example.test/b.png", "detail": "high"}},
					{"type": "input_file", "file_id": "file_1", "filename": "a.txt"},
					{"type": "input_audio", "input_audio": map[string]any{"data": "abc", "format": "wav"}},
					{"type": "input_video", "video_url": map[string]any{"url": "https://example.test/v.mp4"}},
				},
			},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 1)
	assert.Equal(t, "user", got.Messages[0].Role)
	parts := got.Messages[0].ParseContent()
	require.Len(t, parts, 6)
	assert.Equal(t, dto.ContentTypeText, parts[0].Type)
	assert.Equal(t, "look", parts[0].Text)
	assert.Equal(t, dto.ContentTypeImageURL, parts[1].Type)
	assert.Equal(t, "https://example.test/a.png", parts[1].GetImageMedia().Url)
	assert.Equal(t, dto.ContentTypeImageURL, parts[2].Type)
	assert.Equal(t, "https://example.test/b.png", parts[2].GetImageMedia().Url)
	assert.Equal(t, dto.ContentTypeFile, parts[3].Type)
	assert.Equal(t, "file_1", parts[3].GetFile().FileId)
	assert.Equal(t, dto.ContentTypeInputAudio, parts[4].Type)
	assert.Equal(t, "wav", parts[4].GetInputAudio().Format)
	assert.Equal(t, dto.ContentTypeVideoUrl, parts[5].Type)
	assert.Equal(t, "https://example.test/v.mp4", parts[5].GetVideoUrl().Url)

	// The Responses API string form must leave as the Chat Completions object
	// form: strict upstreams reject a bare image_url string inside content.
	encoded, err := kitutil.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "https://example.test/a.png", gjson.GetBytes(encoded, "messages.0.content.1.image_url.url").String())
	assert.Equal(t, "low", gjson.GetBytes(encoded, "messages.0.content.1.image_url.detail").String())
	assert.Equal(t, "https://example.test/b.png", gjson.GetBytes(encoded, "messages.0.content.2.image_url.url").String())
	assert.Equal(t, "high", gjson.GetBytes(encoded, "messages.0.content.2.image_url.detail").String())
}

func TestResponsesRequestToChatCompletionsRequestAssistantTextAndFunctionCallCoexist(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": "I will call."},
				},
			},
			{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "lookup",
				"arguments": map[string]any{"q": "x"},
			},
			{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output":  map[string]any{"ok": true},
			},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 2)
	assert.Equal(t, "assistant", got.Messages[0].Role)
	assert.Equal(t, "I will call.", got.Messages[0].StringContent())
	toolCalls := got.Messages[0].ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "call_1", toolCalls[0].ID)
	assert.Equal(t, "function", toolCalls[0].Type)
	assert.Equal(t, "lookup", toolCalls[0].Function.Name)
	assert.JSONEq(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)
	assert.Equal(t, "tool", got.Messages[1].Role)
	assert.Equal(t, "call_1", got.Messages[1].ToolCallId)
	assert.JSONEq(t, `{"ok":true}`, got.Messages[1].StringContent())
}

func TestResponsesRequestToChatCompletionsRequestOnlyFunctionCallCreatesAssistant(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "lookup",
				"arguments": `{"q":"x"}`,
			},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 1)
	assert.Equal(t, "assistant", got.Messages[0].Role)
	assert.Nil(t, got.Messages[0].Content)
	toolCalls := got.Messages[0].ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)
}

func TestResponsesRequestToChatCompletionsRequestToolsToolChoiceAndTextFormat(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, "hello"),
		Tools: mustRawMessage(t, []map[string]any{
			{
				"type":        "function",
				"name":        "lookup",
				"description": "Lookup data",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"q": map[string]any{"type": "string"},
					},
				},
			},
		}),
		ToolChoice: mustRawMessage(t, map[string]any{
			"type": "function",
			"name": "lookup",
		}),
		Text: mustRawMessage(t, map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "answer",
				"schema": map[string]any{"type": "object"},
				"strict": true,
			},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Tools, 1)
	assert.Equal(t, "function", got.Tools[0].Type)
	assert.Equal(t, "lookup", got.Tools[0].Function.Name)
	assert.Equal(t, "Lookup data", got.Tools[0].Function.Description)
	assert.Equal(t, "object", gjson.GetBytes(got.Tools[0].Function.Parameters, "type").String())
	assert.Equal(t, map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "lookup",
		},
	}, got.ToolChoice)
	require.NotNil(t, got.ResponseFormat)
	assert.Equal(t, "json_schema", got.ResponseFormat.Type)
	assert.Equal(t, "answer", gjson.GetBytes(got.ResponseFormat.JsonSchema, "name").String())
	assert.True(t, gjson.GetBytes(got.ResponseFormat.JsonSchema, "strict").Bool())
}

func TestResponsesRequestToChatCompletionsRequestEncodesCustomToolHistoryAsFunction(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"type":    "custom_tool_call",
				"call_id": "call_custom",
				"name":    "exec",
				"input":   "echo \"hi\" && ls",
			},
			{
				"type":    "custom_tool_call_output",
				"call_id": "call_custom",
				"output":  "hi",
			},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 2)
	toolCalls := got.Messages[0].ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "function", toolCalls[0].Type)
	assert.Equal(t, "call_custom", toolCalls[0].ID)
	assert.Equal(t, "exec", toolCalls[0].Function.Name)
	assert.Equal(t, `echo "hi" && ls`, gjson.Get(toolCalls[0].Function.Arguments, "input").String())
	assert.Empty(t, toolCalls[0].Custom)
	assert.Equal(t, dto.Message{Role: "tool", ToolCallId: "call_custom", Content: "hi"}, got.Messages[1])
}

func TestResponsesRequestToChatCompletionsRequestRejectsNamelessCustomToolCall(t *testing.T) {
	_, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"type": "custom_tool_call", "call_id": "call_custom", "input": "ls"},
		}),
	})
	require.ErrorContains(t, err, "custom_tool_call item is missing name")
}

func TestResponsesRequestToChatCompletionsRequestRejectsStatefulFields(t *testing.T) {
	tests := []struct {
		name string
		req  *dto.OpenAIResponsesRequest
		want string
	}{
		{
			name: "conversation",
			req:  &dto.OpenAIResponsesRequest{Model: "gpt-test", Conversation: mustRawMessage(t, "conv_1")},
			want: "conversation",
		},
		{
			name: "previous response",
			req:  &dto.OpenAIResponsesRequest{Model: "gpt-test", PreviousResponseID: "resp_1"},
			want: "previous_response_id",
		},
		{
			name: "prompt",
			req:  &dto.OpenAIResponsesRequest{Model: "gpt-test", Prompt: mustRawMessage(t, map[string]any{"id": "pmpt_1"})},
			want: "prompt",
		},
		{
			name: "context management",
			req:  &dto.OpenAIResponsesRequest{Model: "gpt-test", ContextManagement: mustRawMessage(t, map[string]any{"type": "auto"})},
			want: "context_management",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResponsesRequestToChatCompletionsRequest(context.Background(), tt.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Contains(t, err.Error(), "stateful fields")
		})
	}
}

func TestResponsesRequestToChatCompletionsRequestPreservesPenalties(t *testing.T) {
	tests := []struct {
		name          string
		frequencyRaw  json.RawMessage
		frequencyWant *float64
		presenceRaw   json.RawMessage
		presenceWant  *float64
	}{
		{
			name:          "positive values",
			frequencyRaw:  json.RawMessage(`0.5`),
			frequencyWant: lo.ToPtr(0.5),
			presenceRaw:   json.RawMessage(`1.5`),
			presenceWant:  lo.ToPtr(1.5),
		},
		{
			name:          "explicit zero values",
			frequencyRaw:  json.RawMessage(`0.0`),
			frequencyWant: lo.ToPtr(0.0),
			presenceRaw:   json.RawMessage(`0.0`),
			presenceWant:  lo.ToPtr(0.0),
		},
		{
			name:         "unset stays nil",
			frequencyRaw: nil,
			presenceRaw:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
				Model:            "gpt-test",
				Input:            mustRawMessage(t, "hello"),
				FrequencyPenalty: tt.frequencyRaw,
				PresencePenalty:  tt.presenceRaw,
			})
			require.NoError(t, err)

			assert.Equal(t, tt.frequencyWant, got.FrequencyPenalty)
			assert.Equal(t, tt.presenceWant, got.PresencePenalty)
		})
	}
}

func TestResponsesRequestToChatCompletionsRequestRejectsMalformedPenalty(t *testing.T) {
	_, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
		Model:            "gpt-test",
		Input:            mustRawMessage(t, "hello"),
		FrequencyPenalty: json.RawMessage(`"not-a-number"`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frequency_penalty")
}

func TestResponsesRequestToChatCompletionsRequestToolOutputContentParts(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	imagePart := map[string]any{"type": "input_image", "image_url": dataURL}

	tests := []struct {
		name        string
		output      any
		wantContent string
		jsonContent bool
		wantMedia   []string
	}{
		{
			name:        "text and image keep text on tool message",
			output:      []any{map[string]any{"type": "input_text", "text": "screenshot taken"}, imagePart},
			wantContent: "screenshot taken",
			wantMedia:   []string{dto.ContentTypeImageURL},
		},
		{
			name:        "image only uses placeholder",
			output:      []any{imagePart},
			wantContent: "[image]",
			wantMedia:   []string{dto.ContentTypeImageURL},
		},
		{
			name:        "mixed media dedupes placeholder labels",
			output:      []any{imagePart, map[string]any{"type": "input_file", "file_id": "file_1"}, imagePart},
			wantContent: "[image] [file]",
			wantMedia:   []string{dto.ContentTypeImageURL, dto.ContentTypeFile, dto.ContentTypeImageURL},
		},
		{
			name: "text parts join with newline",
			output: []any{
				map[string]any{"type": "input_text", "text": "first"},
				map[string]any{"type": "output_text", "text": ""},
				map[string]any{"type": "text", "text": "second"},
			},
			wantContent: "first\nsecond",
		},
		{
			name:        "string passes through",
			output:      "done",
			wantContent: "done",
		},
		{
			name:        "object stays json",
			output:      map[string]any{"ok": true},
			wantContent: `{"ok":true}`,
			jsonContent: true,
		},
		{
			name:        "plain array stays json",
			output:      []any{1, 2},
			wantContent: `[1,2]`,
			jsonContent: true,
		},
		{
			name:        "unknown part keeps whole array",
			output:      []any{imagePart, map[string]any{"type": "refusal", "refusal": "no"}},
			wantContent: `[{"type":"input_image","image_url":"` + dataURL + `"},{"type":"refusal","refusal":"no"}]`,
			jsonContent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
				Model: "gpt-test",
				Input: mustRawMessage(t, []map[string]any{
					{"type": "function_call", "call_id": "call_1", "name": "view_image", "arguments": "{}"},
					{"type": "function_call_output", "call_id": "call_1", "output": tt.output},
					{"role": "user", "content": "next"},
				}),
			})
			require.NoError(t, err)

			wantLen := 3
			if tt.wantMedia != nil {
				wantLen = 4
			}
			require.Len(t, got.Messages, wantLen)
			assert.Equal(t, "tool", got.Messages[1].Role)
			assert.Equal(t, "call_1", got.Messages[1].ToolCallId)
			if tt.jsonContent {
				assert.JSONEq(t, tt.wantContent, got.Messages[1].StringContent())
			} else {
				assert.Equal(t, tt.wantContent, got.Messages[1].StringContent())
			}
			assert.Equal(t, dto.Message{Role: "user", Content: "next"}, got.Messages[wantLen-1])
			if tt.wantMedia == nil {
				return
			}

			assert.Equal(t, "user", got.Messages[2].Role)
			parts := got.Messages[2].ParseContent()
			require.Len(t, parts, len(tt.wantMedia))
			for i, wantType := range tt.wantMedia {
				assert.Equal(t, wantType, parts[i].Type)
			}
			require.NotNil(t, parts[0].GetImageMedia())
			assert.Equal(t, dataURL, parts[0].GetImageMedia().Url)
		})
	}
}

func TestResponsesRequestToChatCompletionsRequestHoistsToolOutputMediaAfterToolBatch(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	imageOutput := []any{map[string]any{"type": "input_image", "image_url": dataURL}}

	t.Run("parallel outputs stay contiguous", func(t *testing.T) {
		got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
			Model: "gpt-test",
			Input: mustRawMessage(t, []map[string]any{
				{"type": "function_call", "call_id": "call_1", "name": "screenshot", "arguments": "{}"},
				{"type": "function_call", "call_id": "call_2", "name": "read_file", "arguments": "{}"},
				{"type": "function_call_output", "call_id": "call_1", "output": imageOutput},
				{"type": "function_call_output", "call_id": "call_2", "output": "file contents"},
				{"role": "user", "content": "what do you see?"},
			}),
		})
		require.NoError(t, err)

		require.Len(t, got.Messages, 5)
		assert.Equal(t, "assistant", got.Messages[0].Role)
		assert.Len(t, got.Messages[0].ParseToolCalls(), 2)
		assert.Equal(t, dto.Message{Role: "tool", ToolCallId: "call_1", Content: "[image]"}, got.Messages[1])
		assert.Equal(t, dto.Message{Role: "tool", ToolCallId: "call_2", Content: "file contents"}, got.Messages[2])
		assert.Equal(t, "user", got.Messages[3].Role)
		parts := got.Messages[3].ParseContent()
		require.Len(t, parts, 1)
		assert.Equal(t, dto.ContentTypeImageURL, parts[0].Type)
		assert.Equal(t, dto.Message{Role: "user", Content: "what do you see?"}, got.Messages[4])
	})

	t.Run("custom tool outputs stay in the same batch", func(t *testing.T) {
		got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
			Model: "gpt-test",
			Input: mustRawMessage(t, []map[string]any{
				{"type": "function_call", "call_id": "call_1", "name": "screenshot", "arguments": "{}"},
				{"type": "custom_tool_call", "call_id": "call_2", "name": "exec", "input": "ls"},
				{"type": "function_call_output", "call_id": "call_1", "output": imageOutput},
				{"type": "custom_tool_call_output", "call_id": "call_2", "output": "file.txt"},
			}),
		})
		require.NoError(t, err)

		require.Len(t, got.Messages, 4)
		assert.Len(t, got.Messages[0].ParseToolCalls(), 2)
		assert.Equal(t, dto.Message{Role: "tool", ToolCallId: "call_1", Content: "[image]"}, got.Messages[1])
		assert.Equal(t, dto.Message{Role: "tool", ToolCallId: "call_2", Content: "file.txt"}, got.Messages[2])
		assert.Equal(t, "user", got.Messages[3].Role)
	})

	t.Run("trailing output flushes media at end of input", func(t *testing.T) {
		got, err := ResponsesRequestToChatCompletionsRequest(context.Background(), &dto.OpenAIResponsesRequest{
			Model: "gpt-test",
			Input: mustRawMessage(t, []map[string]any{
				{"type": "function_call", "call_id": "call_1", "name": "screenshot", "arguments": "{}"},
				{"type": "function_call_output", "call_id": "call_1", "output": imageOutput},
			}),
		})
		require.NoError(t, err)

		require.Len(t, got.Messages, 3)
		assert.Equal(t, dto.Message{Role: "tool", ToolCallId: "call_1", Content: "[image]"}, got.Messages[1])
		assert.Equal(t, "user", got.Messages[2].Role)
		parts := got.Messages[2].ParseContent()
		require.Len(t, parts, 1)
		require.NotNil(t, parts[0].GetImageMedia())
		assert.Equal(t, dataURL, parts[0].GetImageMedia().Url)
	})
}

func mustRawMessage(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := kitutil.Marshal(value)
	require.NoError(t, err)
	return raw
}
