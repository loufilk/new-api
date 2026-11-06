package xunfei

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func xunfeiTestContext(ctx context.Context, modelTest bool) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, modelTest)
	if modelTest {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyOriginalModel, "lite")
	}
	return c
}

func waitXunfeiResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("WebSocket reader did not stop")
		return nil
	}
}

func TestXunfeiCancelledBeforeDialDoesNotSend(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := xunfeiTestContext(ctx, false)
	_, _, _, err := xunfeiMakeRequest(c, dto.GeneralOpenAIRequest{}, "lite", "ws"+strings.TrimPrefix(server.URL, "http"), "test-app")
	require.Error(t, err)
	assert.Zero(t, calls.Load())
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
}

func TestXunfeiCancelInterruptsHandshake(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, cleanup, err := xunfeiMakeRequest(xunfeiTestContext(ctx, false), dto.GeneralOpenAIRequest{}, "lite", "ws"+strings.TrimPrefix(server.URL, "http"), "test-app")
		if cleanup != nil {
			cleanup()
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handshake did not start")
	}
	cancel()
	require.Error(t, waitXunfeiResult(t, result))
}

func TestXunfeiCancelStopsReadAndBlockedDelivery(t *testing.T) {
	for _, sendFrame := range []bool{false, true} {
		t.Run(map[bool]string{false: "socket-read", true: "consumer-stopped"}[sendFrame], func(t *testing.T) {
			closed := make(chan error, 1)
			ready := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					closed <- err
					return
				}
				defer conn.Close()
				if _, _, err = conn.ReadMessage(); err != nil {
					closed <- err
					return
				}
				if sendFrame {
					err = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0},"payload":{"choices":{"status":0,"text":[{"content":"partial"}]}}}`))
					if err != nil {
						closed <- err
						return
					}
				}
				close(ready)
				_, _, err = conn.ReadMessage()
				closed <- err
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := xunfeiTestContext(ctx, false)
			_, done, cleanup, err := xunfeiMakeRequest(c, dto.GeneralOpenAIRequest{}, "lite", "ws"+strings.TrimPrefix(server.URL, "http"), "test-app")
			require.NoError(t, err)
			defer cleanup()
			select {
			case <-ready:
			case <-time.After(2 * time.Second):
				t.Fatal("request not received")
			}
			cancel()
			require.ErrorIs(t, waitXunfeiResult(t, done), context.Canceled)
			require.Error(t, waitXunfeiResult(t, closed), "cancellation must close the upstream socket")
			assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
		})
	}
}

func TestXunfeiTransportDoesNotReplayOrInventSuccess(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		ok    bool
	}{
		{"completed", `{"header":{"code":0},"payload":{"choices":{"status":2,"text":[{"content":"answer"}]},"usage":{"text":{"prompt_tokens":7,"completion_tokens":4,"total_tokens":11}}}}`, true},
		{"malformed", `{broken`, false},
		{"provider-error", `{"header":{"code":10013,"message":"private upstream detail"}}`, false},
		{"closed-before-final", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err = conn.ReadMessage(); err != nil {
					return
				}
				calls.Add(1)
				if tc.frame != "" {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(tc.frame))
				}
			}))
			defer server.Close()
			data, done, cancel, err := xunfeiMakeRequest(xunfeiTestContext(context.Background(), false), dto.GeneralOpenAIRequest{}, "lite", "ws"+strings.TrimPrefix(server.URL, "http"), "test-app")
			require.NoError(t, err)
			defer cancel()
			if tc.ok {
				select {
				case frame := <-data:
					assert.Equal(t, 11, frame.Payload.Usage.Text.TotalTokens)
				case <-time.After(2 * time.Second):
					t.Fatal("final response not delivered")
				}
				require.NoError(t, waitXunfeiResult(t, done))
			} else {
				err := waitXunfeiResult(t, done)
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private upstream detail")
			}
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func setupXunfeiPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires owned PostgreSQL 18.6 TEST_POSTGRES_DSN")
	}
	require.Equal(t, "1", os.Getenv("NO_AGENT_FIXTURE_OWNED"))
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	var version string
	require.NoError(t, admin.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	schema := "xunfei_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.ModelFingerprintSiteConfig{}))
	require.NoError(t, db.Create(&model.Option{Key: model.ModelTestEnabledKey, Value: "true"}).Error)
	targets, err := common.Marshal([]model.ModelFingerprintSiteTarget{{Group: "default", Model: "lite", Enabled: true}})
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.ModelFingerprintSiteConfig{ID: 1, TargetsJSON: string(targets)}).Error)
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		conn, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		conn, err = admin.DB()
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})
	require.NoError(t, model.RequireModelTestTarget(t.Context(), "default", "lite"))
	return db
}

func TestXunfeiPostgresDisableDuringHandshakePreventsGeneration(t *testing.T) {
	db := setupXunfeiPostgres(t)
	var calls atomic.Int32
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error; err != nil {
			result <- err
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err == nil {
			calls.Add(1)
			result <- errors.New("disabled test sent a generation request")
			return
		}
		result <- nil
	}))
	defer server.Close()
	c := xunfeiTestContext(context.Background(), true)
	_, _, cleanup, err := xunfeiMakeRequest(c, dto.GeneralOpenAIRequest{}, "lite", "ws"+strings.TrimPrefix(server.URL, "http"), "test-app")
	if cleanup != nil {
		cleanup()
	}
	require.Error(t, err)
	require.NoError(t, waitXunfeiResult(t, result))
	assert.Zero(t, calls.Load())
	assert.Equal(t, "model_tests_disabled", c.GetString("model_test_error_code"))
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
}

func TestXunfeiPostgresUnboundedTokensKeepResponseMemoryBound(t *testing.T) {
	setupXunfeiPostgres(t)
	requestBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, body, err := conn.ReadMessage()
		if err != nil {
			return
		}
		requestBody <- body
		frame := `{"header":{"code":0},"payload":{"choices":{"status":0,"text":[{"content":"` + strings.Repeat("a", 300<<10) + `"}]}}}`
		for range 2 {
			if conn.WriteMessage(websocket.TextMessage, []byte(frame)) != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	c := xunfeiTestContext(context.Background(), true)
	data, done, cancel, err := xunfeiMakeRequest(c, dto.GeneralOpenAIRequest{}, "lite", "ws"+strings.TrimPrefix(server.URL, "http"), "test-app")
	require.NoError(t, err)
	defer cancel()
	select {
	case body := <-requestBody:
		assert.NotContains(t, string(body), "max_tokens")
	case <-time.After(2 * time.Second):
		t.Fatal("request not received")
	}
	select {
	case frame := <-data:
		assert.Len(t, frame.Payload.Choices.Text[0].Content, 300<<10)
	case <-time.After(2 * time.Second):
		t.Fatal("first frame not received")
	}
	require.ErrorContains(t, waitXunfeiResult(t, done), "response exceeds limit")
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
}
