package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel/providerplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const providerBillingReply = `{"id":"chat-test","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`

func newProviderBillingContext(t *testing.T, source, baseURL string, stream bool) (*gin.Context, *relaycommon.RelayInfo, *providerplugin.Adaptor, *recordedSettlement) {
	t.Helper()
	registry := jsplugin.NewRegistry()
	plugin, err := registry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Unregister(plugin.Meta.Key)) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	common.SetContextKey(c, constant.ContextKeyProviderPlugin, jsplugin.PinnedPlugin{Plugin: plugin})
	billing := &recordedSettlement{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "m", RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
		RequestURLPath: c.Request.URL.Path, IsStream: stream, DisablePing: true, Billing: billing, StartTime: time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeTaskPlugin, ChannelBaseUrl: baseURL, ApiKey: "test", UpstreamModelName: "m",
			ChannelOtherSettings: dto.ChannelOtherSettings{AdvancedCustom: plugin.Meta.ChannelRelayConfig(nil)}},
	}
	info.UserSetting.QuotaWarningEnabled = common.GetPointer(false)
	info.PriceData.ModelRatio, info.PriceData.CompletionRatio, info.PriceData.GroupRatioInfo.GroupRatio = 1, 1, 1
	adaptor := &providerplugin.Adaptor{}
	adaptor.Init(info)
	return c, info, adaptor, billing
}

// Keep the production ordering: convert/relay the response, settle observed
// usage, then let the outer failure policy choose reconciliation or refund.
func finishProviderBillingResponse(t *testing.T, c *gin.Context, info *relaycommon.RelayInfo, adaptor *providerplugin.Adaptor, response any, err error) *types.NewAPIError {
	t.Helper()
	if err != nil {
		return types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	resp, ok := response.(*http.Response)
	require.True(t, ok)
	if resp.StatusCode != http.StatusOK {
		return service.RelayErrorHandler(c.Request.Context(), resp, false)
	}
	usage, apiErr := adaptor.DoResponse(c, resp, info)
	if apiErr != nil {
		consumePartialTextStreamQuota(c, info, usage)
		return apiErr
	}
	textUsage, ok := usage.(*dto.Usage)
	require.True(t, ok)
	service.PostTextConsumeQuota(c, info, textUsage, nil)
	return nil
}

type providerBillingTransport func(*http.Request) (*http.Response, error)

func (f providerBillingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderHTTPChainBillingPreservesAcceptedSubmission(t *testing.T) {
	batch, logConsume, streamingTimeout := common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.StreamingTimeout
	common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.StreamingTimeout = true, false, 10
	t.Cleanup(func() {
		common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.StreamingTimeout = batch, logConsume, streamingTimeout
	})
	type step struct {
		method string
		status int
		body   string
		err    error
	}
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	for _, tc := range []struct {
		name, first string
		steps       []step
		unknown     bool
		success     bool
		retry       bool
		stream      bool
	}{
		{name: "accepted job then poll dial failure", first: `{body:{prompt:"work"}}`, unknown: true,
			steps: []step{{http.MethodPost, 202, `{"next":{"path":"/jobs/accepted"}}`, nil}, {http.MethodGet, 0, "", dialErr}}},
		{name: "accepted job then explicit poll rejection", first: `{body:{prompt:"work"}}`, unknown: true,
			steps: []step{{http.MethodPost, 202, `{"next":{"path":"/jobs/accepted"}}`, nil}, {http.MethodGet, 401, `{}`, nil}}},
		{name: "query alone then dial failure", first: `{method:"GET",path:"/config"}`, retry: true,
			steps: []step{{http.MethodGet, 200, `{"next":{"path":"/next"}}`, nil}, {http.MethodGet, 0, "", dialErr}}},
		{name: "query then submission was never sent", first: `{method:"GET",path:"/config"}`, retry: true,
			steps: []step{{http.MethodGet, 200, `{"next":{"path":"/jobs","body":{"prompt":"work"}}}`, nil}, {http.MethodPost, 0, "", dialErr}}},
		{name: "query then accepted job then failed poll", first: `{method:"GET",path:"/config"}`, unknown: true,
			steps: []step{{http.MethodGet, 200, `{"next":{"path":"/jobs","body":{"prompt":"work"}}}`, nil}, {http.MethodPost, 202, `{"next":{"path":"/jobs/accepted"}}`, nil}, {http.MethodGet, 0, "", dialErr}}},
		{name: "initial submission rejected", first: `{body:{prompt:"work"}}`,
			steps: []step{{http.MethodPost, 400, `{}`, nil}}},
		{name: "initial submission never sent", first: `{body:{prompt:"work"}}`, retry: true,
			steps: []step{{http.MethodPost, 0, "", dialErr}}},
		{name: "accepted response conversion fails", first: `{body:{prompt:"work"}}`, unknown: true,
			steps: []step{{http.MethodPost, 200, `invalid-json`, nil}}},
		{name: "accepted job then complete usage", first: `{body:{prompt:"work"}}`, success: true,
			steps: []step{{http.MethodPost, 202, `{"next":{"path":"/jobs/accepted"}}`, nil}, {http.MethodGet, 200, providerBillingReply, nil}}},
		{name: "accepted job then unmeasured stream rejection", first: `{body:{prompt:"work"},stream:false}`, stream: true, unknown: true,
			steps: []step{{http.MethodPost, 202, `{"next":{"path":"/jobs/accepted","stream":true}}`, nil}, {http.MethodGet, 200, "data: {\"choices\":[]}\n\ndata: {\"error\":{\"type\":\"invalid_request_error\",\"code\":\"invalid_request\",\"message\":\"poll rejected\"}}\n\n", nil}}},
		{name: "accepted job then complete streamed usage", first: `{body:{prompt:"work"},stream:false}`, stream: true, success: true,
			steps: []step{{http.MethodPost, 202, `{"next":{"path":"/jobs/accepted","stream":true}}`, nil}, {http.MethodGet, 200, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\ndata: [DONE]\n\n", nil}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `export const meta = {apiVersion:1,key:"billing-chain",name:"Billing chain",version:"1.0.0",author:{name:"Test"},relay:[{incoming_path:"/v1/chat/completions",upstream_path:"/jobs",converter:"plugin"}]};
export function convertRequest() { return ` + tc.first + `; }
export function convertResponse(ctx, res) {
  if(res.status >= 400) return {error:{status:res.status,code:"do_request_failed",message:"explicit upstream rejection"}};
  const body = JSON.parse(res.body);
  return body.next ? {next:body.next} : {body};
}
export function convertStreamEvent(ctx, event) { return event.done ? {events:[]} : {events:[{data:event.data}]}; }`
			c, info, adaptor, billing := newProviderBillingContext(t, source, "https://provider.invalid", tc.stream)
			service.InitHttpClient()
			client, err := service.GetHttpClientWithProxySettings("", info.ChannelSetting)
			require.NoError(t, err)
			oldTransport := client.Transport
			t.Cleanup(func() { client.Transport = oldTransport })
			calls := 0
			client.Transport = providerBillingTransport(func(r *http.Request) (*http.Response, error) {
				require.Less(t, calls, len(tc.steps))
				step := tc.steps[calls]
				calls++
				assert.Equal(t, step.method, r.Method)
				if step.err != nil {
					return nil, step.err
				}
				contentType := "application/json"
				if strings.HasPrefix(step.body, "data:") {
					contentType = "text/event-stream"
				}
				return &http.Response{StatusCode: step.status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(step.body)), Request: r}, nil
			})
			response, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"m"}`))
			apiErr := finishProviderBillingResponse(t, c, info, adaptor, response, err)
			assert.Equal(t, len(tc.steps), calls)
			if tc.success {
				require.Nil(t, apiErr)
				assert.Equal(t, []int{12}, billing.settled)
				assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey))
				assert.False(t, billing.refunded)
				return
			}
			require.NotNil(t, apiErr)
			apiErr = PreserveFailedRequestBilling(c, info, apiErr)
			if tc.unknown {
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.Equal(t, "stop", service.DecideRelayRetry(c, apiErr, 3).Action)
			} else if tc.retry {
				assert.Equal(t, "retry", service.DecideRelayRetry(c, apiErr, 3).Action)
			}
			RefundFailedRequestBilling(c, info, apiErr)
			assert.Equal(t, tc.unknown, c.GetBool(requestBillingDeliveryUnknownKey))
			assert.Equal(t, !tc.unknown, billing.refunded)
			assert.Empty(t, billing.settled, "failed chains must not manufacture a zero or estimated settlement")
		})
	}
}

func TestProviderWebSocketBillingDistinguishesHandshakeAndSubmission(t *testing.T) {
	batch, logConsume, streamingTimeout := common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.StreamingTimeout
	common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.StreamingTimeout = true, false, 10
	t.Cleanup(func() {
		common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.StreamingTimeout = batch, logConsume, streamingTimeout
	})
	for _, mode := range []string{"cancelled before dial", "rejected handshake", "cancelled handshake only", "closed handshake only", "cancelled initial frame", "cancelled hook frame", "explicit rejection", "complete usage"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if mode == "cancelled before dial" {
				cancel()
			}
			var handshakes atomic.Int32
			seen := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handshakes.Add(1)
				if mode == "rejected handshake" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if !assert.NoError(t, err) {
					return
				}
				defer conn.Close()
				if mode == "cancelled hook frame" {
					assert.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("generate")))
				}
				if mode == "cancelled handshake only" || mode == "closed handshake only" {
					assert.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("waiting")))
					if mode == "cancelled handshake only" {
						cancel()
						_, _, _ = conn.ReadMessage()
					}
					return
				}
				_, body, err := conn.ReadMessage()
				if !assert.NoError(t, err) {
					return
				}
				seen <- string(body)
				switch mode {
				case "explicit rejection":
					assert.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("reject")))
				case "complete usage":
					assert.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("complete")))
				default:
					cancel()
					_, _, _ = conn.ReadMessage()
				}
			}))
			t.Cleanup(upstream.Close)
			messages := `['{"type":"generate"}']`
			if strings.Contains(mode, "handshake") || mode == "cancelled hook frame" {
				messages = "[]"
			}
			source := fmt.Sprintf(`
export const meta = {apiVersion:1,key:"billing-ws",name:"Billing WebSocket",version:"1.0.0",author:{name:"Test"},relay:[{incoming_path:"/v1/chat/completions",upstream_path:"/chat",converter:"plugin"}]};
export function convertRequest() { return {websocket:{path:"/chat",messages:%s}}; }
export function convertResponse() { return {body:{}}; }
export function convertStreamEvent(ctx, event) {
  if(event.data === "generate") return {send:['{"type":"generate"}']};
  if(event.data === "reject") return {error:{status:400,code:"do_request_failed",message:"explicit unused rejection"}};
  if(event.data === "complete") return {body:%s};
  return {events:[]};
}`, messages, providerBillingReply)
			c, info, adaptor, billing := newProviderBillingContext(t, source, upstream.URL, mode != "complete usage")
			c.Request = c.Request.WithContext(ctx)
			response, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"m"}`))
			apiErr := finishProviderBillingResponse(t, c, info, adaptor, response, err)
			if mode == "cancelled before dial" {
				assert.Zero(t, handshakes.Load())
			}
			unknown := mode == "cancelled initial frame" || mode == "cancelled hook frame"
			if unknown || mode == "explicit rejection" || mode == "complete usage" {
				select {
				case body := <-seen:
					assert.JSONEq(t, `{"type":"generate"}`, body)
				default:
					t.Fatal("fixture must prove the upstream received the generation frame")
				}
			}
			if mode == "complete usage" {
				require.Nil(t, apiErr)
				assert.Equal(t, []int{12}, billing.settled)
				assert.False(t, billing.refunded)
				assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey))
				return
			}
			require.NotNil(t, apiErr)
			apiErr = PreserveFailedRequestBilling(c, info, apiErr)
			if unknown {
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.Equal(t, "stop", service.DecideRelayRetry(c, apiErr, 3).Action)
			}
			RefundFailedRequestBilling(c, info, apiErr)
			assert.Equal(t, unknown, c.GetBool(requestBillingDeliveryUnknownKey))
			assert.Equal(t, !unknown, billing.refunded)
			assert.Empty(t, billing.settled)
		})
	}
}
