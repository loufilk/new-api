package baidu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type baiduFixtureTransport func(*http.Request) (*http.Response, error)

func (f baiduFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newBaiduTokenFixtureClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	local, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: baiduFixtureTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "aip.baidubce.com" || request.URL.Path != "/oauth/2.0/token" {
			return nil, errors.New("unexpected Baidu fixture destination")
		}
		localRequest := request.Clone(request.Context())
		localRequest.URL.Scheme, localRequest.URL.Host = local.Scheme, local.Host
		localRequest.Host = local.Host
		return transport.RoundTrip(localRequest)
	})}
}

func TestBaiduTokenRequestCancellation(t *testing.T) {
	for _, entry := range []string{"helper", "adaptor"} {
		t.Run(entry, func(t *testing.T) {
			key := "synthetic-" + entry + "|synthetic-secret"
			baiduTokenStore.Delete(key)
			t.Cleanup(func() { baiduTokenStore.Delete(key) })
			started, stopped := make(chan struct{}), make(chan struct{})
			client := newBaiduTokenFixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "synthetic-"+entry, r.URL.Query().Get("client_id"))
				close(started)
				<-r.Context().Done()
				close(stopped)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			results := make(chan error, 1)
			if entry == "adaptor" {
				service.InitHttpClient()
				shared, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
				require.NoError(t, err)
				oldClient := *shared
				t.Cleanup(func() { *shared = oldClient })
				shared.Transport = client.Transport
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: key, UpstreamModelName: "ERNIE-Bot", ChannelBaseUrl: "https://must-not-be-called.invalid"}}
				go func() {
					_, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(`{}`))
					results <- err
				}()
			} else {
				go func() {
					_, err := getBaiduAccessTokenHelper(ctx, client, key)
					results <- err
				}()
			}
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("local OAuth fixture did not receive request")
			}
			cancel()
			select {
			case err := <-results:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("OAuth request did not stop after cancellation")
			}
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream OAuth connection was not canceled")
			}
			_, cached := baiduTokenStore.Load(key)
			assert.False(t, cached, "a canceled request cannot populate the token cache")
		})
	}
}

func TestBaiduCanceledTokenLookupDoesNotRefresh(t *testing.T) {
	const key = "synthetic-canceled|synthetic-secret"
	baiduTokenStore.Store(key, BaiduAccessToken{AccessToken: "synthetic-cached-token", ExpiresAt: time.Now().Add(time.Minute)})
	t.Cleanup(func() { baiduTokenStore.Delete(key) })
	var calls atomic.Int32
	client := newBaiduTokenFixtureClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	token, err := getBaiduAccessToken(ctx, client, key)
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, token)
	assert.Zero(t, calls.Load())
}

func TestBaiduTokenRefreshSurvivesCallerCancellationWithinDeadline(t *testing.T) {
	const key = "synthetic-refresh|synthetic-secret"
	baiduTokenStore.Store(key, BaiduAccessToken{AccessToken: "synthetic-cached-token", ExpiresAt: time.Now().Add(time.Minute)})
	t.Cleanup(func() { baiduTokenStore.Delete(key) })
	client := newBaiduTokenFixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"synthetic-refreshed-token","expires_in":7200}`)
	})
	transport := client.Transport
	captured := make(chan context.Context, 1)
	release := make(chan struct{})
	client.Transport = baiduFixtureTransport(func(request *http.Request) (*http.Response, error) {
		captured <- request.Context()
		<-release
		return transport.RoundTrip(request)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	token, err := getBaiduAccessToken(ctx, client, key)
	require.NoError(t, err)
	assert.Equal(t, "synthetic-cached-token", token)
	cancel()
	select {
	case refreshCtx := <-captured:
		assert.NoError(t, refreshCtx.Err(), "background refresh is independent of the completed caller")
		deadline, bounded := refreshCtx.Deadline()
		assert.True(t, bounded)
		assert.False(t, deadline.After(time.Now().Add(15*time.Second)), "background OAuth must have a short finite bound")
		close(release)
		require.Eventually(t, func() bool {
			cached, ok := baiduTokenStore.Load(key)
			return ok && cached.(BaiduAccessToken).AccessToken == "synthetic-refreshed-token"
		}, 5*time.Second, 10*time.Millisecond)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("background OAuth refresh did not start")
	}
}

func TestBaiduPreparedTokenBuildsGenerationURL(t *testing.T) {
	adaptor := &Adaptor{accessToken: "synthetic-prepared-token"}
	requestURL, err := adaptor.GetRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ERNIE-Bot", ChannelBaseUrl: "https://synthetic.baidu.invalid"}})
	require.NoError(t, err)
	assert.Equal(t, "https://synthetic.baidu.invalid/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions?access_token=synthetic-prepared-token", requestURL)
	_, err = (&Adaptor{}).GetRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	assert.Error(t, err, "URL construction never initiates unaffiliated OAuth traffic")
}
