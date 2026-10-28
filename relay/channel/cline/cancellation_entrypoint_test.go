package cline_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cancellationStreamFixture = `data: {"success":true,"data":{"id":"test-cancel","model":"deepseek/deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}}

data: [DONE]

`

type cancelStreamOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (r *cancelStreamOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.cancel)
	return err
}

type cancelDeliveryWriter struct {
	gin.ResponseWriter
	cancel      context.CancelFunc
	complete    bool
	breakWrites bool
}

func (w *cancelDeliveryWriter) WriteString(s string) (int, error) {
	if w.breakWrites {
		return 0, io.ErrClosedPipe
	}
	if strings.Contains(s, "event: response.completed\n") {
		w.complete = true
	}
	return w.ResponseWriter.WriteString(s)
}
func (w *cancelDeliveryWriter) Flush() {
	w.ResponseWriter.Flush()
	if w.complete {
		w.cancel()
	}
}

func TestClineResponsesDoneThenCancellationNeverFallsBack(t *testing.T) {
	var poolCalls, fallbackCalls atomic.Int32
	pool, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		poolCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, cancellationStreamFixture)
	})
	fallback, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		fallbackCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, cancellationStreamFixture)
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "deepseek/deepseek-v4.1-flash", pool.URL+"/api/v1")
	ctx, cancel := context.WithCancel(env.context.Request.Context())
	defer cancel()
	env.context.Request = env.context.Request.WithContext(ctx)
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *newResponsesStreamRequest(env.info.OriginModelName))
	require.NoError(t, err)
	resp := env.send(t, converted)
	// Closing the upstream after [DONE] cancels the client before the bridge
	// writes its final Responses frame. No sleeps or ordering assumptions.
	resp.Body = &cancelStreamOnClose{ReadCloser: resp.Body, cancel: cancel}
	rawUsage, apiErr := env.receive(t, resp)
	require.NotNil(t, apiErr)
	assert.Equal(t, service.ClientGoneErrorCode, apiErr.GetErrorCode())
	assert.Equal(t, 499, apiErr.StatusCode)
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.False(t, types.IsRecordErrorLog(apiErr), "normal cancellation is not a channel-error row")
	assert.True(t, common.GetContextKeyBool(env.context, constant.ContextKeyResponsesStreamErrorHandled))
	usage, ok := rawUsage.(*dto.Usage)
	require.True(t, ok)
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.TotalTokens, "an already observed usage snapshot must survive downstream cancellation")
	assert.Equal(t, relaycommon.StreamEndReasonDone, env.info.StreamStatus.EndReason)
	assert.Equal(t, "cancelled", env.info.StreamStatus.ResponseOutcome())
	assert.NotContains(t, env.recorder.Body.String(), "event: response.completed")
	// Exercise the same shared decision used by Relay with a real fallback
	// server; neither of the former 44 -> 54 -> 54 attempts is permitted.
	for remaining := 2; remaining > 0; remaining-- {
		if !service.ShouldRetryRelayError(env.context, apiErr, remaining) {
			break
		}
		env.info.ChannelBaseUrl = fallback.URL + "/api/v1"
		next := env.send(t, converted)
		_ = next.Body.Close()
	}
	assert.Equal(t, int32(1), poolCalls.Load())
	assert.Zero(t, fallbackCalls.Load())
}

func TestClineResponsesDeliveryFailureAndLateCleanup(t *testing.T) {
	for _, broken := range []bool{false, true} {
		env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "deepseek/deepseek-v4.1-flash", "http://127.0.0.1:1/api/v1")
		ctx, cancel := context.WithCancel(env.context.Request.Context())
		env.context.Request = env.context.Request.WithContext(ctx)
		env.context.Writer = &cancelDeliveryWriter{ResponseWriter: env.context.Writer, cancel: cancel, breakWrites: broken}
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(cancellationStreamFixture))}
		rawUsage, apiErr := env.receive(t, resp)
		usage, ok := rawUsage.(*dto.Usage)
		require.True(t, ok)
		require.NotNil(t, usage)
		assert.Equal(t, 7, usage.TotalTokens)
		if broken {
			require.NotNil(t, apiErr)
			assert.Equal(t, service.ClientGoneErrorCode, apiErr.GetErrorCode())
			assert.Nil(t, ctx.Err(), "a socket write error must stop retry even before parent cancellation arrives")
			assert.False(t, service.ShouldRetryRelayError(env.context, apiErr, 3))
		} else {
			assert.Nil(t, apiErr, "late cancellation after a delivered terminal must not rewrite success")
			assert.ErrorIs(t, ctx.Err(), context.Canceled)
			assert.Equal(t, "completed", env.info.StreamStatus.ResponseOutcome())
			assert.Equal(t, 1, strings.Count(env.recorder.Body.String(), "event: response.completed\n"))
		}
		cancel()
	}
}

type cancellationCountingReader struct{ reads *atomic.Int32 }

func (r cancellationCountingReader) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, errors.New("unexpected body read")
}

func TestClineCancelledRequestDoesNotReadBodyOrDial(t *testing.T) {
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, false, "deepseek/deepseek-v4.1-flash", "http://127.0.0.1:1/api/v1")
	ctx, cancel := context.WithCancel(env.context.Request.Context())
	cancel()
	env.context.Request = env.context.Request.WithContext(ctx)
	var reads atomic.Int32
	response, err := env.adaptor.DoRequest(env.context, env.info, cancellationCountingReader{reads: &reads})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, response)
	assert.Zero(t, reads.Load())
}

// Cancellation after headers are sent must reach the in-flight upstream, not
// just prevent the next attempt. The server remains blocked until its own
// request context closes; the bounded deadline is a test failure guard only.
func TestClineCancellationInterruptsInFlightUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	release := make(chan struct{})
	var poolCalls atomic.Int32
	pool, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		poolCalls.Add(1)
		cancel()
		select {
		case <-r.Context().Done():
			close(stopped)
		case <-release:
		}
	})
	// Let the handler exit even when an implementation regression loses context.
	t.Cleanup(func() { close(release) })
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, true, "deepseek/deepseek-v4.1-flash", pool.URL+"/api/v1")
	env.context.Request = env.context.Request.WithContext(ctx)
	type result struct {
		response any
		err      error
	}
	results := make(chan result, 1)
	go func() {
		response, err := env.adaptor.DoRequest(env.context, env.info, strings.NewReader(`{"model":"deepseek/deepseek-v4.1-flash","messages":[{"role":"user","content":"hello"}],"stream":true}`))
		results <- result{response, err}
	}()
	select {
	case got := <-results:
		require.Nil(t, got.response)
		require.ErrorIs(t, got.err, context.Canceled)
		var apiErr *types.NewAPIError
		require.ErrorAs(t, got.err, &apiErr)
		assert.Equal(t, service.ClientGoneErrorCode, apiErr.GetErrorCode())
		assert.Equal(t, 499, apiErr.StatusCode)
		assert.True(t, types.IsSkipRetryError(apiErr))
		assert.False(t, types.IsRecordErrorLog(apiErr), "normal cancellation is not a channel-error row")
		assert.False(t, service.ShouldRetryRelayError(env.context, apiErr, 3))
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight upstream did not stop when its downstream cancelled")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request did not observe downstream cancellation")
	}
	assert.Equal(t, int32(1), poolCalls.Load())
}

type cancelAtReadBody struct {
	cancel context.CancelFunc
	closed bool
}

func (b *cancelAtReadBody) Read([]byte) (int, error) { b.cancel(); return 0, context.Canceled }
func (b *cancelAtReadBody) Close() error             { b.closed = true; return nil }

func TestClineCancellationDuringResponsePeekIsNot500(t *testing.T) {
	for _, stream := range []bool{false, true} {
		env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, stream, "deepseek/deepseek-v4.1-flash", "http://127.0.0.1:1/api/v1")
		ctx, cancel := context.WithCancel(env.context.Request.Context())
		env.context.Request = env.context.Request.WithContext(ctx)
		body := &cancelAtReadBody{cancel: cancel}
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}
		usage, apiErr := env.receive(t, response)
		require.Nil(t, usage)
		require.NotNil(t, apiErr)
		assert.Equal(t, 499, apiErr.StatusCode)
		assert.ErrorIs(t, apiErr, context.Canceled)
		assert.Equal(t, service.ClientGoneErrorCode, apiErr.GetErrorCode())
		assert.True(t, types.IsSkipRetryError(apiErr))
		assert.False(t, types.IsRecordErrorLog(apiErr))
		assert.True(t, body.closed)
		cancel()
	}
}
