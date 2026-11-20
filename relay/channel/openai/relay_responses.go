package openai

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		common.SetContextKey(c, constant.ContextKeyUpstreamResponseDecodeFailed, true)
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	event := dto.ResponsesStreamResponse{Response: &responsesResponse}
	outcome, outcomeErr := validateNativeResponsesOutcome(&event)
	if outcomeErr != nil {
		common.SetContextKey(c, constant.ContextKeyUpstreamResponseDecodeFailed, true)
		if info.StreamStatus == nil {
			info.StreamStatus = relaycommon.NewStreamStatus()
		}
		info.StreamStatus.RequireTerminal()
		usage := &dto.Usage{}
		service.ApplyResponsesUsage(usage, responsesResponse.Usage)
		if !dto.HasOpenAIUsageTokens(usage) {
			usage = nil
		}
		common.SetContextKey(c, constant.ContextKeyResponsesPartialUsage, usage != nil)
		return usage, types.NewErrorWithStatusCode(outcomeErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	if outcome == relaycommon.ResponseOutcomeFailed || outcome == relaycommon.ResponseOutcomeCancelled {
		common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
		if info.StreamStatus == nil {
			info.StreamStatus = relaycommon.NewStreamStatus()
		}
		event.Type = "response." + string(outcome)
		accumulator := service.NewResponsesUsageAccumulator(info)
		accumulator.ObserveWithOutcome(&event, responseBody, outcome)
		usage := accumulator.Finish()
		common.SetContextKey(c, constant.ContextKeyResponsesPartialUsage, usage != nil)
		return usage, newResponsesFailureError(&event)
	}

	info.ObserveResponseModel(responsesResponse.Model)
	if info.OriginModelName != "" && gjson.GetBytes(responseBody, "model").Exists() {
		responseBody, err = sjson.SetBytes(responseBody, "model", info.OriginModelName)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}
	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", responsesResponse.CreatedAt)

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := &dto.Usage{}
	service.ApplyResponsesUsage(usage, responsesResponse.Usage)
	tools := relaycommon.NewResponsesToolUsageObserver(info)
	tools.ObserveResponse(&responsesResponse, responseBody)
	tools.Finish()

	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, common.LogText("invalid response or response body"))
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	accumulator := service.NewResponsesUsageAccumulator(info)
	var streamErr *types.NewAPIError
	terminalSeen := false
	writeFailed := false
	sequenceNumber := 0
	responseID := ""
	failureResponse := dto.OpenAIResponsesResponse{
		ID: "resp_" + common.GetUUID(), Object: "response",
		CreatedAt: dto.IntValue(time.Now().Unix()), Model: info.OriginModelName,
		Status: []byte(`"failed"`), Output: []dto.ResponsesOutput{},
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			streamErr = types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
			return
		}
		if streamResponse.Type == "" || strings.ContainsAny(streamResponse.Type, "\r\n") {
			streamErr = types.NewErrorWithStatusCode(fmt.Errorf("upstream Responses event has an invalid type"), types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
			return
		}
		if streamResponse.SequenceNumber != nil && *streamResponse.SequenceNumber >= sequenceNumber {
			sequenceNumber = *streamResponse.SequenceNumber
		}
		if sequenceNumber < math.MaxInt {
			sequenceNumber++
		}
		response := streamResponse.Response
		if response != nil && response.ID != "" && responseID != "" && response.ID != responseID {
			err := fmt.Errorf("upstream Responses stream changed response ID")
			streamErr = types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(err) // A local protocol error, not a terminal failure from this response.
			return
		}
		outcome, outcomeErr := validateNativeResponsesOutcome(&streamResponse)
		if outcomeErr != nil {
			// Keep reported usage, but not output or tools asserted only by an
			// invalid terminal. Usage from a conflicting response ID was excluded above.
			if response != nil && response.Usage != nil {
				accumulator.ObserveWithOutcome(&dto.ResponsesStreamResponse{Response: &dto.OpenAIResponsesResponse{Usage: response.Usage}}, nil, relaycommon.ResponseOutcomeUnknown)
			}
			streamErr = types.NewErrorWithStatusCode(outcomeErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(outcomeErr) // Keep the provider outcome unknown even though delivery failed.
			return
		}
		if response != nil {
			info.ObserveResponseModel(response.Model)
			if response.ID != "" {
				responseID = response.ID
				failureResponse.ID = response.ID
			}
			if response.CreatedAt != 0 {
				failureResponse.CreatedAt = response.CreatedAt
			}
			if info.OriginModelName != "" && gjson.Get(data, "response.model").Exists() {
				var rewriteErr error
				data, rewriteErr = sjson.Set(data, "response.model", info.OriginModelName)
				if rewriteErr != nil {
					streamErr = types.NewErrorWithStatusCode(rewriteErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
					sr.Stop(streamErr)
					return
				}
			}
			data = string(rewriteSGLangResponsesCreatedAt(info, []byte(data), "response.created_at", response.CreatedAt))
		}
		// The provider has already generated this frame even if downstream
		// delivery fails, so retain its output and native usage before writing.
		accumulator.ObserveWithOutcome(&streamResponse, []byte(data), outcome)
		failed := outcome == relaycommon.ResponseOutcomeFailed || outcome == relaycommon.ResponseOutcomeCancelled
		if failed {
			common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
		}
		if outcome != relaycommon.ResponseOutcomeUnknown && streamResponse.Type != "error" && streamResponse.Type != "response.error" {
			// Keep valid aliases but normalize an event carrying a different
			// terminal outcome, including an error on a nonterminal label.
			terminalType := "response." + string(outcome)
			validAlias := streamResponse.Type == "response.done" && outcome == relaycommon.ResponseOutcomeCompleted ||
				streamResponse.Type == "response.canceled" && outcome == relaycommon.ResponseOutcomeCancelled
			if streamResponse.Type != terminalType && !validAlias {
				streamResponse.Type = terminalType
				var rewriteErr error
				data, rewriteErr = sjson.Set(data, "type", terminalType)
				if rewriteErr == nil {
					data, rewriteErr = sjson.Set(data, "response.status", string(outcome))
				}
				if rewriteErr != nil {
					streamErr = types.NewErrorWithStatusCode(rewriteErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
					sr.Stop(streamErr)
					return
				}
			}
		}
		if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
			writeFailed = helper.IsStreamWriteError(err)
			streamErr = NewStreamResponseError(info, err)
			sr.Stop(streamErr)
			return
		}
		if failed {
			// A native failed/cancelled event is a terminal response. Bare error
			// events still need a Responses terminal synthesized below.
			terminalSeen = streamResponse.Type != "error" && streamResponse.Type != "response.error"
			streamErr = newResponsesFailureError(&streamResponse)
			if outcome == relaycommon.ResponseOutcomeCancelled {
				// Transport reached the terminal. Keep cancellation distinct from
				// a reported provider failure while still returning an API error.
				sr.Done()
			} else {
				sr.Stop(streamErr)
			}
			return
		}
		switch streamResponse.Type {
		case "response.completed", "response.done", "response.incomplete":
			terminalSeen = true
			sr.Done()
		}
	})

	common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, info.StreamStatus)
	info.StreamStatus.RequireTerminal()
	if streamErr == nil {
		streamErr = info.StreamStatus.APIError()
	}
	if streamErr == nil && !(terminalSeen && info.StreamStatus.EndReason == relaycommon.StreamEndReasonDone && info.StreamStatus.ResponseOutcome() == string(relaycommon.ResponseOutcomeCompleted)) {
		streamErr = service.RelayRequestCancellation(c)
	}
	if streamErr == nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
		streamErr = service.NewRelayClientGoneError(info.StreamStatus.EndError)
	}
	if isClientGoneStreamError(streamErr) {
		writeFailed = true
		info.StreamStatus.MarkCancelled()
		info.StreamStatus.SetAPIError(streamErr)
	}
	if streamErr == nil && !terminalSeen {
		statusCode := http.StatusBadGateway
		var err error = fmt.Errorf("upstream response stream ended before a terminal event")
		if c.Request.Context().Err() != nil {
			err = c.Request.Context().Err()
		} else if info.StreamStatus.EndError != nil {
			err = info.StreamStatus.EndError
		} else if info.StreamStatus.EndReason == relaycommon.StreamEndReasonTimeout {
			statusCode = http.StatusGatewayTimeout
			err = fmt.Errorf("upstream response stream timed out before a terminal event")
		}
		streamErr = types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, statusCode, types.ErrOptionWithSkipRetry())
	}
	if streamErr != nil {
		common.SetContextKey(c, constant.ContextKeyResponsesStreamErrorHandled, true)
		if !info.StreamStatus.HasErrors() {
			info.StreamStatus.RecordError(streamErr.Error())
		}
		// A native failed event already carries the error. Bare error events and
		// abnormal EOF need a Responses terminal failure, never a fake completion.
		if !terminalSeen && !writeFailed && c.Request.Context().Err() == nil {
			oaiError := streamErr.ToOpenAIError(common.UpstreamAddresses(c)...)
			var code any = "server_error"
			if streamErr.GetErrorType() == types.ErrorTypeOpenAIError {
				code = oaiError.Code
			}
			failureResponse.Error = map[string]any{"code": code, "message": oaiError.Message}
			failure := dto.ResponsesStreamResponse{Type: "response.failed", SequenceNumber: &sequenceNumber, Response: &failureResponse}
			data, err := common.Marshal(failure)
			if err == nil {
				err = helper.ResponseChunkData(c, failure, string(data))
			}
			if err != nil {
				info.StreamStatus.RecordError(err.Error())
				logger.LogError(c, "failed to send Responses terminal error: "+err.Error())
				if helper.IsStreamWriteError(err) {
					streamErr = NewStreamResponseError(info, err)
				}
			}
		}
		return accumulator.Finish(), streamErr
	}

	return accumulator.Finish(), nil
}

// validateNativeResponsesOutcome is scoped to direct Responses HTTP. A terminal
// label alone does not prove a result; do not apply this policy to other bridges.
func validateNativeResponsesOutcome(event *dto.ResponsesStreamResponse) (relaycommon.ResponseOutcome, error) {
	response := event.Response
	var status string
	var statusErr error
	if response != nil && len(response.Status) > 0 {
		statusErr = common.Unmarshal(response.Status, &status)
	}
	switch status {
	case "failed", "error":
		return relaycommon.ResponseOutcomeFailed, nil
	case "cancelled", "canceled":
		return relaycommon.ResponseOutcomeCancelled, nil
	}
	switch event.Type {
	case "error", "response.error", "response.failed":
		return relaycommon.ResponseOutcomeFailed, nil
	case "response.cancelled", "response.canceled":
		return relaycommon.ResponseOutcomeCancelled, nil
	}
	if response != nil && response.Error != nil {
		switch value := response.Error.(type) {
		case map[string]any:
			if err := response.GetOpenAIError(); err.Type != "" || err.Code != nil || err.Message != "" {
				return relaycommon.ResponseOutcomeFailed, nil
			}
		case string:
			if strings.TrimSpace(value) != "" {
				return relaycommon.ResponseOutcomeFailed, nil
			}
		}
		return relaycommon.ResponseOutcomeUnknown, fmt.Errorf("upstream Responses result has an invalid error object")
	}
	switch event.Type {
	case "", "response.completed", "response.done", "response.incomplete":
	default:
		return relaycommon.ResponseOutcomeUnknown, nil
	}
	if response == nil || statusErr != nil || response.Object != "" && response.Object != "response" {
		return relaycommon.ResponseOutcomeUnknown, fmt.Errorf("upstream Responses terminal has an invalid response object")
	}
	if status != "" && status != "completed" && status != "incomplete" || event.Type == "response.incomplete" && status == "completed" {
		return relaycommon.ResponseOutcomeUnknown, fmt.Errorf("upstream Responses result has no consistent terminal status")
	}
	if response.Output == nil && (event.Type == "" || status == "" || !dto.HasOpenAIUsageTokens(response.Usage)) {
		return relaycommon.ResponseOutcomeUnknown, fmt.Errorf("upstream Responses result has no output array or usable terminal usage")
	}
	for _, item := range response.Output {
		if item.Type == "" {
			return relaycommon.ResponseOutcomeUnknown, fmt.Errorf("upstream Responses result has an invalid output item")
		}
	}
	if status == "" && len(response.Output) == 0 {
		return relaycommon.ResponseOutcomeUnknown, fmt.Errorf("upstream Responses result is missing terminal status")
	}
	if status == "incomplete" || event.Type == "response.incomplete" {
		return relaycommon.ResponseOutcomeIncomplete, nil
	}
	return relaycommon.ResponseOutcomeCompleted, nil
}

func newResponsesFailureError(event *dto.ResponsesStreamResponse) *types.NewAPIError {
	upstreamError := types.OpenAIError{Type: "server_error", Code: event.Code, Message: event.Message, Param: event.Param}
	if event.Response != nil {
		if err := event.Response.GetOpenAIError(); err != nil {
			upstreamError = *err
		}
	}
	if upstreamError.Type == "" {
		upstreamError.Type = "server_error"
	}
	if upstreamError.Code == nil || upstreamError.Code == "" {
		upstreamError.Code = "server_error"
	}
	if strings.TrimSpace(upstreamError.Message) == "" {
		upstreamError.Message = "upstream response stream failed: " + event.Type
	}
	return types.WithOpenAIError(upstreamError, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
}

func rewriteSGLangResponsesCreatedAt(info *relaycommon.RelayInfo, payload []byte, path string, createdAt dto.IntValue) []byte {
	if info.GetChannelType() != constant.ChannelTypeSGLang {
		return payload
	}
	if !ojson.GetBytes(payload, path).Exists() {
		return payload
	}
	patched, err := ojson.SetBytes(payload, path, int(createdAt))
	if err != nil {
		return payload
	}
	return patched
}
