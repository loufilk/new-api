package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/privacy"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func buildUsageFromGeminiMetadata(metadata *dto.GeminiUsageMetadata, fallbackPromptTokens int) dto.Usage {
	usage := relayconvert.UsageFromGeminiMetadata(metadata, fallbackPromptTokens)
	if usage == nil {
		return dto.Usage{}
	}
	return *usage
}

func attachEstimatedGeminiBillingUsage(usage *dto.Usage) *dto.Usage {
	if usage != nil && usage.BillingUsage == nil {
		usage.BillingUsage = dto.NewEstimatedGeminiChatBillingUsage(usage)
	}
	return usage
}

// patchGeminiZeroCompletionUsage estimates completion tokens locally when upstream
// usageMetadata was billable but reported zero completion tokens even though output
// content was actually received. Typical case: the client aborts a stream before the
// final chunk that carries candidatesTokenCount, leaving prompt-only metadata; without
// this patch the output side would settle at zero quota.
func patchGeminiZeroCompletionUsage(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, responseText string, imageCount int) {
	if usage == nil || usage.CompletionTokens > 0 {
		return
	}
	if responseText == "" && imageCount == 0 {
		return
	}
	estimated := service.ResponseText2Usage(c, responseText, info.OriginModelName, usage.PromptTokens)
	usage.CompletionTokens = estimated.CompletionTokens
	if imageCount != 0 && usage.CompletionTokens == 0 {
		usage.CompletionTokens = imageCount * 1400
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	// Settlement prefers BillingUsage, so fill the missing completion in the
	// original upstream dialect without discarding cache or modality details.
	if usage.BillingUsage != nil {
		usage.BillingUsage = dto.CloneBillingUsageWithEstimatedCompletion(usage.BillingUsage, usage.CompletionTokens)
	} else {
		usage.BillingUsage = dto.NewEstimatedGeminiChatBillingUsage(usage)
	}
}

func geminiResponseUsageText(response *dto.GeminiChatResponse) string {
	if response == nil {
		return ""
	}
	var text strings.Builder
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				text.WriteString(part.Text)
			}
			// Function calls are most of an agent turn's output.
			if part.FunctionCall != nil {
				text.WriteString(part.FunctionCall.FunctionName)
				args, _ := common.Marshal(part.FunctionCall.Arguments)
				text.Write(args)
			}
		}
	}
	return text.String()
}

// geminiGroundingCounter accumulates Google Search grounding across one
// upstream response (every stream frame and candidate). Gemini 3 bills each
// unique non-empty search query ("we ignore the empty web search queries when
// counting unique queries", ai.google.dev/gemini-api/docs/google-search);
// Gemini 2.5 and older bill once per grounded prompt, counted here when the
// response carries any search query. Both quantities are recorded and the
// tool price table decides which one costs money for a model. Image-search
// queries count like web-search queries; Google documents no separate rule.
// Google documents no response field holding the billed count, so the unique
// query set is the best available evidence, not a guaranteed match.
type geminiGroundingCounter struct {
	queries map[string]struct{}
}

func (g *geminiGroundingCounter) observe(response *dto.GeminiChatResponse) {
	for _, candidate := range response.Candidates {
		metadata := candidate.GroundingMetadata
		if metadata == nil {
			continue
		}
		for _, query := range slices.Concat(metadata.WebSearchQueries, metadata.ImageSearchQueries) {
			query = strings.TrimSpace(query)
			if query == "" {
				continue
			}
			if g.queries == nil {
				g.queries = make(map[string]struct{})
			}
			g.queries[query] = struct{}{}
		}
	}
}

func (g *geminiGroundingCounter) commit(info *relaycommon.RelayInfo) {
	if len(g.queries) == 0 {
		return
	}
	info.SetBillableToolCount(dto.BuildInToolGoogleSearch, len(g.queries))
	info.SetBillableToolCount(relaycommon.GoogleSearchGroundedPromptTool, 1)
}

func countGeminiBillableFunctionCalls(info *relaycommon.RelayInfo, response *dto.GeminiChatResponse) {
	if info == nil || response == nil {
		return
	}
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall == nil {
				continue
			}
			if part.FunctionCall.WillContinue != nil && *part.FunctionCall.WillContinue {
				continue
			}
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, part.FunctionCall.FunctionName)
		}
	}
}

func buildUsageFromGeminiResponse(c *gin.Context, info *relaycommon.RelayInfo, response *dto.GeminiChatResponse) dto.Usage {
	metadata := response.GetUsageMetadata()
	if dto.HasGeminiUsageMetadataTokens(metadata) {
		usage := buildUsageFromGeminiMetadata(metadata, info.GetEstimatePromptTokens())
		patchGeminiZeroCompletionUsage(c, info, &usage, geminiResponseUsageText(response), geminiResponseInlineImageCount(response))
		return usage
	}
	usage := service.ResponseText2Usage(c, geminiResponseUsageText(response), info.OriginModelName, info.GetEstimatePromptTokens())
	attachEstimatedGeminiBillingUsage(usage)
	return *usage
}

func geminiResponseInlineImageCount(response *dto.GeminiChatResponse) int {
	if response == nil {
		return 0
	}
	count := 0
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil && part.InlineData.MimeType != "" {
				count++
			}
		}
	}
	return count
}

func responseGeminiChat2OpenAI(c *gin.Context, response *dto.GeminiChatResponse) *dto.OpenAITextResponse {
	return relayconvert.ResponseGeminiChat2OpenAI(helper.GetResponseID(c), common.GetTimestamp(), response)
}

func streamResponseGeminiChat2OpenAI(geminiResponse *dto.GeminiChatResponse) (*dto.ChatCompletionsStreamResponse, bool) {
	return relayconvert.StreamResponseGeminiChat2OpenAI(geminiResponse)
}

func handleStream(c *gin.Context, info *relaycommon.RelayInfo, resp *dto.ChatCompletionsStreamResponse) error {
	streamData, err := common.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal stream response: %w", err)
	}
	err = openai.HandleStreamFormat(c, info, string(streamData), info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent)
	if err != nil {
		return fmt.Errorf("failed to handle stream format: %w", err)
	}
	return nil
}

func handleFinalStream(c *gin.Context, info *relaycommon.RelayInfo, resp *dto.ChatCompletionsStreamResponse) error {
	streamData, err := common.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal stream response: %w", err)
	}
	return openai.HandleFinalResponse(c, info, string(streamData), resp.Id, resp.Created, resp.Model, resp.GetSystemFingerprint(), resp.Usage, false)
}

// geminiEnvelopeError checks the provider envelope before interpreting a
// successful HTTP status or frame as generated output.
func geminiEnvelopeError(c *gin.Context, body []byte) *types.NewAPIError {
	payload := ojson.GetBytes(body, "error")
	if !payload.Exists() || payload.Raw == "null" {
		return nil
	}
	envelope := dto.GeneralErrorResponse{Error: common.StringToByteSlice(payload.Raw)}
	status := http.StatusBadGateway
	var failure struct {
		Code int `json:"code"`
	}
	if common.Unmarshal(envelope.Error, &failure) == nil && failure.Code >= 400 && failure.Code <= 599 {
		status = failure.Code
	}
	common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
	if providerError := envelope.TryToOpenAIError(); providerError != nil {
		return types.WithOpenAIError(*providerError, status)
	}
	return types.NewOpenAIError(errors.New("Gemini returned an error envelope"), types.ErrorCodeBadResponseStatusCode, status)
}

func geminiBadResponse(c *gin.Context, err error) *types.NewAPIError {
	common.SetContextKey(c, constant.ContextKeyUpstreamResponseDecodeFailed, true)
	return types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
}

// emitGeminiStreamFailure owns an already-started stream's terminal error,
// leaving an uncommitted failure to the controller's normal JSON path.
func emitGeminiStreamFailure(c *gin.Context, info *relaycommon.RelayInfo, failure *types.NewAPIError) {
	if failure == nil || !c.Writer.Written() {
		return
	}
	common.SetContextKey(c, constant.ContextKeyStreamErrorHandled, true)
	if !info.StreamStatus.ClientGone() && service.RelayRequestCancellation(c) == nil {
		_ = openai.HandleStreamFailure(c, info, failure)
	}
}

func geminiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, callback func(data string, geminiResponse *dto.GeminiChatResponse) bool) (*dto.Usage, *types.NewAPIError) {
	var usage = &dto.Usage{}
	var imageCount int
	var sawResponse bool
	var hasBillableUsageMetadata bool
	var streamErr *types.NewAPIError
	var accumulatedUsageMetadata *dto.GeminiUsageMetadata
	var grounding geminiGroundingCounter
	responseText := strings.Builder{}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		body := common.StringToByteSlice(data)
		if streamErr = geminiEnvelopeError(c, body); streamErr != nil {
			if sawResponse && !hasBillableUsageMetadata {
				common.SetContextKey(c, constant.ContextKeyUpstreamResponseUsageIncomplete, true)
			}
			sr.Stop(streamErr)
			return
		}
		var geminiResponse dto.GeminiChatResponse
		if err := common.Unmarshal(body, &geminiResponse); err != nil {
			streamErr = geminiBadResponse(c, fmt.Errorf("unmarshal Gemini stream response: %w", err))
			sr.Stop(streamErr)
			return
		}
		if len(geminiResponse.Candidates) == 0 && geminiResponse.GetUsageMetadata() == nil && geminiResponse.PromptFeedback == nil {
			streamErr = geminiBadResponse(c, errors.New("invalid Gemini stream response"))
			sr.Stop(streamErr)
			return
		}
		if len(geminiResponse.Candidates) == 0 && geminiResponse.PromptFeedback != nil && geminiResponse.PromptFeedback.BlockReason != nil {
			info.StreamStatus.MarkCompleted()
			info.PerformanceBusinessRejection = true
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, fmt.Sprintf("gemini_block_reason=%s", *geminiResponse.PromptFeedback.BlockReason))
		}
		sawResponse = true
		info.ObserveResponseModel(ojson.Get(data, "modelVersion").Str)
		for _, candidate := range geminiResponse.Candidates {
			if candidate.FinishReason == nil || *candidate.FinishReason == "" || *candidate.FinishReason == "FINISH_REASON_UNSPECIFIED" {
				continue
			}
			switch *candidate.FinishReason {
			case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION":
				info.PerformanceBusinessRejection = true
			}
			info.StreamStatus.MarkCompleted()
		}

		// Commit on every frame so a client abort keeps the queries seen so far.
		grounding.observe(&geminiResponse)
		grounding.commit(info)
		countGeminiBillableFunctionCalls(info, &geminiResponse)

		// 统计图片数量
		for _, candidate := range geminiResponse.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil && part.InlineData.MimeType != "" {
					imageCount++
				}
			}
		}
		responseText.WriteString(geminiResponseUsageText(&geminiResponse))

		// 更新使用量统计
		if metadata := geminiResponse.GetUsageMetadata(); dto.HasGeminiUsageMetadataTokens(metadata) {
			accumulatedUsageMetadata = dto.MergeGeminiUsageMetadataNonZero(accumulatedUsageMetadata, metadata)
			mappedUsage := buildUsageFromGeminiMetadata(accumulatedUsageMetadata, info.GetEstimatePromptTokens())
			*usage = mappedUsage
			hasBillableUsageMetadata = true
		}

		if !callback(data, &geminiResponse) {
			if isGeminiDownstreamStop(c, info) {
				sr.Stop(nil)
				return
			}
			streamErr = geminiBadResponse(c, errors.New("Gemini stream callback stopped"))
			sr.Stop(streamErr)
		}
	})
	info.StreamStatus.RequireTerminal()
	streamStatus := info.StreamStatus
	common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, streamStatus)

	if failure := streamStatus.APIError(); failure != nil {
		streamErr = failure
	} else if cancellation := service.RelayRequestCancellation(c); cancellation != nil {
		streamErr = cancellation
	} else if streamStatus.ClientGone() {
		streamErr = service.NewRelayClientGoneError(streamStatus.EndError)
	}
	if streamErr == nil {
		switch {
		case streamStatus.EndReason == relaycommon.StreamEndReasonTimeout:
			streamErr = types.NewOpenAIError(errors.New("Gemini upstream stream timed out"), types.ErrorCodeBadResponse, http.StatusGatewayTimeout)
		case !streamStatus.IsNormalEnd(), !sawResponse,
			streamStatus.EndReason != relaycommon.StreamEndReasonDone && streamStatus.ResponseOutcome() != string(relaycommon.ResponseOutcomeCompleted):
			streamErr = geminiBadResponse(c, errors.New("Gemini upstream stream ended before completion"))
		default:
			streamStatus.MarkCompleted()
		}
	}
	if streamErr != nil {
		if streamErr.GetErrorCode() == service.ClientGoneErrorCode {
			streamStatus.MarkCancelled()
		} else {
			streamStatus.MarkFailed(string(streamErr.GetErrorCode()), streamErr.ToOpenAIError().Type, streamErr.StatusCode)
		}
		// On failure, do not turn a bare envelope or unmeasured partial
		// text into a charge. Retain only provider-reported usage.
		if hasBillableUsageMetadata {
			measured := buildUsageFromGeminiMetadata(accumulatedUsageMetadata, 0)
			usage = &measured
		}
		return usage, streamErr
	}

	if !hasBillableUsageMetadata {
		if info.ReceivedResponseCount > 0 {
			usage = service.ResponseText2Usage(c, responseText.String(), info.OriginModelName, info.GetEstimatePromptTokens())
		}
		if imageCount != 0 && usage.CompletionTokens == 0 {
			usage.CompletionTokens = imageCount * 1400
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
		}
		attachEstimatedGeminiBillingUsage(usage)
	} else {
		patchGeminiZeroCompletionUsage(c, info, usage, responseText.String(), imageCount)
	}

	return usage, nil
}

func isGeminiDownstreamStop(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
		return true
	}
	return info != nil && info.StreamStatus != nil &&
		info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone
}

func GeminiChatStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	id := helper.GetResponseID(c)
	createAt := common.GetTimestamp()
	finishReason := constant.FinishReasonStop
	toolCallIndexByChoice := make(map[int]map[string]int)
	nextToolCallIndexByChoice := make(map[int]int)

	usage, err := geminiStreamHandler(c, info, resp, func(data string, geminiResponse *dto.GeminiChatResponse) bool {
		response, isStop := streamResponseGeminiChat2OpenAI(geminiResponse)

		response.Id = id
		response.Created = createAt
		response.Model = info.OriginModelName
		if response.IsToolCall() {
			finishReason = constant.FinishReasonToolCalls
			if info.RelayFormat == types.RelayFormatClaude {
				for choiceIdx := range response.Choices {
					response.Choices[choiceIdx].FinishReason = nil
				}
			}
		}
		for choiceIdx := range response.Choices {
			choiceKey := response.Choices[choiceIdx].Index
			for toolIdx := range response.Choices[choiceIdx].Delta.ToolCalls {
				tool := &response.Choices[choiceIdx].Delta.ToolCalls[toolIdx]
				if tool.ID == "" {
					continue
				}
				m := toolCallIndexByChoice[choiceKey]
				if m == nil {
					m = make(map[string]int)
					toolCallIndexByChoice[choiceKey] = m
				}
				if idx, ok := m[tool.ID]; ok {
					tool.SetIndex(idx)
					continue
				}
				idx := nextToolCallIndexByChoice[choiceKey]
				nextToolCallIndexByChoice[choiceKey] = idx + 1
				m[tool.ID] = idx
				tool.SetIndex(idx)
			}
		}

		logger.LogDebug(c, "info.SendResponseCount = %d", info.SendResponseCount)
		if info.SendResponseCount == 0 {
			// send first response
			emptyResponse := helper.GenerateStartEmptyResponse(id, createAt, info.OriginModelName, nil)
			// Claude message_start is emitted from this first OpenAI chunk.
			// Carry upstream usage when the current Gemini frame provided it.
			emptyResponse.Usage = response.Usage
			if response.IsToolCall() {
				if len(emptyResponse.Choices) > 0 && len(response.Choices) > 0 {
					toolCalls := response.Choices[0].Delta.ToolCalls
					copiedToolCalls := make([]dto.ToolCallResponse, len(toolCalls))
					for idx := range toolCalls {
						copiedToolCalls[idx] = toolCalls[idx]
						copiedToolCalls[idx].Function.Arguments = ""
					}
					emptyResponse.Choices[0].Delta.ToolCalls = copiedToolCalls
				}
				finishReason = constant.FinishReasonToolCalls
				err := handleStream(c, info, emptyResponse)
				if err != nil {
					logger.LogError(c, err.Error())
					openai.NewStreamResponseError(info, err)
					return false
				}

				response.ClearToolCalls()
				if response.IsFinished() {
					response.Choices[0].FinishReason = nil
				}
			} else {
				err := handleStream(c, info, emptyResponse)
				if err != nil {
					logger.LogError(c, err.Error())
					openai.NewStreamResponseError(info, err)
					return false
				}
			}
		}

		err := handleStream(c, info, response)
		if err != nil {
			logger.LogError(c, err.Error())
			openai.NewStreamResponseError(info, err)
			return false
		}
		if isStop {
			if info.RelayFormat != types.RelayFormatClaude {
				if err := handleStream(c, info, helper.GenerateStopResponse(id, createAt, info.OriginModelName, finishReason)); err != nil {
					openai.NewStreamResponseError(info, err)
					return false
				}
			}
		}
		return true
	})

	if err != nil {
		emitGeminiStreamFailure(c, info, err)
		if info.StreamStatus.ClientGone() || service.RelayRequestCancellation(c) != nil {
			common.SetContextKey(c, constant.ContextKeyStreamErrorHandled, true)
		}
		return usage, err
	}

	response := helper.GenerateFinalUsageResponse(id, createAt, info.OriginModelName, *usage)
	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo != nil && !info.ClaudeConvertInfo.Done {
		response = helper.GenerateStopResponse(id, createAt, info.OriginModelName, finishReason)
		response.Usage = usage
	}
	handleErr := handleFinalStream(c, info, response)
	if handleErr != nil {
		common.SysLog(common.LogText("send final response failed: %s", handleErr.Error()))
		common.SetContextKey(c, constant.ContextKeyStreamErrorHandled, true)
		return usage, openai.NewStreamResponseError(info, handleErr)
	}
	return usage, nil
}

func GeminiChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, geminiBadResponse(c, err)
	}
	if apiErr := geminiEnvelopeError(c, responseBody); apiErr != nil {
		return nil, apiErr
	}
	var geminiResponse dto.GeminiChatResponse
	err = common.Unmarshal(responseBody, &geminiResponse)
	if err != nil {
		return nil, geminiBadResponse(c, err)
	}
	info.ObserveResponseModel(ojson.GetBytes(responseBody, "modelVersion").Str)
	var grounding geminiGroundingCounter
	grounding.observe(&geminiResponse)
	grounding.commit(info)
	countGeminiBillableFunctionCalls(info, &geminiResponse)
	if len(geminiResponse.Candidates) == 0 {
		if geminiResponse.PromptFeedback == nil || geminiResponse.PromptFeedback.BlockReason == nil || *geminiResponse.PromptFeedback.BlockReason == "" {
			return nil, geminiBadResponse(c, errors.New("Gemini response has no candidates or prompt block reason"))
		}
		usage := buildUsageFromGeminiResponse(c, info, &geminiResponse)

		info.PerformanceBusinessRejection = true
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, fmt.Sprintf("gemini_block_reason=%s", *geminiResponse.PromptFeedback.BlockReason))
		newAPIError := types.NewOpenAIError(
			errors.New("request blocked by Gemini API: "+*geminiResponse.PromptFeedback.BlockReason),
			types.ErrorCodePromptBlocked,
			http.StatusBadRequest,
		)

		service.ResetStatusCode(newAPIError, c.GetString("status_code_mapping"))

		switch info.RelayFormat {
		case types.RelayFormatClaude:
			c.JSON(newAPIError.StatusCode, gin.H{
				"type":  "error",
				"error": privacy.SanitizeClientClaudeError(c, newAPIError.StatusCode, newAPIError.ToClaudeError(common.UpstreamAddresses(c)...)),
			})
		default:
			c.JSON(newAPIError.StatusCode, gin.H{
				"error": privacy.SanitizeClientOpenAIError(c, newAPIError.StatusCode, newAPIError.ToOpenAIError(common.UpstreamAddresses(c)...)),
			})
		}
		return &usage, nil
	}
	fullTextResponse := responseGeminiChat2OpenAI(c, &geminiResponse)
	fullTextResponse.Model = info.OriginModelName
	usage := buildUsageFromGeminiResponse(c, info, &geminiResponse)

	fullTextResponse.Usage = usage

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		responseBody, err = common.Marshal(fullTextResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatClaude:
		convertResult, err := service.ConvertResponse(c, info, types.RelayFormatClaude, fullTextResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		claudeRespStr, err := common.Marshal(convertResult.Value)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = claudeRespStr
	case types.RelayFormatGemini:
		break
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return &usage, nil
}

func GeminiEmbeddingHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, geminiBadResponse(c, readErr)
	}
	if apiErr := geminiEnvelopeError(c, responseBody); apiErr != nil {
		return nil, apiErr
	}

	var geminiResponse dto.GeminiBatchEmbeddingResponse
	if jsonErr := common.Unmarshal(responseBody, &geminiResponse); jsonErr != nil {
		return nil, geminiBadResponse(c, jsonErr)
	}
	if len(geminiResponse.Embeddings) == 0 {
		return nil, geminiBadResponse(c, errors.New("Gemini response has no embeddings"))
	}

	// convert to openai format response
	openAIResponse := dto.OpenAIEmbeddingResponse{
		Object: "list",
		Data:   make([]dto.OpenAIEmbeddingResponseItem, 0, len(geminiResponse.Embeddings)),
		Model:  info.OriginModelName,
	}

	for i, embedding := range geminiResponse.Embeddings {
		if embedding == nil || len(embedding.Values) == 0 {
			return nil, geminiBadResponse(c, errors.New("Gemini response has an empty embedding"))
		}
		openAIResponse.Data = append(openAIResponse.Data, dto.OpenAIEmbeddingResponseItem{
			Object:    "embedding",
			Embedding: embedding.Values,
			Index:     i,
		})
	}

	// calculate usage
	// https://ai.google.dev/gemini-api/docs/pricing?hl=zh-cn#text-embedding-004
	// Google has not yet clarified how embedding models will be billed
	// refer to openai billing method to use input tokens billing
	// https://platform.openai.com/docs/guides/embeddings#what-are-embeddings
	var usage *dto.Usage
	if upstream := geminiResponse.UsageMetadata.GeminiUsageMetadata(); upstream != nil {
		upstreamUsage := buildUsageFromGeminiMetadata(upstream, info.GetEstimatePromptTokens())
		usage = &upstreamUsage
	} else {
		usage = service.ResponseText2Usage(c, "", info.OriginModelName, info.GetEstimatePromptTokens())
	}
	openAIResponse.Usage = *usage

	jsonResponse, jsonErr := common.Marshal(openAIResponse)
	if jsonErr != nil {
		return nil, types.NewOpenAIError(jsonErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, jsonResponse)
	return usage, nil
}

func GeminiImageHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewOpenAIError(readErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	var geminiResponse dto.GeminiImageResponse
	if jsonErr := common.Unmarshal(responseBody, &geminiResponse); jsonErr != nil {
		return nil, types.NewOpenAIError(jsonErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if len(geminiResponse.Predictions) == 0 {
		return nil, types.NewOpenAIError(errors.New("no images generated"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	// convert to openai format response
	openAIResponse := dto.ImageResponse{
		Created: common.GetTimestamp(),
		Data:    make([]dto.ImageData, 0, len(geminiResponse.Predictions)),
	}

	for _, prediction := range geminiResponse.Predictions {
		if prediction.RaiFilteredReason != "" {
			continue // skip filtered image
		}
		openAIResponse.Data = append(openAIResponse.Data, dto.ImageData{
			B64Json: prediction.BytesBase64Encoded,
		})
	}

	jsonResponse, jsonErr := common.Marshal(openAIResponse)
	if jsonErr != nil {
		return nil, types.NewError(jsonErr, types.ErrorCodeBadResponseBody)
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)

	// https://github.com/google-gemini/cookbook/blob/719a27d752aac33f39de18a8d3cb42a70874917e/quickstarts/Counting_Tokens.ipynb
	// each image has fixed 258 tokens
	const imageTokens = 258
	generatedImages := len(openAIResponse.Data)

	usage := &dto.Usage{
		PromptTokens:     imageTokens * generatedImages, // each generated image has fixed 258 tokens
		CompletionTokens: 0,                             // image generation does not calculate completion tokens
		TotalTokens:      imageTokens * generatedImages,
	}

	return usage, nil
}

type GeminiModelsResponse struct {
	Models        []dto.GeminiModel `json:"models"`
	NextPageToken string            `json:"nextPageToken"`
}

func FetchGeminiModels(baseURL, apiKey string, setting dto.ChannelSettings) ([]string, error) {
	client, err := service.GetHttpClientWithProxySettings(setting.Proxy, setting)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %v", err)
	}

	allModels := make([]string, 0)
	nextPageToken := ""
	maxPages := 100 // Safety limit to prevent infinite loops

	for range maxPages {
		url := fmt.Sprintf("%s/v1beta/models", baseURL)
		if nextPageToken != "" {
			url = fmt.Sprintf("%s?pageToken=%s", url, nextPageToken)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		request, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create request: %v", err)
		}

		request.Header.Set("x-goog-api-key", apiKey)

		response, err := service.DoRequestWithoutRedirects(client, request)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("request failed: %v", err)
		}

		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			cancel()
			return nil, fmt.Errorf("server returned error %d: %s", response.StatusCode, string(body))
		}

		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %v", err)
		}

		var modelsResponse GeminiModelsResponse
		if err = common.Unmarshal(body, &modelsResponse); err != nil {
			return nil, fmt.Errorf("failed to parse response: %v", err)
		}

		for _, model := range modelsResponse.Models {
			modelNameValue, ok := model.Name.(string)
			if !ok {
				continue
			}
			modelName := strings.TrimPrefix(modelNameValue, "models/")
			allModels = append(allModels, modelName)
		}

		nextPageToken = modelsResponse.NextPageToken
		if nextPageToken == "" {
			break
		}
	}

	return allModels, nil
}
