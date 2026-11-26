package imagegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
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
	count, err := request.ImageCount(false)
	if err != nil || count > AsyncImageMaxCount {
		c.JSON(400, gin.H{"error": gin.H{"message": "Async image tasks support at most 4 images per request", "code": "async_image_count_unsupported"}})
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
	var hasSlot bool
	select {
	case h.slots <- struct{}{}:
		hasSlot = true
	default:
	}
	releaseSlot := func() {
		if hasSlot {
			<-h.slots
		}
	}
	id := c.Param("operation")
	var task *AsyncImageTask
	if id != "" {
		if _, e := operationTime(id); e != nil {
			releaseSlot()
			c.JSON(400, gin.H{"error": gin.H{"message": "Invalid image operation", "code": "async_image_invalid_operation"}})
			return
		}
		binding, e := requestBinding(tokenID, path, c.ContentType(), c.GetHeader("Content-Type"), data)
		if e != nil {
			releaseSlot()
			c.JSON(400, gin.H{"error": gin.H{"message": "Invalid image request", "code": "invalid_image_request"}})
			return
		}
		var created bool
		task, created, err = h.store.admit(c.Request.Context(), userID, count, id, binding, hasSlot)
		if err == nil && !created {
			releaseSlot()
			acceptImage(c, task, true)
			return
		}
	} else if hasSlot {
		task, err = h.store.Create(c.Request.Context(), userID, count)
	} else {
		// Preserve the legacy endpoint's wire response for existing clients.
		c.JSON(429, gin.H{"error": gin.H{"message": "Async image queue is full"}})
		return
	}
	if err != nil {
		releaseSlot()
		status, code := 503, "async_image_unavailable"
		switch {
		case errors.Is(err, ErrAsyncImageBusy):
			status, code = 429, "async_image_busy"
		case errors.Is(err, errOperationConflict):
			status, code = 409, "async_image_operation_conflict"
		case errors.Is(err, errOperationExpired):
			status, code = 410, "async_image_operation_expired"
		}
		if id == "" {
			c.JSON(status, gin.H{"error": gin.H{"message": "Async image service is busy or unavailable. Try again later.", "code": "async_image_unavailable"}})
			return
		}
		c.JSON(status, gin.H{"id": id, "error": gin.H{"message": http.StatusText(status), "code": code}})
		return
	}
	// Re-enter the normal HTTP pipeline with an independent request: token/IP
	// checks, model rate limits, channel selection, precharge and settlement run
	// unchanged. No Gin context or body storage survives its original lifetime.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), AsyncImageTimeout)
	ctx = relaycommon.WithAsyncImageCountLimit(ctx, count)
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
	acceptImage(c, &accepted, id != "")
}

// Stable across multipart encoders choosing different boundaries. Each part's
// headers and payload digest are independently framed; store only the digest,
// never the prompt, contents, token secret, or client-supplied fingerprint.
func requestBinding(tokenID int, path, mediaType, contentType string, data []byte) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "%d\x00%s\x00%s\x00", tokenID, path, mediaType)
	if mediaType == "multipart/form-data" {
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			return "", err
		}
		r := multipart.NewReader(bytes.NewReader(data), params["boundary"])
		for {
			part, err := r.NextRawPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			headers, err := json.Marshal(part.Header)
			if err != nil {
				return "", err
			}
			body := sha256.New()
			if _, err = io.Copy(body, part); err != nil {
				return "", err
			}
			fmt.Fprintf(hash, "%d:", len(headers))
			hash.Write(headers)
			hash.Write(body.Sum(nil))
		}
	} else {
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func acceptImage(c *gin.Context, task *AsyncImageTask, operation bool) {
	prefix := "/v1/images/tasks/"
	if operation {
		prefix += "operations/"
	}
	c.Header("Location", prefix+task.ID)
	c.Header("Retry-After", "2")
	c.JSON(http.StatusAccepted, task)
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
	writer := &asyncImageResponse{header: make(http.Header), status: 200, limit: task.ResultLimit}
	defer func() {
		if recover() != nil {
			writer.status = 500
			writer.body.Reset()
			_, _ = writer.body.WriteString(`{"error":{"message":"Image task interrupted","code":"async_image_interrupted"}}`)
		}
		task.Status, task.HTTPStatus = "failed", writer.status
		task.RequestID = writer.Header().Get(common.RequestIdKey)
		if writer.Header().Get("X-New-Api-Billing-State") == "review" {
			task.BillingState = "review"
		}
		// Validate the envelope without allocating another copy of every base64
		// image string. The normal relay already validates provider semantics.
		var parsed struct{}
		if writer.overflow || common.GetJsonType(writer.body.Bytes()) != "object" || common.Unmarshal(writer.body.Bytes(), &parsed) != nil {
			task.HTTPStatus = 502
			task.Result = []byte(`{"error":{"message":"Image task response is invalid or exceeds its reserved result limit; check usage before submitting again.","code":"async_image_result_invalid"}}`)
		} else {
			task.Result = writer.body.Bytes()
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

// Issue is session-authorized by the route. No API key, image content,
// operation state, cooldown or charge is involved.
func (h *AsyncImageHandler) Issue(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.GetInt("id")
	if userID <= 0 {
		c.JSON(401, gin.H{"error": gin.H{"message": "Authentication required"}})
		return
	}
	id, err := h.store.issueOperation(c.Request.Context(), userID)
	if err != nil {
		c.JSON(503, gin.H{"error": gin.H{"message": "Async image service is unavailable. Try again later.", "code": "async_image_unavailable"}})
		return
	}
	c.JSON(200, gin.H{"id": id, "kind": "async_image_operation"})
}

func (h *AsyncImageHandler) Poll(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.GetInt("id")
	if userID <= 0 {
		c.JSON(401, gin.H{"error": gin.H{"message": "Authentication required"}})
		return
	}
	if id := c.Param("operation"); id != "" {
		task, err := h.store.getOperation(c.Request.Context(), userID, id)
		if err == nil {
			c.JSON(200, task)
			return
		}
		status, code := 503, "async_image_unavailable"
		switch {
		case errors.Is(err, errOperationPending):
			c.Header("Retry-After", "2")
			c.JSON(200, gin.H{"id": id, "status": "awaiting_admission"})
			return
		case errors.Is(err, errOperationExpired):
			status, code = 410, "async_image_operation_expired"
		case errors.Is(err, errOperationMissing):
			status, code = 404, "async_image_operation_missing"
		}
		c.JSON(status, gin.H{"id": id, "error": gin.H{"message": "Image task is unavailable or expired", "code": code}})
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
	limit             int
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
	if w.overflow || w.limit <= 0 || len(data) > w.limit-w.body.Len() {
		w.overflow = true
		return 0, io.ErrShortBuffer
	}
	return w.body.Write(data)
}
func (w *asyncImageResponse) Flush() {}
