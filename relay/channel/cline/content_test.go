package cline

import (
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClineContentCanonicalizesNativeImagesAndEmptyText(t *testing.T) {
	for _, content := range []string{
		`[{"type":"text"},{"type":"image_url","image_url":"https://example.test/a.png"}]`,
		`[{"type":"text","text":""},{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]`,
		`[{"type":"input_text","text":""},{"type":"input_image","image_url":"https://example.test/a.png"}]`,
	} {
		body := []byte(`{"model":"deepseek/deepseek-v4.1-flash","temperature":0,"stream":false,"vendor":{"number":9007199254740993},"messages":[{"role":"user","content":` + content + `}]}`)
		got, err := normalizeClineWireContent(body)
		require.NoError(t, err)
		assert.Equal(t, `""`, gjson.GetBytes(got, "messages.0.content.0.text").Raw)
		assert.Equal(t, "https://example.test/a.png", gjson.GetBytes(got, "messages.0.content.1.image_url.url").Str)
		assert.Equal(t, "image_url", gjson.GetBytes(got, "messages.0.content.1.type").Str)
		for _, path := range []string{"temperature", "stream", "vendor.number"} {
			assert.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(got, path).Raw)
		}
		again, err := normalizeClineWireContent(got)
		require.NoError(t, err)
		assert.JSONEq(t, string(got), string(again))
	}
	request := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: []dto.MediaContent{
		{Type: "text"}, {Type: "text", Text: "ordinary text without any image"},
	}}}}
	body, err := common.Marshal(request)
	require.NoError(t, err)
	got, err := normalizeClineWireContent(body)
	require.NoError(t, err)
	assert.Equal(t, `""`, gjson.GetBytes(got, "messages.0.content.0.text").Raw)
	assert.Equal(t, "ordinary text without any image", gjson.GetBytes(got, "messages.0.content.1.text").Str)
}

func TestClineContentPreservesOriginalDetailAndDataURL(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"data:image/png;base64,aGk=","detail":"original"}]}]}`)
	got, err := normalizeClineWireContent(body)
	require.NoError(t, err)
	assert.Equal(t, "data:image/png;base64,aGk=", gjson.GetBytes(got, "messages.0.content.0.image_url.url").Str)
	assert.Equal(t, "original", gjson.GetBytes(got, "messages.0.content.0.image_url.detail").Str)
	assert.False(t, gjson.GetBytes(got, "messages.0.content.0.detail").Exists())
}

func TestClineContentHoistsAllToolMediaAfterContiguousBatch(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"shot","arguments":"{}"}},{"id":"call_b","type":"function","function":{"name":"shot","arguments":"{}"}}]},
		{"role":"tool","name":"shot","tool_call_id":"call_a","content":[{"type":"text","text":"first"},{"type":"image_url","image_url":"https://example.test/a.png"}]},
		{"role":"tool","tool_call_id":"call_b","content":[{"type":"image_url","image_url":{"url":"https://example.test/b.png","detail":"high"}}]},
		{"role":"user","content":"compare them"}
	]}`)
	got, err := normalizeClineWireContent(body)
	require.NoError(t, err)
	assert.Equal(t, `["assistant","tool","tool","user","user"]`, gjson.GetBytes(got, "messages.#.role").Raw)
	assert.Equal(t, "first", gjson.GetBytes(got, "messages.1.content").Str)
	assert.Equal(t, "[image]", gjson.GetBytes(got, "messages.2.content").Str)
	assert.Equal(t, "call_a", gjson.GetBytes(got, "messages.1.tool_call_id").Str)
	assert.Equal(t, "call_b", gjson.GetBytes(got, "messages.2.tool_call_id").Str)
	assert.Equal(t, "shot", gjson.GetBytes(got, "messages.1.name").Str)
	assert.Equal(t, "https://example.test/a.png", gjson.GetBytes(got, "messages.3.content.0.image_url.url").Str)
	assert.Equal(t, "https://example.test/b.png", gjson.GetBytes(got, "messages.3.content.1.image_url.url").Str)
	assert.Equal(t, "compare them", gjson.GetBytes(got, "messages.4.content").Str)
	assert.JSONEq(t, gjson.GetBytes(body, "messages.0.tool_calls").Raw, gjson.GetBytes(got, "messages.0.tool_calls").Raw)
	again, err := normalizeClineWireContent(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(got), string(again), "hoisting must not duplicate media on retries")
}

func TestClineContentRejectsMalformedPartsWithSafePrecisePaths(t *testing.T) {
	for _, tc := range []struct{ content, suffix string }{
		{`[{"type":"text","text":42}]`, ".0.text"},
		{`[{"type":"text","text":null}]`, ".0.text"},
		{`[{"type":"text","text":false}]`, ".0.text"},
		{`[{"type":"image_url","image_url":null}]`, ".0.image_url"},
		{`[{"type":"image_url","image_url":{"file_id":"private-secret"}}]`, ".0.image_url.url"},
		{`[{"type":"image_url","image_url":{"url":false}}]`, ".0.image_url.url"},
		{`[{"type":"image_url","image_url":""}]`, ".0.image_url.url"},
		{`[{"type":"image_url","image_url":"file:///private-secret"}]`, ".0.image_url.url"},
		{`[{"type":"image_url","image_url":"data:image/png;base64,"}]`, ".0.image_url.url"},
		{`[{"type":"image_url","image_url":{"url":"https://example.test/a.png","detail":0}}]`, ".0.image_url.detail"},
		{`[{"type":"video_url","video_url":"private-secret"}]`, ".0.type"},
		{`[{"type":"input_video","video_url":"private-secret"}]`, ".0.type"},
		{`[{"type":"file","file":{"file_id":"private-secret"}}]`, ".0.file"},
		{`[{"type":"new-unknown","secret":"private-secret"}]`, ".0.type"},
		{`[null]`, ".0.type"},
		{`[42]`, ""},
		{`{"text":"private-secret"}`, ""},
		{`false`, ""},
	} {
		t.Run(tc.content, func(t *testing.T) {
			got, err := normalizeClineWireContent([]byte(`{"messages":[{"role":"user","content":` + tc.content + `}]}`))
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Nil(t, got)
			assert.Equal(t, 400, apiErr.StatusCode)
			assert.Equal(t, "messages.0.content"+tc.suffix, apiErr.ToOpenAIError().Param)
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.NotContains(t, apiErr.Error(), "private-secret")
		})
	}
}

func TestClineContentRejectsInvalidMessageContainers(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{}`, `{"messages":null}`, `{"messages":[]}`, `{"messages":[null]}`, `{"messages":[{"role":"invalid","content":"hi"}]}`} {
		_, err := normalizeClineWireContent([]byte(body))
		require.Error(t, err, body)
	}
}

func TestClineContentReadBoundDoesNotConsumeUnboundedInput(t *testing.T) {
	previous := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1
	t.Cleanup(func() { constant.MaxRequestBodyMB = previous })
	reader := strings.NewReader(strings.Repeat("x", (1<<20)+200))
	_, err := normalizeClineRequestBody(reader)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 413, apiErr.StatusCode)
	assert.Equal(t, 199, reader.Len())
	assert.True(t, types.IsSkipRetryError(apiErr))
	_, err = normalizeClineRequestBody(io.LimitReader(strings.NewReader("{}"), 2))
	require.Error(t, err)
}
