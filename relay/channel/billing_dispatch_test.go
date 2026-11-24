package channel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	hostcommon "github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// No database owner is installed by this slice. Substitute only that durable
// boundary while exercising the actual common HTTP send and failure paths.
type dispatchTestBilling struct {
	claims    atomic.Int32
	claimErr  error
	claimWait time.Duration
}

func (*dispatchTestBilling) Settle(int) error              { return nil }
func (*dispatchTestBilling) Refund(*gin.Context)           {}
func (*dispatchTestBilling) NeedsRefund() bool             { return true }
func (*dispatchTestBilling) GetPreConsumedQuota() int      { return 1 }
func (*dispatchTestBilling) Reserve(int) error             { return nil }
func (*dispatchTestBilling) ReserveBeforeSubmit(int) error { return nil }
func (s *dispatchTestBilling) ClaimBillingHTTPDispatch(context.Context) error {
	if s.claims.Add(1) != 1 {
		return errors.New("durable owner already claimed")
	}
	if s.claimWait > 0 {
		time.Sleep(s.claimWait)
	}
	return s.claimErr
}
func dispatchTestInfo(billing *dispatchTestBilling) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI}
	if billing != nil {
		info.Billing = billing
	}
	return info
}
func dispatchTestRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/v1/chat/completions", bytes.NewBufferString(`{"model":"synthetic","messages":[]}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer synthetic-only")
	req.Header.Set("Idempotency-Key", "synthetic-id")
	return req
}

func TestBillingHTTPDispatchOptionalOwner(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "owned"}[owned], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "/v1/chat/completions", r.URL.Path)
				assert.Equal(t, "Bearer synthetic-only", r.Header.Get("Authorization"))
				assert.Equal(t, "synthetic-id", r.Header.Get("Idempotency-Key"))
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, `{"model":"synthetic","messages":[]}`, string(body))
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			useTokenTransportTestClient(t, server.Client())
			c := tokenTransportTestContext(false)
			// Neither plausible inbound nor outbound headers can enable this.
			c.Request.Header.Set("X-Enterprise-Lane", "true")
			info := dispatchTestInfo(nil)
			billing := &dispatchTestBilling{}
			if owned {
				info.Billing = billing
			}
			req := dispatchTestRequest(t, server.URL)
			resp, err := DoRequest(c, req, info)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.EqualValues(t, 1, calls.Load())
			assert.Equal(t, owned, relaycommon.BillingHTTPDispatchOwned(c, info))
			if owned {
				assert.EqualValues(t, 1, billing.claims.Load())
				assert.Nil(t, req.GetBody)
				// Losing the in-memory source must not reopen ordinary transport.
				info.Billing = nil
			} else {
				assert.NotNil(t, req.GetBody)
			}
			resp, err = DoRequest(c, dispatchTestRequest(t, server.URL), info)
			if owned {
				require.Error(t, err)
				assert.Nil(t, resp)
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.EqualValues(t, 1, calls.Load())
				assert.EqualValues(t, 1, billing.claims.Load())
			} else {
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.EqualValues(t, 2, calls.Load())
			}
		})
	}
}

func TestBillingHTTPDispatchRefusedOrLostAckDoesNotSend(t *testing.T) {
	for _, reason := range []string{"owner conflict", "commit acknowledgement lost"} {
		t.Run(reason, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			defer server.Close()
			useTokenTransportTestClient(t, server.Client())
			c := tokenTransportTestContext(false)
			billing := &dispatchTestBilling{claimErr: errors.New(reason)}
			info := dispatchTestInfo(billing)
			for range 2 {
				resp, err := DoRequest(c, dispatchTestRequest(t, server.URL), info)
				require.Error(t, err)
				assert.Nil(t, resp)
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.False(t, hostcommon.GetContextKeyBool(c, appconstant.ContextKeyUpstreamRequestAttempted))
			}
			assert.EqualValues(t, 1, billing.claims.Load())
			assert.Zero(t, calls.Load())
		})
	}
}

func TestBillingHTTPDispatchRejectsUnsupportedBinding(t *testing.T) {
	for _, kind := range []string{"method", "body", "outbound route", "query", "inbound route", "format", "websocket"} {
		t.Run(kind, func(t *testing.T) {
			c := tokenTransportTestContext(false)
			billing := &dispatchTestBilling{}
			info := dispatchTestInfo(billing)
			req := dispatchTestRequest(t, "https://synthetic.invalid")
			switch kind {
			case "method":
				req.Method = http.MethodGet
			case "body":
				req.Body = http.NoBody
			case "outbound route":
				req.URL.Path = "/v1/responses"
			case "query":
				req.URL.RawQuery = "alternate=true"
			case "inbound route":
				c.Request.URL.Path = "/v1/responses"
			case "format":
				info.RelayFormat = types.RelayFormatClaude
			case "websocket":
				conn, err := DoWssRequest(nil, c, info, nil)
				require.Error(t, err)
				assert.Nil(t, conn)
				assert.Zero(t, billing.claims.Load())
				return
			}
			err := relaycommon.ClaimBillingHTTPDispatch(c, info, req)
			require.NotNil(t, err)
			assert.True(t, types.IsSkipRetryError(err))
			assert.Zero(t, billing.claims.Load())
		})
	}
}

func TestBillingHTTPDispatchDoesNotReplayWrittenBody(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/warmup" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	warmup, err := client.Get(server.URL + "/warmup")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, warmup.Body)
	require.NoError(t, err)
	require.NoError(t, warmup.Body.Close())
	useTokenTransportTestClient(t, client)
	c := tokenTransportTestContext(false)
	billing := &dispatchTestBilling{}
	info := dispatchTestInfo(billing)
	resp, err := DoRequest(c, dispatchTestRequest(t, server.URL), info)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.EqualValues(t, 1, calls.Load())
	resp, err = DoRequest(c, dispatchTestRequest(t, server.URL), info)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.EqualValues(t, 1, calls.Load())
	assert.EqualValues(t, 1, billing.claims.Load())
}

func TestBillingHTTPDispatchDoesNotReplayHTTP2RefusedStream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	results := runResetOnFirstStreamServer(ln, false)
	client, transport := newH2PriorKnowledgeClient(ln)
	defer transport.CloseIdleConnections()
	useTokenTransportTestClient(t, client)
	c := tokenTransportTestContext(false)
	billing := &dispatchTestBilling{}
	resp, err := DoRequest(c, dispatchTestRequest(t, "http://synthetic.invalid"), dispatchTestInfo(billing))
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.EqualValues(t, 1, billing.claims.Load())
	result := awaitH2ServerResult(t, results)
	require.NoError(t, result.err)
	assert.Equal(t, 1, result.streamCount)
}

func TestBillingHTTPDispatchSlowRefusalDoesNotCommitSSE(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	enabled, interval := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 1
	t.Cleanup(func() { settings.PingIntervalEnabled, settings.PingIntervalSeconds = enabled, interval })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	useTokenTransportTestClient(t, server.Client())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	billing := &dispatchTestBilling{claimErr: errors.New("unacknowledged claim"), claimWait: 1200 * time.Millisecond}
	info := dispatchTestInfo(billing)
	info.IsStream = true
	resp, err := DoRequest(c, dispatchTestRequest(t, server.URL), info)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.False(t, c.Writer.Written(), "SQL claim cannot be preceded by a keepalive 200 response")
	assert.Empty(t, recorder.Body.String())
	assert.Zero(t, calls.Load())
}

func TestBillingHTTPDispatchDoesNotFollowRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/v1/chat/completions")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	useTokenTransportTestClient(t, source.Client())
	c := tokenTransportTestContext(false)
	billing := &dispatchTestBilling{}
	resp, err := DoRequest(c, dispatchTestRequest(t, source.URL), dispatchTestInfo(billing))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
	assert.Zero(t, targetCalls.Load())
	assert.EqualValues(t, 1, billing.claims.Load())
}
