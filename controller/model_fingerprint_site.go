package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	requestdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelfingerprint"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func init() { service.RegisterModelFingerprintSiteExecutor(executeSiteModelFingerprint) }

// Automatic work uses the ordinary group/model router and provider adaptors. It
// never borrows a visitor/root account or changes a user's wallet. Only aggregate
// token usage is recorded; this feature imposes no output or spending budget.
func fingerprintSiteChannel(target model.ModelFingerprintSiteTarget) (*model.Channel, error) {
	channel, err := model.GetChannelById(target.ChannelID, true)
	if err != nil || channel == nil {
		return nil, errors.New("channel_unavailable")
	}
	if channel.Status != common.ChannelStatusEnabled || !slices.Contains(channel.GetGroups(), target.Group) || !slices.Contains(channel.GetModels(), target.Model) {
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
	usageKnown := true
	defer func() {
		// Caller cancellation never prevents recording that a paid call may exist.
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer finishCancel()
		if err := model.FinishModelFingerprintSiteRun(finishCtx, run.ID, result, usageKnown); err != nil {
			resultErr = err
		}
	}()
	fail := func(code string) error { result.Assessment = code; return errors.New(code) }
	if err := modelFingerprintScorer.ready(); err != nil {
		return fail("fingerprint_unavailable")
	}
	challenges, err := modelFingerprintScorer.challenges()
	if err != nil || len(challenges) != 3 {
		return fail("fingerprint_unavailable")
	}
	guard, err := model.BeginGroupConfigRead(ctx)
	if err != nil {
		return fail("group_configuration_changed")
	}
	base, _ := gin.CreateTestContext(httptest.NewRecorder())
	base.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", nil)
	base.Request.Header.Set("Content-Type", "application/json")
	base.Set("id", 0)
	base.Set("group", target.Group)
	common.SetContextKey(base, constant.ContextKeyUsingGroup, target.Group)
	common.SetContextKey(base, constant.ContextKeyChannelTest, true)
	common.SetContextKey(base, constant.ContextKeyModelTestSingleAttempt, true)
	base.Set(model.GroupConfigVersionContextKey, guard.Version)
	service.GetChannelConstraints(base).AddFilter(requestdto.ChannelFilter{Kind: requestdto.FilterRequestPath, RequestPath: base.Request.URL.Path})
	service.AppendTaskPluginIdentityFilter(base, "")
	guard.Close() // The request selector takes its own version-fenced read lock.
	channel, _, selectionErr := service.SelectChannelForRequest(base, target.Model, &service.RetryParam{Ctx: base, TokenGroup: target.Group, ModelName: target.Model, RequestPath: base.Request.URL.Path})
	if selectionErr != nil || channel == nil {
		return fail("model_or_group_unavailable")
	}
	guard, err = model.BeginRequestGroupConfigRead(base)
	if err != nil {
		return fail("group_configuration_changed")
	}
	// Old saved channel IDs are not pins. Resolve once through normal routing,
	// then freeze the chosen channel and credential slot for all three samples.
	target.ChannelID = channel.Id
	channel, err = fingerprintSiteChannel(target)
	if err != nil {
		guard.Close()
		return fail(err.Error())
	}
	channelBytes, err := modelFingerprintChannelSnapshot(channel)
	if err != nil {
		guard.Close()
		return fail("channel_unavailable")
	}
	channelHash := sha256.Sum256(channelBytes)
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
		body, err := common.Marshal(gin.H{"model": target.Model, "stream": false, "messages": []gin.H{{"role": "user", "content": challenge.Prompt}}})
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
		if err = model.DB.WithContext(ctx).Model(&model.ModelFingerprintSiteRun{}).Where("id = ? AND status = ?", run.ID, "running").Updates(map[string]any{"channel_id": channel.Id, "channel_key_index": common.GetContextKeyInt(base, constant.ContextKeyChannelMultiKeyIndex), "mapped_model": mappedModel}).Error; err != nil {
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
			return fail("unsupported_test_protocol")
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
		var httpResponse *http.Response
		var raw []byte
		if response != nil {
			var ok bool
			httpResponse, ok = response.(*http.Response)
			if !ok {
				return fail("invalid_response")
			}
		}
		// HTTP adaptors expose raw bytes here; SDK/WebSocket adaptors perform
		// their ordinary response step below and return normalized usage instead.
		if httpResponse != nil && httpResponse.Body != nil {
			var readErr error
			raw, readErr = io.ReadAll(io.LimitReader(httpResponse.Body, modelTestResponseLimit+1))
			_ = httpResponse.Body.Close()
			if readErr != nil || len(raw) > modelTestResponseLimit || httpResponse.StatusCode != http.StatusOK {
				return fail("upstream_outcome_unknown")
			}
			httpResponse.Body = io.NopCloser(bytes.NewReader(raw))
		}
		reportedUsage, responseErr := adaptor.DoResponse(probe, httpResponse, info)
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
		}
		// Normalize provider-native responses through the selected adaptor. Real
		// reported usage is checked separately, never replaced by local estimates.
		if common.Unmarshal(buffer.body.Bytes(), &parsed) != nil || len(parsed.Choices) != 1 {
			return fail("invalid_response")
		}
		var usage modelFingerprintUsage
		if len(raw) > 0 {
			usage, err = fingerprintSiteReportedUsage(raw)
			// Coze returns message detail here, while its explicit usage belongs
			// to the preceding completed-chat response. Only that adaptor may
			// use its independently validated upstream usage snapshot.
			if err != nil && apiType == constant.APITypeCoze {
				usage, err = fingerprintSiteCozeUsage(reportedUsage)
			}
		} else {
			usage, err = fingerprintSiteAdaptorUsage(reportedUsage)
		}
		if err != nil {
			return fail("usage_unknown")
		}
		if err = model.CompleteModelFingerprintSiteProbe(ctx, run.ID, challenge.ID, int(usage.PromptTokens), int(usage.CompletionTokens), parsed.Model); err != nil {
			return fail("history_unavailable")
		}
		usageKnown = true
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
	analysis, err := modelFingerprintScorer.analyze(samples, true)
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
