package openai

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	"github.com/tidwall/gjson"
)

func OaiResponsesToChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var responsesResp dto.OpenAIResponsesResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	if err := common.Unmarshal(body, &responsesResp); err != nil {
		common.SetContextKey(c, constant.ContextKeyUpstreamResponseDecodeFailed, true)
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	event := dto.ResponsesStreamResponse{Type: "response.completed", Response: &responsesResp}
	outcome := service.ObserveResponsesOutcome(info, &event)
	if outcome == relaycommon.ResponseOutcomeFailed || outcome == relaycommon.ResponseOutcomeCancelled || responsesResp.GetOpenAIError() != nil {
		common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
		if info.StreamStatus == nil {
			info.StreamStatus = relaycommon.NewStreamStatus()
		}
		event.Type = "response.failed"
		if outcome == relaycommon.ResponseOutcomeCancelled {
			event.Type = "response.cancelled"
		}
		accumulator := service.NewResponsesUsageAccumulator(info)
		accumulator.Observe(&event, body)
		usage := accumulator.Finish()
		common.SetContextKey(c, constant.ContextKeyResponsesPartialUsage, usage != nil)
		return usage, newResponsesFailureError(&event)
	}
	tools := relaycommon.NewResponsesToolUsageObserver(info)
	tools.ObserveResponse(&responsesResp, body)
	tools.Finish()

	info.ObserveResponseModel(responsesResp.Model)
	responseValue, usage, err := convertResponsesResponseForClient(c, info, &responsesResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	responseBody, err := common.Marshal(responseValue)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func OaiResponsesToChatBufferedStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.RequireTerminal()
	common.SetContextKey(c, constant.ContextKeyResponsesPartialUsage, false)
	accumulator := relayconvert.NewResponsesBufferedAccumulator()
	billingOutput := relayconvert.NewResponsesBufferedAccumulator()
	usageAccumulator := service.NewResponsesUsageAccumulator(info)
	var finalResponse *dto.OpenAIResponsesResponse
	var streamErr *types.NewAPIError
	terminalSeen := false

	reader := newBufferedStreamReader(c.Request.Context(), resp.Body, time.Duration(constant.StreamingTimeout)*time.Second)
	defer reader.stop()
	scanner := helper.NewStreamScanner(reader)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 6 || line[:5] != "data:" {
			continue
		}
		data := line[5:]
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				break
			}
			continue
		}

		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal buffered responses stream event: %s", err.Error()))
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			break
		}
		if streamResp.Type == "" || strings.ContainsAny(streamResp.Type, "\r\n") {
			streamErr = types.NewOpenAIError(fmt.Errorf("upstream Responses event has an invalid type"), types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			break
		}
		outcome := usageAccumulator.Observe(&streamResp, []byte(data))
		accumulator.ProcessEvent(&streamResp)
		observeResponsesBufferedBillingOutput(billingOutput, &streamResp, []byte(data))
		if outcome == relaycommon.ResponseOutcomeFailed || outcome == relaycommon.ResponseOutcomeCancelled ||
			streamResp.Response != nil && streamResp.Response.GetOpenAIError() != nil {
			streamErr = newResponsesFailureError(&streamResp)
			if outcome != relaycommon.ResponseOutcomeCancelled {
				info.StreamStatus.MarkFailed(string(streamErr.GetErrorCode()), streamErr.ToOpenAIError().Type, streamErr.StatusCode)
			}
			break
		}
		switch streamResp.Type {
		case "response.completed", "response.done", "response.incomplete":
			terminalSeen = true
			finalResponse = streamResp.Response
			if finalResponse == nil {
				streamErr = types.NewOpenAIError(fmt.Errorf("upstream Responses terminal event has no response"), types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
				break
			}
			if outcome == relaycommon.ResponseOutcomeIncomplete {
				finalResponse.Status = []byte(`"incomplete"`)
			}
		}
		if streamErr != nil || terminalSeen {
			break
		}
	}
	if streamErr == nil {
		streamErr = service.RelayRequestCancellation(c)
	}
	if err := scanner.Err(); streamErr == nil && err != nil {
		reason := relaycommon.StreamEndReasonScannerErr
		if errors.Is(err, errBufferedStreamIdle) {
			reason = relaycommon.StreamEndReasonTimeout
		}
		info.StreamStatus.SetEndReason(reason, err)
		streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	if streamErr == nil && !terminalSeen {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
		streamErr = types.NewOpenAIError(fmt.Errorf("upstream Responses stream ended before a terminal event"), types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	if streamErr != nil {
		bufferedResponse := &dto.OpenAIResponsesResponse{Output: billingOutput.BuildOutput()}
		var bufferedText strings.Builder
		for _, item := range bufferedResponse.Output {
			switch item.Type {
			case "message":
				for _, content := range item.Content {
					if content.Type == "output_text" {
						bufferedText.WriteString(content.Text)
					}
				}
			case dto.BuildInCallFunctionCall, "custom_tool_call":
				bufferedText.WriteString(item.ArgumentsString())
			}
		}
		bufferedText.WriteString(relayconvert.ExtractReasoningTextFromResponses(bufferedResponse))
		usage := usageAccumulator.FinishBuffered(true, bufferedText.String())
		common.SetContextKey(c, constant.ContextKeyResponsesPartialUsage, usage != nil)
		info.StreamStatus.SetAPIError(streamErr)
		info.StreamStatus.RecordError(streamErr.Error())
		if streamErr.GetErrorCode() == service.ClientGoneErrorCode {
			info.StreamStatus.MarkCancelled()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, streamErr)
		} else {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonHandlerStop, streamErr)
		}
		return usage, streamErr
	}
	accumulator.SupplementResponseOutput(finalResponse)
	observedUsage := usageAccumulator.FinishBuffered(false, "")
	finalResponse.Usage = observedUsage
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)

	responseValue, usage, err := convertResponsesResponseForClient(c, info, finalResponse)
	if err != nil {
		common.SetContextKey(c, constant.ContextKeyResponsesPartialUsage, observedUsage != nil)
		apiErr := types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
		info.StreamStatus.SetAPIError(apiErr)
		info.StreamStatus.RecordError(apiErr.Error())
		return observedUsage, apiErr
	}
	responseBody, err := common.Marshal(responseValue)
	if err != nil {
		common.SetContextKey(c, constant.ContextKeyResponsesPartialUsage, usage != nil)
		apiErr := types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
		info.StreamStatus.SetAPIError(apiErr)
		info.StreamStatus.RecordError(apiErr.Error())
		return usage, apiErr
	}

	// The source was SSE, but the buffered client response is ordinary JSON.
	resp.Header.Del("Content-Type")
	c.Header("Content-Type", "application/json")
	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

// The client conversion DTO does not retain refusal text and custom tools use
// input rather than function arguments. Project those fields only into this
// billing buffer; the original response and its conversion remain untouched.
func observeResponsesBufferedBillingOutput(accumulator *relayconvert.ResponsesBufferedAccumulator, event *dto.ResponsesStreamResponse, raw []byte) {
	root := gjson.ParseBytes(raw)
	projected := *event
	projected.Item = responsesBufferedBillingItem(event.Item, root.Get("item"))
	switch event.Type {
	case "response.refusal.delta":
		projected.Type = "response.output_text.delta"
	case "response.refusal.done":
		projected.Type = dto.ResponsesOutputTypeItemDone
		projected.Item = &dto.ResponsesOutput{ID: event.ItemID, Type: "message", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: root.Get("refusal").String()}}}
	case "response.custom_tool_call_input.done":
		projected.Type = dto.ResponsesOutputTypeItemDone
		projected.Item = &dto.ResponsesOutput{ID: event.ItemID, Type: "custom_tool_call", Arguments: []byte(root.Get("input").Raw)}
	case "response.content_part.added", "response.content_part.done":
		if part := root.Get("part"); part.Get("type").String() == "refusal" {
			projected.Type = dto.ResponsesOutputTypeItemDone
			projected.Item = &dto.ResponsesOutput{ID: event.ItemID, Type: "message", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: part.Get("refusal").String()}}}
		}
	}
	accumulator.ProcessEvent(&projected)
	if event.Response != nil {
		rawItems := root.Get("response.output").Array()
		for i := range event.Response.Output {
			var rawItem gjson.Result
			if i < len(rawItems) {
				rawItem = rawItems[i]
			}
			item := responsesBufferedBillingItem(&event.Response.Output[i], rawItem)
			accumulator.ProcessEvent(&dto.ResponsesStreamResponse{Type: dto.ResponsesOutputTypeItemDone, OutputIndex: &i, Item: item})
		}
	}
}

func responsesBufferedBillingItem(item *dto.ResponsesOutput, raw gjson.Result) *dto.ResponsesOutput {
	if item == nil {
		return nil
	}
	projected := *item
	if item.Type == "custom_tool_call" {
		projected.Arguments = item.Input
	}
	if item.Type == "message" {
		projected.Content = append([]dto.ResponsesOutputContent(nil), item.Content...)
		rawContent := raw.Get("content").Array()
		for i := range projected.Content {
			if projected.Content[i].Type == "refusal" && i < len(rawContent) {
				projected.Content[i].Type = "output_text"
				projected.Content[i].Text = rawContent[i].Get("refusal").String()
			}
		}
	}
	return &projected
}

func convertResponsesResponseForClient(c *gin.Context, info *relaycommon.RelayInfo, response *dto.OpenAIResponsesResponse) (any, *dto.Usage, error) {
	if responseID := helper.GetResponseID(c); responseID != "" {
		response.ID = responseID
	}

	usage := relayconvert.UsageFromResponsesUsage(response.Usage)
	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(response)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		response.Usage = relayconvert.UsageFromChatUsage(usage)
	}

	result, err := service.ConvertResponse(c, info, info.RelayFormat, response)
	if err != nil {
		return nil, nil, err
	}
	if result.Usage != nil && result.Usage.TotalTokens != 0 {
		usage = result.Usage
	}
	return result.Value, usage, nil
}

func OaiResponsesToChatStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	responseId := helper.GetResponseID(c)
	createAt := time.Now().Unix()
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAIResponses, info.RelayFormat, relayconvert.ResponseStreamOptions{
		ID:               responseId,
		Model:            info.UpstreamModelName,
		Created:          createAt,
		DisableUsageText: info.RelayFormat == types.RelayFormatOpenAI,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	streamErr := (*types.NewAPIError)(nil)
	var estimator *service.StreamingEstimateByModel
	if info.RelayFormat == types.RelayFormatOpenAI {
		estimator = service.NewStreamingEstimateByModel(info.UpstreamModelName)
	}
	terminalSeen := false
	var failedUsage *dto.Usage
	tools := relaycommon.NewResponsesToolUsageObserver(info)

	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo == nil {
		info.ClaudeConvertInfo = &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone}
	}

	// A downstream write failure is not an upstream account failure. Retain
	// generated usage for settlement and stop the upstream immediately.
	writeFailed := func(err error) bool {
		streamErr = NewStreamResponseError(info, err)
		return false
	}

	sendGeminiResponse := func(geminiResponse *dto.GeminiChatResponse) bool {
		if geminiResponse == nil {
			return true
		}
		if err := helper.ObjectData(c, geminiResponse); err != nil {
			return writeFailed(err)
		}
		return true
	}

	feedChatChoices := func(choices []dto.ChatCompletionsStreamResponseChoice) {
		if estimator == nil {
			return
		}
		for i := range choices {
			delta := &choices[i].Delta
			if content := delta.GetContentString(); content != "" {
				estimator.WriteString(content)
			}
			if reasoning := delta.GetReasoningContent(); reasoning != "" {
				estimator.WriteString(reasoning)
			}
			for j := range delta.ToolCalls {
				function := &delta.ToolCalls[j].Function
				if function.Arguments != "" {
					estimator.WriteString(function.Arguments)
				}
				if function.Name != "" {
					estimator.WriteString(function.Name)
				}
			}
		}
	}

	sendStreamResult := func(result relayconvert.ResponseResult) bool {
		switch value := result.Value.(type) {
		case dto.ChatCompletionsStreamResponse:
			if len(value.Choices) == 0 && value.Usage == nil {
				return true
			}
			if err := helper.ObjectData(c, &value); err != nil {
				return writeFailed(err)
			}
			return true
		case *dto.ChatCompletionsStreamResponse:
			if value == nil || (len(value.Choices) == 0 && value.Usage == nil) {
				return true
			}
			if err := helper.ObjectData(c, value); err != nil {
				return writeFailed(err)
			}
			return true
		case dto.ClaudeResponse:
			if err := helper.ClaudeData(c, value); err != nil {
				return writeFailed(err)
			}
			return true
		case *dto.ClaudeResponse:
			if value == nil {
				return true
			}
			if err := helper.ClaudeData(c, *value); err != nil {
				return writeFailed(err)
			}
			return true
		case dto.GeminiChatResponse:
			return sendGeminiResponse(&value)
		case *dto.GeminiChatResponse:
			return sendGeminiResponse(value)
		default:
			streamErr = types.NewOpenAIError(fmt.Errorf("unsupported converted stream response type %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			return false
		}
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			stopStream(sr, streamErr)
			return
		}

		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal responses stream event: %s", err.Error()))
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			stopStream(sr, streamErr)
			return
		}

		if streamResp.Response != nil {
			info.ObserveResponseModel(streamResp.Response.Model)
		}
		service.ObserveResponsesOutcome(info, &streamResp)
		tools.Observe(&streamResp, []byte(data))
		var responseStatus string
		if streamResp.Response != nil {
			_ = common.Unmarshal(streamResp.Response.Status, &responseStatus)
		}
		if streamResp.Type == "error" || streamResp.Type == "response.error" || streamResp.Type == "response.failed" ||
			streamResp.Type == "response.cancelled" || streamResp.Type == "response.canceled" ||
			responseStatus == "failed" || responseStatus == "cancelled" || responseStatus == "canceled" {
			upstreamError := types.OpenAIError{Type: "server_error", Code: streamResp.Code, Message: streamResp.Message, Param: streamResp.Param}
			if streamResp.Response != nil {
				failedUsage = streamResp.Response.Usage
				if oaiErr := streamResp.Response.GetOpenAIError(); oaiErr != nil {
					upstreamError = *oaiErr
				}
			}
			if upstreamError.Type == "" {
				upstreamError.Type = "server_error"
			}
			if strings.TrimSpace(upstreamError.Message) == "" {
				upstreamError.Message = "responses stream error: " + streamResp.Type
			}
			streamErr = types.WithOpenAIError(upstreamError, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			stopStream(sr, streamErr)
			return
		}
		switch streamResp.Type {
		case "response.completed", "response.done", "response.incomplete":
			terminalSeen = true
		}

		results, err := service.ConvertStreamResponseChunk(c, info, state, &streamResp)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			stopStream(sr, streamErr)
			return
		}
		// The converter has observed the entire source event. A single event
		// can produce a role prelude followed by content, so account for the
		// whole batch before even the first downstream write can fail.
		for _, result := range results {
			switch value := result.Value.(type) {
			case dto.ChatCompletionsStreamResponse:
				feedChatChoices(value.Choices)
			case *dto.ChatCompletionsStreamResponse:
				if value != nil {
					feedChatChoices(value.Choices)
				}
			}
		}
		for _, result := range results {
			if !sendStreamResult(result) {
				stopStream(sr, streamErr)
				return
			}
		}
		if terminalSeen {
			sr.Done()
		}
	})

	info.StreamStatus.RequireTerminal()
	if streamErr == nil {
		streamErr = info.StreamStatus.APIError()
	}
	if streamErr == nil {
		streamErr = service.RelayRequestCancellation(c)
	}
	if streamErr == nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
		streamErr = service.NewRelayClientGoneError(info.StreamStatus.EndError)
	}
	if streamErr == nil && !terminalSeen {
		err := info.StreamStatus.EndError
		if err == nil {
			err = fmt.Errorf("Responses stream ended without a terminal event")
		}
		status := http.StatusBadGateway
		if info.StreamStatus.EndReason == relaycommon.StreamEndReasonTimeout {
			status = http.StatusGatewayTimeout
		}
		streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, status, types.ErrOptionWithSkipRetry())
	}

	usage := dto.MergeUsageNonZero(nil, state.Usage())
	tools.Finish()
	if failedUsage != nil {
		usage = dto.MergeUsageNonZero(usage, relayconvert.UsageFromResponsesUsage(failedUsage))
	}
	if streamErr != nil && !state.HasUsageText() && !dto.HasOpenAIUsageTokens(usage) {
		// A role-only prelude does not turn a failed request into prompt usage.
		// Completed hosted tools remain billable on their own without
		// manufacturing token counts when the provider reported none.
		if !tools.HasCalls() {
			usage = nil
		}
	} else if !dto.HasOpenAIUsageTokens(usage) || usage.PromptTokens == 0 || usage.CompletionTokens == 0 && state.HasUsageText() {
		var estimate *dto.Usage
		if estimator != nil {
			estimate = service.StreamingEstimate2Usage(c, estimator, info.UpstreamModelName, info.GetEstimatePromptTokens())
		} else {
			estimate = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		}
		if usage.CompletionTokens == 0 {
			usage.CompletionTokens = estimate.CompletionTokens
		}
		if usage.PromptTokens == 0 {
			usage.PromptTokens = estimate.PromptTokens
		}
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		if usage.BillingUsage != nil {
			usage.BillingUsage = dto.CloneBillingUsageWithEstimatedCompletion(usage.BillingUsage, usage.CompletionTokens)
			// Settlement prefers the original provider snapshot. Fill its missing
			// Responses input count too, without replacing cache/modality facts.
			if original := usage.BillingUsage.OpenAIUsage; original != nil && original.InputTokens == 0 && original.PromptTokens == 0 && usage.PromptTokens > 0 {
				original.InputTokens = usage.PromptTokens
				original.PromptTokens = usage.PromptTokens
				original.TotalTokens = max(original.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
				usage.BillingUsage.Estimated = true
			}
		}
	}
	state.SetUsage(usage)
	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo != nil {
		info.ClaudeConvertInfo.Usage = usage
	}
	if streamErr == nil {
		finalResults, err := service.FinalizeStreamResponse(c, info, state)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
		} else {
			for _, result := range finalResults {
				if !sendStreamResult(result) {
					break
				}
			}
		}
	}
	if streamErr == nil && info.RelayFormat == types.RelayFormatOpenAI && info.ShouldIncludeUsage && usage != nil {
		if err := helper.ObjectData(c, helper.GenerateFinalUsageResponse(responseId, createAt, info.UpstreamModelName, *usage)); err != nil {
			writeFailed(err)
		}
	}
	if streamErr == nil && info.RelayFormat == types.RelayFormatOpenAI {
		if err := helper.StringData(c, "[DONE]"); err != nil {
			writeFailed(err)
		}
	}
	if streamErr != nil {
		common.SetContextKey(c, constant.ContextKeyStreamErrorHandled, true)
		info.StreamStatus.SetAPIError(streamErr)
		clientGone := streamErr.GetErrorType() == types.ErrorTypeNewAPIError && streamErr.GetErrorCode() == service.ClientGoneErrorCode
		if !clientGone && service.RelayRequestCancellation(c) == nil {
			info.StreamStatus.MarkFailed(string(streamErr.GetErrorCode()), streamErr.ToOpenAIError().Type, streamErr.StatusCode)
			if err := HandleStreamFailure(c, info, streamErr); err != nil {
				info.StreamStatus.RecordError(err.Error())
			}
		}
		// Keep the failure visible to policy/health accounting. Relay callers
		// settle non-nil observed usage before the outer refund handles it.
		return usage, types.NewError(streamErr, streamErr.GetErrorCode(), types.ErrOptionWithSkipRetry())
	}
	return usage, nil
}
