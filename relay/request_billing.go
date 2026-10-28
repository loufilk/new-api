package relay

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

const requestBillingUsageObservedKey = "request_billing_usage_observed"
const requestBillingDeliveryUnknownKey = "request_billing_delivery_unknown"
const requestBillingNotSubmittedKey = "request_billing_not_submitted"

// PrepareRequestBilling estimates and reserves one request's charge. Transports
// provide the current request body through BodyStorage or BillingRequestInput;
// channel retries retain the resulting billing session and pricing snapshot.
func PrepareRequestBilling(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	needSensitiveCheck := setting.ShouldCheckPromptSensitive()
	meta := &types.TokenCountMeta{TokenType: types.TokenTypeTokenizer}
	if info.Request != nil && (needSensitiveCheck || constant.CountToken) {
		meta = info.Request.GetTokenCountMeta()
	} else {
		// Avoid building CombineText when only the pricing quantities are needed.
		switch request := info.Request.(type) {
		case *dto.GeneralOpenAIRequest:
			meta.MaxTokens = int(max(lo.FromPtr(request.MaxTokens), lo.FromPtr(request.MaxCompletionTokens)))
		case *dto.OpenAIResponsesRequest:
			meta.MaxTokens = int(lo.FromPtr(request.MaxOutputTokens))
		case *dto.ClaudeRequest:
			meta.MaxTokens = int(lo.FromPtr(request.MaxTokens))
		case *dto.ImageRequest:
			meta = request.GetTokenCountMeta()
		}
	}

	if meta != nil {
		if apiErr := service.CheckRequestSensitiveText(c, meta.CombineText); apiErr != nil {
			return apiErr
		}
	}

	tokens, err := service.EstimateRequestToken(c, meta, info)
	if err != nil {
		return types.NewError(err, types.ErrorCodeCountTokenFailed)
	}
	info.SetEstimatePromptTokens(tokens)

	priceData, err := helper.ModelPriceHelper(c, info, tokens, meta)
	if err != nil {
		return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest))
	}
	if priceData.FreeModel {
		if info.RelayFormat == types.RelayFormatOpenAIRealtime {
			return service.PreConsumeFreeRealtimeBilling(c, info)
		}
		logger.LogInfo(c, common.LogText("model %s is free, skipping pre-consume", info.OriginModelName))
		return nil
	}
	return service.PreConsumeBilling(c, priceData.QuotaToPreConsume, info)
}

// RefundFailedRequestBilling applies the common final-failure policy after all
// eligible attempts have ended. A settled BillingSession never refunds again.
func RefundFailedRequestBilling(c *gin.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) *types.NewAPIError {
	if apiErr == nil {
		return nil
	}
	apiErr = service.NormalizeViolationFeeError(apiErr)
	apiErr = PreserveFailedRequestBilling(c, info, apiErr)
	if c.GetBool(requestBillingDeliveryUnknownKey) {
		return apiErr
	}
	if info.Billing != nil {
		info.Billing.Refund(c)
	}
	service.ChargeViolationFeeIfNeeded(c, info, apiErr)
	return apiErr
}

// PreserveFailedRequestBilling runs before retry selection as well as the final
// failure defer. An ambiguous completed send must never start another generation.
// A typed DNS/dial failure proves no request was submitted and remains retryable.
func PreserveFailedRequestBilling(c *gin.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) *types.NewAPIError {
	if apiErr == nil {
		return nil
	}
	if !c.GetBool(requestBillingUsageObservedKey) && !c.GetBool(requestBillingDeliveryUnknownKey) {
		preserveUnmeasuredStreamBilling(c, info, nil)
		var dialErr *net.OpError
		var dnsErr *net.DNSError
		definitelyUnsent := common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent) ||
			errors.As(apiErr, &dnsErr) || errors.As(apiErr, &dialErr) && dialErr.Op == "dial"
		if info.RelayFormat == types.RelayFormatOpenAIRealtime && info.TargetWs == nil {
			definitelyUnsent = true // Realtime DoRequest only opens the socket; no generation was submitted.
		}
		// A cancellation before submission is unused; after an attempted send,
		// absence of response bytes is not evidence that upstream did no work.
		if !definitelyUnsent && !common.GetContextKeyBool(c, constant.ContextKeyUpstreamExplicitRejection) &&
			!c.GetBool(requestBillingDeliveryUnknownKey) && !c.GetBool(requestBillingNotSubmittedKey) && info.StreamStatus == nil &&
			common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted) &&
			(apiErr.GetErrorCode() == service.ClientGoneErrorCode || errors.Is(apiErr, context.Canceled) || errors.Is(apiErr, context.DeadlineExceeded) ||
				apiErr.GetErrorCode() == types.ErrorCodeReadResponseBodyFailed || apiErr.GetErrorCode() == types.ErrorCodeDoRequestFailed ||
				common.GetContextKeyBool(c, constant.ContextKeyUpstreamResponseDecodeFailed)) {
			preserveUnknownRequestBilling(c, info)
		}
	}
	if c.GetBool(requestBillingDeliveryUnknownKey) {
		return types.NewError(apiErr, apiErr.GetErrorCode(), types.ErrOptionWithSkipRetry())
	}
	return apiErr
}

// consumePartialTextStreamQuota settles observed usage from a failed upstream
// stream before the outer refund path, including a buffered nonstream response.
// Missing usage on an interrupted transport remains unknown, not confirmed free.
func consumePartialTextStreamQuota(c *gin.Context, info *relaycommon.RelayInfo, rawUsage any) {
	usage, ok := rawUsage.(*dto.Usage)
	if info == nil {
		return
	}
	if preserveUnmeasuredStreamBilling(c, info, usage) || !ok || usage == nil {
		return
	}
	streamFailure := info.IsStream && common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled)
	bufferedFailure := !info.IsStream && common.GetContextKeyBool(c, constant.ContextKeyResponsesPartialUsage)
	if !streamFailure && !bufferedFailure {
		return
	}
	c.Set(requestBillingUsageObservedKey, true)
	containsAudio := usage.CompletionTokenDetails.AudioTokens > 0 || usage.PromptTokensDetails.AudioTokens > 0
	hasAudioPrice := ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)
	if info.RelayFormat == types.RelayFormatOpenAI && containsAudio && hasAudioPrice {
		service.PostAudioConsumeQuota(c, info, usage, nil)
		return
	}
	service.PostTextConsumeQuota(c, info, usage, nil)
}

// preserveUnmeasuredStreamBilling separates an interrupted transport without
// accounting evidence from an explicit protocol rejection or measured partial
// response. A durable review owner prevents both a guessed charge and a refund.
func preserveUnmeasuredStreamBilling(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if info == nil || info.StreamStatus == nil {
		return false
	}
	if common.GetContextKeyBool(c, constant.ContextKeyUpstreamResponseUsageIncomplete) {
		preserveUnknownRequestBilling(c, info)
		return true
	}
	if dto.HasOpenAIUsageTokens(usage) {
		if usage.PromptTokens > 0 || usage.CompletionTokens > 0 || usage.UsageSemantic == "anthropic" ||
			usage.UsageSemantic == "" && info.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
			return false
		}
		// OpenAI detail/total-only snapshots prove work, but the settlement
		// path requires input/output aggregates to price it. Preserve those
		// facts for review instead of turning incomplete measurement into zero.
		preserveUnknownRequestBilling(c, info)
		return true
	}
	if common.GetContextKeyBool(c, constant.ContextKeyUpstreamExplicitRejection) {
		return false
	}
	if info.ResponsesUsageInfo != nil {
		for _, tool := range info.ResponsesUsageInfo.BuiltInTools {
			if tool != nil && tool.CallCount > 0 {
				return false
			}
		}
	}
	status := info.StreamStatus.OutcomeSnapshot()
	switch status.Response {
	case relaycommon.ResponseOutcomeCompleted, relaycommon.ResponseOutcomeIncomplete:
		return false
	case relaycommon.ResponseOutcomeFailed:
		if status.ErrorCode != string(types.ErrorCodeBadResponse) && status.ErrorCode != string(types.ErrorCodeBadResponseBody) && status.ErrorCode != string(types.ErrorCodeJsonMarshalFailed) {
			return false
		}
		// Conversion/decoding failures are locally generated, not a provider
		// rejection proving the request unused.
	case relaycommon.ResponseOutcomeCancelled:
		if !info.StreamStatus.ClientGone() && service.RelayRequestCancellation(c) == nil {
			return false // An explicit provider cancellation, not client delivery loss.
		}
	}
	if status.Response != relaycommon.ResponseOutcomeFailed && status.EndReason == relaycommon.StreamEndReasonDone &&
		!common.GetContextKeyBool(c, constant.ContextKeyResponsesStreamErrorHandled) && !common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled) &&
		!info.StreamStatus.ClientGone() && service.RelayRequestCancellation(c) == nil {
		return false
	}
	if status.EndReason == relaycommon.StreamEndReasonNone && !status.ExpectsTerminal && info.StreamStatus.APIError() == nil && service.RelayRequestCancellation(c) == nil {
		return false
	}
	preserveUnknownRequestBilling(c, info)
	return true
}

func preserveUnknownRequestBilling(c *gin.Context, info *relaycommon.RelayInfo) {
	if c.GetBool(requestBillingDeliveryUnknownKey) {
		return
	}
	c.Set(requestBillingDeliveryUnknownKey, true)
	service.PreserveUnknownBillingDelivery(info)
	// The service logs any failed durable admission with the frozen request ID.
	// This diagnostic describes a review requirement, never a successful close.
	logger.LogError(c, "billing usage unavailable after upstream delivery; manual reconciliation required")
}
