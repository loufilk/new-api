package imagegen

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the synchronous image relay and the asynchronous task
// worker through the real xAI converter and HTTP transport. Only Redis and the
// upstream provider are local fixtures; no paid provider request is made. The
// provider returns a deliberate error after capture, before quota settlement.
func TestXAIImageRelayPreservesRequestThroughSyncAndAsync(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalPassthrough := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = false
	t.Cleanup(func() { settings.PassThroughRequestEnabled = originalPassthrough })
	for _, tc := range []struct {
		name, operation, body string
	}{
		{
			name: "generation with quality", operation: "generations",
			body: `{"model":"grok-imagine-image-2.0","prompt":"A lake","n":2,"aspect_ratio":"16:9","resolution":"1k","quality":"low","response_format":"b64_json"}`,
		},
		{
			name: "single reference edit", operation: "edits",
			body: `{"model":"grok-imagine-image","prompt":"Add a hat","n":1,"image":{"url":"https://example.com/cat.png","type":"image_url"},"response_format":"url"}`,
		},
		{
			name: "ordered multi reference edit", operation: "edits",
			body: `{"model":"grok-imagine-image-2.0","prompt":"Combine these subjects","n":2,"images":[{"url":"data:image/png;base64,aGVsbG8="},{"url":"data:image/jpeg;base64,/9j/2Q=="}],"aspect_ratio":"3:2","resolution":"1k","quality":"medium","response_format":"b64_json"}`,
		},
	} {
		for _, async := range []bool{false, true} {
			modeName := "sync"
			if async {
				modeName = "async"
			}
			t.Run(tc.name+"/"+modeName, func(t *testing.T) {
				type capturedRequest struct {
					path, contentType string
					body              []byte
				}
				captured := make(chan capturedRequest, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if !assert.NoError(t, err) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					captured <- capturedRequest{r.URL.Path, r.Header.Get("Content-Type"), body}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnprocessableEntity)
					_, _ = io.WriteString(w, `{"error":{"type":"fixture_request_captured","message":"Request captured without billing"}}`)
				}))
				t.Cleanup(upstream.Close)

				path := "/v1/images/" + tc.operation
				engine := gin.New()
				engine.POST(path, func(c *gin.Context) {
					defer common.CleanupBodyStorage(c)
					mode := relayconstant.RelayModeImagesGenerations
					if tc.operation == "edits" {
						mode = relayconstant.RelayModeImagesEdits
					}
					request, err := helper.GetAndValidOpenAIImageRequest(c, mode)
					if !assert.NoError(t, err) {
						c.Status(http.StatusBadRequest)
						return
					}
					common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeXai)
					common.SetContextKey(c, constant.ContextKeyChannelId, 20)
					common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
					common.SetContextKey(c, constant.ContextKeyOriginalModel, request.Model)
					info := &relaycommon.RelayInfo{
						Request: request, OriginModelName: request.Model, RequestURLPath: path,
						RelayMode: mode, RelayFormat: types.RelayFormatOpenAIImage,
					}
					apiErr := relay.ImageHelper(c, info)
					if !assert.NotNil(t, apiErr) {
						return
					}
					assert.Equal(t, http.StatusUnprocessableEntity, apiErr.StatusCode, "%v", apiErr)
					c.JSON(apiErr.StatusCode, gin.H{"error": gin.H{"message": apiErr.Error()}})
				})

				response := httptest.NewRecorder()
				if !async {
					request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
					request.Header.Set("Content-Type", "application/json")
					engine.ServeHTTP(response, request)
					require.Equal(t, http.StatusUnprocessableEntity, response.Code)
				} else {
					redisServer := miniredis.RunT(t)
					client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
					t.Cleanup(func() { require.NoError(t, client.Close()) })
					workers := lifecycle.NewGroup()
					t.Cleanup(func() {
						workers.ForceCancel()
						workers.BeginDrain()
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						require.NoError(t, workers.Wait(ctx))
					})
					handler := newAsyncImageHandler(engine, client, workers)
					c, _ := gin.CreateTestContext(response)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/tasks/"+tc.operation, strings.NewReader(tc.body))
					c.Request.Header.Set("Content-Type", "application/json")
					c.Set("id", 1)
					common.SetContextKey(c, constant.ContextKeyTokenId, 2)
					handler.Submit(c)
					common.CleanupBodyStorage(c)
					require.Equal(t, http.StatusAccepted, response.Code)
					var accepted AsyncImageTask
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &accepted))
					workers.BeginDrain()
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					require.NoError(t, workers.Wait(ctx))
					result, err := handler.store.Get(context.Background(), 1, accepted.ID)
					require.NoError(t, err)
					assert.Equal(t, "failed", result.Status)
					assert.Equal(t, http.StatusUnprocessableEntity, result.HTTPStatus)
					assert.Contains(t, string(result.Result), "Request captured without billing")
				}

				require.Len(t, captured, 1)
				actual := <-captured
				assert.Equal(t, path, actual.path)
				assert.Equal(t, "application/json", actual.contentType)
				assert.JSONEq(t, tc.body, string(actual.body))
			})
		}
	}
}
