package relay

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func ResponsesHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	var responsesReq *dto.OpenAIResponsesRequest
	switch req := info.Request.(type) {
	case *dto.OpenAIResponsesRequest:
		responsesReq = req
	case *dto.OpenAIResponsesCompactionRequest:
		// Only fields documented for POST /v1/responses/compact are forwarded:
		// model, input, instructions, previous_response_id, prompt_cache_key,
		// prompt_cache_options, prompt_cache_retention, service_tier.
		// Undocumented Codex-parity fields (tools, reasoning, text) are parsed
		// for client compatibility but intentionally not sent upstream.
		responsesReq = &dto.OpenAIResponsesRequest{
			Model:                req.Model,
			Input:                req.Input,
			Instructions:         req.Instructions,
			PreviousResponseID:   req.PreviousResponseID,
			ParallelToolCalls:    req.ParallelToolCalls,
			ServiceTier:          req.ServiceTier,
			PromptCacheKey:       req.PromptCacheKey,
			PromptCacheOptions:   req.PromptCacheOptions,
			PromptCacheRetention: req.PromptCacheRetention,
		}
	default:
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected dto.OpenAIResponsesRequest or dto.OpenAIResponsesCompactionRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	adaptor, requestBody, closer, apiErr := PrepareResponsesRequest(c, info, responsesReq)
	if apiErr != nil {
		return apiErr
	}
	defer closer.Close()

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		httpResp = resp.(*http.Response)

		if httpResp.StatusCode != http.StatusOK {
			newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			// reset status code 重置状态码
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}

	common.SetContextKey(c, constant.ContextKeyResponsesPartialUsage, false)
	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		partialUsage, _ := usage.(*dto.Usage)
		if preserveUnmeasuredStreamBilling(c, info, partialUsage) {
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
		// Native failures can still carry generated output or completed tools.
		// Settle those facts once before the outer error handler refunds; empty
		// explicit failures return nil usage and remain fully refundable.
		if partialUsage, ok := usage.(*dto.Usage); ok && partialUsage != nil && (!info.IsStream && common.GetContextKeyBool(c, constant.ContextKeyResponsesPartialUsage) ||
			info.IsStream && info.ReceivedResponseCount > 0 && common.GetContextKeyBool(c, constant.ContextKeyResponsesStreamErrorHandled)) {
			ConsumeResponsesQuota(c, info, partialUsage)
		}
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	usageDto := usage.(*dto.Usage)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact {
		originModelName := info.OriginModelName
		originPriceData := info.PriceData

		_, err := helper.ModelPriceHelper(c, info, info.GetEstimatePromptTokens(), &types.TokenCountMeta{})
		if err != nil {
			info.OriginModelName = originModelName
			info.PriceData = originPriceData
			return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithSkipRetry(), types.ErrOptionWithStatusCode(http.StatusBadRequest))
		}
		service.PostTextConsumeQuota(c, info, usageDto, nil)

		info.OriginModelName = originModelName
		info.PriceData = originPriceData
		return nil
	}

	ConsumeResponsesQuota(c, info, usageDto)
	return nil
}

// prepareResponsesPassThroughBody preserves the original Responses payload when
// a channel has no parameter overrides. If overrides are configured, it applies
// them to the replayable raw JSON body so pass-through channels behave the same
// as the Chat Completions and Claude relay paths.
func prepareResponsesPassThroughBody(c *gin.Context, info *relaycommon.RelayInfo) (common.ReplayableBody, io.Closer, *types.NewAPIError) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, nil, types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) == 0 {
		return common.NewReplayableBodyReader(storage), nil, nil
	}

	jsonData, err := storage.Bytes()
	if err != nil {
		return nil, nil, types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
	if err != nil {
		return nil, nil, newAPIErrorFromParamOverride(err)
	}
	logger.LogDebug(c, "responses pass-through request body after param override: %s", jsonData)
	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return body, closer, nil
}

// ConsumeResponsesQuota applies the same settlement dispatch to HTTP and
// WebSocket Responses usage. Compact requests keep their separate repricing.
func ConsumeResponsesQuota(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) {
	if preserveUnmeasuredStreamBilling(c, info, usage) {
		return
	}
	c.Set(requestBillingUsageObservedKey, true)
	if usage == nil && info.StreamStatus != nil {
		outcome := info.StreamStatus.ResponseOutcome()
		if outcome == string(relaycommon.ResponseOutcomeFailed) || outcome == string(relaycommon.ResponseOutcomeCancelled) {
			// The native accumulator returns nil only for explicit failures
			// without billable work. WebSocket calls also need their reserve
			// released here; successful nil usage retains normal estimation.
			if err := service.SettleBilling(c, info, 0); err != nil {
				logger.LogError(c, "failed to release empty Responses failure reservation: "+err.Error())
				if info.Billing != nil {
					info.Billing.Refund(c)
				}
			}
			return
		}
	}
	if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
		service.PostAudioConsumeQuota(c, info, usage, nil)
		return
	}
	service.PostTextConsumeQuota(c, info, usage, nil)
}
