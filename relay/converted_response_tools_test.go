package relay_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/ali"
	"github.com/QuantumNous/new-api/relay/channel/deepseek"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/relay/channel/xai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Init runs while the incoming request is still Chat/Claude. Request routing
// and tool billing must follow its later conversion to the Responses protocol.
// Exercise JSON, buffered SSE and live SSE against the same provider facts.
func TestConvertedResponsesUsesVendorProtocolAndCumulativeToolUsage(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for _, vendor := range []struct {
		name, path, extra string
		channelType       int
		adaptor           func() channel.Adaptor
		output            string
		counts            map[string]int
	}{
		{
			name: "deepseek", path: "/responses", channelType: constant.ChannelTypeDeepSeek,
			adaptor: func() channel.Adaptor { return &deepseek.Adaptor{} },
			output:  `[{"type":"web_search_call","id":"search","status":"completed","action":{"type":"search"}},{"type":"web_search_call","id":"fetch","status":"completed","action":{"type":"open_page"}},{"type":"web_search_call","id":"empty","status":"failed","action":{"type":"open_page"}}]`,
			extra:   `"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}`,
			counts:  map[string]int{deepseek.SearchToolPriceKey: 1, deepseek.FetchToolPriceKey: 1},
		},
		{
			name: "ali", path: "/compatible-mode/v1/responses", channelType: constant.ChannelTypeAli,
			adaptor: func() channel.Adaptor { return &ali.Adaptor{} },
			output:  `[{"type":"web_search_call","id":"search","status":"completed"}]`,
			extra:   `"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"x_tools":{"web_search":{"count":2}}}`,
			counts:  map[string]int{"search_strategy_agent": 2},
		},
		{
			name: "xai", path: "/v1/responses", channelType: constant.ChannelTypeXai,
			adaptor: func() channel.Adaptor { return &xai.Adaptor{} },
			output:  `[{"type":"web_search_call","id":"search","status":"completed"}]`,
			extra:   `"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"server_side_tool_usage_details":{"web_search_calls":2,"x_posts_fetched":3,"x_users_fetched":1}}`,
			counts:  map[string]int{dto.BuildInToolWebSearch: 2, "x_search_posts": 3, "x_search_profiles": 1},
		},
		{
			name: "azure", path: "/openai/v1/responses?api-version=preview", channelType: constant.ChannelTypeAzure,
			adaptor: func() channel.Adaptor { return &openaichannel.Adaptor{} },
			output:  `[{"type":"web_search_call","id":"search","status":"completed"}]`,
			extra:   `"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2},"tool_usage":{"web_search":{"num_requests":2}}`,
			counts:  map[string]int{"bing_web_search": 2},
		},
	} {
		for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude} {
			for _, transport := range []string{"json", "buffered", "stream"} {
				t.Run(fmt.Sprintf("%s/%s/%s", vendor.name, format, transport), func(t *testing.T) {
					info := &relaycommon.RelayInfo{
						OriginModelName: "converted-model", RelayFormat: format, RelayMode: relayconstant.RelayModeChatCompletions,
						IsStream: transport == "stream", DisablePing: true,
						ChannelMeta: &relaycommon.ChannelMeta{ChannelType: vendor.channelType, ChannelBaseUrl: "https://provider.example", UpstreamModelName: "converted-model"},
					}
					adaptor := vendor.adaptor()
					adaptor.Init(info)
					info.RelayMode, info.RequestURLPath = relayconstant.RelayModeResponses, "/v1/responses"
					_, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{Model: "converted-model"})
					require.NoError(t, err)
					url, err := adaptor.GetRequestURL(info)
					require.NoError(t, err)
					assert.Equal(t, "https://provider.example"+vendor.path, url)
					require.NotNil(t, info.VendorToolUsage)

					body := `{"id":"resp_tools","model":"converted-model","status":"completed","output":` + vendor.output + `,` + vendor.extra + `}`
					contentType := "application/json"
					if transport != "json" {
						var output []dto.ResponsesOutput
						require.NoError(t, common.UnmarshalJsonStr(vendor.output, &output))
						var events strings.Builder
						for i := range output {
							encoded, err := common.Marshal(dto.ResponsesStreamResponse{Type: dto.ResponsesOutputTypeItemDone, Item: &output[i], OutputIndex: &i})
							require.NoError(t, err)
							for range 2 {
								fmt.Fprintf(&events, "data: %s\n\n", encoded)
							}
						}
						fmt.Fprintf(&events, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", body)
						body, contentType = events.String(), "text/event-stream"
					}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {contentType}}}
					var usage *dto.Usage
					var apiErr *types.NewAPIError
					switch transport {
					case "json":
						usage, apiErr = openaichannel.OaiResponsesToChatHandler(c, info, resp)
					case "buffered":
						usage, apiErr = openaichannel.OaiResponsesToChatBufferedStreamHandler(c, info, resp)
					case "stream":
						usage, apiErr = openaichannel.OaiResponsesToChatStreamHandler(c, info, resp)
					}
					require.Nil(t, apiErr)
					require.NotNil(t, usage)
					assert.Equal(t, 2, usage.TotalTokens)
					actual := make(map[string]int)
					require.NotNil(t, info.ResponsesUsageInfo)
					for key, tool := range info.ResponsesUsageInfo.BuiltInTools {
						if tool != nil && tool.CallCount > 0 {
							actual[key] = tool.CallCount
						}
					}
					assert.Equal(t, vendor.counts, actual, "replayed done items and terminal snapshots must never add a second surcharge")
				})
			}
		}
	}
}
