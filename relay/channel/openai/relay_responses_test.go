package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOaiResponsesStreamHandlerForwardsEachRawEventOnce(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	events := []string{
		`{"type":"response.created","sequence_number":0}`,
		`{"type":"response.reasoning_summary_text.delta","sequence_number":1,"delta":"thinking"}`,
		`{"type":"response.output_text.delta","sequence_number":2,"delta":"same sentence"}`,
		`{"type":"response.output_text.delta","sequence_number":3,"delta":"same sentence"}`,
		`{"type":"response.completed","sequence_number":4,"response":{"status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
	}
	lines := make([]string, 0, len(events)*2+1)
	for _, event := range events {
		lines = append(lines, "data: "+event, "")
	}
	lines = append(lines, "data: [DONE]", "")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(strings.Join(lines, "\n"))),
	}
	start := time.Now().Add(-time.Second)
	info := &relaycommon.RelayInfo{
		StartTime:   start,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	usage, relayErr := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, relayErr)
	require.NotNil(t, usage)
	assert.Equal(t, 1, usage.PromptTokens)
	assert.Equal(t, 2, usage.CompletionTokens)
	assert.Equal(t, 3, usage.TotalTokens)
	assert.Equal(t, len(events), info.ReceivedResponseCount)
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)

	forwarded := recorder.Body.String()
	for _, event := range events {
		assert.Equal(t, 1, strings.Count(forwarded, "data: "+event), event)
	}
	assert.Equal(t, 1, strings.Count(forwarded, `event: response.completed`))
	assert.Equal(t, 2, strings.Count(forwarded, `event: response.output_text.delta`))
}

func TestOaiResponsesModelRewritePreservesPayload(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	const upstream = `{"id":"resp_1","model":"private-provider-model","status":"completed","created_at":1786588600.0,"vendor_extension":{"integer":9007199254740993,"nullable":null},"output":[{"type":"function_call","name":"lookup","arguments":"{\"model\":\"user-content\"}","vendor_field":false}],"usage":{"input_tokens":3,"output_tokens":5,"total_tokens":8,"vendor_tokens":2}}`
	for _, stream := range []bool{false, true} {
		name := "json"
		if stream {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{
				OriginModelName: "public-model", DisablePing: true,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "private-provider-model"},
			}
			body := upstream
			expected := strings.Replace(upstream, `"model":"private-provider-model"`, `"model":"public-model"`, 1)
			if stream {
				body = "data: {\"type\":\"response.completed\",\"extension\":true,\"response\":" + upstream + "}\n\n"
				expected = "event: response.completed\ndata: {\"type\":\"response.completed\",\"extension\":true,\"response\":" + expected + "}\n\n"
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
			handler := OaiResponsesHandler
			if stream {
				handler = OaiResponsesStreamHandler
			}
			usage, apiErr := handler(c, info, resp)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, expected, recorder.Body.String())
			assert.Equal(t, 8, usage.TotalTokens)
			require.NotNil(t, info.ResponseModel)
			assert.Equal(t, "private-provider-model", info.ResponseModel.ReturnedModel)
			assert.Equal(t, "private-provider-model", info.GetUpstreamModelName())
		})
	}
}
