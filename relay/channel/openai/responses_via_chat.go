package openai

import (
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func OaiChatToResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var chatResp dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResp); err != nil {
		common.SetContextKey(c, constant.ContextKeyUpstreamResponseDecodeFailed, true)
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := chatResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	info.ObserveResponseModel(chatResp.Model)
	if responseID := helper.GetResponseID(c); responseID != "" {
		chatResp.Id = responseID
	}
	convertResult, err := service.ConvertResponse(c, info, types.RelayFormatOpenAIResponses, &chatResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	responsesResp, ok := convertResult.Value.(*dto.OpenAIResponsesResponse)
	if !ok {
		return nil, types.NewOpenAIError(fmt.Errorf("expected OpenAI responses response, got %T", convertResult.Value), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	usage := convertResult.Usage
	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(responsesResp)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		responsesResp.Usage = relayconvert.UsageFromChatUsage(usage)
	}

	responseBody, err := common.Marshal(responsesResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

// Preserve an untyped nil when a converter stops without an error.
func stopStream(sr *helper.StreamResult, streamErr *types.NewAPIError) {
	if streamErr == nil {
		sr.Stop(nil)
		return
	}
	sr.Stop(streamErr)
}

func OaiChatToResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (usage *dto.Usage, apiErr *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	responseID := helper.GetResponseID(c)
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID:                 responseID,
		Model:              info.UpstreamModelName,
		EmitSequenceNumber: true,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	streamErr := (*types.NewAPIError)(nil)
	upstreamTerminal := false
	defer func() {
		if apiErr == nil {
			return
		}
		// An upstream [DONE] can precede the final downstream Responses write.
		// Losing that write must not discard observed usage and refund the whole
		// request, or start another generation on a fallback account.
		if usage == nil {
			usage = state.Usage()
			if !dto.HasOpenAIUsageTokens(usage) {
				usage = nil
			}
			if usage == nil && state.UsageText() != "" {
				usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
			}
		}
		apiErr = types.NewError(apiErr, apiErr.GetErrorCode(), types.ErrOptionWithSkipRetry())
		if info.StreamStatus != nil {
			common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, info.StreamStatus)
			if apiErr.GetErrorCode() == service.ClientGoneErrorCode {
				info.StreamStatus.MarkCancelled()
			}
			if !info.StreamStatus.HasErrors() {
				info.StreamStatus.RecordError(apiErr.Error())
			}
		}
		if c.Writer.Written() || service.RelayRequestCancellation(c) != nil || apiErr.GetErrorCode() == service.ClientGoneErrorCode {
			common.SetContextKey(c, constant.ContextKeyResponsesStreamErrorHandled, true)
		}
	}()

	sendEvent := func(event relayconvert.ChatToResponsesStreamEvent) bool {
		data, err := common.Marshal(event.Payload)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		if err := helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event.Type}, string(data)); err != nil {
			streamErr = service.NewRelayClientGoneError(err)
			return false
		}
		if event.Type == "response.completed" && info.StreamStatus != nil {
			info.StreamStatus.MarkCompleted()
		}
		return true
	}
	failResponsesStream := func(err error) bool {
		if streamErr == nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
		}
		if info.StreamStatus != nil {
			if upstreamError := info.StreamStatus.APIError(); upstreamError != nil {
				streamErr = upstreamError
			}
			info.StreamStatus.SetAPIError(streamErr)
			info.StreamStatus.MarkFailed(string(streamErr.GetErrorCode()), streamErr.ToOpenAIError().Type, streamErr.StatusCode)
		}
		failureResults, handled := state.FailResponsesStream("server_error", err.Error(), "")
		if !handled {
			return false
		}
		for _, result := range failureResults {
			event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
			if !ok {
				streamErr = types.NewOpenAIError(fmt.Errorf("expected OAI responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
				return true
			}
			if !sendEvent(event) {
				return true
			}
		}
		return true
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}

		var errorResp dto.OpenAITextResponse
		if err := common.UnmarshalJsonStr(data, &errorResp); err == nil {
			if oaiError := errorResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
				common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
				streamErr = types.WithOpenAIError(*oaiError, http.StatusBadGateway)
				if failResponsesStream(fmt.Errorf("%s", oaiError.Message)) {
					sr.Stop(streamErr)
					return
				}
				sr.Stop(streamErr)
				return
			}
		}

		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal chat stream response: %s", err.Error()))
			if failResponsesStream(err) {
				sr.Stop(streamErr)
				return
			}
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}

		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				upstreamTerminal = true
			}
		}
		info.ObserveResponseModel(chunk.Model)
		results, err := service.ConvertStreamResponseChunk(c, info, state, &chunk)
		if err != nil {
			if failResponsesStream(err) {
				sr.Stop(streamErr)
				return
			}
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, result := range results {
			event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
			if !ok {
				streamErr = types.NewOpenAIError(fmt.Errorf("expected OAI responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
				sr.Stop(streamErr)
				return
			}
			if !sendEvent(event) {
				sr.Stop(streamErr)
				return
			}
		}
	})

	info.StreamStatus.RequireTerminal()
	if streamErr == nil {
		streamErr = info.StreamStatus.APIError()
	}
	if streamErr != nil {
		return nil, streamErr
	}

	if stopped := service.RelayRequestCancellation(c); stopped != nil {
		return nil, stopped
	}
	if info.StreamStatus.ClientGone() {
		return nil, service.NewRelayClientGoneError(info.StreamStatus.EndError)
	}
	if !upstreamTerminal && info.StreamStatus.EndReason != relaycommon.StreamEndReasonDone {
		status := http.StatusBadGateway
		if info.StreamStatus.EndReason == relaycommon.StreamEndReasonTimeout {
			status = http.StatusGatewayTimeout
		}
		streamErr = types.NewErrorWithStatusCode(fmt.Errorf("upstream chat stream ended before a terminal event"), types.ErrorCodeBadResponse, status, types.ErrOptionWithSkipRetry())
		failResponsesStream(streamErr)
		return nil, streamErr
	}

	usage = state.Usage()
	if usage == nil || usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		state.SetUsage(usage)
	}

	finalResults, err := service.FinalizeStreamResponse(c, info, state)
	if err != nil {
		if failResponsesStream(err) {
			return usage, streamErr
		}
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	for _, result := range finalResults {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			return nil, types.NewOpenAIError(fmt.Errorf("expected OAI responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		if !sendEvent(event) {
			return nil, streamErr
		}
	}

	return usage, nil
}
