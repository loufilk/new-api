package claude

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ClaudeResponsesStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	responseID := helper.GetResponseID(c)
	created := common.GetTimestamp()
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatClaude, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID:                 responseID,
		Model:              info.UpstreamModelName,
		Created:            created,
		EmitSequenceNumber: true,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	hostedBridge := relayconvert.NewClaudeHostedStreamBridge()

	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   responseID,
		Created:      created,
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	var streamErr *types.NewAPIError

	sendResponsesEvent := func(eventType string, payload dto.ResponsesStreamResponse) bool {
		payload.Type = eventType
		data, err := common.Marshal(payload)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		if err := helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: eventType}, string(data)); err != nil {
			streamErr = service.NewRelayClientGoneError(err)
			return false
		}
		return true
	}
	sendResult := func(result relayconvert.ResponseResult) bool {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			streamErr = types.NewOpenAIError(
				fmt.Errorf("expected OpenAI Responses stream event, got %T", result.Value),
				types.ErrorCodeBadResponse,
				http.StatusInternalServerError,
			)
			return false
		}
		return sendResponsesEvent(event.Type, event.Payload)
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var claudeResponse dto.ClaudeResponse
		if err := common.UnmarshalJsonStr(data, &claudeResponse); err != nil {
			streamErr = types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
			sr.Stop(streamErr)
			return
		}
		if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
			common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
			streamErr = types.WithClaudeError(*claudeError, http.StatusBadGateway)
			sr.Stop(streamErr)
			return
		}

		if claudeResponse.StopReason != "" {
			maybeMarkClaudeRefusal(c, info, claudeResponse.StopReason)
		}
		if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
			maybeMarkClaudeRefusal(c, info, *claudeResponse.Delta.StopReason)
		}
		if claudeResponse.Type == "message_stop" {
			info.StreamStatus.MarkCompleted()
		}
		if claudeResponse.Type == "message_start" && claudeResponse.Message != nil {
			info.ObserveResponseModel(claudeResponse.Message.Model)
			info.UpstreamModelName = claudeResponse.Message.Model
		}
		FormatClaudeResponseInfo(&claudeResponse, nil, claudeInfo)
		countClaudeStreamBillableTools(info, &claudeResponse)
		hostedEvents, consumed, err := hostedBridge.Convert(&claudeResponse, state)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, event := range hostedEvents {
			if !sendResponsesEvent(event.Type, event.Payload) {
				sr.Stop(streamErr)
				return
			}
		}
		if consumed {
			return
		}

		results, err := service.ConvertStreamResponseChunk(c, info, state, &claudeResponse)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, result := range results {
			if !sendResult(result) {
				sr.Stop(streamErr)
				return
			}
		}
		if claudeResponse.Type == "message_stop" {
			sr.Done()
		}
	})
	info.StreamStatus.RequireTerminal()
	failed := func(apiErr *types.NewAPIError) (*dto.Usage, *types.NewAPIError) {
		failure := apiErr
		if failure.GetErrorCode() != service.ClientGoneErrorCode && service.RelayRequestCancellation(c) == nil && !info.StreamStatus.ClientGone() {
			results, _ := state.FailResponsesStream("server_error", failure.Error(), "")
			for _, result := range results {
				if !sendResult(result) {
					failure = streamErr
					break
				}
			}
		}
		common.SetContextKey(c, constant.ContextKeyResponsesStreamErrorHandled, true)
		return InterruptedStreamUsage(c, info, claudeInfo, failure), types.NewError(failure, failure.GetErrorCode(), types.ErrOptionWithSkipRetry())
	}
	if streamErr != nil {
		return failed(streamErr)
	}
	if terminalErr := StreamTerminalError(c, info, nil); terminalErr != nil {
		return failed(terminalErr)
	}

	HandleStreamFinalResponse(c, info, claudeInfo)
	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
	state.SetUsage(&openAIUsage)
	finalResults, err := service.FinalizeStreamResponse(c, info, state)
	if err != nil {
		return failed(types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError))
	}
	for _, result := range finalResults {
		if !sendResult(result) {
			return failed(streamErr)
		}
	}
	return claudeInfo.Usage, nil
}
