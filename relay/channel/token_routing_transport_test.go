package channel

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tokenTransportTestContext(noRetry bool) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyTokenNoRetryWithinGroup, noRetry)
	return c
}

func useTokenTransportTestClient(t *testing.T, client *http.Client) {
	t.Helper()
	service.InitHttpClient()
	shared := service.GetHttpClient()
	previous := *shared
	*shared = *client
	t.Cleanup(func() { *shared = previous })
}

func TestTokenSingleAttemptTransportPolicyPreservesBodyHeadersAndTimeout(t *testing.T) {
	for _, mode := range []string{"legacy", "token", "model-test"} {
		t.Run(mode, func(t *testing.T) {
			c := tokenTransportTestContext(mode == "token")
			common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, mode == "model-test")
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://synthetic.invalid", bytes.NewReader([]byte("payload")))
			require.NoError(t, err)
			req.Header.Set("Idempotency-Key", "synthetic-id")
			require.NotNil(t, req.GetBody)
			applySingleAttemptTransportPolicy(c, req)
			assert.Equal(t, int64(7), req.ContentLength)
			assert.Equal(t, "synthetic-id", req.Header.Get("Idempotency-Key"))
			assert.Same(t, ctx, req.Context())
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.NoError(t, req.Body.Close())
			assert.Equal(t, "payload", string(body))
			if mode == "legacy" {
				assert.NotNil(t, req.GetBody)
			} else {
				assert.Nil(t, req.GetBody)
			}
		})
	}
}

func TestTokenNoRetryHTTP1DoesNotReplayWrittenBodyOnReusedConnection(t *testing.T) {
	for _, noRetry := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-replays", true: "single-attempt"}[noRetry], func(t *testing.T) {
			var calls atomic.Int32
			payload := []byte(`{"model":"synthetic","messages":[]}`)
			observed := make(chan []byte, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/warmup" {
					w.WriteHeader(http.StatusOK)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					return
				}
				observed <- body
				if calls.Add(1) == 1 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				w.WriteHeader(http.StatusOK)
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
			c := tokenTransportTestContext(noRetry)
			req, err := http.NewRequest(http.MethodPost, server.URL+"/submit", bytes.NewReader(payload))
			require.NoError(t, err)
			req.Header.Set("Idempotency-Key", "synthetic-id")
			resp, err := doRequest(c, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
			if noRetry {
				require.Error(t, err)
				assert.Nil(t, resp)
				assert.EqualValues(t, 1, calls.Load())
				assert.Nil(t, req.GetBody)
			} else {
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.EqualValues(t, 2, calls.Load())
				assert.NotNil(t, req.GetBody)
			}
			for range int(calls.Load()) {
				assert.Equal(t, payload, <-observed)
			}
		})
	}
}

func TestTokenNoRetryHTTP2DoesNotReplayWrittenBodyAfterRefusedStream(t *testing.T) {
	for _, noRetry := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-replays", true: "single-attempt"}[noRetry], func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer ln.Close()
			results := runResetOnFirstStreamServer(ln, !noRetry)
			client, transport := newH2PriorKnowledgeClient(ln)
			defer transport.CloseIdleConnections()
			useTokenTransportTestClient(t, client)
			payload := []byte(`{"model":"synthetic","messages":[]}`)
			body, storage := newPassThroughBody(t, payload)
			defer storage.Close()
			req, err := http.NewRequest(http.MethodPost, "http://synthetic.invalid/submit", body)
			require.NoError(t, err)
			ApplyUpstreamBodyMetadata(req, body)
			require.NotNil(t, req.GetBody)
			resp, err := doRequest(tokenTransportTestContext(noRetry), req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
			if noRetry {
				require.Error(t, err)
				assert.Nil(t, resp)
				assert.Nil(t, req.GetBody)
			} else {
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
			}
			result := awaitH2ServerResult(t, results)
			require.NoError(t, result.err)
			assert.Equal(t, map[bool]int{false: 2, true: 1}[noRetry], result.streamCount)
			for _, actual := range result.attemptBodies {
				assert.Equal(t, payload, actual)
			}
			// Host-managed safe cross-group replay still owns the intact storage.
			replay, err := storage.Bytes()
			require.NoError(t, err)
			assert.Equal(t, payload, replay)
		})
	}
}
