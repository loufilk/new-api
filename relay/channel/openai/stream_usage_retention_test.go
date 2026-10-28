package openai

import (
	"strings"
	"testing"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOaiStreamRetainsCumulativeUsageBeforeDeliveryLoss(t *testing.T) {
	first := `data: {"id":"usage-first","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24,"prompt_tokens_details":{"cached_tokens":8,"cache_write_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}` + "\n\n"
	updated := `data: {"id":"usage-updated","choices":[],"usage":{"completion_tokens":6,"total_tokens":26}}` + "\n\n"
	empty := func(id string) string {
		return `data: {"id":"` + id + `","choices":[{"index":0,"delta":{}}]}` + "\n\n"
	}
	for _, mode := range []string{"write", "flush"} {
		t.Run(mode, func(t *testing.T) {
			// Two empty frames separate the usage snapshot from the failed write;
			// retaining only the last and second-last frames loses measured work.
			body := first + updated + updated + empty("empty-one") + empty("empty-two") + empty("empty-three")
			c, _, info, resp := responsesStreamFixture(t, strings.NewReader(body))
			info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
			info.SetEstimatePromptTokens(100)
			writer := attachStreamDeliveryFailure(c, `"id":"empty-two"`, mode)

			usage, apiErr := OaiStreamHandler(c, info, resp)

			assertStreamDeliveryFailure(t, apiErr, writer)
			require.NotNil(t, usage)
			assert.Equal(t, 20, usage.PromptTokens)
			assert.Equal(t, 6, usage.CompletionTokens, "cumulative/repeated snapshots must overwrite, not add")
			assert.Equal(t, 26, usage.TotalTokens)
			assert.Equal(t, 8, usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, 3, usage.PromptTokensDetails.CacheWriteTokens)
			assert.Equal(t, 2, usage.CompletionTokenDetails.ReasoningTokens)
		})
	}
}

func TestOaiStreamFinalUsageOverridesEarlierSnapshot(t *testing.T) {
	body := `data: {"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":8,"cache_write_tokens":3}}}` + "\n\n" +
		deliveryChatChunk(`{"content":"generated output"}`) +
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":24,"completion_tokens":9,"total_tokens":33,"prompt_tokens_details":{"cached_tokens":10}}}` + "\n\ndata: [DONE]\n\n"
	c, _, info, resp := responsesStreamFixture(t, strings.NewReader(body))
	info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
	usage, apiErr := OaiStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 24, usage.PromptTokens)
	assert.Equal(t, 9, usage.CompletionTokens)
	assert.Equal(t, 33, usage.TotalTokens)
	assert.Equal(t, 10, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 3, usage.PromptTokensDetails.CacheWriteTokens)
}

func TestOaiStreamMissingUsageKeepsPartialOutputAndUnknownDistinct(t *testing.T) {
	for _, output := range []string{"", "partial output"} {
		t.Run(output, func(t *testing.T) {
			body := deliveryChatChunk(`{"role":"assistant"}`)
			if output != "" {
				body += deliveryChatChunk(`{"content":"partial output"}`)
			}
			body += `data: {"id":"fail-here","choices":[{"index":0,"delta":{}}]}` + "\n\n" + deliveryChatChunk(`{}`)
			c, _, info, resp := responsesStreamFixture(t, strings.NewReader(body))
			info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
			info.SetEstimatePromptTokens(20)
			writer := attachStreamDeliveryFailure(c, `"id":"fail-here"`, "write")
			usage, apiErr := OaiStreamHandler(c, info, resp)
			assertStreamDeliveryFailure(t, apiErr, writer)
			if output == "" {
				assert.Nil(t, usage, "missing accounting evidence must not become a fabricated zero-usage receipt")
				return
			}
			require.NotNil(t, usage)
			assert.Equal(t, 20, usage.PromptTokens)
			assert.Equal(t, service.ResponseText2Usage(c, output, info.UpstreamModelName, 20).CompletionTokens, usage.CompletionTokens)
			assert.Positive(t, usage.CompletionTokens)
		})
	}
}

func TestOaiStreamHTTP200WithoutTerminalIsNotCompletion(t *testing.T) {
	for _, output := range []string{"", "partial output"} {
		t.Run(output, func(t *testing.T) {
			body := deliveryChatChunk(`{"role":"assistant"}`)
			if output != "" {
				body += deliveryChatChunk(`{"content":"partial output"}`)
			}
			c, writer, info, resp := responsesStreamFixture(t, strings.NewReader(body))
			info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
			info.SetEstimatePromptTokens(20)
			usage, apiErr := OaiStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.NotContains(t, writer.Body.String(), "[DONE]", "an interrupted stream must not synthesize successful completion")
			if output == "" {
				assert.Nil(t, usage)
			} else {
				require.NotNil(t, usage)
				assert.Positive(t, usage.CompletionTokens)
			}
		})
	}
}

func TestOaiStreamRetainsDetailOnlyUsageAfterEOF(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire string
		want dto.Usage
	}{
		{"cached input", `{"prompt_tokens_details":{"cached_tokens":8,"cache_write_tokens":3}}`, dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 8, CacheWriteTokens: 3}}},
		{"reasoning output", `{"completion_tokens_details":{"reasoning_tokens":2}}`, dto.Usage{CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 2}}},
		{"audio output", `{"completion_tokens_details":{"audio_tokens":5}}`, dto.Usage{CompletionTokenDetails: dto.OutputTokenDetails{AudioTokens: 5}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `data: {"choices":[],"usage":` + tc.wire + "}\n\n" + deliveryChatChunk(`{"role":"assistant"}`)
			c, _, info, resp := responsesStreamFixture(t, strings.NewReader(body))
			info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
			info.SetEstimatePromptTokens(100)
			usage, apiErr := OaiStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			require.NotNil(t, usage, "all measured token dimensions must survive an interrupted stream")
			assert.Equal(t, tc.want, *usage)
			assert.True(t, types.IsSkipRetryError(apiErr))
		})
	}
}

func TestOaiStreamBareDoneRemainsNativeChatTerminal(t *testing.T) {
	body := deliveryChatChunk(`{"role":"assistant"}`) + "data: [DONE]\n\n"
	c, writer, info, resp := responsesStreamFixture(t, strings.NewReader(body))
	info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
	info.SetEstimatePromptTokens(20)
	usage, apiErr := OaiStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 20, usage.PromptTokens)
	assert.Contains(t, writer.Body.String(), "[DONE]")
}

func TestChatToResponsesEOFDoesNotSynthesizeCompletion(t *testing.T) {
	for _, output := range []string{"", "partial output"} {
		t.Run(output, func(t *testing.T) {
			body := deliveryChatChunk(`{"role":"assistant"}`)
			if output != "" {
				body += deliveryChatChunk(`{"content":"partial output"}`)
			}
			c, writer, info, resp := responsesStreamFixture(t, strings.NewReader(body))
			info.RelayFormat, info.RelayMode = types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses
			info.SetEstimatePromptTokens(20)
			usage, apiErr := OaiChatToResponsesStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.NotContains(t, writer.Body.String(), "event: response.completed")
			assert.Contains(t, writer.Body.String(), "event: response.failed")
			if output == "" {
				assert.Nil(t, usage, "a local terminal must not manufacture prompt usage from a Chat role prelude")
			} else {
				require.NotNil(t, usage)
				assert.Positive(t, usage.CompletionTokens)
			}
		})
	}
}

func TestChatToResponsesBareDoneRemainsNativeChatTerminal(t *testing.T) {
	body := deliveryChatChunk(`{"role":"assistant"}`) + "data: [DONE]\n\n"
	c, writer, info, resp := responsesStreamFixture(t, strings.NewReader(body))
	info.RelayFormat, info.RelayMode = types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses
	info.SetEstimatePromptTokens(20)
	usage, apiErr := OaiChatToResponsesStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 20, usage.PromptTokens)
	assert.Contains(t, writer.Body.String(), "event: response.completed")
}

func TestOaiStreamProviderErrorRetainsObservedUsageAndOutput(t *testing.T) {
	for _, ending := range []string{"", "data: [DONE]\n\n"} {
		t.Run("ending="+ending, func(t *testing.T) {
			body := `data: {"id":"prior-work","choices":[{"index":0,"delta":{"content":"observed output"}}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24,"prompt_tokens_details":{"cached_tokens":8}}}` + "\n\n" +
				`data: {"error":{"type":"server_error","code":"bad_response_body","message":"provider failure"},"usage":{"completion_tokens":6,"total_tokens":26}}` + "\n\n" + ending
			c, writer, info, resp := responsesStreamFixture(t, strings.NewReader(body))
			info.RelayFormat, info.RelayMode = types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions
			info.SetEstimatePromptTokens(100)
			usage, apiErr := OaiStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
			assert.True(t, types.IsSkipRetryError(apiErr))
			require.NotNil(t, usage)
			assert.Equal(t, 20, usage.PromptTokens)
			assert.Equal(t, 6, usage.CompletionTokens)
			assert.Equal(t, 26, usage.TotalTokens)
			assert.Equal(t, 8, usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, 1, strings.Count(writer.Body.String(), "observed output"), "flush prior buffered output exactly once")
			assert.NotContains(t, writer.Body.String(), "[DONE]")
		})
	}
}
