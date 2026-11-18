package providerplugin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPConverterCancelsUpstreamWithClient(t *testing.T) {
	service.InitHttpClient()
	for _, headersSent := range []bool{false, true} {
		name := "waiting for response headers"
		if headersSent {
			name = "reading a nonstream response body"
		}
		t.Run(name, func(t *testing.T) {
			accepted := make(chan struct{})
			upstreamEnded := make(chan error, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := io.Copy(io.Discard, r.Body)
				assert.NoError(t, err)
				if headersSent {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Content-Length", "100")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				close(accepted)
				<-r.Context().Done()
				upstreamEnded <- r.Context().Err()
			}))
			t.Cleanup(upstream.Close)
			t.Cleanup(upstream.CloseClientConnections)

			registry := jsplugin.NewRegistry()
			plugin, err := registry.Register(`
export const meta = {apiVersion:1,key:"http-context",name:"HTTP context",version:"1.0.0",author:{name:"Test"},relay:[{incoming_path:"/v1/chat/completions",upstream_path:"/chat",converter:"plugin"}]};
export function convertRequest(ctx, req) { return {body:req.body}; }
export function convertResponse(ctx, res) { return {body:JSON.parse(res.body)}; }
export function convertStreamEvent() { return {events:[]}; }
`, jsplugin.Options{})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, registry.Unregister(plugin.Meta.Key)) })
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`)).WithContext(ctx)
			common.SetContextKey(c, constant.ContextKeyProviderPlugin, jsplugin.PinnedPlugin{Plugin: plugin})
			info := &relaycommon.RelayInfo{
				OriginModelName: "m", RelayFormat: types.RelayFormatOpenAI, RequestURLPath: c.Request.URL.Path,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeTaskPlugin, ChannelBaseUrl: upstream.URL, UpstreamModelName: "m", ApiKey: "test",
					ChannelOtherSettings: dto.ChannelOtherSettings{AdvancedCustom: plugin.Meta.ChannelRelayConfig(nil)}},
			}
			adaptor := &Adaptor{}
			adaptor.Init(info)
			completed := make(chan error, 1)
			go func() {
				_, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"m"}`))
				completed <- err
			}()
			select {
			case <-accepted:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream never received the request")
			}
			cancel()
			select {
			case err := <-completed:
				assert.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("client cancellation did not stop the converter's HTTP request")
			}
			select {
			case err := <-upstreamEnded:
				assert.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("client cancellation did not close the upstream HTTP request")
			}
		})
	}
}
