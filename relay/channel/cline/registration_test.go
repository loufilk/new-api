package cline_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/cline"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClineChannelRegistration(t *testing.T) {
	assert.Equal(t, 64, constant.ChannelTypeCline)
	assert.Equal(t, constant.ChannelTypeCline, constant.ChannelTypeDummy,
		"in this const block Dummy repeats the last real type as the inclusive iteration bound")
	assert.Equal(t, "Cline", constant.GetChannelTypeName(constant.ChannelTypeCline))
	assert.Equal(t, "https://api.cline.bot/api/v1", constant.GetChannelBaseURL(constant.ChannelTypeCline))
	assert.Equal(t, cline.DefaultBaseURL, constant.GetChannelBaseURL(constant.ChannelTypeCline))

	apiType, ok := common.ChannelType2APIType(constant.ChannelTypeCline)
	require.True(t, ok, "Cline must have an explicit API type instead of the OpenAI fallback")
	assert.Equal(t, constant.APITypeCline, apiType)
	require.NotNil(t, relay.GetAdaptor(apiType))
	assert.IsType(t, &cline.Adaptor{}, relay.GetAdaptor(apiType))
}

func TestClineEndpointCapabilities(t *testing.T) {
	endpoints := common.GetEndpointTypesByChannelType(constant.ChannelTypeCline, "cline-free/deepseek-v4.1-flash")
	assert.Contains(t, endpoints, constant.EndpointTypeOpenAI)
	assert.Contains(t, endpoints, constant.EndpointTypeOpenAIResponse)
	assert.NotContains(t, endpoints, constant.EndpointTypeOpenAIResponseCompact)
	assert.NotContains(t, endpoints, constant.EndpointTypeOpenAIAlphaSearch)
	assert.NotContains(t, endpoints, constant.EndpointTypeAnthropic)
	assert.NotContains(t, endpoints, constant.EndpointTypeGemini)

	assert.False(t, common.SupportsResponsesCompact(constant.ChannelTypeCline, constant.APITypeCline),
		"the Cline bridge exposes no upstream compact endpoint")
}

func TestClineSupportsStreamOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeCline)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.InitChannelMeta(c)
	assert.True(t, info.SupportStreamOptions, "official Cline sends stream_options.include_usage")
}
