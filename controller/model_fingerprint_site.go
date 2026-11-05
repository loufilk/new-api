package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelfingerprint"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func init() { service.RegisterModelFingerprintSiteExecutor(executeSiteModelFingerprint) }

// Automatic work never borrows a visitor/root account, creates a token, touches
// wallet balances or invokes end-user settlement/commission hooks. Its separate
// quota ledger measures configured site pricing, not an upstream invoice.
type fingerprintSitePrice struct {
	Model       string
	GroupRatio  float64
	Price       float64
	Fixed       bool
	InputRatio  float64
	OutputRatio float64
}

func fingerprintSitePricing(target model.ModelFingerprintSiteTarget) (fingerprintSitePrice, error) {
	name := helper.ResolveBillingModelName(target.Model)
	price := fingerprintSitePrice{Model: name, GroupRatio: ratio_setting.GetGroupRatio(target.Group)}
	// Arbitrary expressions cannot be proven bounded by output-token limits.
	// They remain available to explicitly confirmed personal tests, not unattended
	// site sampling. No fallback silently ignores an expression or tariff mode.
	if billing_setting.GetBillingMode(name) == billing_setting.BillingModeTieredExpr {
		return price, errors.New("automatic_static_pricing_required")
	}
	if !ratio_setting.ContainsGroupRatio(target.Group) || target.Group == "auto" {
		return price, errors.New("model_or_group_unavailable")
	}
	var exists bool
	price.Price, price.Fixed = ratio_setting.GetModelPrice(name, false)
	if !price.Fixed {
		price.InputRatio, exists, _ = ratio_setting.GetModelRatio(name)
		if !exists {
			return price, errors.New("automatic_static_pricing_required")
		}
		price.OutputRatio = ratio_setting.GetCompletionRatio(name)
	}
	for _, v := range []float64{price.GroupRatio, price.Price, price.InputRatio, price.OutputRatio} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return price, errors.New("invalid_site_pricing")
		}
	}
	return price, nil
}
func (price fingerprintSitePrice) quota(input, output int) (int64, error) {
	if input < 0 || output < 0 || input > common.MaxQuota || output > common.MaxQuota {
		return 0, errors.New("invalid_site_usage")
	}
	value := price.Price * common.QuotaPerUnit * price.GroupRatio
	if !price.Fixed {
		value = (float64(input) + float64(output)*price.OutputRatio) * price.InputRatio * price.GroupRatio
	}
	amount, err := common.QuotaFromFloatStrict(math.Ceil(value))
	return int64(amount), err
}

func fingerprintSiteChannel(target model.ModelFingerprintSiteTarget) (*model.Channel, error) {
	channel, err := model.GetChannelById(target.ChannelID, true)
	if err != nil || channel == nil {
		return nil, errors.New("channel_unavailable")
	}
	if channel.Status != common.ChannelStatusEnabled || channel.Type != constant.ChannelTypeOpenAI || !slices.Contains(channel.GetGroups(), target.Group) || !slices.Contains(channel.GetModels(), target.Model) {
		return nil, errors.New("model_or_group_unavailable")
	}
	var count int64
	if err = model.DB.Model(&model.Ability{}).Where(&model.Ability{Group: target.Group, Model: target.Model, ChannelId: target.ChannelID}).Where("enabled = ?", true).Count(&count).Error; err != nil || count != 1 {
		return nil, errors.New("model_or_group_unavailable")
	}
	settings := channel.GetSetting()
	if settings.SystemPrompt != "" || settings.SystemPromptOverride || settings.ThinkingToContent || settings.PassThroughBodyEnabled || len(channel.GetParamOverride()) != 0 || len(channel.GetHeaderOverride()) != 0 {
		return nil, errors.New("channel_overrides_not_supported")
	}
	return channel, nil
}

func executeSiteModelFingerprint(parent context.Context, task *model.SystemTask, runnerID string, target model.ModelFingerprintSiteTarget, run *model.ModelFingerprintSiteRun) (resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	result := model.ModelFingerprintSiteResult{Status: "failed", BenchmarkID: "lm-detector-v1", FingerprintVersion: modelfingerprint.SDKCommit}
	var actual int64
	usageKnown := true
	defer func() {
		var known *int64
		if usageKnown {
			known = &actual
		}
		// Caller cancellation never prevents recording that a paid call may exist.
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer finishCancel()
		if err := model.FinishModelFingerprintSiteRun(finishCtx, run.ID, result, known); err != nil {
			resultErr = err
		}
	}()
	fail := func(code string) error { result.Assessment = code; return errors.New(code) }
	if err := modelfingerprint.Ready(); err != nil {
		return fail("fingerprint_unavailable")
	}
	challenges, err := modelfingerprint.GenerateChallenges()
	if err != nil || len(challenges) != 3 {
		return fail("fingerprint_unavailable")
	}
	guard, err := model.BeginGroupConfigRead(ctx)
	if err != nil {
		return fail("group_configuration_changed")
	}
	channel, err := fingerprintSiteChannel(target)
	if err != nil {
		guard.Close()
		return fail(err.Error())
	}
	price, err := fingerprintSitePricing(target)
	if err != nil {
		guard.Close()
		return fail(err.Error())
	}
	var reservation int64
	for _, challenge := range challenges {
		// Bound input conservatively above tokenizer count for fixed public prompts.
		quota, quotaErr := price.quota(service.CountTokenInput(challenge.Prompt, target.Model)+128, target.MaxOutputTokens)
		if quotaErr != nil || quota > common.MaxQuota || reservation > target.MaxRunQuota-quota {
			guard.Close()
			return fail("run_budget_insufficient")
		}
		reservation += quota
	}
	if reservation > target.MaxRunQuota {
		guard.Close()
		return fail("run_budget_insufficient")
	}
	channelBytes, err := modelFingerprintChannelSnapshot(channel)
	if err != nil {
		guard.Close()
		return fail("channel_unavailable")
	}
	channelHash := sha256.Sum256(channelBytes)
	base, _ := gin.CreateTestContext(httptest.NewRecorder())
	base.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", nil)
	base.Request.Header.Set("Content-Type", "application/json")
	base.Set("id", 0)
	base.Set("group", target.Group)
	common.SetContextKey(base, constant.ContextKeyUsingGroup, target.Group)
	common.SetContextKey(base, constant.ContextKeyChannelTest, true)
	common.SetContextKey(base, constant.ContextKeyModelTestSingleAttempt, true)
	common.SetContextKey(base, constant.ContextKeyModelTestOutputLimit, target.MaxOutputTokens)
	base.Set(model.GroupConfigVersionContextKey, guard.Version)
	if middleware.SetupContextForSelectedChannel(base, channel, target.Model) != nil {
		guard.Close()
		return fail("channel_unavailable")
	}
	if guard.Finish() != nil {
		return fail("group_configuration_changed")
	}
	samples := make([]modelfingerprint.Sample, 0, 3)
	var mappedModel string
	for _, challenge := range challenges {
		if ctx.Err() != nil {
			return fail("request_interrupted")
		}
		currentGuard, err := model.BeginRequestGroupConfigRead(base)
		if err != nil {
			return fail("group_configuration_changed")
		}
		current, err := fingerprintSiteChannel(target)
		if err != nil {
			currentGuard.Close()
			return fail("channel_changed")
		}
		currentBytes, err := modelFingerprintChannelSnapshot(current)
		if err != nil || sha256.Sum256(currentBytes) != channelHash {
			currentGuard.Close()
			return fail("channel_changed")
		}
		currentPrice, err := fingerprintSitePricing(target)
		if err != nil || currentPrice != price {
			currentGuard.Close()
			return fail("pricing_changed")
		}
		body, err := common.Marshal(gin.H{"model": target.Model, "stream": false, "max_tokens": target.MaxOutputTokens, "messages": []gin.H{{"role": "user", "content": challenge.Prompt}}})
		if err != nil {
			currentGuard.Close()
			return fail("invalid_request")
		}
		request := &dto.GeneralOpenAIRequest{}
		if err = common.Unmarshal(body, request); err != nil {
			currentGuard.Close()
			return fail("invalid_request")
		}
		probe := base.Copy()
		probe.Request = base.Request.Clone(ctx)
		probe.Request.Body = io.NopCloser(bytes.NewReader(body))
		probe.Request.ContentLength = int64(len(body))
		probe.Set(common.KeyRequestBody, body)
		probe.Set(common.RequestIdKey, uuid.NewString())
		buffer := &modelTestWriter{ResponseWriter: base.Writer, header: make(http.Header), status: 200, cancel: cancel}
		probe.Writer = buffer
		info, err := relaycommon.GenRelayInfo(probe, types.RelayFormatOpenAI, request, nil)
		if err != nil {
			currentGuard.Close()
			return fail("invalid_request")
		}
		info.IsChannelTest = true
		info.InitChannelMeta(probe)
		if err = helper.ModelMappedHelper(probe, info, request); err != nil {
			currentGuard.Close()
			return fail("model_mapping_failed")
		}
		if err = helper.ApplyReasoningModelSuffix(probe, info, request); err != nil {
			currentGuard.Close()
			return fail("model_mapping_failed")
		}
		if mappedModel != "" && info.UpstreamModelName != mappedModel {
			currentGuard.Close()
			return fail("model_mapping_changed")
		}
		mappedModel = info.UpstreamModelName
		if err = model.DB.WithContext(ctx).Model(&model.ModelFingerprintSiteRun{}).Where("id = ? AND status = ?", run.ID, "running").Updates(map[string]any{"channel_key_index": common.GetContextKeyInt(base, constant.ContextKeyChannelMultiKeyIndex), "mapped_model": mappedModel}).Error; err != nil {
			currentGuard.Close()
			return fail("history_unavailable")
		}
		request.SetModelName(mappedModel)
		apiType, _ := common.ChannelType2APIType(channel.Type)
		adaptor := relay.GetAdaptor(apiType)
		if adaptor == nil {
			currentGuard.Close()
			return fail("unsupported_test_channel")
		}
		adaptor.Init(info)
		converted, err := adaptor.ConvertOpenAIRequest(probe, info, request)
		if err != nil {
			currentGuard.Close()
			return fail("invalid_request")
		}
		wire, err := common.Marshal(converted)
		if err != nil {
			currentGuard.Close()
			return fail("invalid_request")
		}
		if currentGuard.Finish() != nil {
			return fail("group_configuration_changed")
		}
		if err = model.AuthorizeModelFingerprintSiteProbe(ctx, run.ID, task.TaskID, runnerID, challenge.ID, info.RequestId); err != nil {
			return fail("dispatch_not_authorized")
		}
		usageKnown = false // Durable dispatch intent exists; no retry after this boundary.
		response, err := adaptor.DoRequest(probe, info, bytes.NewReader(wire))
		if err != nil {
			return fail("upstream_outcome_unknown")
		}
		httpResponse, ok := response.(*http.Response)
		if !ok || httpResponse == nil {
			return fail("upstream_outcome_unknown")
		}
		// Bound the complete upstream body before any adaptor's parser can read it.
		raw, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, modelTestResponseLimit+1))
		_ = httpResponse.Body.Close()
		if readErr != nil || len(raw) > modelTestResponseLimit || httpResponse.StatusCode != http.StatusOK {
			return fail("upstream_outcome_unknown")
		}
		httpResponse.Body = io.NopCloser(bytes.NewReader(raw))
		_, responseErr := adaptor.DoResponse(probe, httpResponse, info)
		if responseErr != nil || buffer.overflow || buffer.status != http.StatusOK {
			return fail("invalid_response")
		}
		var parsed struct {
			Model   string `json:"model"`
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		// Require real upstream usage rather than adaptor-estimated or zero defaults.
		if common.Unmarshal(raw, &parsed) != nil || parsed.Usage == nil || parsed.Usage.PromptTokens <= 0 || parsed.Usage.CompletionTokens <= 0 || parsed.Usage.PromptTokens > common.MaxQuota || parsed.Usage.CompletionTokens > common.MaxQuota || parsed.Usage.TotalTokens != parsed.Usage.PromptTokens+parsed.Usage.CompletionTokens || parsed.Usage.CompletionTokens > target.MaxOutputTokens {
			return fail("usage_unknown")
		}
		amount, err := price.quota(parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens)
		if err != nil || amount > target.MaxRunQuota || actual > target.MaxRunQuota-amount {
			return fail("budget_exceeded")
		}
		if err = model.CompleteModelFingerprintSiteProbe(ctx, run.ID, challenge.ID, parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens, parsed.Model); err != nil {
			return fail("history_unavailable")
		}
		actual += amount
		usageKnown = true
		if common.Unmarshal(buffer.body.Bytes(), &parsed) != nil || len(parsed.Choices) != 1 {
			return fail("invalid_response")
		}
		sample := modelfingerprint.Sample{ChallengeID: challenge.ID, ExpectedCount: challenge.ExpectedCount, Response: parsed.Choices[0].Message.Content}
		if parsed.Choices[0].FinishReason != "stop" {
			sample.Error = "incomplete_response"
		}
		samples = append(samples, sample)
		if sample.Error != "" {
			break
		}
	}
	for len(samples) < 3 {
		challenge := challenges[len(samples)]
		samples = append(samples, modelfingerprint.Sample{ChallengeID: challenge.ID, ExpectedCount: challenge.ExpectedCount, Error: "not_dispatched"})
	}
	analysis, err := modelfingerprint.Analyze(samples, true)
	if err != nil {
		return fail("fingerprint_analysis_failed")
	}
	safeResult, err := common.Marshal(analysis)
	if err != nil {
		return fail("fingerprint_analysis_failed")
	}
	result.FingerprintResult = string(safeResult)
	result.Assessment = analysis.Status
	result.Status = "succeeded"
	return nil
}
