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
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageAdmissionOwnershipAndCompletion(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	worker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/images/generations", r.URL.Path)
		assert.Equal(t, "Bearer fixture", r.Header.Get("Authorization"))
		close(started)
		<-release
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.JSONEq(t, `{"model":"image","prompt":"fixture","n":1}`, string(body))
		assert.NoError(t, r.Context().Err(), "client disconnect must not cancel accepted generation")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aGVsbG8="}]}`))
		close(done)
	})
	handler := newAsyncImageHandler(worker, client, lifecycle.NewGroup())
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	submit := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(submit)
	c.Request = httptest.NewRequest("POST", "/v1/images/tasks/generations", strings.NewReader(`{"model":"image","prompt":"fixture","n":1}`)).WithContext(requestCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Authorization", "Bearer fixture")
	c.Set("id", 1)
	common.SetContextKey(c, constant.ContextKeyTokenId, 2)
	handler.Submit(c)
	require.Equal(t, http.StatusAccepted, submit.Code)
	var accepted struct {
		ID string `json:"id"`
	}
	require.NoError(t, common.Unmarshal(submit.Body.Bytes(), &accepted))
	require.NotEmpty(t, accepted.ID)
	cancel()
	common.CleanupBodyStorage(c)
	<-started
	for _, userID := range []int{3, 1} {
		poll := httptest.NewRecorder()
		pc, _ := gin.CreateTestContext(poll)
		pc.Request = httptest.NewRequest("GET", "/v1/images/tasks/"+accepted.ID, nil)
		pc.Params = gin.Params{{Key: "id", Value: accepted.ID}}
		pc.Set("id", userID)
		handler.Poll(pc)
		if userID == 3 {
			assert.Equal(t, 404, poll.Code)
		} else {
			assert.Equal(t, 200, poll.Code)
			assert.Contains(t, poll.Body.String(), `"status":"running"`)
		}
		assert.Equal(t, "no-store", poll.Header().Get("Cache-Control"))
	}
	close(release)
	<-done
	handler.workers.BeginDrain()
	wait, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	require.NoError(t, handler.workers.Wait(wait))
	poll := httptest.NewRecorder()
	pc, _ := gin.CreateTestContext(poll)
	pc.Request = httptest.NewRequest("GET", "/", nil)
	pc.Params = gin.Params{{Key: "id", Value: accepted.ID}}
	pc.Set("id", 1)
	common.SetContextKey(pc, constant.ContextKeyTokenId, 2)
	handler.Poll(pc)
	assert.Contains(t, poll.Body.String(), `"status":"succeeded"`)
	assert.Contains(t, poll.Body.String(), `"b64_json":"aGVsbG8="`)
}

func TestAsyncImageRejectsInvalidRequestAndMissingAuthentication(t *testing.T) {
	handler := newAsyncImageHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid request reached relay") }), nil, lifecycle.NewGroup())
	for _, tc := range []struct {
		name, body        string
		user, token, want int
	}{
		{"missing identity", `{"model":"image","prompt":"test"}`, 0, 0, 401},
		{"negative count", `{"model":"image","prompt":"test","n":-1}`, 1, 2, 400},
		{"streaming", `{"model":"image","prompt":"test","stream":true}`, 1, 2, 400},
		{"storage unavailable", `{"model":"image","prompt":"test"}`, 1, 2, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest("POST", "/v1/images/tasks/generations", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("id", tc.user)
			common.SetContextKey(c, constant.ContextKeyTokenId, tc.token)
			defer common.CleanupBodyStorage(c)
			handler.Submit(c)
			assert.Equal(t, tc.want, response.Code)
		})
	}
}

func TestAsyncImageWorkerPersistsFailureAndEnforcesResultLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantCode string
	}{
		{"upstream quota", 429, `{"error":{"message":"quota exhausted","code":"quota"}}`, "quota"},
		{"oversized result", 200, strings.Repeat("x", AsyncImageMaxResult+1), "async_image_result_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			h := newAsyncImageHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}), client, lifecycle.NewGroup())
			job, err := h.store.Create(context.Background(), 1)
			require.NoError(t, err)
			h.slots <- struct{}{}
			request := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader("{}"))
			h.run(request, func() {}, 1, job)
			read, err := h.store.Get(context.Background(), 1, job.ID)
			require.NoError(t, err)
			assert.Equal(t, "failed", read.Status)
			assert.Contains(t, string(read.Result), tc.wantCode)
		})
	}
}
