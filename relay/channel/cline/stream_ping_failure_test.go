package cline

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pipe feeds the real Cline reader. Close signals that the shared scanner
// has already recorded the provider error and started cleanup, while a pending
// ping still holds its writer lock.
type clineObservedStreamBody struct {
	reader io.Reader
	closer io.Closer
	closed chan struct{}
	once   sync.Once
}

func (b *clineObservedStreamBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

func (b *clineObservedStreamBody) Close() error {
	var err error
	b.once.Do(func() {
		err = b.closer.Close()
		close(b.closed)
	})
	return err
}

type clineBlockedPingWriter struct {
	*httptest.ResponseRecorder
	failure            string
	bodySent           chan struct{}
	bodyOnce           sync.Once
	pingStarted        chan struct{}
	releasePing        <-chan struct{}
	pingPending        bool
	failed             bool
	writesAfterFailure int
}

func (w *clineBlockedPingWriter) Write(p []byte) (int, error) {
	if w.failed {
		w.writesAfterFailure++
		return 0, io.ErrClosedPipe
	}
	if string(p) == ": PING\n\n" {
		if w.failure == "write" {
			close(w.pingStarted)
			<-w.releasePing
			w.failed = true
			return 0, io.ErrClosedPipe
		}
		w.pingPending = true
	}
	n, err := w.ResponseRecorder.Write(p)
	if strings.Contains(string(p), "observed output") {
		w.bodyOnce.Do(func() { close(w.bodySent) })
	}
	return n, err
}

func (w *clineBlockedPingWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *clineBlockedPingWriter) FlushError() error {
	if w.pingPending {
		close(w.pingStarted)
		<-w.releasePing
		w.failed = true
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}

func TestClineProviderFailureBeforePingFailureDoesNotWriteAgain(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	oldEnabled, oldSeconds := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	oldTimeout := constant.StreamingTimeout
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 1
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		settings.PingIntervalEnabled, settings.PingIntervalSeconds = oldEnabled, oldSeconds
		constant.StreamingTimeout = oldTimeout
	})

	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		for _, failure := range []string{"write", "flush"} {
			t.Run(string(format)+"/"+failure, func(t *testing.T) {
				pr, pw := io.Pipe()
				releasePing := make(chan struct{})
				var releaseOnce sync.Once
				release := func() { releaseOnce.Do(func() { close(releasePing) }) }
				w := &clineBlockedPingWriter{
					ResponseRecorder: httptest.NewRecorder(), failure: failure,
					bodySent: make(chan struct{}), pingStarted: make(chan struct{}), releasePing: releasePing,
				}
				c, _ := gin.CreateTestContext(w)
				ctx, cancel := context.WithCancel(context.Background())
				c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil).WithContext(ctx)
				info := &relaycommon.RelayInfo{
					IsStream: true, RelayFormat: format, RelayMode: relayconstant.RelayModeChatCompletions,
					OriginModelName: "public-model", StartTime: time.Now(), ShouldIncludeUsage: true,
					ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "cline-free/upstream-model"},
				}
				info.SetEstimatePromptTokens(8)
				body := &clineObservedStreamBody{reader: newClineStreamReader(pr, info), closer: pr, closed: make(chan struct{})}
				resp := &http.Response{Body: body, Header: make(http.Header)}
				type result struct {
					usage  *dto.Usage
					apiErr *types.NewAPIError
				}
				done := make(chan struct{})
				var got result
				go func() {
					defer close(done)
					got.usage, got.apiErr = openai.OaiStreamHandler(c, info, resp)
				}()
				t.Cleanup(func() {
					cancel()
					release()
					_ = pr.Close()
					_ = pw.Close()
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("Cline stream did not release its goroutines")
					}
				})
				writeUpstream := func(data string) {
					written := make(chan error, 1)
					go func() { _, err := io.WriteString(pw, data); written <- err }()
					select {
					case err := <-written:
						require.NoError(t, err)
					case <-time.After(3 * time.Second):
						t.Fatal("Cline did not read the controlled upstream event")
					}
				}
				writeUpstream(`data: {"success":true,"data":{"id":"cline-ping","choices":[{"index":0,"delta":{"role":"assistant","content":"observed output"}}]}}` + "\n\n" +
					`data: {"success":true,"data":{"id":"cline-ping","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}}}}` + "\n\n")
				select {
				case <-w.bodySent:
				case <-time.After(3 * time.Second):
					t.Fatal("the observed Cline output was not delivered")
				}
				select {
				case <-w.pingStarted:
				case <-time.After(3 * time.Second):
					t.Fatal("the ping did not enter the controlled writer")
				}

				// The ping is blocked under the shared writer lock. Cline can
				// still read and record a provider error on the scanner goroutine.
				writeUpstream(`data: {"success":false,"error":{"code":"provider_first","message":"provider diagnostic retained"}}` + "\n\n")
				select {
				case <-body.closed:
				case <-time.After(3 * time.Second):
					t.Fatal("the provider failure did not reach scanner cleanup")
				}
				firstAPIError := info.StreamStatus.APIError()
				require.NotNil(t, firstAPIError)
				assert.Contains(t, firstAPIError.Error(), "provider diagnostic retained")
				assert.Equal(t, types.ErrorCode("provider_first"), firstAPIError.GetErrorCode())
				assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
				release()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("the failed ping did not release the Chat owner")
				}

				assert.True(t, w.failed, "the ping must actually fail, even though the provider error was recorded first")
				assert.Zero(t, w.writesAfterFailure, "neither a buffered usage frame nor an error frame may be written after the ping fails")
				require.NotNil(t, got.apiErr)
				assert.True(t, types.IsSkipRetryError(got.apiErr))
				assert.Same(t, firstAPIError, info.StreamStatus.APIError(), "the original provider diagnostic must remain available")
				assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason, "the first transport failure must remain available")
				require.NotNil(t, got.usage)
				assert.Equal(t, 11, got.usage.PromptTokens)
				assert.Equal(t, 4, got.usage.CompletionTokens)
				assert.Equal(t, 15, got.usage.TotalTokens)
				assert.Equal(t, 3, got.usage.PromptTokensDetails.CachedTokens)
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled))
				assert.NoError(t, ctx.Err(), "the test must stop on real failures without client cancellation")
				assert.Contains(t, w.Body.String(), "observed output")
				assert.NotContains(t, w.Body.String(), "provider diagnostic retained")
				assert.NotContains(t, w.Body.String(), "[DONE]")
				assert.NotContains(t, w.Body.String(), "message_stop")
				assert.NotContains(t, w.Body.String(), `"finishReason":"STOP"`)
			})
		}
	}
}
