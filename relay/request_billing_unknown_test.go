package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmeasuredStreamBillingSeparatesUnknownFromProtocolOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		end     relaycommon.StreamEndReason
		outcome relaycommon.ResponseOutcome
		usage   *dto.Usage
		tool    bool
		unknown bool
	}{
		{"client gone missing usage", relaycommon.StreamEndReasonClientGone, "", nil, false, true},
		{"client gone zero usage", relaycommon.StreamEndReasonClientGone, relaycommon.ResponseOutcomeCancelled, &dto.Usage{}, false, true},
		{"timeout without usage", relaycommon.StreamEndReasonTimeout, "", nil, false, true},
		{"read failure without usage", relaycommon.StreamEndReasonScannerErr, "", &dto.Usage{}, false, true},
		{"missing terminal", relaycommon.StreamEndReasonEOF, "", &dto.Usage{}, false, true},
		{"provider rejection", relaycommon.StreamEndReasonHandlerStop, relaycommon.ResponseOutcomeFailed, nil, false, false},
		{"provider cancellation", relaycommon.StreamEndReasonHandlerStop, relaycommon.ResponseOutcomeCancelled, nil, false, false},
		{"completed response", relaycommon.StreamEndReasonDone, relaycommon.ResponseOutcomeCompleted, &dto.Usage{}, false, false},
		{"measured partial usage", relaycommon.StreamEndReasonClientGone, "", &dto.Usage{PromptTokens: 10, CompletionTokens: 2}, false, false},
		{"incomplete cache-only measurement", relaycommon.StreamEndReasonClientGone, "", &dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 3}}, false, true},
		{"separately measured Claude cache", relaycommon.StreamEndReasonClientGone, "", &dto.Usage{UsageSemantic: "anthropic", PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 3}}, false, false},
		{"completed tool", relaycommon.StreamEndReasonClientGone, "", &dto.Usage{}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			billing := &recordedSettlement{}
			info := &relaycommon.RelayInfo{IsStream: true, Billing: billing, StreamStatus: relaycommon.NewStreamStatus()}
			info.StreamStatus.RequireTerminal()
			info.StreamStatus.SetEndReason(tc.end, io.ErrUnexpectedEOF)
			switch tc.outcome {
			case relaycommon.ResponseOutcomeCompleted:
				info.StreamStatus.MarkCompleted()
			case relaycommon.ResponseOutcomeFailed:
				info.StreamStatus.MarkFailed("upstream_rejected", "invalid_request_error", 400)
			case relaycommon.ResponseOutcomeCancelled:
				info.StreamStatus.MarkCancelled()
			}
			if tc.tool {
				info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
			}
			assert.Equal(t, tc.unknown, preserveUnmeasuredStreamBilling(c, info, tc.usage))
			assert.Equal(t, tc.unknown, c.GetBool(requestBillingDeliveryUnknownKey))
			if tc.unknown {
				ConsumeResponsesQuota(c, info, tc.usage)
				ConsumeResponsesQuota(c, info, tc.usage)
				apiErr := service.NewRelayClientGoneError(context.Canceled)
				require.Same(t, apiErr, RefundFailedRequestBilling(c, info, apiErr))
				assert.Empty(t, billing.settled, "unknown delivery must not settle zero or a guessed fixed fee")
				assert.False(t, billing.refunded, "unknown delivery must not refund its reservation")
			}
		})
	}
}

func TestCancelledRequestBillingDistinguishesBeforeAndAfterSend(t *testing.T) {
	for _, sent := range []bool{false, true} {
		name := "before send"
		if sent {
			name = "after attempted send"
		}
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			billing := &recordedSettlement{}
			info := &relaycommon.RelayInfo{Billing: billing}
			if sent {
				common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, true)
			}
			apiErr := service.NewRelayClientGoneError(context.Canceled)
			require.Same(t, apiErr, RefundFailedRequestBilling(c, info, apiErr))
			assert.Equal(t, !sent, billing.refunded)
			assert.Empty(t, billing.settled)
			assert.Equal(t, sent, c.GetBool(requestBillingDeliveryUnknownKey))
		})
	}
}

func TestPartialStreamSettlementIsNotReclassifiedAsUnknown(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set(requestBillingUsageObservedKey, true)
	billing := &recordedSettlement{}
	info := &relaycommon.RelayInfo{Billing: billing, StreamStatus: relaycommon.NewStreamStatus()}
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)
	apiErr := types.NewError(context.Canceled, service.ClientGoneErrorCode, types.ErrOptionWithSkipRetry())
	RefundFailedRequestBilling(c, info, apiErr)
	assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey), "the BillingSession owns an already attempted actual settlement")
	assert.True(t, billing.refunded, "the ordinary failure defer still reaches the session's settlement guard")
}

func TestHTTPAcceptedBodyWithoutResponseRetainsUnknownBilling(t *testing.T) {
	service.InitHttpClient()
	readBody := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			readBody <- string(body)
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(server.Close)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	billing := &recordedSettlement{}
	info := &relaycommon.RelayInfo{Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{}}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, server.URL, strings.NewReader(`{"model":"test"}`))
	require.NoError(t, err)
	response, err := relaychannel.DoRequest(c, request, info)
	require.Error(t, err)
	assert.Nil(t, response)
	select {
	case body := <-readBody:
		assert.Equal(t, `{"model":"test"}`, body)
	default:
		t.Fatal("fixture must prove upstream received the generation body")
	}
	apiErr := types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusBadGateway)
	originalCode, originalStatus, originalMessage := apiErr.GetErrorCode(), apiErr.StatusCode, apiErr.Error()
	apiErr = PreserveFailedRequestBilling(c, info, apiErr)
	assert.Equal(t, originalCode, apiErr.GetErrorCode())
	assert.Equal(t, originalStatus, apiErr.StatusCode)
	assert.Equal(t, originalMessage, apiErr.Error())
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Equal(t, "stop", service.DecideRelayRetry(c, apiErr, 3).Action, "do not generate again after an accepted body loses its response")
	RefundFailedRequestBilling(c, info, apiErr)
	assert.True(t, c.GetBool(requestBillingDeliveryUnknownKey))
	assert.False(t, billing.refunded)
	assert.Empty(t, billing.settled)
}

func TestWebSocketHandshakeWithoutCreateRemainsUnused(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Set(requestBillingNotSubmittedKey, true)
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestOrigin, "https://upstream.invalid")
	billing := &recordedSettlement{}
	info := &relaycommon.RelayInfo{Billing: billing}
	apiErr := types.NewError(context.Canceled, types.ErrorCodeDoRequestFailed)
	RefundFailedRequestBilling(c, info, apiErr)
	assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey))
	assert.True(t, billing.refunded)
	assert.Empty(t, billing.settled)
}

func TestLocalStreamConversionFailureIsNotConfirmedProviderRejection(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	info.StreamStatus.MarkFailed(string(types.ErrorCodeBadResponse), "server_error", http.StatusBadGateway)
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonHandlerStop, io.ErrUnexpectedEOF)
	assert.True(t, preserveUnmeasuredStreamBilling(c, info, nil))
}

func TestHTTPConnectionRefusedBeforeSubmissionRemainsRetryable(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	billing := &recordedSettlement{}
	info := &relaycommon.RelayInfo{Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{}}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, strings.NewReader(`{"model":"test"}`))
	require.NoError(t, err)
	_, err = relaychannel.DoRequest(c, request, info)
	require.Error(t, err)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent), "typed no-send evidence must survive sanitized transport errors")
	apiErr := PreserveFailedRequestBilling(c, info, types.NewError(err, types.ErrorCodeDoRequestFailed))
	assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey))
	assert.Equal(t, "retry", service.DecideRelayRetry(c, apiErr, 3).Action)
	RefundFailedRequestBilling(c, info, apiErr)
	assert.True(t, billing.refunded)
	assert.Empty(t, billing.settled)
}

func TestResponsesWSPreAcceptRejectionIsRefundable(t *testing.T) {
	for _, code := range []string{"invalid_request", string(types.ErrorCodeBadResponse)} {
		t.Run(code, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			billing := &recordedSettlement{}
			info := &relaycommon.RelayInfo{Billing: billing, StreamStatus: relaycommon.NewStreamStatus()}
			info.StreamStatus.RequireTerminal()
			rejected := newResponsesWSRequestRejection(c, info, responsesWSErrorEvent{
				Status: http.StatusBadRequest,
				Error:  &types.OpenAIError{Type: "invalid_request_error", Code: code},
			}, &dto.ResponsesStreamResponse{Type: "error"})
			require.NotNil(t, rejected)
			assert.Equal(t, string(relaycommon.ResponseOutcomeFailed), info.StreamStatus.ResponseOutcome())
			RefundFailedRequestBilling(c, info, rejected)
			assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey))
			assert.True(t, billing.refunded)
			assert.Empty(t, billing.settled)
		})
	}
}

func TestResponsesBareDoneWithoutNativeTerminalRemainsUnknown(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	common.SetContextKey(c, constant.ContextKeyResponsesStreamErrorHandled, true)
	billing := &recordedSettlement{}
	info := &relaycommon.RelayInfo{Billing: billing, StreamStatus: relaycommon.NewStreamStatus()}
	info.StreamStatus.RequireTerminal()
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	ConsumeResponsesQuota(c, info, &dto.Usage{})
	assert.True(t, c.GetBool(requestBillingDeliveryUnknownKey))
	assert.Empty(t, billing.settled)
	assert.False(t, billing.refunded)
}

func TestRealtimeHandshakeFailureRemainsUnused(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestOrigin, "https://upstream.invalid")
	billing := &recordedSettlement{}
	info := &relaycommon.RelayInfo{Billing: billing, RelayFormat: types.RelayFormatOpenAIRealtime}
	apiErr := types.NewErrorWithStatusCode(context.Canceled, types.ErrorCodeDoRequestFailed, http.StatusUnauthorized)
	RefundFailedRequestBilling(c, info, apiErr)
	assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey))
	assert.True(t, billing.refunded)
	assert.Empty(t, billing.settled)
}

func TestResponsesPreludeWithoutNativeTerminalRetainsUnknownBilling(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, ending := range []string{"", "data: [DONE]\n\n"} {
		t.Run("ending="+ending, func(t *testing.T) {
			body := `data: {"type":"response.created","response":{"id":"resp_unknown","status":"in_progress"}}` + "\n\n" + ending
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			billing := &recordedSettlement{}
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "test"}}
			info.SetEstimatePromptTokens(100)
			resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"text/event-stream"}}}
			usage, apiErr := openai.OaiResponsesStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			require.Nil(t, usage, "a Responses prelude and a bare Chat [DONE] cannot manufacture prompt tokens")
			ConsumeResponsesQuota(c, info, usage)
			apiErr = PreserveFailedRequestBilling(c, info, apiErr)
			RefundFailedRequestBilling(c, info, apiErr)
			assert.True(t, c.GetBool(requestBillingDeliveryUnknownKey))
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.Empty(t, billing.settled)
			assert.False(t, billing.refunded)
		})
	}
}

func TestHTTP200DecodeFailureDoesNotBecomeUnusedRequest(t *testing.T) {
	service.InitHttpClient()
	for _, format := range []struct {
		name    string
		handler func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError)
	}{
		{"chat", openai.OpenaiHandler},
		{"responses", openai.OaiResponsesHandler},
		{"responses to chat", openai.OaiResponsesToChatHandler},
		{"chat to responses", openai.OaiChatToResponsesHandler},
	} {
		for _, rejectionCode := range []string{"", "bad_response_body", "do_request_failed", "read_response_body_failed"} {
			providerRejection := rejectionCode != ""
			t.Run(fmt.Sprintf("%s/rejection=%s", format.name, rejectionCode), func(t *testing.T) {
				body := `{"id":"resp_received","choices":[`
				if providerRejection {
					body = `{"error":{"type":"invalid_request_error","code":"` + rejectionCode + `","message":"explicit upstream rejection"}}`
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, body)
				}))
				t.Cleanup(server.Close)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				billing := &recordedSettlement{}
				info := &relaycommon.RelayInfo{Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{}}
				request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, server.URL, strings.NewReader(`{"model":"test"}`))
				require.NoError(t, err)
				response, err := relaychannel.DoRequest(c, request, info)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, response.StatusCode)
				usage, apiErr := format.handler(c, info, response)
				require.NotNil(t, apiErr)
				assert.Nil(t, usage)
				apiErr = PreserveFailedRequestBilling(c, info, apiErr)
				if !providerRejection {
					assert.True(t, types.IsSkipRetryError(apiErr))
					assert.Equal(t, "stop", service.DecideRelayRetry(c, apiErr, 3).Action)
				}
				RefundFailedRequestBilling(c, info, apiErr)
				assert.Equal(t, !providerRejection, c.GetBool(requestBillingDeliveryUnknownKey))
				assert.Equal(t, providerRejection, billing.refunded)
				assert.Empty(t, billing.settled, "neither unmeasured delivery nor explicit rejection is a zero-usage settlement")
			})
		}
	}
}

func TestCancelledHTTPAttemptDoesNotReusePreviousRequestOrigin(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestOrigin, "https://previous-attempt.invalid")
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, true)
	billing := &recordedSettlement{}
	info := &relaycommon.RelayInfo{Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://not-dialed.invalid", strings.NewReader(`{"model":"test"}`))
	require.NoError(t, err)
	_, err = relaychannel.DoRequest(c, request, info)
	require.Error(t, err)
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
	apiErr := PreserveFailedRequestBilling(c, info, types.NewError(err, types.ErrorCodeDoRequestFailed))
	RefundFailedRequestBilling(c, info, apiErr)
	assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey))
	assert.True(t, billing.refunded)
	assert.Empty(t, billing.settled)
}

func TestChatDetailOnlyUsageNeverSettlesZeroAfterInterruptedStream(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, wire := range []string{
		`{"prompt_tokens_details":{"cached_tokens":8}}`,
		`{"completion_tokens_details":{"reasoning_tokens":3}}`,
		`{"completion_tokens_details":{"audio_tokens":5}}`,
	} {
		t.Run(wire, func(t *testing.T) {
			body := `data: {"choices":[],"usage":` + wire + "}\n\n"
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			billing := &recordedSettlement{}
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{}}
			resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"text/event-stream"}}}
			usage, apiErr := openai.OaiStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			require.NotNil(t, usage)
			assert.True(t, dto.HasOpenAIUsageTokens(usage))
			consumePartialTextStreamQuota(c, info, usage)
			RefundFailedRequestBilling(c, info, apiErr)
			assert.True(t, c.GetBool(requestBillingDeliveryUnknownKey))
			assert.Empty(t, billing.settled, "work evidence without aggregate usage must not become a zero charge")
			assert.False(t, billing.refunded)
		})
	}
}

func TestNativeChatErrorEnvelopeSeparatesUnusedFromObservedWork(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	batch, logConsume := common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.BatchUpdateEnabled, common.LogConsumeEnabled = true, false
	t.Cleanup(func() {
		constant.StreamingTimeout = oldTimeout
		common.BatchUpdateEnabled, common.LogConsumeEnabled = batch, logConsume
	})
	errorEvent := `{"error":{"type":"invalid_request_error","code":"bad_response_body","message":"explicit rejection"}}`
	for _, tc := range []struct {
		name, prelude, event string
		hasWork, unknown     bool
	}{
		{"explicit unused", "", errorEvent, false, false},
		{"measured partial", `data: {"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":6,"total_tokens":26}}` + "\n\n", errorEvent, true, false},
		{"text partial", `data: {"choices":[{"index":0,"delta":{"content":"observed output"}}]}` + "\n\n", errorEvent, true, false},
		{"malformed response", "", `{"choices":[`, false, true},
	} {
		for _, ending := range []string{"", "data: [DONE]\n\n"} {
			t.Run(tc.name+"/ending="+ending, func(t *testing.T) {
				body := tc.prelude + "data: " + tc.event + "\n\n" + ending
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				billing := &recordedSettlement{}
				info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{}}
				info.SetEstimatePromptTokens(100)
				info.UserSetting.QuotaWarningEnabled = common.GetPointer(false)
				info.PriceData.ModelRatio, info.PriceData.CompletionRatio, info.PriceData.GroupRatioInfo.GroupRatio = 1, 1, 1
				resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"text/event-stream"}}}
				usage, apiErr := openai.OaiStreamHandler(c, info, resp)
				require.NotNil(t, apiErr)
				assert.True(t, types.IsSkipRetryError(apiErr))
				consumePartialTextStreamQuota(c, info, usage)
				apiErr = PreserveFailedRequestBilling(c, info, apiErr)
				assert.Equal(t, "stop", service.DecideRelayRetry(c, apiErr, 3).Action)
				assert.Equal(t, tc.unknown, c.GetBool(requestBillingDeliveryUnknownKey))
				assert.Equal(t, !tc.unknown, common.GetContextKeyBool(c, constant.ContextKeyUpstreamExplicitRejection))
				if tc.hasWork {
					require.NotNil(t, usage)
					assert.Positive(t, usage.CompletionTokens)
					assert.Equal(t, []int{usage.TotalTokens}, billing.settled, "an explicit error cannot discard or duplicate already observed work")
					assert.True(t, c.GetBool(requestBillingUsageObservedKey))
				} else {
					assert.Nil(t, usage)
					RefundFailedRequestBilling(c, info, apiErr)
					assert.Equal(t, !tc.unknown, billing.refunded)
					assert.Empty(t, billing.settled)
				}
			})
		}
	}
}

func TestNativeChatUsageMeasurementsRequirePriceableAggregates(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	batch, logConsume := common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.BatchUpdateEnabled, common.LogConsumeEnabled = true, false
	t.Cleanup(func() {
		constant.StreamingTimeout = oldTimeout
		common.BatchUpdateEnabled, common.LogConsumeEnabled = batch, logConsume
	})
	for _, route := range []struct {
		name    string
		format  types.RelayFormat
		handler func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError)
	}{
		{"native chat", types.RelayFormatOpenAI, openai.OaiStreamHandler},
	} {
		for _, tc := range []struct {
			name, wire string
			complete   bool
		}{
			{"cached details", `{"prompt_tokens_details":{"cached_tokens":8}}`, false},
			{"reasoning details", `{"completion_tokens_details":{"reasoning_tokens":3}}`, false},
			{"audio details", `{"completion_tokens_details":{"audio_tokens":5}}`, false},
			{"unsplit total", `{"total_tokens":10}`, false},
			{"complete aggregates", `{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}`, true},
		} {
			for _, ending := range []string{"", "data: [DONE]\n\n"} {
				t.Run(route.name+"/"+tc.name+"/ending="+ending, func(t *testing.T) {
					body := `data: {"choices":[],"usage":` + tc.wire + "}\n\n" + ending
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					billing := &recordedSettlement{}
					info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: route.format, RelayMode: relayconstant.RelayModeChatCompletions, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{}}
					info.SetEstimatePromptTokens(100)
					info.UserSetting.QuotaWarningEnabled = common.GetPointer(false)
					info.PriceData.ModelRatio, info.PriceData.CompletionRatio, info.PriceData.GroupRatioInfo.GroupRatio = 1, 1, 1
					resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"text/event-stream"}}}
					usage, apiErr := route.handler(c, info, resp)
					require.NotNil(t, usage, "do not discard measured details while withholding an unknown charge")
					if route.format == types.RelayFormatOpenAIResponses {
						ConsumeResponsesQuota(c, info, usage)
					} else if apiErr == nil {
						// Exercise the normal success dispatcher too: it does not run
						// the partial-stream unknown guard before settlement.
						service.PostTextConsumeQuota(c, info, usage, nil)
					} else {
						consumePartialTextStreamQuota(c, info, usage)
					}
					if tc.complete {
						assert.Equal(t, []int{10}, billing.settled)
						assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey))
						if ending != "" {
							assert.Nil(t, apiErr, "complete usage and a real Chat terminal remain successful")
						}
						return
					}
					assert.Empty(t, billing.settled, "terminal delivery does not make incomplete measurement free or justify guessed prompt usage")
					require.NotNil(t, apiErr)
					assert.True(t, types.IsSkipRetryError(apiErr))
					RefundFailedRequestBilling(c, info, apiErr)
					assert.True(t, c.GetBool(requestBillingDeliveryUnknownKey))
					assert.False(t, billing.refunded)
				})
			}
		}
	}
}

func TestNativeChatRetainedSnapshotDoesNotHideUnmeasuredOutput(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	batch, logConsume := common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.BatchUpdateEnabled, common.LogConsumeEnabled = true, false
	t.Cleanup(func() {
		constant.StreamingTimeout = oldTimeout
		common.BatchUpdateEnabled, common.LogConsumeEnabled = batch, logConsume
	})
	for _, tc := range []struct {
		name                           string
		output, finalUsage, done       bool
		unknown                        bool
		wantCompletion, wantSettlement int
	}{
		{"early prompt then output EOF", true, false, false, true, 0, 0},
		{"early prompt then output DONE", true, false, true, true, 0, 0},
		{"later complete native snapshot", true, true, true, false, 4, 24},
		{"input only without generated output", false, false, true, false, 0, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `data: {"choices":[],"usage":{"prompt_tokens":20,"total_tokens":20,"prompt_tokens_details":{"cached_tokens":8}}}` + "\n\n"
			if tc.output {
				body += `data: {"choices":[{"index":0,"delta":{"content":"observed output"}}]}` + "\n\n"
			}
			// A retained early snapshot must not become a final complete
			// measurement merely because later metadata frames omit usage.
			body += strings.Repeat("data: {\"choices\":[]}\n\n", 3)
			if tc.finalUsage {
				body += `data: {"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}` + "\n\n"
			}
			if tc.done {
				body += `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			billing := &recordedSettlement{}
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{}}
			info.SetEstimatePromptTokens(100)
			info.UserSetting.QuotaWarningEnabled = common.GetPointer(false)
			info.PriceData.ModelRatio, info.PriceData.CompletionRatio, info.PriceData.GroupRatioInfo.GroupRatio, info.PriceData.CacheRatio = 1, 1, 1, 1
			resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"text/event-stream"}}}
			usage, apiErr := openai.OaiStreamHandler(c, info, resp)
			require.NotNil(t, usage)
			assert.Equal(t, 20, usage.PromptTokens)
			assert.Equal(t, 8, usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, tc.wantCompletion, usage.CompletionTokens, "keep native measurement rather than inventing output quantities")
			if apiErr == nil {
				service.PostTextConsumeQuota(c, info, usage, nil)
			} else {
				consumePartialTextStreamQuota(c, info, usage)
			}
			if !tc.unknown {
				assert.Nil(t, apiErr)
				assert.Equal(t, []int{tc.wantSettlement}, billing.settled)
				assert.False(t, c.GetBool(requestBillingDeliveryUnknownKey))
				return
			}
			assert.Empty(t, billing.settled, "do not charge only the old prompt snapshot after observing unmeasured output")
			require.NotNil(t, apiErr)
			assert.True(t, types.IsSkipRetryError(apiErr))
			RefundFailedRequestBilling(c, info, apiErr)
			assert.True(t, c.GetBool(requestBillingDeliveryUnknownKey))
			assert.False(t, billing.refunded)
		})
	}
}
