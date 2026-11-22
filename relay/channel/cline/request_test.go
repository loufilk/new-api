package cline

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func testRelayInfo(format types.RelayFormat, mode int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat: format,
		RelayMode:   mode,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeCline,
			ChannelBaseUrl: DefaultBaseURL,
		},
	}
}

func testContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestGetRequestURLAvoidsDoubleV1(t *testing.T) {
	for _, tc := range []struct {
		name     string
		baseURL  string
		format   types.RelayFormat
		mode     int
		expected string
	}{
		{name: "default base", baseURL: DefaultBaseURL, format: types.RelayFormatOpenAI, mode: relayconstant.RelayModeChatCompletions, expected: "https://api.cline.bot/api/v1/chat/completions"},
		{name: "trailing slash", baseURL: "https://api.cline.bot/api/v1/", format: types.RelayFormatOpenAI, mode: relayconstant.RelayModeChatCompletions, expected: "https://api.cline.bot/api/v1/chat/completions"},
		{name: "custom base without v1", baseURL: "https://cline-proxy.example/gateway", format: types.RelayFormatOpenAI, mode: relayconstant.RelayModeChatCompletions, expected: "https://cline-proxy.example/gateway/v1/chat/completions"},
		{name: "claude conversion", baseURL: DefaultBaseURL, format: types.RelayFormatClaude, mode: relayconstant.RelayModeUnknown, expected: "https://api.cline.bot/api/v1/chat/completions"},
		{name: "gemini conversion", baseURL: DefaultBaseURL, format: types.RelayFormatGemini, mode: relayconstant.RelayModeUnknown, expected: "https://api.cline.bot/api/v1/chat/completions"},
		{name: "responses bridge", baseURL: DefaultBaseURL, format: types.RelayFormatOpenAIResponses, mode: relayconstant.RelayModeResponses, expected: "https://api.cline.bot/api/v1/chat/completions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := testRelayInfo(tc.format, tc.mode)
			info.ChannelBaseUrl = tc.baseURL
			url, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, url)
		})
	}
}

func TestGetRequestURLRejectsUnsupportedModes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format types.RelayFormat
		mode   int
	}{
		{name: "embedding", format: types.RelayFormatOpenAI, mode: relayconstant.RelayModeEmbeddings},
		{name: "completions", format: types.RelayFormatOpenAI, mode: relayconstant.RelayModeCompletions},
		{name: "images", format: types.RelayFormatOpenAI, mode: relayconstant.RelayModeImagesGenerations},
		{name: "audio", format: types.RelayFormatOpenAI, mode: relayconstant.RelayModeAudioSpeech},
		{name: "realtime", format: types.RelayFormatOpenAIRealtime, mode: relayconstant.RelayModeRealtime},
		{name: "responses compact", format: types.RelayFormatOpenAIResponses, mode: relayconstant.RelayModeResponsesCompact},
		{name: "alpha search", format: types.RelayFormatOpenAIAlphaSearch, mode: relayconstant.RelayModeAlphaSearch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&Adaptor{}).GetRequestURL(testRelayInfo(tc.format, tc.mode))
			require.Error(t, err)
			apiErr, ok := err.(*types.NewAPIError)
			require.True(t, ok)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Equal(t, types.ErrorCodeInvalidApiType, apiErr.GetErrorCode())
			assert.True(t, types.IsSkipRetryError(apiErr))
		})
	}
}

func TestGetRequestURLRequiresBaseURL(t *testing.T) {
	info := testRelayInfo(types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions)
	info.ChannelBaseUrl = "  "
	_, err := (&Adaptor{}).GetRequestURL(info)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base URL")
}

func TestSetupRequestHeaderSendsClineClientIdentityAndBearerToken(t *testing.T) {
	c := testContext()
	info := testRelayInfo(types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions)
	info.ApiKey = "cline-secret"
	header := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &header, info))
	assert.Equal(t, "Bearer cline-secret", header.Get("Authorization"))
	assert.Equal(t, "cline-cli", header.Get("X-Client-Type"))
}

func TestConvertOpenAIRequestPreservesExplicitStreamOptions(t *testing.T) {
	c := testContext()
	info := testRelayInfo(types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions)
	info.SupportStreamOptions = true
	info.IsStream = true
	stream := true
	request := &dto.GeneralOpenAIRequest{
		Model:         "cline-free/deepseek-v4.1-flash",
		Stream:        &stream,
		StreamOptions: &dto.StreamOptions{IncludeUsage: false},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, convertedRequest.StreamOptions)
	assert.False(t, convertedRequest.StreamOptions.IncludeUsage)
	encoded, err := common.Marshal(convertedRequest)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(encoded, "stream_options").Exists())
	assert.False(t, gjson.GetBytes(encoded, "stream_options.include_usage").Bool())
}

func TestConvertOpenAIRequestDoesNotInventStreamOptions(t *testing.T) {
	c := testContext()
	info := testRelayInfo(types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions)
	info.SupportStreamOptions = true
	info.IsStream = true
	stream := true
	request := &dto.GeneralOpenAIRequest{Model: "cline-free/deepseek-v4.1-flash", Stream: &stream}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Nil(t, convertedRequest.StreamOptions)
}

func TestConvertOpenAIRequestNormalizesRequiredEmptyContent(t *testing.T) {
	c := testContext()
	info := testRelayInfo(types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions)
	empty := ""
	whitespace := "   "
	request := &dto.GeneralOpenAIRequest{
		Model: "cline-free/deepseek-v4.1-flash",
		Messages: []dto.Message{
			{Role: "user", Content: nil},
			{Role: "system", Content: empty},
			{Role: "developer", Content: []any{}},
			{Role: "user", Content: []any{map[string]any{"type": "text"}}},
			{Role: "user", Content: []any{map[string]any{"type": "text", "text": ""}}},
			{Role: "assistant", Content: empty},
			{Role: "tool", Content: empty},
			{Role: "user", Content: whitespace},
			{Role: "user", Content: []any{map[string]any{"type": "text", "text": "hi"}}},
			{Role: "user", Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}}}},
			{Role: "user", Content: []dto.MediaContent{{Type: "text", Text: ""}}},
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	encoded, err := common.Marshal(convertedRequest)
	require.NoError(t, err)

	const canonical = `[{"type":"text","text":""}]`
	for _, index := range []int{0, 1, 2, 3, 4, 10} {
		path := "messages." + strconv.Itoa(index) + ".content"
		assert.JSONEq(t, canonical, gjson.GetBytes(encoded, path).Raw, "message %d", index)
	}
	assert.JSONEq(t, `""`, gjson.GetBytes(encoded, "messages.5.content").Raw)
	assert.JSONEq(t, `""`, gjson.GetBytes(encoded, "messages.6.content").Raw)
	assert.JSONEq(t, `"   "`, gjson.GetBytes(encoded, "messages.7.content").Raw)
	assert.Equal(t, "hi", gjson.GetBytes(encoded, "messages.8.content.0.text").String())
	assert.Equal(t, "image_url", gjson.GetBytes(encoded, "messages.9.content.0.type").String())
}

func TestConvertOpenAIRequestPreservesExplicitZeroValues(t *testing.T) {
	c := testContext()
	info := testRelayInfo(types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions)
	zeroFloat := 0.0
	zeroInt := 0
	request := &dto.GeneralOpenAIRequest{
		Model:            "cline-free/deepseek-v4.1-flash",
		Messages:         []dto.Message{{Role: "user", Content: "hello"}},
		Temperature:      &zeroFloat,
		TopP:             &zeroFloat,
		PresencePenalty:  &zeroFloat,
		FrequencyPenalty: &zeroFloat,
		N:                &zeroInt,
		MaxTokens:        common.GetPointer(uint(0)),
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(encoded, "temperature").Exists())
	assert.Equal(t, float64(0), gjson.GetBytes(encoded, "temperature").Float())
	assert.True(t, gjson.GetBytes(encoded, "top_p").Exists())
	assert.Equal(t, float64(0), gjson.GetBytes(encoded, "top_p").Float())
	assert.True(t, gjson.GetBytes(encoded, "presence_penalty").Exists())
	assert.True(t, gjson.GetBytes(encoded, "frequency_penalty").Exists())
	assert.True(t, gjson.GetBytes(encoded, "n").Exists())
	assert.Equal(t, int64(0), gjson.GetBytes(encoded, "n").Int())
}
