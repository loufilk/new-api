package imagegen

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Exercise the actual handler, atomic admission, result commit and owner-only
// read path. The sole upstream and billing counter are synthetic.
func TestImageOperationLostACKConcurrentReplayAndExpiry(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	srv := miniredis.NewMiniRedis()
	t.Cleanup(srv.Close)
	srv.SetTime(now)
	client := redis.NewClient(&redis.Options{Dialer: func(context.Context, string, string) (net.Conn, error) { return srv.Dial() }})
	t.Cleanup(func() { _ = client.Close() })
	var calls, mockBills atomic.Int32
	release := make(chan struct{})
	workers := lifecycle.NewGroup()
	h := newAsyncImageHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		<-release
		mockBills.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"url":"https://example.com/result"}]}`))
	}), client, workers)
	id, err := h.store.issueOperation(context.Background(), 1)
	require.NoError(t, err)
	submit := func(owner, key int, prompt string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/v1/images/tasks/operations/"+id+"/generations", strings.NewReader(fmt.Sprintf(`{"model":"image","prompt":%q,"n":1}`, prompt)))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "operation", Value: id}}
		c.Set("id", owner)
		common.SetContextKey(c, constant.ContextKeyTokenId, key)
		defer common.CleanupBodyStorage(c)
		h.Submit(c)
		return w
	}
	poll := func(owner int) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/v1/images/tasks/operations/"+id, nil)
		c.Params = gin.Params{{Key: "operation", Value: id}}
		c.Set("id", owner)
		h.Poll(c)
		return w
	}
	require.Contains(t, poll(1).Body.String(), "awaiting_admission")
	// A proxy or multi-instance caller may duplicate an identical consent. Some
	// callers can miss every 202. Polling needs only the previously known ID.
	const n = 12
	var wg sync.WaitGroup
	codes := make(chan int, n)
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- submit(1, 2, "fixture").Code }()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		require.Equal(t, 202, code)
	}
	require.Contains(t, poll(1).Body.String(), `"status":"running"`)
	require.Equal(t, 404, poll(3).Code)
	require.Contains(t, submit(1, 2, "different").Body.String(), "async_image_operation_conflict")
	require.Equal(t, 409, submit(1, 4, "fixture").Code)
	require.Equal(t, asyncOperationTTL, srv.TTL(receiptKey(1, id)))
	close(release)
	workers.BeginDrain()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, workers.Wait(ctx))
	require.Contains(t, poll(1).Body.String(), "https://example.com/result")
	require.Equal(t, 202, submit(1, 2, "fixture").Code)
	require.EqualValues(t, 1, calls.Load())
	require.EqualValues(t, 1, mockBills.Load())
	srv.FastForward(AsyncImageTTL + time.Second)
	srv.SetTime(now.Add(AsyncImageTTL + time.Second))
	expired := poll(1)
	require.Equal(t, 410, expired.Code)
	require.Contains(t, expired.Body.String(), id)
	require.Equal(t, 410, submit(1, 2, "fixture").Code)
	require.Equal(t, 404, poll(3).Code, "other owner cannot see the receipt")
	srv.FastForward(asyncOperationTTL)
	srv.SetTime(now.Add(asyncOperationTTL + AsyncImageTTL + time.Second))
	require.False(t, srv.Exists(receiptKey(1, id)))
	require.Equal(t, 404, poll(1).Code)
	require.Equal(t, 410, submit(1, 2, "fixture").Code, "old consent is not reusable after tombstone expiry")
	require.EqualValues(t, 1, calls.Load())
}

func TestImageOperationBindsMultipartPayloadNotBoundary(t *testing.T) {
	encode := func(boundary, content string) (string, []byte) {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		require.NoError(t, w.SetBoundary(boundary))
		require.NoError(t, w.WriteField("prompt", "draw"))
		f, e := w.CreateFormFile("image[]", "fixture.png")
		require.NoError(t, e)
		_, e = f.Write([]byte(content))
		require.NoError(t, e)
		require.NoError(t, w.Close())
		return w.FormDataContentType(), b.Bytes()
	}
	digest := func(boundary, content string) string {
		ct, data := encode(boundary, content)
		v, e := requestBinding(1, "/v1/images/edits", "multipart/form-data", ct, data)
		require.NoError(t, e)
		return v
	}
	require.Equal(t, digest("one", "abc"), digest("two", "abc"))
	require.NotEqual(t, digest("one", "abc"), digest("one", "xyz"))
}
