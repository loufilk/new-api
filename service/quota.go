package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type TokenDetails struct {
	TextTokens  int
	AudioTokens int
}

type QuotaInfo struct {
	InputDetails  TokenDetails
	OutputDetails TokenDetails
	ModelName     string
	UsePrice      bool
	ModelPrice    float64
	ModelRatio    float64
	GroupRatio    float64
}

func hasCustomModelRatio(modelName string, currentRatio float64) bool {
	defaultRatio, exists := ratio_setting.GetDefaultModelRatioMap()[modelName]
	if !exists {
		return true
	}
	return currentRatio != defaultRatio
}

func calculateAudioQuota(info QuotaInfo) (int, *common.QuotaClamp) {
	if info.UsePrice {
		modelPrice := decimal.NewFromFloat(info.ModelPrice)
		quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		groupRatio := decimal.NewFromFloat(info.GroupRatio)

		quota := modelPrice.Mul(quotaPerUnit).Mul(groupRatio)
		return common.QuotaFromDecimalChecked(quota)
	}

	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(info.ModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(info.ModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(info.ModelName))

	groupRatio := decimal.NewFromFloat(info.GroupRatio)
	modelRatio := decimal.NewFromFloat(info.ModelRatio)
	ratio := groupRatio.Mul(modelRatio)

	inputTextTokens := decimal.NewFromInt(int64(info.InputDetails.TextTokens))
	outputTextTokens := decimal.NewFromInt(int64(info.OutputDetails.TextTokens))
	inputAudioTokens := decimal.NewFromInt(int64(info.InputDetails.AudioTokens))
	outputAudioTokens := decimal.NewFromInt(int64(info.OutputDetails.AudioTokens))

	quota := decimal.Zero
	quota = quota.Add(inputTextTokens)
	quota = quota.Add(outputTextTokens.Mul(completionRatio))
	quota = quota.Add(inputAudioTokens.Mul(audioRatio))
	quota = quota.Add(outputAudioTokens.Mul(audioRatio).Mul(audioCompletionRatio))

	quota = quota.Mul(ratio)

	// If ratio is not zero and quota is less than or equal to zero, set quota to 1
	if !ratio.IsZero() && quota.LessThanOrEqual(decimal.Zero) {
		quota = decimal.NewFromInt(1)
	}

	return common.QuotaFromDecimalChecked(quota)
}

func PreWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage) error {
	if relayInfo == nil || usage == nil {
		return errors.New("missing realtime billing usage")
	}
	var normalized dto.RealtimeUsage
	if err := dto.AddRealtimeUsage(&normalized, usage); err != nil {
		PreserveUnknownBillingDelivery(relayInfo)
		return err
	}
	usage = &normalized

	billingModelName := relayInfo.GetBillingModelName()
	textInputTokens := usage.InputTokenDetails.TextTokens
	textOutTokens := usage.OutputTokenDetails.TextTokens
	audioInputTokens := usage.InputTokenDetails.AudioTokens
	audioOutTokens := usage.OutputTokenDetails.AudioTokens
	groupRatio := ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
	modelRatio, _, _ := ratio_setting.GetModelRatio(billingModelName)

	autoGroup, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroup)
	if exists {
		groupRatio = ratio_setting.GetGroupRatio(autoGroup.(string))
		logger.LogDebug(ctx, "final group ratio: %f", groupRatio)
		relayInfo.UsingGroup = autoGroup.(string)
	}

	actualGroupRatio := groupRatio
	userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup)
	if ok {
		actualGroupRatio = userGroupRatio
	}

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  billingModelName,
		UsePrice:   relayInfo.UsePrice,
		ModelRatio: modelRatio,
		GroupRatio: actualGroupRatio,
	}

	quota, clamp := calculateAudioQuota(quotaInfo)
	noteQuotaClamp(relayInfo, clamp)

	if relayInfo.UsePrice || relayInfo.PriceData.FreeModel {
		quota = 0
	}
	session, ok := relayInfo.Billing.(*BillingSession)
	if !ok {
		return errors.New("realtime billing session missing")
	}
	if err := session.reserveObservedRealtime(usage, quota); err != nil {
		return err
	}

	logger.LogInfo(ctx, "realtime streaming consume quota success, quota: "+fmt.Sprintf("%d", quota))
	return nil
}

func PostWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelName string,
	usage *dto.RealtimeUsage, extraContent []*common.Message) {

	if relayInfo == nil || usage == nil {
		return
	}
	var normalized dto.RealtimeUsage
	if err := dto.AddRealtimeUsage(&normalized, usage); err != nil {
		PreserveUnknownBillingDelivery(relayInfo)
		return
	}
	usage = &normalized
	quota, tieredResult := realtimeActualQuota(relayInfo, usage)

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()

	tokenName := ctx.GetString("token_name")
	billingModelName := relayInfo.GetBillingModelName()
	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(billingModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(billingModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(billingModelName))

	modelRatio, _, _ := ratio_setting.GetModelRatio(billingModelName)
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	totalTokens := usage.TotalTokens
	var logContent []*common.Message
	if !usePrice {
		logContent = append(logContent, common.NewMessage("Model ratio {{model_ratio}}, completion ratio {{completion_ratio}}, audio ratio {{audio_ratio}}, audio completion ratio {{audio_completion_ratio}}, group ratio {{group_ratio}}", map[string]any{
			"model_ratio":            fmt.Sprintf("%.2f", modelRatio),
			"completion_ratio":       fmt.Sprintf("%.2f", completionRatio.InexactFloat64()),
			"audio_ratio":            fmt.Sprintf("%.2f", audioRatio.InexactFloat64()),
			"audio_completion_ratio": fmt.Sprintf("%.2f", audioCompletionRatio.InexactFloat64()),
			"group_ratio":            fmt.Sprintf("%.2f", groupRatio),
		}))
	} else {
		logContent = append(logContent, common.NewMessage("Model price {{model_price}}, group ratio {{group_ratio}}", map[string]any{
			"model_price": fmt.Sprintf("%.2f", modelPrice),
			"group_ratio": fmt.Sprintf("%.2f", groupRatio),
		}))
	}

	// record all the consume log even if quota is 0
	if totalTokens == 0 {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent = append(logContent, common.NewMessage("Possibly an upstream timeout"))
		logger.LogError(ctx, common.LogText("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d",
			relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	}

	session, atomicSession := relayInfo.Billing.(*BillingSession)
	atomicSession = atomicSession && session.durableTerminalSource()
	if atomicSession {
		if err := session.freezeRealtimeUsage(usage, quota); err != nil {
			logger.LogError(ctx, "realtime usage cannot be reconciled: "+err.Error())
			PreserveUnknownBillingDelivery(relayInfo)
			return
		}
	} else if totalTokens != 0 {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}
	settleErr := SettleBilling(ctx, relayInfo, quota)
	if settleErr != nil {
		logger.LogError(ctx, "error settling billing: "+settleErr.Error())
		return
	}
	if atomicSession && !session.claimRealtimeDisplay() {
		return
	}

	logModel := modelName
	logContent = append(logContent, extraContent...)
	other := GenerateWssOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	// P1-2：钱包净份额记录（仅结算成功）
	if relayInfo.BillingSource == BillingSourceWallet && settleErr == nil {
		FillWalletCostLogInfo(other, relayInfo.UserId, RelayWalletCostRefs(relayInfo))
	}
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
}

func CalcOpenRouterCacheCreateTokens(usage dto.Usage, priceData types.PriceData) int {
	if priceData.CacheCreationRatio == 1 {
		return 0
	}
	quotaPrice := priceData.ModelRatio / common.QuotaPerUnit
	promptCacheCreatePrice := quotaPrice * priceData.CacheCreationRatio
	promptCacheReadPrice := quotaPrice * priceData.CacheRatio
	completionPrice := quotaPrice * priceData.CompletionRatio

	cost, _ := usage.Cost.(float64)
	totalPromptTokens := float64(usage.PromptTokens)
	completionTokens := float64(usage.CompletionTokens)
	promptCacheReadTokens := float64(usage.PromptTokensDetails.CachedTokens)

	value := (cost -
		totalPromptTokens*quotaPrice +
		promptCacheReadTokens*(quotaPrice-promptCacheReadPrice) -
		completionTokens*completionPrice) /
		(promptCacheCreatePrice - quotaPrice)
	quota, clamp := common.QuotaRoundChecked(value)
	if clamp != nil {
		return -1
	}
	return quota
}

func PostAudioConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent []*common.Message) {
	if usage == nil {
		usage = &dto.Usage{PromptTokens: relayInfo.GetEstimatePromptTokens(), TotalTokens: relayInfo.GetEstimatePromptTokens()}
	}

	var tieredUsedVars map[string]bool
	if snap := relayInfo.TieredBillingSnapshot; snap != nil {
		tieredUsedVars = billingexpr.UsedVarsByHash(snap.ExprString, snap.ExprHash)
	}
	var tieredResult *billingexpr.TieredResult
	tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, BuildTieredTokenParams(usage, false, tieredUsedVars))
	if tieredOk {
		tieredResult = tieredRes
	}
	fixedPriceBilling := tieredOk && isFixedPriceSettlement(relayInfo, tieredRes)

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	textInputTokens := usage.PromptTokensDetails.TextTokens
	textOutTokens := usage.CompletionTokenDetails.TextTokens

	audioInputTokens := usage.PromptTokensDetails.AudioTokens
	audioOutTokens := usage.CompletionTokenDetails.AudioTokens

	tokenName := ctx.GetString("token_name")
	billingModelName := relayInfo.GetBillingModelName()
	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(billingModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(billingModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(billingModelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  billingModelName,
		UsePrice:   usePrice,
		ModelPrice: modelPrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}

	quota, clamp := calculateAudioQuota(quotaInfo)
	noteQuotaClamp(relayInfo, clamp)
	if tieredOk {
		quota = tieredQuota
	}

	totalTokens := usage.TotalTokens
	var logContent []*common.Message
	if !usePrice {
		logContent = append(logContent, common.NewMessage("Model ratio {{model_ratio}}, completion ratio {{completion_ratio}}, audio ratio {{audio_ratio}}, audio completion ratio {{audio_completion_ratio}}, group ratio {{group_ratio}}", map[string]any{
			"model_ratio":            fmt.Sprintf("%.2f", modelRatio),
			"completion_ratio":       fmt.Sprintf("%.2f", completionRatio.InexactFloat64()),
			"audio_ratio":            fmt.Sprintf("%.2f", audioRatio.InexactFloat64()),
			"audio_completion_ratio": fmt.Sprintf("%.2f", audioCompletionRatio.InexactFloat64()),
			"group_ratio":            fmt.Sprintf("%.2f", groupRatio),
		}))
	} else {
		logContent = append(logContent, common.NewMessage("Model price {{model_price}}, group ratio {{group_ratio}}", map[string]any{
			"model_price": fmt.Sprintf("%.2f", modelPrice),
			"group_ratio": fmt.Sprintf("%.2f", groupRatio),
		}))
	}

	// record all the consume log even if quota is 0
	if totalTokens == 0 && !fixedPriceBilling {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent = append(logContent, common.NewMessage("Possibly an upstream timeout"))
		logger.LogError(ctx, common.LogText("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d",
			relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, billingModelName, relayInfo.FinalPreConsumedQuota))
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}

	settleErr := SettleBilling(ctx, relayInfo, quota)
	if settleErr != nil {
		logger.LogError(ctx, "error settling billing: "+settleErr.Error())
	}

	logModel := billingModelName
	logContent = append(logContent, extraContent...)
	other := GenerateAudioOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	// P1-2：钱包净份额记录（仅结算成功）
	if relayInfo.BillingSource == BillingSourceWallet && settleErr == nil {
		FillWalletCostLogInfo(other, relayInfo.UserId, RelayWalletCostRefs(relayInfo))
	}
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
	relayInfo.PerformanceOutputTokens = int64(usage.CompletionTokens)
}

func PreConsumeTokenQuota(relayInfo *relaycommon.RelayInfo, quota int) error {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if relayInfo.IsPlayground {
		return nil
	}
	// 原子预扣：检查与扣减在同一操作中完成，并发请求不可能同时通过检查后超扣。
	reserved, err := model.TryReserveTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota, relayInfo.TokenUnlimited)
	if err != nil {
		return err
	}
	if !reserved {
		remainQuota := 0
		if token, tokenErr := model.GetTokenByKey(relayInfo.TokenKey, false); tokenErr == nil && token != nil {
			remainQuota = token.RemainQuota
		}
		return fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuota(remainQuota), logger.FormatQuota(quota))
	}
	return nil
}

type postConsumeQuotaResult struct {
	Replayed       bool
	FundingApplied bool
	TokenApplied   bool
}

func PostConsumeQuota(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool) error {
	_, err := postConsumeQuotaWithResult(relayInfo, quota, preConsumedQuota, sendEmail)
	return err
}

func postConsumeQuotaWithResult(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool) (result postConsumeQuotaResult, err error) {
	return postConsumeQuotaWithOperation(relayInfo, quota, preConsumedQuota, sendEmail, "")
}

func postConsumeQuotaWithOperation(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool, operationSuffix string) (result postConsumeQuotaResult, err error) {
	return postConsumeQuotaWithWalletCommit(relayInfo, quota, preConsumedQuota, sendEmail, operationSuffix, nil)
}

// commit persists a task's refund receipt inside the wallet/token transaction.
// It runs for replays too, so callers can recover the already committed state.
func postConsumeQuotaWithWalletCommit(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool, operationSuffix string, commit func(*gorm.DB, *model.LedgerResult) error) (result postConsumeQuotaResult, err error) {
	return postConsumeQuotaWithWalletCommitPurpose(relayInfo, quota, preConsumedQuota, sendEmail, operationSuffix, "", commit)
}

// purpose only adds financial provenance. It does not change fee policy, amount,
// timing, reason, idempotency key or signature. Unmarked historical operations
// are never retroactively inferred to be independent fees.
func postConsumeQuotaWithWalletCommitPurpose(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool, operationSuffix, purpose string, commit func(*gorm.DB, *model.LedgerResult) error) (result postConsumeQuotaResult, err error) {
	if relayInfo == nil || relayInfo.RequestId == "" || strings.TrimSpace(relayInfo.RequestId) != relayInfo.RequestId || len(relayInfo.RequestId) > 64 || quota < -common.MaxQuota || quota > common.MaxQuota {
		return result, errors.New("invalid billing adjustment")
	}
	if purpose != "" && (purpose != "violation_fee" || quota <= 0 || preConsumedQuota != 0 || commit != nil) {
		return result, errors.New("invalid independent billing purpose")
	}

	if quota == 0 && commit == nil {
		return result, nil
	}

	// 1) Consume from wallet quota OR subscription item
	if relayInfo != nil && relayInfo.BillingSource == BillingSourceSubscription {
		if commit != nil {
			return result, errors.New("wallet commit callback does not support subscriptions")
		}
		if operationSuffix == "" && purpose == "" {
			if relayInfo.Billing == nil {
				return result, errors.New("subscription adjustment has no durable billing owner")
			}
			err = relayInfo.Billing.Settle(preConsumedQuota + quota)
			return postConsumeQuotaResult{FundingApplied: err == nil, TokenApplied: err == nil && !relayInfo.IsPlayground}, err
		}
		if quota == 0 {
			return result, nil
		}
		if quota < 0 || relayInfo.SubscriptionId <= 0 || relayInfo.SubscriptionQuotaPeriod <= 0 {
			return result, errors.New("independent subscription adjustment has no original period evidence")
		}
		applied, applyErr := applySubscriptionSupplement(relayInfo, quota, operationSuffix, purpose)
		if applyErr != nil {
			return result, applyErr
		}
		result.Replayed = !applied
		result.FundingApplied, result.TokenApplied = true, !relayInfo.IsPlayground

	} else {
		// Wallet：差额结算写账本（幂等键 = request_id + settle / settle_refund）。
		// P0-5：扣减与退款均按模型赠金资格确定账户顺序/份额（与预扣一致）。
		entry := model.LedgerEntry{
			UserId:        relayInfo.UserId,
			Account:       model.AccountPaid,
			Reason:        model.LedgerReasonBillingSettle,
			ReferenceType: "relay",
			ReferenceId:   relayInfo.RequestId,
			RequestId:     relayInfo.RequestId,
		}
		if purpose == "violation_fee" {
			entry.SetMetadata(map[string]interface{}{"independent_fee": "violation_fee"})
		}
		operationPart := ""
		if operationSuffix != "" {
			operationPart = ":" + operationSuffix
		}
		// Resolve model policy before opening the wallet transaction. The policy
		// lookup may hit model.DB; doing that from inside a transaction can block
		// forever on single-connection SQLite test/runtime configurations.
		debitOrder := WalletDebitOrder(relayInfo)
		var ledgerResult *model.LedgerResult
		var outstanding []model.LedgerShare
		for attempt := 0; attempt < 2; attempt++ {
			ledgerResult = nil
			err = model.DB.Transaction(func(tx *gorm.DB) error {
				if quota > 0 {
					entry.Delta = quota
					entry.IdempotencyKey = fmt.Sprintf("billing:settle:%s%s:%d", relayInfo.RequestId, operationPart, quota)
					ledgerResult, err = model.DebitQuotaTx(tx, entry, debitOrder...)
				} else {
					refund := -quota
					entry.IdempotencyKey = fmt.Sprintf("billing:settle_refund:%s%s:%d", relayInfo.RequestId, operationPart, refund)
					ledgerResult, outstanding, err = model.CreditWalletRefundTx(tx, entry, refund)
				}
				if err != nil {
					return err
				}
				if ledgerResult != nil && !ledgerResult.Replayed && !relayInfo.IsPlayground {
					if err := model.SettleTokenQuotaDeltaTx(tx, relayInfo.TokenId, relayInfo.UserId, relayInfo.TokenKey, -quota); err != nil {
						return err
					}
				}
				if purpose == "violation_fee" && ledgerResult != nil && !ledgerResult.Replayed {
					if err := model.ApplyTaskUsageDeltaTx(tx, relayInfo.UserId, relayInfo.ChannelId, quota, 1); err != nil {
						return err
					}
				}
				if commit != nil {
					return commit(tx, ledgerResult)
				}
				return nil
			})
			if !errors.Is(err, model.ErrLedgerDuplicate) {
				break
			}
		}
		if err != nil {
			if quota > 0 && !WalletBonusEligible(relayInfo) && errors.Is(err, model.ErrLedgerInsufficientBalance) {
				return result, ErrModelBonusNotSupported
			}
			return result, err
		}
		if ledgerResult != nil {
			result.Replayed = ledgerResult.Replayed
			model.RefreshUserQuotaCacheAfterBillingFinalization(relayInfo.UserId)
			if quota > 0 && !ledgerResult.Replayed {
				relayInfo.WalletDebitShares = appendShares(relayInfo.WalletDebitShares, ledgerResult)
			} else if quota < 0 {
				relayInfo.WalletDebitShares = make([]relaycommon.WalletDebitShare, 0, len(outstanding))
				for _, share := range outstanding {
					relayInfo.WalletDebitShares = append(relayInfo.WalletDebitShares, relaycommon.WalletDebitShare{Account: string(share.Account), Amount: share.Amount})
				}
			}
		}
		result.FundingApplied = true
		result.TokenApplied = !relayInfo.IsPlayground
	}

	if sendEmail && !result.Replayed {
		if (quota + preConsumedQuota) != 0 {
			checkAndSendQuotaNotify(relayInfo, quota, preConsumedQuota)
		}
	}

	return result, nil
}

func checkAndSendQuotaNotify(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int) {
	gopool.Go(func() {
		userSetting := relayInfo.UserSetting
		// UserQuota is the balance before this request; quota plus
		// preConsumedQuota is what the request finally cost.
		balanceAfter := relayInfo.UserQuota - (quota + preConsumedQuota)
		if !quotaReminderDue(userSetting, int64(relayInfo.UserQuota), int64(balanceAfter)) {
			return
		}
		lang := userSetting.Language
		prompt := i18n.Translate(lang, i18n.MsgNotifyQuotaLowTitle)
		content, values := quotaNotifyContent(lang, userSetting.NotifyType, prompt, logger.FormatQuota(balanceAfter))
		err := NotifyUser(relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values))
		if err != nil {
			common.SysError(common.LogText("failed to send quota notify to user %d: %s", relayInfo.UserId, err.Error()))
		}
	})
}

func checkAndSendSubscriptionQuotaNotify(relayInfo *relaycommon.RelayInfo) {
	gopool.Go(func() {
		if relayInfo == nil {
			return
		}
		if relayInfo.SubscriptionSnapshotUnknown || relayInfo.SubscriptionId == 0 || relayInfo.SubscriptionAmountTotal <= 0 {
			return
		}

		userSetting := relayInfo.UserSetting
		usedBefore := relayInfo.SubscriptionAmountUsedAfterPreConsume - relayInfo.SubscriptionPreConsumed
		usedAfter := relayInfo.SubscriptionAmountUsedAfterPreConsume + relayInfo.SubscriptionPostDelta
		remaining := relayInfo.SubscriptionAmountTotal - usedAfter
		if !quotaReminderDue(userSetting, relayInfo.SubscriptionAmountTotal-usedBefore, remaining) {
			return
		}

		lang := userSetting.Language
		prompt := i18n.Translate(lang, i18n.MsgNotifySubscriptionQuotaLowTitle)
		content, values := quotaNotifyContent(lang, userSetting.NotifyType, prompt, logger.FormatQuota(int(remaining)))
		if err := NotifyUser(relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values)); err != nil {
			common.SysError(common.LogText("failed to send subscription quota notify to user %d: %s", relayInfo.UserId, err.Error()))
		}
	})
}

// quotaReminderDue reports whether a charge that moved a balance (wallet or
// subscription) from before to after earns a low-balance reminder. The site
// and the user must both have reminders on, and the charge must cross the
// user's threshold, or the site default when the user set none. A balance
// already below the threshold earns nothing more until a top-up lifts it back
// above, so a user gets one reminder per drop.
func quotaReminderDue(userSetting dto.UserSetting, before int64, after int64) bool {
	if !common.QuotaRemindEnabled {
		return false
	}
	if userSetting.QuotaWarningEnabled != nil && !*userSetting.QuotaWarningEnabled {
		return false
	}
	threshold := float64(common.QuotaRemindThreshold)
	if userSetting.QuotaWarningThreshold != 0 {
		threshold = userSetting.QuotaWarningThreshold
	}
	return float64(before) >= threshold && float64(after) < threshold
}

// quotaNotifyContent renders a low-quota notice in the user's saved language.
// Bark and Gotify get plain text; email and webhook get HTML with the top-up
// link. values stay in the notify payload for webhook receivers.
func quotaNotifyContent(lang string, notifyType string, prompt string, remaining string) (string, []any) {
	switch notifyType {
	case dto.NotifyTypeBark:
		// Bark推送使用简短文本，不支持HTML
		return i18n.Translate(lang, i18n.MsgNotifyQuotaLowBark, map[string]any{"Prompt": prompt, "Quota": remaining}), []any{prompt, remaining}
	case dto.NotifyTypeGotify:
		return i18n.Translate(lang, i18n.MsgNotifyQuotaLowGotify, map[string]any{"Prompt": prompt, "Quota": remaining}), []any{prompt, remaining}
	default:
		topUpLink := PaymentReturnURL("/wallet")
		return i18n.Translate(lang, i18n.MsgNotifyQuotaLowEmail, map[string]any{"Prompt": prompt, "Quota": remaining, "Link": topUpLink}), []any{prompt, remaining, topUpLink, topUpLink}
	}
}
