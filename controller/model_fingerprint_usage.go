package controller

import (
	"encoding/json"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type fingerprintReportedUsage struct {
	PromptTokens             *int64 `json:"prompt_tokens"`
	CompletionTokens         *int64 `json:"completion_tokens"`
	TotalTokens              *int64 `json:"total_tokens"`
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	PromptTokensPascal       *int64 `json:"PromptTokens"`
	CompletionTokensPascal   *int64 `json:"CompletionTokens"`
	TotalTokensPascal        *int64 `json:"TotalTokens"`
	CacheCreation            *struct {
		FiveMinute *int64 `json:"ephemeral_5m_input_tokens"`
		OneHour    *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func fingerprintUsageCountsValid(values ...*int64) bool {
	for _, value := range values {
		if value != nil && (*value < 0 || *value > common.MaxQuota) {
			return false
		}
	}
	return true
}

func fingerprintUsageResult(input, output, total int64) (modelFingerprintUsage, error) {
	if input <= 0 || input > common.MaxQuota || output <= 0 || output > common.MaxQuota || total > common.MaxQuota || total != input+output {
		return modelFingerprintUsage{}, errors.New("usage_unknown")
	}
	return modelFingerprintUsage{PromptTokens: input, CompletionTokens: output, TotalTokens: total}, nil
}

func (u *fingerprintReportedUsage) counts() (modelFingerprintUsage, error) {
	fail := errors.New("usage_unknown")
	if !fingerprintUsageCountsValid(u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens, u.PromptTokensPascal, u.CompletionTokensPascal, u.TotalTokensPascal) {
		return modelFingerprintUsage{}, fail
	}
	if u.PromptTokens != nil && u.CompletionTokens != nil && u.TotalTokens != nil {
		return fingerprintUsageResult(*u.PromptTokens, *u.CompletionTokens, *u.TotalTokens)
	}
	if u.PromptTokensPascal != nil && u.CompletionTokensPascal != nil && u.TotalTokensPascal != nil {
		return fingerprintUsageResult(*u.PromptTokensPascal, *u.CompletionTokensPascal, *u.TotalTokensPascal)
	}
	if u.InputTokens == nil || u.OutputTokens == nil {
		return modelFingerprintUsage{}, fail
	}
	input, output := *u.InputTokens, *u.OutputTokens
	cacheCreation := int64(0)
	if u.CacheCreation != nil {
		if !fingerprintUsageCountsValid(u.CacheCreation.FiveMinute, u.CacheCreation.OneHour) {
			return modelFingerprintUsage{}, fail
		}
		if u.CacheCreation.FiveMinute != nil {
			cacheCreation += *u.CacheCreation.FiveMinute
		}
		if u.CacheCreation.OneHour != nil {
			cacheCreation += *u.CacheCreation.OneHour
		}
	}
	if u.CacheCreationInputTokens != nil && *u.CacheCreationInputTokens > 0 {
		cacheCreation = *u.CacheCreationInputTokens
	}
	input += cacheCreation
	if u.CacheReadInputTokens != nil {
		input += *u.CacheReadInputTokens
	}
	total := input + output
	if u.TotalTokens != nil && *u.TotalTokens != total {
		return modelFingerprintUsage{}, fail
	}
	return fingerprintUsageResult(input, output, total)
}

// Use explicit upstream counters, never a local token estimate. Cache inputs and
// reasoning outputs count toward the recorded totals, not a provider invoice.
func fingerprintSiteReportedUsage(body []byte) (modelFingerprintUsage, error) {
	fail := errors.New("usage_unknown")
	for depth := 0; depth < 4; depth++ {
		var payload struct {
			Usage         *fingerprintReportedUsage `json:"usage"`
			UsageMetadata *struct {
				Prompt     *int64 `json:"promptTokenCount"`
				Candidates *int64 `json:"candidatesTokenCount"`
				Thoughts   *int64 `json:"thoughtsTokenCount"`
				Total      *int64 `json:"totalTokenCount"`
			} `json:"usageMetadata"`
			Meta *struct {
				BilledUnits *fingerprintReportedUsage `json:"billed_units"`
				Tokens      *fingerprintReportedUsage `json:"tokens"`
			} `json:"meta"`
			PromptEvalCount *int64          `json:"prompt_eval_count"`
			EvalCount       *int64          `json:"eval_count"`
			Data            json.RawMessage `json:"data"`
			Response        json.RawMessage `json:"Response"`
		}
		if common.Unmarshal(body, &payload) != nil {
			return modelFingerprintUsage{}, fail
		}
		switch {
		case payload.Usage != nil:
			return payload.Usage.counts()
		case payload.UsageMetadata != nil:
			u := payload.UsageMetadata
			if !fingerprintUsageCountsValid(u.Prompt, u.Candidates, u.Thoughts, u.Total) || u.Prompt == nil || u.Candidates == nil || u.Total == nil {
				return modelFingerprintUsage{}, fail
			}
			output := *u.Candidates
			if u.Thoughts != nil {
				output += *u.Thoughts
			}
			return fingerprintUsageResult(*u.Prompt, output, *u.Total)
		case payload.Meta != nil && payload.Meta.Tokens != nil:
			return payload.Meta.Tokens.counts()
		case payload.Meta != nil && payload.Meta.BilledUnits != nil:
			return payload.Meta.BilledUnits.counts()
		case payload.PromptEvalCount != nil || payload.EvalCount != nil:
			if !fingerprintUsageCountsValid(payload.PromptEvalCount, payload.EvalCount) || payload.PromptEvalCount == nil || payload.EvalCount == nil {
				return modelFingerprintUsage{}, fail
			}
			return fingerprintUsageResult(*payload.PromptEvalCount, *payload.EvalCount, *payload.PromptEvalCount+*payload.EvalCount)
		case len(payload.Data) > 0:
			body = payload.Data
		case len(payload.Response) > 0:
			body = payload.Response
		default:
			return modelFingerprintUsage{}, fail
		}
	}
	return modelFingerprintUsage{}, fail
}

// SDK and WebSocket adaptors have no HTTP body for the controller to inspect.
// Their returned usage is still validated, including native billing provenance.
func fingerprintSiteAdaptorUsage(value any) (modelFingerprintUsage, error) {
	usage, ok := value.(*dto.Usage)
	if !ok || usage == nil {
		return modelFingerprintUsage{}, errors.New("usage_unknown")
	}
	var payload any
	if billing := usage.BillingUsage; billing != nil {
		if billing.Estimated {
			return modelFingerprintUsage{}, errors.New("usage_unknown")
		}
		switch {
		case billing.ClaudeUsage != nil:
			payload = map[string]any{"usage": billing.ClaudeUsage}
		case billing.GeminiUsageMetadata != nil:
			payload = map[string]any{"usageMetadata": billing.GeminiUsageMetadata}
		case billing.OpenAIUsage != nil:
			usage = billing.OpenAIUsage
		}
	}
	if payload == nil {
		return fingerprintUsageResult(int64(usage.PromptTokens), int64(usage.CompletionTokens), int64(usage.TotalTokens))
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return modelFingerprintUsage{}, errors.New("usage_unknown")
	}
	return fingerprintSiteReportedUsage(body)
}

// Coze's message-detail response has no usage. A verified completed-chat
// snapshot is the sole permitted alternate source for that protocol.
func fingerprintSiteCozeUsage(value any) (modelFingerprintUsage, error) {
	usage, ok := value.(*dto.Usage)
	if !ok || usage == nil || usage.BillingUsage == nil || usage.BillingUsage.Source != dto.BillingUsageSourceCozeChat || usage.BillingUsage.Estimated || usage.BillingUsage.OpenAIUsage == nil {
		return modelFingerprintUsage{}, errors.New("usage_unknown")
	}
	return fingerprintSiteAdaptorUsage(usage)
}
