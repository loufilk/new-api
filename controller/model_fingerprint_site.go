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
	"sync"
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
	ctx, cancel := context.WithTimeout(parent, modelTestTimeout)
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
	// Resolve the identity once, then run three isolated samples concurrently.
	// Mapped-model persistence is serialized, while HTTP work is independent.
	var mappingMu sync.Mutex
	var mappedModel string
	setMappedModel := func(probe *gin.Context, name string) error {
		mappingMu.Lock()
		defer mappingMu.Unlock()
		if mappedModel != "" && mappedModel != name {
			return errors.New("model_mapping_changed")
		}
		if mappedModel != "" {
			return nil
		}
		if err := model.DB.WithContext(probe.Request.Context()).Model(&model.ModelFingerprintSiteRun{}).Where("id = ? AND status = ?", run.ID, "running").Updates(map[string]any{"channel_id": channel.Id, "channel_key_index": common.GetContextKeyInt(probe, constant.ContextKeyChannelMultiKeyIndex), "mapped_model": name}).Error; err != nil {
			return errors.New("history_unavailable")
		}
		mappedModel = name
		return nil
	}
	probes := make([]*gin.Context, len(challenges))
	for i := range probes {
		probes[i] = copyModelFingerprintContext(base)
	}
	outcomes := make([]modelFingerprintSiteProbeOutcome, len(challenges))
	var workers sync.WaitGroup
	for i, challenge := range challenges {
		workers.Go(func() {
			outcomes[i] = executeSiteModelFingerprintProbe(probes[i], challenge, target, channelHash, run.ID, task.TaskID, runnerID, setMappedModel)
		})
	}
	workers.Wait()
	samples := make([]modelfingerprint.Sample, len(challenges))
	var failure string
	for i, outcome := range outcomes {
		samples[i] = outcome.sample
		if !outcome.usageKnown {
			// A later success must never erase another probe's uncertain send.
			if usageKnown {
				failure = outcome.errorCode
			}
			usageKnown = false
		} else if failure == "" {
			failure = outcome.errorCode
		}
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
	if failure != "" {
		return fail(failure)
	}
	result.Assessment = analysis.Status
	result.Status = "succeeded"
	return nil
}

type modelFingerprintSiteProbeOutcome struct {
	sample     modelfingerprint.Sample
	errorCode  string
	usageKnown bool
}

func executeSiteModelFingerprintProbe(probe *gin.Context, challenge modelfingerprint.Challenge, target model.ModelFingerprintSiteTarget, channelHash [32]byte, runID, taskID, runnerID string, setMappedModel func(*gin.Context, string) error) (outcome modelFingerprintSiteProbeOutcome) {
	outcome.sample = modelfingerprint.Sample{ChallengeID: challenge.ID, ExpectedCount: challenge.ExpectedCount, Error: "not_dispatched"}
	outcome.usageKnown = true
	ctx, cancel := context.WithTimeout(probe.Request.Context(), modelTestSampleTimeout)
	defer cancel()
	defer common.CleanupBodyStorage(probe)
	defer func() {
		if recover() != nil {
			outcome.errorCode = "upstream_outcome_unknown"
		}
		if outcome.errorCode != "" {
			outcome.sample.Error = outcome.errorCode
		}
	}()
	fail := func(code string) modelFingerprintSiteProbeOutcome {
		outcome.errorCode = code
		return outcome
	}
	buffer, err := prepareModelFingerprintProbe(probe, ctx, cancel, target.Model, challenge, uuid.NewString())
	if err != nil {
		return fail("invalid_request")
	}
	if ctx.Err() != nil {
		return fail("request_interrupted")
	}
	currentGuard, err := model.BeginRequestGroupConfigRead(probe)
	if err != nil {
		return fail("group_configuration_changed")
	}
	defer currentGuard.Close()
	current, err := fingerprintSiteChannel(target)
	if err != nil {
		return fail("channel_changed")
	}
	currentBytes, err := modelFingerprintChannelSnapshot(current)
	if err != nil || sha256.Sum256(currentBytes) != channelHash {
		return fail("channel_changed")
	}
	request := &dto.GeneralOpenAIRequest{}
	body, _ := probe.Get(common.KeyRequestBody)
	if err = common.Unmarshal(body.([]byte), request); err != nil {
		return fail("invalid_request")
	}
	info, err := relaycommon.GenRelayInfo(probe, types.RelayFormatOpenAI, request, nil)
	if err != nil {
		return fail("invalid_request")
	}
	info.IsChannelTest = true
	info.InitChannelMeta(probe)
	if err = helper.ModelMappedHelper(probe, info, request); err != nil {
		return fail("model_mapping_failed")
	}
	if err = helper.ApplyReasoningModelSuffix(probe, info, request); err != nil {
		return fail("model_mapping_failed")
	}
	if err = setMappedModel(probe, info.UpstreamModelName); err != nil {
		return fail(err.Error())
	}
	request.SetModelName(info.UpstreamModelName)
	apiType, _ := common.ChannelType2APIType(current.Type)
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		return fail("unsupported_test_channel")
	}
	adaptor.Init(info)
	converted, err := adaptor.ConvertOpenAIRequest(probe, info, request)
	if err != nil {
		return fail("unsupported_test_protocol")
	}
	wire, err := common.Marshal(converted)
	if err != nil {
		return fail("invalid_request")
	}
	if currentGuard.Finish() != nil {
		return fail("group_configuration_changed")
	}
	if err = model.AuthorizeModelFingerprintSiteProbe(ctx, runID, taskID, runnerID, challenge.ID, info.RequestId); err != nil {
		return fail("dispatch_not_authorized")
	}
	outcome.usageKnown = false // Durable intent exists; never replay this probe.
	finishUnsent := func() modelFingerprintSiteProbeOutcome {
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer persistCancel()
		if model.MarkModelFingerprintSiteProbeUnsent(persistCtx, runID, challenge.ID) != nil {
			return fail("history_unavailable")
		}
		outcome.usageKnown = true
		if code := probe.GetString("model_test_error_code"); code != "" {
			return fail(code)
		}
		if ctx.Err() != nil {
			return fail("request_interrupted")
		}
		return fail("dispatch_not_authorized")
	}
	response, err := adaptor.DoRequest(probe, info, bytes.NewReader(wire))
	if err != nil {
		if common.GetContextKeyBool(probe, constant.ContextKeyUpstreamRequestUnsent) {
			return finishUnsent()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fail("request_timeout")
		}
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
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fail("request_timeout")
			}
			return fail("upstream_outcome_unknown")
		}
		httpResponse.Body = io.NopCloser(bytes.NewReader(raw))
	}
	reportedUsage, responseErr := adaptor.DoResponse(probe, httpResponse, info)
	if responseErr != nil || buffer.overflow || buffer.status != http.StatusOK {
		// SDK/WebSocket adaptors can perform their final dispatch gate during
		// DoResponse, rather than during the earlier request preparation.
		if common.GetContextKeyBool(probe, constant.ContextKeyUpstreamRequestUnsent) {
			return finishUnsent()
		}
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
	if common.Unmarshal(buffer.body.Bytes(), &parsed) != nil || len(parsed.Choices) != 1 {
		return fail("invalid_response")
	}
	var usage modelFingerprintUsage
	if len(raw) > 0 {
		usage, err = fingerprintSiteReportedUsage(raw)
		// Coze's explicit usage belongs to the preceding completed-chat
		// response; only that adaptor may use its validated usage snapshot.
		if err != nil && apiType == constant.APITypeCoze {
			usage, err = fingerprintSiteCozeUsage(reportedUsage)
		}
	} else {
		usage, err = fingerprintSiteAdaptorUsage(reportedUsage)
	}
	if err != nil {
		return fail("usage_unknown")
	}
	// A response can finish just as the client or scheduler cancels. Preserve
	// observed usage with a bounded independent write before the final join.
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()
	if err = model.CompleteModelFingerprintSiteProbe(persistCtx, runID, challenge.ID, int(usage.PromptTokens), int(usage.CompletionTokens), parsed.Model); err != nil {
		return fail("history_unavailable")
	}
	outcome.usageKnown = true
	outcome.sample.Response, outcome.sample.Error = parsed.Choices[0].Message.Content, ""
	if parsed.Choices[0].FinishReason != "stop" {
		outcome.sample.Error = "incomplete_response"
		outcome.sample.Truncated = parsed.Choices[0].FinishReason == "length"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fail("request_timeout")
	}
	if ctx.Err() != nil {
		return fail("request_interrupted")
	}
	return outcome
}
