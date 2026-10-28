package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openrouter"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/tokenkit"

	"github.com/gin-gonic/gin"
)

func sendStreamData(c *gin.Context, info *relaycommon.RelayInfo, data string, forceFormat bool, thinkToContent bool) error {
	if data == "" {
		return nil
	}

	var lastStreamResponse dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(data, &lastStreamResponse); err != nil {
		return err
	}
	if info != nil && info.OriginModelName != "" {
		info.ObserveResponseModel(lastStreamResponse.Model)
		lastStreamResponse.Model = info.OriginModelName
	}

	if !forceFormat && !thinkToContent {
		return helper.ObjectData(c, lastStreamResponse)
	}

	if !thinkToContent {
		return helper.ObjectData(c, lastStreamResponse)
	}

	hasThinkingContent := false
	hasContent := false
	var thinkingContent strings.Builder
	for _, choice := range lastStreamResponse.Choices {
		if len(choice.Delta.GetReasoningContent()) > 0 {
			hasThinkingContent = true
			thinkingContent.WriteString(choice.Delta.GetReasoningContent())
		}
		if len(choice.Delta.GetContentString()) > 0 {
			hasContent = true
		}
	}

	// Handle think to content conversion
	if info.ThinkingContentInfo.IsFirstThinkingContent {
		if hasThinkingContent {
			response := lastStreamResponse.Copy()
			for i := range response.Choices {
				// send `think` tag with thinking content
				response.Choices[i].Delta.SetContentString("<think>\n" + thinkingContent.String())
				response.Choices[i].Delta.ReasoningContent = nil
				response.Choices[i].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.IsFirstThinkingContent = false
			info.ThinkingContentInfo.HasSentThinkingContent = true
			return helper.ObjectData(c, response)
		}
	}

	if lastStreamResponse.Choices == nil || len(lastStreamResponse.Choices) == 0 {
		return helper.ObjectData(c, lastStreamResponse)
	}

	// Process each choice
	for i, choice := range lastStreamResponse.Choices {
		// Handle transition from thinking to content
		// only send `</think>` tag when previous thinking content has been sent
		if hasContent && !info.ThinkingContentInfo.SendLastThinkingContent && info.ThinkingContentInfo.HasSentThinkingContent {
			response := lastStreamResponse.Copy()
			for j := range response.Choices {
				response.Choices[j].Delta.SetContentString("\n</think>\n")
				response.Choices[j].Delta.ReasoningContent = nil
				response.Choices[j].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.SendLastThinkingContent = true
			if err := helper.ObjectData(c, response); err != nil {
				return err
			}
		}

		// Convert reasoning content to regular content if any
		if len(choice.Delta.GetReasoningContent()) > 0 {
			lastStreamResponse.Choices[i].Delta.SetContentString(choice.Delta.GetReasoningContent())
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		} else if !hasThinkingContent && !hasContent {
			// flush thinking content
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		}
	}

	return helper.ObjectData(c, lastStreamResponse)
}

func OaiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, common.LogText("invalid response or response body"))
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	model := info.UpstreamModelName
	var responseId string
	var createAt int64 = 0
	var systemFingerprint string
	var containStreamUsage bool
	var responseTextBuilder strings.Builder
	var toolCount int
	var usage = &dto.Usage{}
	var lastStreamData string
	var usageFrame string
	seenStreamToolCalls := make(map[string]struct{})
	var streamFunctionCallNames []string
	var streamFailure *types.NewAPIError
	formatFailed := false
	providerRejected := false

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		pendingData := lastStreamData
		if len(data) > 0 {
			var streamResponse struct {
				Usage *dto.Usage         `json:"usage"`
				Error *types.OpenAIError `json:"error"`
			}
			if err := common.UnmarshalJsonStr(data, &streamResponse); err == nil {
				if streamResponse.Usage != nil {
					// Usage frames are cumulative snapshots, not increments. Retain
					// them before a write fails or a later frame omits measurement.
					applyUsagePostProcessing(info, streamResponse.Usage, common.StringToByteSlice(data))
					usage = dto.MergeUsageNonZero(usage, streamResponse.Usage)
					if dto.HasOpenAIUsageTokens(usage) {
						containStreamUsage = true
						usageFrame = data
					}
				}
				if upstreamError := streamResponse.Error; upstreamError != nil &&
					(upstreamError.Type != "" || upstreamError.Message != "" || upstreamError.Code != nil) {
					providerRejected = true
					common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
					streamFailure = types.WithOpenAIError(*upstreamError, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
				}
			}

			lastStreamData = data
			observeStreamChoices(info, data, seenStreamToolCalls, &streamFunctionCallNames)
			if err := processTokenData(info, data, &responseTextBuilder, &toolCount); err != nil {
				logger.LogError(c, common.LogText("error processing stream token data: %s", err.Error()))
				sr.Error(err)
			}
		}
		// One frame is buffered for terminal-usage handling. Account for the
		// newly received frame before a write of its predecessor can fail.
		if pendingData != "" {
			if err := HandleStreamFormat(c, info, pendingData, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent); err != nil {
				common.SysLog(common.LogText("error handling stream format: %s", err.Error()))
				formatFailed = true
				streamFailure = NewStreamResponseError(info, err)
				sr.Stop(streamFailure)
			}
		}
		if providerRejected && !sr.IsStopped() {
			sr.Stop(streamFailure)
		}
	})

	info.StreamStatus.RequireTerminal()
	if streamFailure == nil {
		streamFailure = info.StreamStatus.APIError()
	}
	if streamFailure == nil {
		streamFailure = service.RelayRequestCancellation(c)
	}
	if streamFailure == nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
		streamFailure = service.NewRelayClientGoneError(info.StreamStatus.EndError)
	}
	if streamFailure == nil && info.StreamStatus.EndReason != relaycommon.StreamEndReasonDone &&
		info.StreamStatus.ResponseOutcome() != string(relaycommon.ResponseOutcomeCompleted) {
		status := http.StatusBadGateway
		if info.StreamStatus.EndReason == relaycommon.StreamEndReasonTimeout {
			status = http.StatusGatewayTimeout
		}
		streamFailure = types.NewOpenAIError(fmt.Errorf("upstream chat stream ended before a terminal event"), types.ErrorCodeBadResponse, status, types.ErrOptionWithSkipRetry())
	}

	// 处理最后的响应
	shouldSendLastResp := true
	if err := handleLastResponse(lastStreamData, &responseId, &createAt, &systemFingerprint, &model, &usage,
		&containStreamUsage, info, &shouldSendLastResp); err != nil {
		logger.LogError(c, common.LogText("error handling last response: %s, lastStreamData: [%s]", err.Error(), lastStreamData))
	}

	if usageFrame == "" {
		usageFrame = lastStreamData
	}

	hasGeneratedOutput := responseTextBuilder.Len() > 0 || toolCount > 0
	if !containStreamUsage {
		if streamFailure == nil || hasGeneratedOutput {
			usage = service.ResponseText2Usage(c, responseTextBuilder.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
			usage.CompletionTokens += toolCount * 7
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		} else {
			usage = nil
		}
	}

	if containStreamUsage && usage.CompletionTokens == 0 && (usage.PromptTokens == 0 || hasGeneratedOutput) {
		// A terminal event or an early prompt snapshot does not measure later
		// output. Retain every native fact, but do not settle zero or only the
		// old prompt after observing work whose quantity is still unavailable.
		common.SetContextKey(c, constant.ContextKeyUpstreamResponseUsageIncomplete, true)
		if streamFailure == nil {
			streamFailure = types.NewOpenAIError(fmt.Errorf("upstream chat usage is incomplete for observed output"), types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
		}
	}

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(usageFrame))

	for _, name := range streamFunctionCallNames {
		info.CountBillableToolCall(dto.BuildInCallFunctionCall, name)
	}

	if streamFailure != nil && !formatFailed && !providerRejected && !info.StreamStatus.ClientGone() && !isClientGoneStreamError(streamFailure) && service.RelayRequestCancellation(c) == nil {
		// A provider-declared failure can arrive while a valid output frame is
		// buffered. Flush that frame only while client delivery is still usable.
		if lastStreamData != "" {
			if err := HandleStreamFormat(c, info, lastStreamData, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent); err != nil {
				streamFailure = NewStreamResponseError(info, err)
			}
		}
	}
	if streamFailure == nil && info.RelayFormat == types.RelayFormatOpenAI && shouldSendLastResp {
		if err := sendStreamData(c, info, lastStreamData, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent); err != nil {
			streamFailure = NewStreamResponseError(info, err)
		}
	}
	if streamFailure == nil {
		if err := HandleFinalResponse(c, info, lastStreamData, responseId, createAt, model, systemFingerprint, usage, containStreamUsage); err != nil {
			streamFailure = NewStreamResponseError(info, err)
		}
	}
	if streamFailure != nil {
		if !containStreamUsage && !hasGeneratedOutput {
			usage = nil
		}
		common.SetContextKey(c, constant.ContextKeyStreamErrorHandled, true)
		info.StreamStatus.SetAPIError(streamFailure)
		if !info.StreamStatus.ClientGone() && !isClientGoneStreamError(streamFailure) && service.RelayRequestCancellation(c) == nil {
			if err := HandleStreamFailure(c, info, streamFailure); err != nil {
				info.StreamStatus.RecordError(err.Error())
				if helper.IsStreamWriteError(err) {
					streamFailure = NewStreamResponseError(info, err)
				}
			}
		}
		return usage, types.NewError(streamFailure, streamFailure.GetErrorCode(), types.ErrOptionWithSkipRetry())
	}

	return usage, nil
}

// observeStreamChoices collects billable function call names and records the
// finish reason facts used by health sampling from one parsed chunk.
func observeStreamChoices(info *relaycommon.RelayInfo, data string, seen map[string]struct{}, names *[]string) {
	var streamResponse dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
		return
	}
	for _, choice := range streamResponse.Choices {
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			if *choice.FinishReason == constant.FinishReasonContentFilter {
				info.PerformanceBusinessRejection = true
			}
			info.StreamStatus.MarkCompleted()
		}
		for i, tc := range choice.Delta.ToolCalls {
			name := strings.TrimSpace(tc.Function.Name)
			if name == "" {
				continue
			}
			toolIdx := i
			if tc.Index != nil {
				toolIdx = *tc.Index
			}
			fallbackKey := fmt.Sprintf("index\x00%d\x00%d\x00%s", choice.Index, toolIdx, name)
			activeKey := fmt.Sprintf("active\x00%d\x00%d\x00%s", choice.Index, toolIdx, name)
			callID := strings.TrimSpace(tc.ID)
			if callID != "" {
				idKey := fmt.Sprintf("id\x00%d\x00%s", choice.Index, callID)
				if _, ok := seen[idKey]; ok {
					continue
				}
				seen[idKey] = struct{}{}
				seen[activeKey] = struct{}{}
				if _, delayedID := seen[fallbackKey]; delayedID {
					delete(seen, fallbackKey)
					continue
				}
			} else {
				if _, ok := seen[fallbackKey]; ok {
					continue
				}
				if _, ok := seen[activeKey]; ok {
					continue
				}
				seen[fallbackKey] = struct{}{}
				seen[activeKey] = struct{}{}
			}
			*names = append(*names, name)
		}
	}
}

func OpenaiHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	var simpleResponse dto.OpenAITextResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	logger.LogDebug(c, "upstream response body: %s", responseBody)
	// Unmarshal to simpleResponse
	if info.ChannelType == constant.ChannelTypeOpenRouter && info.ChannelOtherSettings.IsOpenRouterEnterprise() {
		// 尝试解析为 openrouter enterprise
		var enterpriseResponse openrouter.OpenRouterEnterpriseResponse
		err = common.Unmarshal(responseBody, &enterpriseResponse)
		if err != nil {
			common.SetContextKey(c, constant.ContextKeyUpstreamResponseDecodeFailed, true)
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if enterpriseResponse.Success {
			responseBody = enterpriseResponse.Data
		} else {
			logger.LogError(c, common.LogText("openrouter enterprise response success=false, data: %s", enterpriseResponse.Data))
			return nil, types.NewOpenAIError(fmt.Errorf("openrouter response success=false"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}

	err = common.Unmarshal(responseBody, &simpleResponse)
	if err != nil {
		common.SetContextKey(c, constant.ContextKeyUpstreamResponseDecodeFailed, true)
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if oaiError := simpleResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	info.ObserveResponseModel(simpleResponse.Model)
	for _, choice := range simpleResponse.Choices {
		if choice.FinishReason == constant.FinishReasonContentFilter {
			info.PerformanceBusinessRejection = true
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "openai_finish_reason=content_filter")
			break
		}
	}

	for _, choice := range simpleResponse.Choices {
		for _, tc := range choice.Message.ParseToolCalls() {
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, tc.Function.Name)
		}
	}
	info.ApplyVendorToolUsage(responseBody)

	forceFormat := false
	if info.ChannelSetting.ForceFormat {
		forceFormat = true
	}

	usageModified := false
	if simpleResponse.Usage.PromptTokens == 0 {
		completionTokens := simpleResponse.Usage.CompletionTokens
		if completionTokens == 0 {
			for _, choice := range simpleResponse.Choices {
				ctkm := tokenkit.Count(info.UpstreamModelName, choice.Message.StringContent()+choice.Message.GetReasoningContent())
				completionTokens += ctkm
			}
		}
		fallbackUsage := &dto.Usage{
			PromptTokens:     info.GetEstimatePromptTokens(),
			CompletionTokens: completionTokens,
			TotalTokens:      info.GetEstimatePromptTokens() + completionTokens,
		}
		simpleResponse.Usage = *fallbackUsage
		usageModified = true
	}

	applyUsagePostProcessing(info, &simpleResponse.Usage, responseBody)

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		if usageModified {
			var bodyMap map[string]any
			err = common.Unmarshal(responseBody, &bodyMap)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
			bodyMap["usage"] = simpleResponse.Usage
			responseBody, _ = common.Marshal(bodyMap)
		}
		if forceFormat {
			responseBody, err = common.Marshal(simpleResponse)
			if err != nil {
				return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
			}
		} else {
			break
		}
	case types.RelayFormatClaude:
		convertResult, err := service.ConvertResponse(c, info, types.RelayFormatClaude, &simpleResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		claudeRespStr, err := common.Marshal(convertResult.Value)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = claudeRespStr
	case types.RelayFormatGemini:
		convertResult, err := service.ConvertResponse(c, info, types.RelayFormatGemini, &simpleResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		geminiRespStr, err := common.Marshal(convertResult.Value)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = geminiRespStr
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return &simpleResponse.Usage, nil
}
