package service

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUserCallReplyNormalProtocols(t *testing.T) {
	const reply = "维护通知\nPlease contact support.\n\"quoted\" <safe>"
	for _, tc := range []struct{ protocol, textPath, zeroPath string }{
		{"chat", "choices.0.message.content", "usage.total_tokens"},
		{"playground", "choices.0.message.content", "usage.total_tokens"},
		{"completions", "choices.0.text", "usage.total_tokens"},
		{"responses", "output.0.content.0.text", "usage.total_tokens"},
		{"claude", "content.0.text", "usage.output_tokens"},
		{"gemini", "candidates.0.content.parts.0.text", "usageMetadata.totalTokenCount"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/", nil)
			WriteUserCallReply(c, UserCallReply{Protocol: tc.protocol, Model: "fixture-model"}, reply, "fixture-id")
			require.Equal(t, 200, w.Code)
			require.True(t, gjson.Valid(w.Body.String()))
			require.Equal(t, reply, gjson.Get(w.Body.String(), tc.textPath).String())
			usage := gjson.Get(w.Body.String(), tc.zeroPath)
			require.True(t, usage.Exists(), "zero usage must be explicit")
			require.Zero(t, usage.Int())
			require.False(t, gjson.Get(w.Body.String(), "error.type").Exists())
		})
	}
}

func TestUserCallReplyStreamingTerminalContracts(t *testing.T) {
	const reply = "中文通知\nEnglish reply\n event: error\n data: [DONE]"
	for _, protocol := range []string{"chat", "playground", "completions", "responses", "claude", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/", nil)
			WriteUserCallReply(c, UserCallReply{Protocol: protocol, Model: "fixture", Stream: true}, reply, "test")
			require.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
			var events []gjson.Result
			var names []string
			done := false
			for line := range strings.SplitSeq(w.Body.String(), "\n") {
				if name, ok := strings.CutPrefix(line, "event: "); ok {
					names = append(names, name)
				}
				if data, ok := strings.CutPrefix(line, "data: "); ok {
					if data == "[DONE]" {
						done = true
						continue
					}
					require.True(t, gjson.Valid(data), data)
					events = append(events, gjson.Parse(data))
				}
			}
			require.NotEmpty(t, events)
			switch protocol {
			case "chat", "playground":
				require.True(t, done)
				require.Equal(t, "assistant", events[0].Get("choices.0.delta.role").String())
				require.Equal(t, reply, events[1].Get("choices.0.delta.content").String())
				require.Equal(t, "stop", events[2].Get("choices.0.finish_reason").String())
				require.Equal(t, "0", events[2].Get("usage.total_tokens").Raw)
			case "completions":
				require.True(t, done)
				require.Equal(t, reply, events[0].Get("choices.0.text").String())
			case "responses":
				require.False(t, done)
				require.Equal(t, []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed"}, names)
				for i, event := range events {
					require.Equal(t, int64(i), event.Get("sequence_number").Int())
					require.Equal(t, names[i], event.Get("type").String())
				}
				require.Equal(t, reply, events[4].Get("delta").String())
				require.Equal(t, reply, events[8].Get("response.output.0.content.0.text").String())
				require.Equal(t, "completed", events[8].Get("response.status").String())
				require.Equal(t, "0", events[8].Get("response.usage.total_tokens").Raw)
			case "claude":
				require.False(t, done)
				require.Equal(t, []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}, names)
				require.Equal(t, reply, events[2].Get("delta.text").String())
				require.Equal(t, "end_turn", events[4].Get("delta.stop_reason").String())
				require.Equal(t, "0", events[4].Get("usage.output_tokens").Raw)
			case "gemini":
				require.False(t, done)
				require.Equal(t, reply, events[0].Get("candidates.0.content.parts.0.text").String())
				require.Equal(t, "STOP", events[0].Get("candidates.0.finishReason").String())
				require.Equal(t, "0", events[0].Get("usageMetadata.totalTokenCount").Raw)
			}
		})
	}
}

func TestUserCallReplyGeminiStreamingJSONArray(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	WriteUserCallReply(c, UserCallReply{Protocol: "gemini", Stream: true, JSONArray: true}, "text", "id")
	var result []map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result, 1)
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
}
