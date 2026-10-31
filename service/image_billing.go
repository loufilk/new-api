package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ImageUsageRequiresReview prevents a delivered token-priced image from being
// settled using the synthetic one-token placeholder used by per-image prices.
// Explicit flat prices remain the administrator's contract; no token usage is
// required or invented for them.
func ImageUsageRequiresReview(info *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if info == nil {
		return false
	}
	if snap := info.TieredBillingSnapshot; snap != nil {
		// A mixed expression can estimate a fixed branch at c=0 and select
		// token pricing later. Only token-independent expressions are exempt.
		used := billingexpr.UsedVarsByHash(snap.ExprString, snap.ExprHash)
		needsInput := used["p"] || used["len"] || used["img"] || used["img_cr"] || used["cr"] || used["cc"] || used["cc1h"] || used["cc12h"]
		needsOutput := used["c"] || used["img_o"]
		return usage == nil && (needsInput || needsOutput) || usage != nil &&
			(needsInput && usage.PromptTokens <= 0 || needsOutput && usage.CompletionTokens <= 0)
	}
	if info.PriceData.UsePrice || !strings.HasPrefix(info.OriginModelName, "gpt-image-") {
		return false
	}
	return usage == nil || usage.PromptTokens <= 0 || usage.CompletionTokens <= 0
}

// PrepareImageBillingForRequest reserves the effective outbound image quantity
// before each attempt, including channel retries and parameter overrides. The
// replayable client body stays frozen; pricing uses the validated outbound
// scalar snapshot and the independent quantity for this attempt.
func PrepareImageBillingForRequest(c *gin.Context, info *relaycommon.RelayInfo, count int) *types.NewAPIError {
	if count < 1 || count > dto.MaxImageN {
		return types.NewErrorWithStatusCode(fmt.Errorf("image_count must be an integer between 1 and %d", dto.MaxImageN), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.ImageRequestCount = count
	var quota int
	var err error
	if snap := info.TieredBillingSnapshot; snap != nil && snap.BillingMode == "tiered_expr" {
		// Reevaluate all image expressions: resolution, quality or size may
		// change even when the expression does not use image_count. The
		// expression alone decides whether quantity is a multiplier.
		request := billingexpr.RequestInput{}
		if info.BillingRequestInput != nil {
			request = *info.BillingRequestInput
		}
		request.ImageCount = &count
		cost, trace, runErr := billingexpr.RunExprByHashWithRequest(snap.ExprString, snap.ExprHash, billingexpr.TokenParams{
			P: float64(snap.EstimatedPromptTokens), C: float64(snap.EstimatedCompletionTokens), Len: float64(snap.EstimatedPromptTokens),
		}, request)
		if runErr != nil {
			return types.NewErrorWithStatusCode(runErr, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		beforeGroup := cost / 1_000_000 * snap.QuotaPerUnit
		if trace.BillingUnit != billingexpr.BillingUnitRequest && snap.PreConsumeMultiplier != 0 {
			beforeGroup *= snap.PreConsumeMultiplier
		}
		quota, err = billingexpr.QuotaRoundStrict(beforeGroup * info.PriceData.GroupRatioInfo.GroupRatio)
		if err == nil {
			snap.EstimatedImageCount = trace.ImageCount
			snap.EstimatedQuotaBeforeGroup = beforeGroup
			snap.EstimatedQuotaAfterGroup = quota
			snap.EstimatedTier = trace.MatchedTier
			snap.EstimatedBillingUnit = trace.BillingUnit
			snap.EstimatedFixedPrice = trace.FixedPrice
			snap.GroupRatio = info.PriceData.GroupRatioInfo.GroupRatio
		}
	} else {
		quantity := 1
		if info.PriceData.UsePrice {
			quantity = count
		}
		// Overwrite the per-attempt ratio so a failed attempt cannot leak its
		// quantity into another channel.
		info.PriceData.AddOtherRatio("n", float64(quantity))
		base := info.ImageQuotaBeforeGroup
		if info.PriceData.UsePrice {
			base = info.PriceData.ModelPrice * common.QuotaPerUnit
		}
		quota, err = common.QuotaFromFloatStrict(info.PriceData.ApplyOtherRatiosToFloat(base * info.PriceData.GroupRatioInfo.GroupRatio))
	}
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.PriceData.QuotaToPreConsume = quota
	if quota == 0 && info.Billing == nil {
		return nil
	}
	info.PriceData.FreeModel = false
	if info.Billing == nil {
		return PreConsumeBilling(c, quota, info)
	}
	if err := info.Billing.Reserve(quota); err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) {
			return apiErr
		}
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	info.FinalPreConsumedQuota = info.Billing.GetPreConsumedQuota()
	return nil
}
