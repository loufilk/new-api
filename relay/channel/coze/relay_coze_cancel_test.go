package coze

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cozeTestContext(ctx context.Context, modelTest bool) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, modelTest)
	c.Set("coze_conversation_id", "conversation")
	c.Set("coze_chat_id", "chat")
	return c
}

func cozeTestInfo(baseURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{OriginModelName: "coze-test", ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: baseURL, ApiKey: "synthetic-only"}}
}

func awaitCozeError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Coze request did not stop on cancellation")
		return nil
	}
}

func TestCozeDefaultUserIdentifierIsValidJSON(t *testing.T) {
	request := convertCozeChatRequest(cozeTestContext(context.Background(), false), dto.GeneralOpenAIRequest{})
	body, err := common.Marshal(request)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"user_id":"chatcmpl-"`)
}

func TestCozePollingCancellationDoesNotReplayGeneration(t *testing.T) {
	for _, stall := range []bool{false, true} {
		t.Run(map[bool]string{false: "poll-interval", true: "retrieve-read"}[stall], func(t *testing.T) {
			service.InitHttpClient()
			var generations atomic.Int32
			polled := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					generations.Add(1)
					fmt.Fprint(w, `{"code":0,"data":{"id":"chat","conversation_id":"conversation"}}`)
					return
				}
				select {
				case polled <- struct{}{}:
				default:
				}
				if stall {
					<-r.Context().Done()
					return
				}
				fmt.Fprint(w, `{"code":0,"data":{"status":"in_progress"}}`)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := cozeTestContext(ctx, false)
			result := make(chan error, 1)
			go func() {
				_, err := (&Adaptor{}).DoRequest(c, cozeTestInfo(server.URL), strings.NewReader(`{}`))
				result <- err
			}()
			select {
			case <-polled:
			case <-time.After(2 * time.Second):
				t.Fatal("poll did not start")
			}
			cancel()
			require.ErrorIs(t, awaitCozeError(t, result), context.Canceled)
			assert.EqualValues(t, 1, generations.Load())
			assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
		})
	}
}

func TestCozeDetailReadHonorsCancellation(t *testing.T) {
	service.InitHttpClient()
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := getChatDetail(&Adaptor{}, cozeTestContext(ctx, true), cozeTestInfo(server.URL))
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("detail request did not start")
	}
	cancel()
	require.ErrorIs(t, awaitCozeError(t, result), context.Canceled)
}

func TestCozeFingerprintPollsPreserveDispatchAndBounds(t *testing.T) {
	for _, mode := range []string{"redirect", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			service.InitHttpClient()
			var followed atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/trap" {
					followed.Add(1)
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, "/trap", http.StatusFound)
					return
				}
				fmt.Fprint(w, strings.Repeat("a", (512<<10)+1))
			}))
			defer server.Close()
			c := cozeTestContext(context.Background(), true)
			common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, true)
			common.SetContextKey(c, constant.ContextKeyUpstreamRequestUnsent, false)
			err, completed := checkIfChatComplete(&Adaptor{}, c, cozeTestInfo(server.URL))
			require.Error(t, err)
			assert.False(t, completed)
			response, err := getChatDetail(&Adaptor{}, c, cozeTestInfo(server.URL))
			if mode == "redirect" {
				require.NoError(t, err)
				assert.Equal(t, http.StatusFound, response.StatusCode)
				require.NoError(t, response.Body.Close())
			} else {
				require.Error(t, err)
			}
			assert.Zero(t, followed.Load())
			assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
		})
	}
}

func TestCozeCompletedPollCarriesOnlyRealUsage(t *testing.T) {
	service.InitHttpClient()
	for _, counters := range []string{
		`{"input_count":7,"output_count":4,"token_count":11}`,
		`{"input_count":7,"output_count":4}`, `{}`, `{"input_count":7,"output_count":4,"token_count":12}`,
	} {
		t.Run(counters, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"code":0,"data":{"status":"completed","usage":%s}}`, counters)
			}))
			defer server.Close()
			c := cozeTestContext(context.Background(), true)
			err, complete := checkIfChatComplete(&Adaptor{}, c, cozeTestInfo(server.URL))
			if !strings.Contains(counters, `"token_count":11`) {
				require.Error(t, err)
				assert.False(t, complete)
				_, ok := c.Get("coze_reported_usage")
				assert.False(t, ok)
				return
			}
			require.NoError(t, err)
			require.True(t, complete)
			response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":[{"type":"answer","content":"0.1, 0.2"}]}`))}
			usage, apiErr := cozeChatHandler(c, cozeTestInfo(server.URL), response)
			require.Nil(t, apiErr)
			require.NotNil(t, usage.BillingUsage)
			assert.Equal(t, dto.BillingUsageSourceCozeChat, usage.BillingUsage.Source)
			assert.Equal(t, dto.BillingUsageSemanticOpenAI, usage.BillingUsage.Semantic)
			assert.False(t, usage.BillingUsage.Estimated)
			assert.Equal(t, 11, usage.TotalTokens)
			assert.Equal(t, 11, usage.BillingUsage.OpenAIUsage.TotalTokens)
		})
	}
}

func TestCozeReportedUsageRejectsMissingInvalidAndOverflow(t *testing.T) {
	for _, usage := range []string{
		`null`, `{}`, `{"input_count":1,"output_count":1}`, `{"input_count":-1,"output_count":2,"token_count":1}`,
		`{"input_count":2147483647,"output_count":1,"token_count":2147483648}`,
		`{"input_count":1,"output_count":1,"token_count":3}`,
		`{"input_count":0,"output_count":0,"token_count":0}`,
		`{"input_count":1.5,"output_count":1,"token_count":2.5}`,
	} {
		_, err := cozeReportedUsage([]byte(`{"data":{"usage":` + usage + `}}`))
		require.Error(t, err, usage)
	}
}
