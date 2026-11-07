package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	requestdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelfingerprint"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type modelFingerprintUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type modelFingerprintSampleAudit struct {
	ResponseModel  string                 `json:"response_model,omitempty"`
	Usage          *modelFingerprintUsage `json:"usage,omitempty"`
	ChallengeID    string                 `json:"challenge_id"`
	ExpectedCount  int                    `json:"expected_count"`
	PromptSHA256   string                 `json:"prompt_sha256"`
	ResponseSHA256 string                 `json:"response_sha256,omitempty"`
	Status         string                 `json:"status"`
	RequestID      string                 `json:"request_id"`
	StartedAt      int64                  `json:"started_at"`
	CompletedAt    int64                  `json:"completed_at,omitempty"`
	Error          string                 `json:"error,omitempty"`
}

// All three invocations inherit one selected channel, credential slot and model
// mapping. Each worker owns its request, response, policy and billing state; the
// journal serializes audit snapshots and the caller joins every worker before
// finalizing the run. A failed sample never retries or cancels its siblings.
func executeModelFingerprint(c *gin.Context) {
	run, ok := c.MustGet("model_test_run").(*model.ModelTestRun)
	if !ok {
		modelTestError(c, 500, "history_unavailable")
		return
	}
	selected := c.MustGet("model_test_selection").(modelTestSelection)
	challenges, ok := c.MustGet("model_test_challenges").([]modelfingerprint.Challenge)
	if !ok || len(challenges) != 3 {
		modelTestError(c, 503, "fingerprint_unavailable")
		return
	}
	run.ChannelID = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	run.ChannelKeyIndex = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
	channel, err := model.GetChannelById(run.ChannelID, true)
	if err != nil || channel.Status != common.ChannelStatusEnabled {
		modelTestError(c, 409, "channel_changed")
		return
	}
	settings := channel.GetSetting()
	if settings.SystemPrompt != "" || settings.SystemPromptOverride || settings.ThinkingToContent || settings.PassThroughBodyEnabled || len(channel.GetParamOverride()) != 0 || len(channel.GetHeaderOverride()) != 0 {
		modelTestError(c, 409, "channel_overrides_not_supported")
		return
	}
	keys := []string{channel.Key}
	if channel.ChannelInfo.IsMultiKey {
		keys = channel.GetKeys()
	}
	keyIndex := run.ChannelKeyIndex
	if keyIndex < 0 || keyIndex >= len(keys) || keys[keyIndex] != common.GetContextKeyString(c, constant.ContextKeyChannelKey) || channel.GetBaseURL() != common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl) || channel.GetModelMapping() != common.GetContextKeyString(c, constant.ContextKeyChannelModelMapping) || channel.Type != common.GetContextKeyInt(c, constant.ContextKeyChannelType) {
		modelTestError(c, 409, "channel_changed")
		return
	}
	frozen, err := modelFingerprintChannelSnapshot(channel)
	if err != nil {
		modelTestError(c, 503, "channel_unavailable")
		return
	}
	channelHash := sha256.Sum256(frozen)
	channelID := run.ChannelID
	journal := modelFingerprintAuditJournal{run: run, audits: make([]modelFingerprintSampleAudit, len(challenges))}
	probes := make([]*gin.Context, len(challenges))
	// BodyStorage is a mutable seek/close handle. Discard the parent's handle
	// once, before copying any context, so each Relay creates its own storage.
	common.CleanupBodyStorage(c)
	for i, challenge := range challenges {
		journal.audits[i] = modelFingerprintSampleAudit{ExpectedCount: challenge.ExpectedCount, PromptSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(challenge.Prompt))), ChallengeID: challenge.ID, Status: "not_dispatched", RequestID: uuid.NewString()}
		probes[i] = copyModelFingerprintContext(c)
	}
	run.RequestID = journal.audits[0].RequestID
	outcomes := make([]modelFingerprintProbeOutcome, len(challenges))
	var workers sync.WaitGroup
	for i, challenge := range challenges {
		workers.Go(func() {
			outcomes[i] = executeModelFingerprintProbe(probes[i], selected, challenges, challenge, channelID, channelHash, &journal, i)
		})
	}
	workers.Wait()
	samples := make([]modelfingerprint.Sample, len(challenges))
	for i, outcome := range outcomes {
		samples[i] = outcome.sample
		if outcome.uncertain {
			c.Set("model_test_uncertain", true)
		}
		if outcome.sample.Error == "request_timeout" {
			c.Set("model_test_sample_timeout", true)
		}
		if outcome.errorCode != "" && c.GetString("model_test_error_code") == "" {
			c.Set("model_test_error_code", outcome.errorCode)
		}
	}
	// The three probes are independent. A timed-out sibling must not discard
	// complete responses; the scorer marks one/two-sample rankings as partial.
	result, err := modelFingerprintScorer.analyze(samples, false)
	if err != nil {
		c.Set("model_test_error_code", "fingerprint_analysis_failed")
		return
	}
	c.Set("model_test_fingerprint_result", result)
}

// Gin copies Keys only shallowly. These entries contain mutable per-request
// state, including maps written by routing and seekable body storage. Keep the
// fixed authorization/routing values, but give each invocation fresh state.
func copyModelFingerprintContext(base *gin.Context) *gin.Context {
	probe := base.Copy()
	for _, key := range []string{"request_policy_state", "token_routing_attempts", "use_channel", "model_test_run", "request_billing_usage_observed", "request_billing_delivery_unknown", "request_billing_not_submitted", common.KeyBodyStorage, common.KeyRequestBody} {
		delete(probe.Keys, key)
	}
	if constraints, ok := common.GetContextKeyType[*requestdto.ChannelConstraints](base, constant.ContextKeyChannelConstraints); ok && constraints != nil {
		independent := *constraints
		independent.Pins = slices.Clone(constraints.Pins)
		independent.Filters = slices.Clone(constraints.Filters)
		for i := range independent.Filters {
			independent.Filters[i].TaskPluginKeys = slices.Clone(independent.Filters[i].TaskPluginKeys)
			independent.Filters[i].TaskPluginChannelTypes = slices.Clone(independent.Filters[i].TaskPluginChannelTypes)
		}
		common.SetContextKey(probe, constant.ContextKeyChannelConstraints, &independent)
	}
	return probe
}

func prepareModelFingerprintProbe(probe *gin.Context, ctx context.Context, cancel context.CancelFunc, modelName string, challenge modelfingerprint.Challenge, requestID string) (*modelTestWriter, error) {
	body, err := common.Marshal(gin.H{"model": modelName, "stream": false, "messages": []gin.H{{"role": "user", "content": challenge.Prompt}}})
	if err != nil {
		return nil, err
	}
	probe.Request = probe.Request.Clone(ctx)
	probe.Request.Body = io.NopCloser(bytes.NewReader(body))
	probe.Request.ContentLength = int64(len(body))
	probe.Set(common.KeyRequestBody, body)
	probe.Set(common.RequestIdKey, requestID)
	// Even optional ResponseWriter methods must never reach the parent's writer.
	isolated, _ := gin.CreateTestContext(httptest.NewRecorder())
	buffer := &modelTestWriter{ResponseWriter: isolated.Writer, header: make(http.Header), status: http.StatusOK, cancel: cancel}
	probe.Writer = buffer
	return buffer, nil
}

type modelFingerprintProbeOutcome struct {
	sample    modelfingerprint.Sample
	errorCode string
	uncertain bool
}

type modelFingerprintAuditJournal struct {
	mu     sync.Mutex
	run    *model.ModelTestRun
	audits []modelFingerprintSampleAudit
	failed bool
}

func (j *modelFingerprintAuditJournal) save(index int, audit modelFingerprintSampleAudit) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	// A failed intent write prevents further dispatch, while already admitted
	// workers still finish and attempt to persist their own delivery outcome.
	if j.failed && audit.Status == "dispatching" {
		return errors.New("fingerprint audit unavailable")
	}
	j.audits[index] = audit
	data, err := common.Marshal(j.audits)
	if err == nil {
		j.run.SamplesJSON = string(data)
		err = model.SaveModelTestSamples(j.run)
	}
	if err != nil {
		j.failed = true
	}
	return err
}

func executeModelFingerprintProbe(probe *gin.Context, selected modelTestSelection, challenges []modelfingerprint.Challenge, challenge modelfingerprint.Challenge, channelID int, channelHash [32]byte, journal *modelFingerprintAuditJournal, index int) (outcome modelFingerprintProbeOutcome) {
	outcome.sample = modelfingerprint.Sample{ChallengeID: challenge.ID, ExpectedCount: challenge.ExpectedCount, Error: "not_dispatched"}
	// The audit's immutable identity is copied before workers can replace slots.
	journal.mu.Lock()
	audit := journal.audits[index]
	journal.mu.Unlock()
	ctx, cancel := context.WithTimeout(probe.Request.Context(), modelTestSampleTimeout)
	defer cancel()
	defer common.CleanupBodyStorage(probe)
	completeResponse := false
	deliveryUnknown := func() bool {
		return probe.GetBool("request_billing_delivery_unknown") || !completeResponse && common.GetContextKeyBool(probe, constant.ContextKeyUpstreamRequestAttempted) && !common.GetContextKeyBool(probe, constant.ContextKeyUpstreamRequestUnsent) && !common.GetContextKeyBool(probe, constant.ContextKeyUpstreamExplicitRejection)
	}
	defer func() {
		if recover() != nil {
			outcome.sample.Error, outcome.errorCode = "relay_failed", "relay_failed"
		}
		if outcome.sample.Error != "" {
			audit.Status, audit.Error = "failed", outcome.sample.Error
			if deliveryUnknown() {
				audit.Status, outcome.uncertain = "unknown", true
			}
		}
		if probe.GetBool("request_billing_delivery_unknown") {
			// Financial ownership can remain unresolved even when the adaptor
			// produced a complete response. Preserve Relay's authoritative hold.
			audit.Status, outcome.uncertain = "unknown", true
		}
		audit.CompletedAt = time.Now().Unix()
		if err := journal.save(index, audit); err != nil {
			outcome.errorCode = "history_unavailable"
			// Keep delivery uncertainty separate from a definite rejection.
			outcome.uncertain = outcome.uncertain || deliveryUnknown()
		}
	}()
	buffer, err := prepareModelFingerprintProbe(probe, ctx, cancel, selected.Model, challenge, audit.RequestID)
	if err != nil {
		outcome.errorCode = "invalid_request"
		return
	}
	if accessErr := service.CheckModelTestAccess(probe); accessErr != nil {
		outcome.errorCode = probe.GetString("model_test_error_code")
		return
	}
	if ctx.Err() != nil {
		outcome.sample.Error, outcome.errorCode = "request_interrupted", "request_cancelled"
		return
	}
	_, identity, tokenErr := selectedModelTestToken(probe, selected)
	if tokenErr != nil || identity != probe.GetString("model_test_token_identity") {
		outcome.errorCode = "confirmation_token_changed"
		return
	}
	estimate := estimateModelTestQuota(probe.GetInt("id"), selected, challenges)
	confirmedEstimate, _ := probe.Get("model_test_estimated_quota")
	confirmedQuota, validEstimate := confirmedEstimate.(*int)
	if !validEstimate || !modelTestQuotaEstimateMatches(estimate, confirmedQuota) {
		outcome.errorCode = "confirmation_price_changed"
		return
	}
	pricingHash, priceErr := modelTestPricingHash(probe.GetInt("id"), selected)
	if priceErr != nil || pricingHash != probe.GetString("model_test_confirmed_pricing_hash") {
		outcome.errorCode = "confirmation_price_changed"
		return
	}
	current, loadErr := model.GetChannelById(channelID, true)
	currentBytes, marshalErr := modelFingerprintChannelSnapshot(current)
	if loadErr != nil || marshalErr != nil || sha256.Sum256(currentBytes) != channelHash {
		outcome.errorCode = "channel_changed"
		return
	}
	audit.Status, audit.StartedAt = "dispatching", time.Now().Unix()
	if journal.save(index, audit) != nil {
		outcome.errorCode = "history_unavailable"
		return
	}
	Relay(probe, types.RelayFormatOpenAI)
	audit.Status = "completed"
	outcome.sample.Error = ""
	var response struct {
		Model   string                 `json:"model"`
		Usage   *modelFingerprintUsage `json:"usage"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if buffer.header.Get("X-Newapi-Call-Intercepted") == "true" {
		outcome.sample.NonModel = true
		outcome.sample.Error = "user_call_intercepted"
	} else if buffer.overflow {
		outcome.sample.Error = "response_too_large"
	} else if buffer.status < 200 || buffer.status >= 300 || common.Unmarshal(buffer.body.Bytes(), &response) != nil || len(response.Choices) != 1 {
		outcome.sample.Error = "incomplete_response"
	} else {
		completeResponse = true
		outcome.sample.Response = response.Choices[0].Message.Content
		if len(response.Model) <= 255 && !strings.ContainsFunc(response.Model, unicode.IsControl) {
			audit.ResponseModel = response.Model
		}
		if usage := response.Usage; usage != nil && usage.PromptTokens >= 0 && usage.CompletionTokens >= 0 && usage.TotalTokens >= 0 && usage.PromptTokens <= 2147483647 && usage.CompletionTokens <= 2147483647 && usage.TotalTokens <= 2147483647 {
			audit.Usage = usage
		}
		audit.ResponseSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(outcome.sample.Response)))
		if response.Choices[0].FinishReason != "stop" {
			outcome.sample.Error = "incomplete_response"
			outcome.sample.Truncated = response.Choices[0].FinishReason == "length"
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		outcome.sample.Error = "request_timeout"
	} else if ctx.Err() != nil && !buffer.overflow {
		outcome.sample.Error = "request_interrupted"
	}
	if outcome.sample.Error != "" {
		outcome.errorCode = probe.GetString("model_test_error_code")
		if outcome.errorCode == "" {
			outcome.errorCode = "relay_failed"
		}
	}
	return
}

// Counters and round-robin cursors may legitimately change during a test; only
// authorization and upstream identity/configuration participate in this guard.
func modelFingerprintChannelSnapshot(channel *model.Channel) ([]byte, error) {
	if channel == nil {
		return nil, errors.New("channel unavailable")
	}
	return common.Marshal([]any{channel.Id, channel.Type, channel.Status, channel.Key, channel.OpenAIOrganization, channel.Other, channel.BaseURL, channel.Group, channel.Models, channel.ModelMapping, channel.Setting, channel.OtherSettings, channel.ParamOverride, channel.HeaderOverride, channel.ChannelInfo.IsMultiKey, channel.ChannelInfo.MultiKeyStatusList})
}
