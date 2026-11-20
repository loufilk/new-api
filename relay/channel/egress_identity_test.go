package channel

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type egressFixtureAdaptor struct {
	Adaptor
	target    string
	authCalls int
}

func (a *egressFixtureAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) {
	return a.target, nil
}

func (a *egressFixtureAdaptor) SetupRequestHeader(_ *gin.Context, header *http.Header, _ *relaycommon.RelayInfo) error {
	a.authCalls++
	header.Set("Authorization", "Bearer fixture")
	return nil
}

func (*egressFixtureAdaptor) SignUpstreamRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ string, target *url.URL, _ http.Header) error {
	query := target.Query()
	query.Set("fixture", "signed")
	target.RawQuery = query.Encode()
	return nil
}

func TestChannelEgressUsesApprovedTarget(t *testing.T) {
	type received struct{ authority, authorization, query, contentType string }
	for _, mode := range []string{"http", "form", "websocket"} {
		t.Run(mode, func(t *testing.T) {
			result := make(chan received, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result <- received{r.Host, r.Header.Get("Authorization"), r.URL.Query().Get("fixture"), r.Header.Get("Content-Type")}
				if mode == "websocket" {
					conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				_, _ = fmt.Fprint(w, "ok")
			}))
			t.Cleanup(server.Close)
			target, err := url.Parse(server.URL)
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{HeadersOverride: map[string]any{"Host": target.Host}}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/relay", http.NoBody)
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			a := &egressFixtureAdaptor{target: server.URL + "/relay"}
			if mode == "websocket" {
				conn, err := DoWssRequest(a, c, info, nil)
				require.NoError(t, err)
				require.NoError(t, conn.Close())
			} else {
				send := DoApiRequest
				if mode == "form" {
					send = DoFormRequest
				}
				resp, err := send(a, c, info, strings.NewReader("sample=form"))
				require.NoError(t, err)
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Equal(t, "ok", string(body))
			}
			got := <-result
			assert.Equal(t, target.Host, got.authority)
			assert.Equal(t, "Bearer fixture", got.authorization)
			assert.Equal(t, "signed", got.query)
			if mode == "form" {
				assert.Equal(t, "application/x-www-form-urlencoded", got.contentType)
			}
			assert.Equal(t, 1, a.authCalls)
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
			assert.Equal(t, mode != "websocket", common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
		})
	}
}

func TestChannelEgressKeepsConfiguredHTTPProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	}))
	t.Cleanup(upstream.Close)
	type observed struct{ target, originAuth, proxyAuth string }
	got := make(chan observed, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- observed{r.URL.String(), r.Header.Get("Authorization"), r.Header.Get("Proxy-Authorization")}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	proxyURL.User = url.UserPassword("fixture", "local")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{Proxy: proxyURL.String()}}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/relay", http.NoBody)
	a := &egressFixtureAdaptor{target: upstream.URL + "/relay"}
	resp, err := DoApiRequest(a, c, info, http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	result := <-got
	assert.Equal(t, upstream.URL+"/relay?fixture=signed", result.target)
	assert.Equal(t, "Bearer fixture", result.originAuth)
	assert.NotEmpty(t, result.proxyAuth, "the selected transport supplies proxy authentication")
}

func TestChannelEgressRejectsIncompleteConfigurationBeforeHeaders(t *testing.T) {
	for _, spec := range []struct {
		name, url, proxy string
	}{
		{name: "relative upstream", url: "/v1/chat/completions"},
		{name: "proxy missing a host", url: "https://relay.invalid/v1", proxy: "http://"},
	} {
		t.Run(spec.name, func(t *testing.T) {
			for _, mode := range []string{"http", "prebuilt", "task", "websocket"} {
				a := &egressFixtureAdaptor{target: spec.url}
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{Proxy: spec.proxy}}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/relay", http.NoBody)
				common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, true)
				common.SetContextKey(c, constant.ContextKeyUpstreamRequestOrigin, "https://prior.invalid")
				common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
				common.SetContextKey(c, constant.ContextKeyUpstreamSubmissionPending, true)
				switch mode {
				case "websocket":
					_, err := DoWssRequest(a, c, info, nil)
					require.Error(t, err)
				case "task":
					task := &stubTaskAdaptor{baseURL: spec.url}
					_, err := DoTaskApiRequest(task, c, info, http.NoBody)
					require.Error(t, err)
					assert.Nil(t, task.capturedReq, "invalid task configuration must stop before header setup")
				case "prebuilt":
					req, err := http.NewRequest(http.MethodPost, spec.url, http.NoBody)
					require.NoError(t, err)
					_, err = DoRequest(c, req, info)
					require.Error(t, err)
				default:
					_, err := DoApiRequest(a, c, info, http.NoBody)
					require.Error(t, err)
				}
				assert.Zero(t, a.authCalls, "invalid configuration must stop before credential setup")
				assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
				assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyUpstreamRequestOrigin))
				assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamExplicitRejection))
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamSubmissionPending))
			}
		})
	}
}

func TestChannelEgressValidatesConfiguredHost(t *testing.T) {
	target, err := url.Parse("https://relay.invalid/v1")
	require.NoError(t, err)
	origin, err := channelEgressOrigin(target, false)
	require.NoError(t, err)
	require.NoError(t, origin.check(target, "RELAY.INVALID:443", http.Header{"Host": {"relay.invalid"}}))
	require.Error(t, origin.check(target, "", http.Header{"Host": {"other.relay.invalid"}}))
}
