package service

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/shopspring/decimal"

	"github.com/gin-gonic/gin"
)

const (
	ViolationFeeCodePrefix = "violation_fee."
	CSAMViolationMarker    = "Failed check: SAFETY_CHECK_TYPE_CSAM"
)

func IsViolationFeeCode(code types.ErrorCode) bool {
	return strings.HasPrefix(string(code), ViolationFeeCodePrefix)
}

// A generic policy error is not evidence of a chargeable xAI event. Accept
// structured provider-specific evidence only after the selected xAI channel
// supplied a 4xx; keep native public error fields unchanged.
func shouldChargeViolationFee(err *types.NewAPIError, channelType int) bool {
	if err == nil || channelType != constant.ChannelTypeXai || err.StatusCode < http.StatusBadRequest || err.StatusCode >= http.StatusInternalServerError {
		return false
	}
	if err.GetErrorType() == types.ErrorTypeNewAPIError && err.GetErrorCode() == types.ErrorCodeViolationFeeGrokCSAM {
		return true
	}
	if err.GetErrorType() != types.ErrorTypeOpenAIError {
		return false
	}
	oai := err.ToOpenAIError()
	code, _ := oai.Code.(string)
	switch strings.ToLower(code) {
	case "csam", "safety_check_type_csam":
		return true
	}
	msg := strings.TrimSpace(oai.Message)
	return msg == CSAMViolationMarker || strings.HasPrefix(msg, CSAMViolationMarker+":")
}

func NormalizeViolationFeeError(err *types.NewAPIError, channelType int) *types.NewAPIError {
	if shouldChargeViolationFee(err, channelType) {
		types.ErrOptionWithSkipRetry()(err)
	}
	return err
}

func calcViolationFeeQuota(amount, groupRatio float64) (int, *common.QuotaClamp) {
	unit := common.QuotaPerUnit
	if amount <= 0 || groupRatio <= 0 || unit <= 0 ||
		math.IsNaN(amount) || math.IsInf(amount, 0) || math.IsNaN(groupRatio) || math.IsInf(groupRatio, 0) ||
		math.IsNaN(unit) || math.IsInf(unit, 0) {
		return 0, nil
	}
	return common.QuotaFromDecimalChecked(decimal.NewFromFloat(amount).
		Mul(decimal.NewFromFloat(unit)).
		Mul(decimal.NewFromFloat(groupRatio)).
		Round(0))
}

// ChargeViolationFeeIfNeeded charges an additional fee after the normal flow finishes (including refund).
// It uses Grok fee settings as the fee policy.
func ChargeViolationFeeIfNeeded(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, apiErr *types.NewAPIError) bool {
	if ctx == nil || relayInfo == nil || apiErr == nil {
		return false
	}
	//if relayInfo.IsPlayground {
	//	return false
	//}
	if relayInfo.ChannelMeta == nil || !shouldChargeViolationFee(apiErr, relayInfo.ChannelType) {
		return false
	}

	settings := model_setting.GetGrokSettings()
	if settings == nil || !settings.ViolationDeductionEnabled {
		return false
	}

	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	feeQuota, clamp := calcViolationFeeQuota(settings.ViolationDeductionAmount, groupRatio)
	noteQuotaClamp(relayInfo, clamp)
	if feeQuota <= 0 {
		return false
	}

	result, err := postConsumeQuotaWithWalletCommitPurpose(relayInfo, feeQuota, 0, true, "", "violation_fee", nil)
	if err != nil {
		logger.LogError(ctx, common.LogText("failed to charge violation fee: %s", err.Error()))
		return false
	}

	if result.Replayed {
		return true
	} // money and main-DB usage already committed

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	tokenName := ctx.GetString("token_name")
	oai := apiErr.ToOpenAIError(common.UpstreamAddresses(ctx)...)

	other := model.NewLogOther()
	other.MergePublic(map[string]any{
		"violation_fee":        true,
		"violation_fee_code":   string(types.ErrorCodeViolationFeeGrokCSAM),
		"fee_quota":            feeQuota,
		"base_amount":          settings.ViolationDeductionAmount,
		"group_ratio":          groupRatio,
		"status_code":          apiErr.StatusCode,
		"upstream_error_type":  oai.Type,
		"upstream_error_code":  fmt.Sprintf("%v", oai.Code),
		"violation_fee_marker": CSAMViolationMarker,
	})

	attachQuotaSaturation(ctx, relayInfo, other)
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:      relayInfo.ChannelId,
		ModelName:      relayInfo.OriginModelName,
		TokenName:      tokenName,
		Quota:          feeQuota,
		Content:        []*common.Message{common.NewMessage("Violation fee charged")},
		TokenId:        relayInfo.TokenId,
		UseTimeSeconds: int(useTimeSeconds),
		IsStream:       relayInfo.IsStream,
		Group:          relayInfo.UsingGroup,
		Other:          other,
	})

	return true
}
