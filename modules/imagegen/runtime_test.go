package imagegen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageRuntimeDrainWaitsForResult(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "graceful"
		if force {
			name = "forced"
		}
		t.Run(name, func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			workers := lifecycle.NewGroup()
			started, release := make(chan context.Context, 1), make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() {
				workers.ForceCancel()
				releaseOnce.Do(func() { close(release) })
				workers.BeginDrain()
				wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				require.NoError(t, workers.Wait(wait))
			})
			handler := newAsyncImageHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started <- r.Context()
				<-release
				if r.Context().Err() != nil {
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte(`{"error":{"code":"cancelled"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":[{"url":"https://example.com/image.png"}]}`))
			}), client, workers)
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest("POST", "/v1/images/tasks/generations", strings.NewReader(`{"model":"image","prompt":"test"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("id", 1)
			common.SetContextKey(c, constant.ContextKeyTokenId, 2)
			defer common.CleanupBodyStorage(c)
			handler.Submit(c)
			require.Equal(t, http.StatusAccepted, response.Code)
			var accepted AsyncImageTask
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &accepted))
			var requestCtx context.Context
			select {
			case requestCtx = <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("accepted task did not start")
			}
			workers.BeginDrain()
			assert.NoError(t, requestCtx.Err(), "graceful drain must preserve accepted generation")
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, workers.Wait(cancelled), context.Canceled, "202 is not completion of the accepted task")
			if force {
				workers.ForceCancel()
				select {
				case <-requestCtx.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("forced shutdown did not cancel the upstream request")
				}
			}
			// The fake upstream exits only after the cancellation assertions.
			releaseOnce.Do(func() { close(release) })
			wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			require.NoError(t, workers.Wait(wait))
			result, err := handler.store.Get(context.Background(), 1, accepted.ID)
			require.NoError(t, err)
			if force {
				assert.Equal(t, "failed", result.Status)
				assert.Equal(t, http.StatusBadGateway, result.HTTPStatus)
			} else {
				assert.Equal(t, "succeeded", result.Status)
				assert.Contains(t, string(result.Result), "image.png")
			}
			assert.False(t, server.Exists("image-async:{tasks}:active:1"), "drain must include result persistence and slot release")
		})
	}
}

func TestAsyncImageDrainRejectsAdmissionWithoutLeavingActiveSlot(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	workers := lifecycle.NewGroup()
	workers.BeginDrain()
	handler := newAsyncImageHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("rejected task must never reach upstream or billing")
	}), client, workers)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("POST", "/v1/images/tasks/generations", strings.NewReader(`{"model":"image","prompt":"test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 1)
	common.SetContextKey(c, constant.ContextKeyTokenId, 2)
	defer common.CleanupBodyStorage(c)
	handler.Submit(c)
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Contains(t, response.Body.String(), "async_image_draining")
	assert.Empty(t, response.Header().Get("Location"))
	assert.False(t, server.Exists("image-async:{tasks}:active:1"))
	_, err := handler.store.Create(context.Background(), 1)
	require.NoError(t, err, "the user must be able to retry on a healthy instance")
}

// The Redis boundary is held after the upstream has returned. Shutdown must
// still wait: neither an HTTP 202 nor generation completion proves persistence.
type asyncImageFinishBarrier struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *asyncImageFinishBarrier) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *asyncImageFinishBarrier) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *asyncImageFinishBarrier) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		if cmd.Name() == "evalsha" && len(args) > 1 && args[1] == finishAsyncImage.Hash() {
			h.once.Do(func() { close(h.started) })
			select {
			case <-h.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return next(ctx, cmd)
	}
}

func TestAsyncImageRuntimeWaitIncludesResultPersistence(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), Protocol: 2})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	workers := lifecycle.NewGroup()
	barrier := &asyncImageFinishBarrier{started: make(chan struct{}), release: make(chan struct{})}
	client.AddHook(barrier)
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(barrier.release) })
		workers.BeginDrain()
		wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, workers.Wait(wait))
	})
	handler := newAsyncImageHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"url":"https://example.com/persisted.png"}]}`))
	}), client, workers)
	engine := gin.New()
	engine.POST("/v1/images/tasks/generations", func(c *gin.Context) {
		c.Set("id", 1)
		common.SetContextKey(c, constant.ContextKeyTokenId, 2)
		defer common.CleanupBodyStorage(c)
		handler.Submit(c)
	})
	gate := lifecycle.NewGate()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/images/tasks/generations", strings.NewReader(`{"model":"image","prompt":"test"}`))
	request.Header.Set("Content-Type", "application/json")
	gate.Wrap(engine).ServeHTTP(response, request)
	require.Equal(t, http.StatusAccepted, response.Code)
	var accepted AsyncImageTask
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &accepted))
	select {
	case <-barrier.started:
	case <-time.After(5 * time.Second):
		t.Fatal("result storage was not reached")
	}
	gate.BeginDrain()
	workers.BeginDrain()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, gate.Wait(cancelled), "the original HTTP handler has already returned")
	require.ErrorIs(t, workers.Wait(cancelled), context.Canceled, "the result write still owns shared resources")
	pending, err := handler.store.Get(context.Background(), 1, accepted.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", pending.Status)
	releaseOnce.Do(func() { close(barrier.release) })
	wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	require.NoError(t, workers.Wait(wait))
	completed, err := handler.store.Get(context.Background(), 1, accepted.ID)
	require.NoError(t, err)
	assert.Equal(t, "succeeded", completed.Status)
	assert.Contains(t, string(completed.Result), "persisted.png")
	assert.False(t, server.Exists("image-async:{tasks}:active:1"))
}
