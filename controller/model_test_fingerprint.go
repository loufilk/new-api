package controller

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelfingerprint"
	"github.com/QuantumNous/new-api/relaykit/types"
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
// mapping. Relay receives fresh per-call state, ordinary token accounting and
// distinct request IDs; neither distribution nor a retry occurs in this loop.
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
	samples := make([]modelfingerprint.Sample, 0, 3)
	audits := make([]modelFingerprintSampleAudit, 0, 3)
	for _, challenge := range challenges {
		if !requireModelTestEnabled(c) {
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}
		_, identity, tokenErr := selectedModelTestToken(c, selected)
		if tokenErr != nil || identity != c.GetString("model_test_token_identity") {
			c.Set("model_test_error_code", "confirmation_token_changed")
			return
		}
		estimate := estimateModelTestQuota(c.GetInt("id"), selected, challenges)
		if estimate == nil || *estimate != c.GetInt("model_test_estimated_quota") {
			c.Set("model_test_error_code", "confirmation_price_changed")
			return
		}
		pricingHash, priceErr := modelTestPricingHash(c.GetInt("id"), selected)
		if priceErr != nil || pricingHash != c.GetString("model_test_confirmed_pricing_hash") {
			c.Set("model_test_error_code", "confirmation_price_changed")
			return
		}
		current, loadErr := model.GetChannelById(run.ChannelID, true)
		currentBytes, marshalErr := modelFingerprintChannelSnapshot(current)
		if loadErr != nil || marshalErr != nil || sha256.Sum256(currentBytes) != channelHash {
			c.Set("model_test_error_code", "channel_changed")
			return
		}
		audit := modelFingerprintSampleAudit{ExpectedCount: challenge.ExpectedCount, PromptSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(challenge.Prompt))), ChallengeID: challenge.ID, Status: "dispatching", RequestID: uuid.NewString(), StartedAt: time.Now().Unix()}
		if len(audits) == 0 {
			run.RequestID = audit.RequestID
		}
		audits = append(audits, audit)
		auditJSON, err := common.Marshal(audits)
		if err != nil {
			c.Set("model_test_error_code", "history_unavailable")
			return
		}
		run.SamplesJSON = string(auditJSON)
		if model.SaveModelTestSamples(run) != nil {
			c.Set("model_test_error_code", "history_unavailable")
			return
		}
		body, err := common.Marshal(gin.H{"model": selected.Model, "stream": false, "max_tokens": selected.MaxOutputTokens, "messages": []gin.H{{"role": "user", "content": challenge.Prompt}}})
		if err != nil {
			c.Set("model_test_error_code", "invalid_request")
			return
		}
		common.CleanupBodyStorage(c)
		probe := c.Copy()
		delete(probe.Keys, "request_policy_state")
		probe.Request = c.Request.Clone(c.Request.Context())
		probe.Request.Body = io.NopCloser(bytes.NewReader(body))
		probe.Request.ContentLength = int64(len(body))
		probe.Set(common.KeyRequestBody, body)
		probe.Set(common.RequestIdKey, audit.RequestID)
		buffer := &modelTestWriter{ResponseWriter: c.Writer, header: make(http.Header), status: 200, cancel: func() {}}
		probe.Writer = buffer
		Relay(probe, types.RelayFormatOpenAI)
		common.CleanupBodyStorage(probe)
		index := len(audits) - 1
		audits[index].CompletedAt = time.Now().Unix()
		audits[index].Status = "completed"
		sample := modelfingerprint.Sample{ChallengeID: challenge.ID, ExpectedCount: challenge.ExpectedCount}
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
			sample.NonModel = true
			sample.Error = "user_call_intercepted"
		} else if buffer.overflow {
			sample.Error = "response_too_large"
		} else if buffer.status < 200 || buffer.status >= 300 || common.Unmarshal(buffer.body.Bytes(), &response) != nil || len(response.Choices) != 1 || response.Choices[0].FinishReason != "stop" {
			sample.Error = "incomplete_response"
		} else {
			sample.Response = response.Choices[0].Message.Content
			if len(response.Model) <= 255 && !strings.ContainsFunc(response.Model, unicode.IsControl) {
				audits[index].ResponseModel = response.Model
			}
			if usage := response.Usage; usage != nil && usage.PromptTokens >= 0 && usage.CompletionTokens >= 0 && usage.TotalTokens >= 0 && usage.PromptTokens <= 2147483647 && usage.CompletionTokens <= 2147483647 && usage.TotalTokens <= 2147483647 {
				audits[index].Usage = usage
			}
			audits[index].ResponseSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(sample.Response)))
		}
		if c.Request.Context().Err() != nil {
			sample.Error = "request_interrupted"
		}
		if sample.Error != "" {
			code := probe.GetString("model_test_error_code")
			if code == "" {
				code = "relay_failed"
			}
			c.Set("model_test_error_code", code)
			audits[index].Status = "failed"
			audits[index].Error = sample.Error
			if common.GetContextKeyBool(probe, constant.ContextKeyUpstreamRequestAttempted) && !common.GetContextKeyBool(probe, constant.ContextKeyUpstreamRequestUnsent) && !common.GetContextKeyBool(probe, constant.ContextKeyUpstreamExplicitRejection) {
				audits[index].Status = "unknown"
				c.Set("model_test_uncertain", true)
			}
		}
		auditJSON, err = common.Marshal(audits)
		if err != nil {
			c.Set("model_test_error_code", "history_unavailable")
			return
		}
		run.SamplesJSON = string(auditJSON)
		if model.SaveModelTestSamples(run) != nil {
			c.Set("model_test_uncertain", true)
			c.Set("model_test_error_code", "history_unavailable")
			return
		}
		samples = append(samples, sample)
		// No additional billable sample follows an uncertain/failed delivery.
		if sample.Error != "" {
			break
		}
	}
	for len(samples) < 3 {
		challenge := challenges[len(samples)]
		samples = append(samples, modelfingerprint.Sample{ChallengeID: challenge.ID, ExpectedCount: challenge.ExpectedCount, Error: "not_dispatched"})
	}
	result, err := modelfingerprint.Analyze(samples, true)
	if err != nil {
		c.Set("model_test_error_code", "fingerprint_analysis_failed")
		return
	}
	c.Set("model_test_fingerprint_result", result)

}

// Counters and round-robin cursors may legitimately change during a test; only
// authorization and upstream identity/configuration participate in this guard.
func modelFingerprintChannelSnapshot(channel *model.Channel) ([]byte, error) {
	if channel == nil {
		return nil, errors.New("channel unavailable")
	}
	return common.Marshal([]any{channel.Id, channel.Type, channel.Status, channel.Key, channel.OpenAIOrganization, channel.Other, channel.BaseURL, channel.Group, channel.Models, channel.ModelMapping, channel.Setting, channel.OtherSettings, channel.ParamOverride, channel.HeaderOverride, channel.ChannelInfo.IsMultiKey, channel.ChannelInfo.MultiKeyStatusList})
}
