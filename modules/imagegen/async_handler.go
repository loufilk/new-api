package imagegen

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type AsyncImageHandler struct {
	relay   http.Handler
	store   AsyncImageStore
	slots   chan struct{}
	workers *lifecycle.Group
}

func NewAsyncImageHandler(relay http.Handler, workers *lifecycle.Group) *AsyncImageHandler {
	if !common.RedisEnabled || common.RDB == nil {
		return newAsyncImageHandler(relay, nil, workers)
	}
	return newAsyncImageHandler(relay, common.RDB, workers)
}

func newAsyncImageHandler(relay http.Handler, client redis.UniversalClient, workers *lifecycle.Group) *AsyncImageHandler {
	return &AsyncImageHandler{relay: relay, store: AsyncImageStore{Client: client}, slots: make(chan struct{}, 8), workers: workers}
}

func (h *AsyncImageHandler) Submit(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID, tokenID := c.GetInt("id"), common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	if userID <= 0 || tokenID <= 0 {
		c.JSON(401, gin.H{"error": gin.H{"message": "Authentication required"}})
		return
	}
	mode, path := relayconstant.RelayModeImagesGenerations, "/v1/images/generations"
	if strings.HasSuffix(c.Request.URL.Path, "/edits") {
		mode, path = relayconstant.RelayModeImagesEdits, "/v1/images/edits"
	}
	request, err := helper.GetAndValidOpenAIImageRequest(c, mode)
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{"message": "Invalid image request", "code": "invalid_image_request"}})
		return
	}
	if request.Stream != nil && *request.Stream {
		c.JSON(400, gin.H{"error": gin.H{"message": "Streaming is not supported for asynchronous image tasks"}})
		return
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{"message": "Failed to read image request"}})
		return
	}
	if storage.Size() > 24<<20 {
		c.JSON(413, gin.H{"error": gin.H{"message": "Async image request exceeds 24 MiB"}})
		return
	}
	data, err := storage.Bytes()
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{"message": "Failed to read image request"}})
		return
	}
	select {
	case h.slots <- struct{}{}:
	default:
		c.JSON(429, gin.H{"error": gin.H{"message": "Async image queue is full"}})
		return
	}
	task, err := h.store.Create(c.Request.Context(), userID)
	if err != nil {
		<-h.slots
		status := 503
		if errors.Is(err, ErrAsyncImageBusy) {
			status = 429
		}
		c.JSON(status, gin.H{"error": gin.H{"message": "Async image service is busy or unavailable. Try again later.", "code": "async_image_unavailable"}})
		return
	}
	// Re-enter the normal HTTP pipeline with an independent request: token/IP
	// checks, model rate limits, channel selection, precharge and settlement run
	// unchanged. No Gin context or body storage survives its original lifetime.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), AsyncImageTimeout)
	upstream := c.Request.Clone(ctx)
	upstream.URL.Path, upstream.URL.RawPath, upstream.URL.RawQuery = path, "", ""
	upstream.RequestURI = path
	upstream.Body = io.NopCloser(bytes.NewReader(bytes.Clone(data)))
	upstream.GetBody = nil
	upstream.ContentLength = int64(len(data))
	upstream.Header.Del("Content-Encoding")
	upstream.Header.Del("Accept-Encoding")
	upstream.Header.Del("Content-Length")
	upstream.TransferEncoding = nil
	upstream.Form, upstream.PostForm, upstream.MultipartForm = nil, nil, nil
	accepted := *task
	// Admission is counted before returning 202. Graceful drain leaves the
	// accepted request alive; only the runtime's forced deadline cancels it.
	if !h.workers.Go("async-image", func(_, work context.Context) {
		stop := context.AfterFunc(work, cancel)
		defer stop()
		h.run(upstream, cancel, userID, task)
	}) {
		cancel()
		_ = upstream.Body.Close()
		<-h.slots
		// Draining may race Redis admission. Release the user's active slot
		// without ever invoking the relay or charging for a rejected request.
		task.Status, task.HTTPStatus = "failed", http.StatusServiceUnavailable
		task.Result = []byte(`{"error":{"message":"Server is shutting down","code":"async_image_draining"}}`)
		if err := h.store.Finish(userID, task); err != nil {
			common.SysError("Failed to release rejected async image task: " + task.ID)
		}
		c.Header("Retry-After", "1")
		c.Data(http.StatusServiceUnavailable, "application/json", task.Result)
		return
	}
	c.Header("Location", "/v1/images/tasks/"+task.ID)
	c.Header("Retry-After", "2")
	c.JSON(http.StatusAccepted, accepted)
}

func (h *AsyncImageHandler) run(request *http.Request, cancel context.CancelFunc, userID int, task *AsyncImageTask) {
	defer func() {
		<-h.slots
		cancel()
		_ = request.Body.Close()
		if request.MultipartForm != nil {
			_ = request.MultipartForm.RemoveAll()
		}
	}()
	writer := &asyncImageResponse{header: make(http.Header), status: 200}
	defer func() {
		if recover() != nil {
			writer.status = 500
			writer.body.Reset()
			_, _ = writer.body.WriteString(`{"error":{"message":"Image task interrupted","code":"async_image_interrupted"}}`)
		}
		task.Status, task.HTTPStatus = "failed", writer.status
		task.RequestID = writer.Header().Get(common.RequestIdKey)
		var parsed map[string]any
		if writer.overflow || common.Unmarshal(writer.body.Bytes(), &parsed) != nil {
			task.HTTPStatus = 502
			task.Result = []byte(`{"error":{"message":"Image task response is invalid or exceeds 8 MiB; check usage before submitting again.","code":"async_image_result_invalid"}}`)
		} else {
			task.Result = bytes.Clone(writer.body.Bytes())
			if writer.status >= 200 && writer.status < 300 {
				task.Status = "succeeded"
			}
		}
		if err := h.store.Finish(userID, task); err != nil {
			common.SysError("Failed to store async image task result: " + task.ID)
		}
	}()
	h.relay.ServeHTTP(writer, request)
}

func (h *AsyncImageHandler) Poll(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.GetInt("id")
	if userID <= 0 {
		c.JSON(401, gin.H{"error": gin.H{"message": "Authentication required"}})
		return
	}
	task, err := h.store.Get(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		status := 503
		if errors.Is(err, redis.Nil) {
			status = 404
		}
		c.JSON(status, gin.H{"error": gin.H{"message": "Image task is unavailable or expired", "code": "async_image_unavailable"}})
		return
	}
	c.JSON(200, task)
}

type asyncImageResponse struct {
	header            http.Header
	body              bytes.Buffer
	status            int
	written, overflow bool
}

func (w *asyncImageResponse) Header() http.Header { return w.header }
func (w *asyncImageResponse) WriteHeader(status int) {
	if !w.written {
		w.status = status
		w.written = true
	}
}
func (w *asyncImageResponse) Write(data []byte) (int, error) {
	w.written = true
	if w.body.Len()+len(data) > AsyncImageMaxResult {
		w.overflow = true
		return 0, io.ErrShortBuffer
	}
	return w.body.Write(data)
}
func (w *asyncImageResponse) Flush() {}
