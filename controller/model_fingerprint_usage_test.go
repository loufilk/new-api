package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFingerprintSiteReportedNativeUsage(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		input, output int64
	}{
		{"cline", `{"success":true,"data":{"usage":{"prompt_tokens":120,"completion_tokens":100,"total_tokens":220}}}`, 120, 100},
		{"tencent", `{"Response":{"Usage":{"PromptTokens":120,"CompletionTokens":100,"TotalTokens":220}}}`, 120, 100},
		{"cohere", `{"meta":{"billed_units":{"input_tokens":120,"output_tokens":100}}}`, 120, 100},
		{"cohere-free", `{"meta":{"tokens":{"input_tokens":120,"output_tokens":100},"billed_units":{"input_tokens":0,"output_tokens":0}}}`, 120, 100},
		{"cohere-discounted", `{"meta":{"tokens":{"input_tokens":120,"output_tokens":100},"billed_units":{"input_tokens":60,"output_tokens":50}}}`, 120, 100},
		{"cohere-tokens", `{"meta":{"tokens":{"input_tokens":120,"output_tokens":100}}}`, 120, 100},
		{"ollama", `{"prompt_eval_count":120,"eval_count":100}`, 120, 100},
		{"anthropic-cache-lifetimes", `{"usage":{"input_tokens":20,"output_tokens":100,"cache_read_input_tokens":80,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":10}}}`, 120, 100},
		{"openai", `{"usage":{"prompt_tokens":120,"completion_tokens":100,"total_tokens":220}}`, 120, 100},
		{"above-old-limit", `{"usage":{"input_tokens":1,"output_tokens":10000}}`, 1, 10000},
		{"anthropic", `{"usage":{"input_tokens":120,"output_tokens":100}}`, 120, 100},
		{"anthropic-cache", `{"usage":{"input_tokens":20,"output_tokens":100,"cache_read_input_tokens":80,"cache_creation_input_tokens":20}}`, 120, 100},
		{"gemini", `{"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":100,"totalTokenCount":220}}`, 120, 100},
		{"gemini-thinking", `{"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":40,"thoughtsTokenCount":60,"totalTokenCount":220}}`, 120, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, err := fingerprintSiteReportedUsage([]byte(tc.body))
			require.NoError(t, err)
			assert.Equal(t, tc.input, usage.PromptTokens)
			assert.Equal(t, tc.output, usage.CompletionTokens)
			assert.Equal(t, tc.input+tc.output, usage.TotalTokens)
		})
	}
	for _, body := range []string{
		`{"meta":{"billed_units":{"input_tokens":1}}}`,
		`{"meta":{"tokens":{"input_tokens":-1,"output_tokens":1},"billed_units":{"input_tokens":1,"output_tokens":1}}}`,
		`{"prompt_eval_count":1,"eval_count":-1}`,
		`{"prompt_eval_count":1}`,
		`{"Response":{"Usage":{"PromptTokens":1,"CompletionTokens":2,"TotalTokens":4}}}`,
		`{"data":{"data":{"data":{"data":{"usage":{"input_tokens":1,"output_tokens":1}}}}}}`,
		`{}`, `{"usage":{}}`, `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		`{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":1}}`,
		`{"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":-1}}`,
		`{"usage":{"input_tokens":2147483647,"output_tokens":1,"cache_creation_input_tokens":1}}`,
		`{"usage":{"input_tokens":1,"output_tokens":18446744073709551615}}`,
		`{"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":3}}`,
		`{"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2}}`,
		`{"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":4}}`,
		`{"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"thoughtsTokenCount":-1,"totalTokenCount":2}}`,
		`{"usage":{"prompt_tokens":0,"completion_tokens":1,"total_tokens":1}}`,
	} {
		_, err := fingerprintSiteReportedUsage([]byte(body))
		assert.Error(t, err, body)
	}
}

func TestFingerprintSiteAdaptorUsage(t *testing.T) {
	for _, usage := range []*dto.Usage{
		{PromptTokens: 120, CompletionTokens: 100, TotalTokens: 220},
		{BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 20, CacheReadInputTokens: 80, CacheCreationInputTokens: 20, OutputTokens: 100})},
		{BillingUsage: dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{PromptTokenCount: 120, CandidatesTokenCount: 80, ThoughtsTokenCount: 20, TotalTokenCount: 220})},
		{BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 120, CompletionTokens: 100, TotalTokens: 220})},
	} {
		result, err := fingerprintSiteAdaptorUsage(usage)
		require.NoError(t, err)
		assert.EqualValues(t, 120, result.PromptTokens)
		assert.EqualValues(t, 100, result.CompletionTokens)
		assert.EqualValues(t, 220, result.TotalTokens)
	}
	for _, usage := range []any{
		nil, (*dto.Usage)(nil), &dto.Usage{},
		&dto.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 2},
		&dto.Usage{PromptTokens: 2147483647, CompletionTokens: 1, TotalTokens: 2147483648},
		&dto.Usage{PromptTokens: 120, CompletionTokens: 100, TotalTokens: 220, BillingUsage: &dto.BillingUsage{Estimated: true}},
	} {
		_, err := fingerprintSiteAdaptorUsage(usage)
		assert.Error(t, err)
	}
}

func TestFingerprintSiteCozeCompletedUsage(t *testing.T) {
	reported := &dto.Usage{PromptTokens: 120, CompletionTokens: 100, TotalTokens: 220}
	usage := &dto.Usage{BillingUsage: &dto.BillingUsage{Source: "coze_chat", Semantic: dto.BillingUsageSemanticOpenAI, OpenAIUsage: reported}}
	result, err := fingerprintSiteCozeUsage(usage)
	require.NoError(t, err)
	assert.EqualValues(t, 220, result.TotalTokens)
	for _, invalid := range []any{
		nil, reported,
		&dto.Usage{BillingUsage: dto.NewOpenAIChatBillingUsage(reported)},
		&dto.Usage{BillingUsage: &dto.BillingUsage{Source: "coze_chat", Estimated: true, OpenAIUsage: reported}},
		&dto.Usage{BillingUsage: &dto.BillingUsage{Source: "coze_chat"}},
		&dto.Usage{BillingUsage: &dto.BillingUsage{Source: "coze_chat", OpenAIUsage: &dto.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 1}}},
	} {
		_, err := fingerprintSiteCozeUsage(invalid)
		assert.Error(t, err)
	}
}
