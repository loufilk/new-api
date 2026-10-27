package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/tidwall/gjson"
)

// ResponsesUsageAccumulator owns the accounting facts for one Responses stream.
// HTTP SSE and WebSocket transports feed the same events into it, then settle
// Finish's usage through the normal text billing path, including interrupted
// streams. Observe and Finish must be called by the same stream owner.
type ResponsesUsageAccumulator struct {
	info       *relaycommon.RelayInfo
	usage      *dto.Usage
	outputText strings.Builder
	tools      *relaycommon.ResponsesToolUsageObserver
	started    bool
	finished   bool
	failed     bool
	cancelled  bool
	hasOutput  bool
	terminal   bool // Observed provider terminal, never inferred from client status.
}

func NewResponsesUsageAccumulator(info *relaycommon.RelayInfo) *ResponsesUsageAccumulator {
	return &ResponsesUsageAccumulator{info: info, usage: &dto.Usage{}, tools: relaycommon.NewResponsesToolUsageObserver(info)}
}

// Observe feeds one decoded stream event. raw is the same event's wire bytes;
// the vendor tool-usage reader runs on it once, at the terminal event.
func (a *ResponsesUsageAccumulator) Observe(event *dto.ResponsesStreamResponse, raw []byte) relaycommon.ResponseOutcome {
	if a == nil || event == nil || a.finished {
		return relaycommon.ResponseOutcomeUnknown
	}
	if event.Response != nil {
		a.info.ObserveResponseModel(event.Response.Model)
		// Compatible providers can publish cumulative usage before their
		// terminal event. A later error or disconnected transport must not
		// erase already-observed billing facts.
		ApplyResponsesUsage(a.usage, event.Response.Usage)
	}
	a.started = true
	outcome := ObserveResponsesOutcome(a.info, event)
	a.terminal = a.terminal || outcome != relaycommon.ResponseOutcomeUnknown
	a.failed = a.failed || outcome == relaycommon.ResponseOutcomeFailed
	a.cancelled = a.cancelled || outcome == relaycommon.ResponseOutcomeCancelled
	a.hasOutput = a.hasOutput || responsesEventHasOutput(event, raw, outcome)
	a.tools.Observe(event, raw)
	if event.Type == "error" && event.Response != nil {
		a.tools.ObserveResponse(event.Response, raw)
	}
	switch event.Type {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "response.error", "error":
		if event.Response != nil {
			if a.outputText.Len() == 0 {
				// Some upstreams carry the output only on the terminal event.
				a.outputText.WriteString(relayconvert.ExtractOutputTextFromResponses(event.Response))
			}
		}
	case "response.output_text.delta", "response.function_call_arguments.delta",
		"response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.refusal.delta":
		// Every delta kind here is generated output that upstream bills as
		// output tokens, so all of them feed the missing-usage estimate.
		a.outputText.WriteString(event.Delta)
	}
	return outcome
}

// FinishBuffered finalizes accounting for an upstream SSE response that has not
// been sent to the client. Successful responses retain the normal nonstream
// estimator in the response converter. Failed buffers only estimate generated
// output; a prelude without usage or tool work must remain fully refundable.
// bufferedText is deduplicated generated text assembled from both deltas and
// whole items. It is an alternative estimate, never added to the same deltas.
func (a *ResponsesUsageAccumulator) FinishBuffered(failed bool, bufferedText string) *dto.Usage {
	if a.finished {
		return a.usage
	}
	if failed {
		a.failed = true
		if a.usage.CompletionTokens == 0 && bufferedText != "" && !responsesUsageUnpriced(a.usage) {
			// A completed output item may arrive without text deltas. Preserve
			// the larger observed estimate without double-billing its done item
			// or discarding reasoning/tool deltas already collected here.
			model := a.info.GetUpstreamModelName()
			a.usage.CompletionTokens = max(tokenkit.Count(model, bufferedText), tokenkit.Count(model, a.outputText.String()))
		}
		return a.Finish()
	}
	a.finished = true
	a.tools.Finish()
	return a.usage
}

func (a *ResponsesUsageAccumulator) Finish() *dto.Usage {
	if a.finished {
		return a.usage
	}
	a.finished = true
	// A final image item can already have reached the client before the stream
	// disconnects, so completed tool usage is retained even without a terminal.
	a.tools.Finish()
	// A created/in_progress prelude is not billable generation evidence. On an
	// uncompleted transport, do not manufacture prompt usage (or a fixed-price
	// charge) from that prelude. The transport must distinguish this unknown
	// result from an explicit provider rejection before refunding any hold.
	interrupted := false
	if !a.terminal && a.info != nil && a.info.StreamStatus != nil {
		end := a.info.StreamStatus.OutcomeSnapshot().EndReason
		interrupted = end != relaycommon.StreamEndReasonNone // A bare [DONE] is not a Responses terminal event.
	}
	if (a.failed || a.cancelled || interrupted) && !dto.HasOpenAIUsageTokens(a.usage) && !a.hasOutput && !a.tools.HasCalls() {
		a.usage = nil
		return nil
	}
	// A total/detail-only snapshot proves consumption but lacks the input /
	// output aggregates required for pricing. Preserve it for the common
	// unknown-delivery guard instead of clearing it or inventing a prompt.
	if responsesUsageUnpriced(a.usage) {
		return a.usage
	}
	if a.usage.CompletionTokens == 0 {
		if output := a.outputText.String(); output != "" {
			a.usage.CompletionTokens = tokenkit.Count(a.info.GetUpstreamModelName(), output)
		}
	}
	// Upstream bills the prompt as soon as it starts generating, so a stream
	// that produced any event but no usage still owes its input tokens unless
	// upstream reported an explicit failure.
	billsPrompt := a.usage.CompletionTokens != 0 || (a.started && !a.failed)
	if a.usage.PromptTokens == 0 && billsPrompt {
		a.usage.PromptTokens = a.info.GetEstimatePromptTokens()
	}
	a.usage.TotalTokens = a.usage.PromptTokens + a.usage.CompletionTokens
	if a.usage.BillingUsage != nil {
		a.usage.BillingUsage = dto.CloneBillingUsageWithEstimatedCompletion(a.usage.BillingUsage, a.usage.CompletionTokens)
		if original := a.usage.BillingUsage.OpenAIUsage; original != nil && original.InputTokens == 0 && original.PromptTokens == 0 && a.usage.PromptTokens > 0 {
			original.InputTokens = a.usage.PromptTokens
			original.PromptTokens = a.usage.PromptTokens
			original.TotalTokens = max(original.TotalTokens, a.usage.PromptTokens+a.usage.CompletionTokens)
			a.usage.BillingUsage.Estimated = true
		}
	}
	return a.usage
}

func responsesUsageUnpriced(usage *dto.Usage) bool {
	return usage != nil && usage.PromptTokens == 0 && usage.CompletionTokens == 0 && dto.HasOpenAIUsageTokens(usage)
}

// ObserveResponsesOutcome records the protocol outcome of one Responses event
// on the stream status for health classification. Only codes and types are
// kept; messages never leave the event.
func ObserveResponsesOutcome(info *relaycommon.RelayInfo, event *dto.ResponsesStreamResponse) relaycommon.ResponseOutcome {
	if event == nil {
		return relaycommon.ResponseOutcomeUnknown
	}
	var responseStatus string
	if event.Response != nil {
		_ = common.Unmarshal(event.Response.Status, &responseStatus)
	}
	var outcome relaycommon.ResponseOutcome
	switch {
	case event.Type == "error" || event.Type == "response.failed" || event.Type == "response.error" || responseStatus == "failed" || responseStatus == "error":
		outcome = relaycommon.ResponseOutcomeFailed
	case event.Type == "response.incomplete" || responseStatus == "incomplete":
		outcome = relaycommon.ResponseOutcomeIncomplete
	case event.Type == "response.cancelled" || event.Type == "response.canceled" || responseStatus == "cancelled" || responseStatus == "canceled":
		outcome = relaycommon.ResponseOutcomeCancelled
	case event.Type == "response.completed" || event.Type == "response.done" || responseStatus == "completed":
		outcome = relaycommon.ResponseOutcomeCompleted
	}
	if info == nil || info.StreamStatus == nil {
		return outcome
	}
	switch outcome {
	case relaycommon.ResponseOutcomeFailed:
		code, errorType := event.Code, ""
		if event.Response != nil {
			if oaiErr := event.Response.GetOpenAIError(); oaiErr != nil {
				if oaiErr.Code != nil {
					code = fmt.Sprint(oaiErr.Code)
				}
				errorType = oaiErr.Type
			}
		}
		info.StreamStatus.MarkFailed(code, errorType, 0)
	case relaycommon.ResponseOutcomeIncomplete:
		reason := ""
		if event.Response != nil && event.Response.IncompleteDetails != nil {
			reason = event.Response.IncompleteDetails.Reason
		}
		info.StreamStatus.MarkIncomplete(reason)
	case relaycommon.ResponseOutcomeCancelled:
		info.StreamStatus.MarkCancelled()
	case relaycommon.ResponseOutcomeCompleted:
		info.StreamStatus.MarkCompleted()
	}
	return outcome
}

// Whole output items can arrive without deltas or token usage. Inspect the
// wire payload as well as the DTO: compatible providers may include refusal or
// encrypted reasoning fields that the typed content model does not retain.
// This only preserves the existing billing path; it does not invent token use.
func responsesEventHasOutput(event *dto.ResponsesStreamResponse, raw []byte, outcome relaycommon.ResponseOutcome) bool {
	if len(raw) == 0 {
		raw, _ = common.Marshal(event)
	}
	root := gjson.ParseBytes(raw)
	var outputField string
	switch event.Type {
	case "response.output_text.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta",
		"response.mcp_call_arguments.delta", "response.code_interpreter_call_code.delta", "response.audio.delta", "response.audio_transcript.delta",
		"response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.refusal.delta":
		outputField = "delta"
	case "response.output_text.done", "response.reasoning_summary_text.done", "response.reasoning_text.done":
		outputField = "text"
	case "response.function_call_arguments.done", "response.mcp_call_arguments.done":
		outputField = "arguments"
	case "response.custom_tool_call_input.done":
		outputField = "input"
	case "response.code_interpreter_call_code.done":
		outputField = "code"
	case "response.refusal.done":
		outputField = "refusal"
	}
	if outputField != "" && root.Get(outputField).Str != "" {
		return true
	}
	var items []gjson.Result
	if event.Response != nil {
		if root.Get("response").Exists() {
			items = root.Get("response.output").Array()
		} else {
			// Non-streaming HTTP supplies the response itself, not an event
			// envelope. Its echoed input is never generated output.
			items = root.Get("output").Array()
		}
	}
	switch event.Type {
	case dto.ResponsesOutputTypeItemAdded, dto.ResponsesOutputTypeItemDone:
		items = append(items, root.Get("item"))
	case "response.content_part.added", "response.content_part.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		items = append(items, root.Get("part"))
	}
	for _, item := range items {
		status := item.Get("status").Str
		completed := status == "completed" || status == "" && (outcome != relaycommon.ResponseOutcomeUnknown || event.Type == dto.ResponsesOutputTypeItemDone)
		if completed && strings.HasSuffix(item.Get("type").Str, "_call") &&
			(item.Get("id").Str != "" || item.Get("name").Str != "" || item.Get("call_id").Str != "") {
			// Selecting a completed tool is generated work even when it takes
			// no arguments and has no configured per-call surcharge.
			return true
		}
		for _, field := range []string{"text", "refusal", "arguments", "input", "code", "output", "outputs", "result", "results", "tools", "encrypted_content"} {
			value := item.Get(field)
			if value.Type == gjson.String && value.Str != "" || value.IsObject() || value.IsArray() && len(value.Array()) > 0 {
				return true
			}
		}
		for _, field := range []string{"action", "queries", "operation"} {
			value := item.Get(field)
			if value.Type == gjson.String && value.Str != "" || value.IsObject() && len(value.Map()) > 0 || value.IsArray() && len(value.Array()) > 0 {
				return true
			}
		}
		for _, field := range []string{"content", "summary"} {
			for _, part := range item.Get(field).Array() {
				if part.Get("text").Str != "" || part.Get("refusal").Str != "" {
					return true
				}
			}
		}
	}
	return false
}

func ApplyResponsesUsage(dst *dto.Usage, src *dto.Usage) {
	if dst == nil || src == nil {
		return
	}
	incoming := relayconvert.NormalizeResponsesUsage(src)
	if src.InputTokensDetails != nil {
		inputDetails := src.InputTokensDetails.Clone()
		incoming.InputTokensDetails = &inputDetails
	}
	if src.OutputTokensDetails != nil {
		incoming.CompletionTokenDetails = *src.OutputTokensDetails
	}
	incoming.PromptCacheHitTokens = src.PromptCacheHitTokens
	dto.MergeUsageNonZero(dst, incoming)
	outputDetails := dst.CompletionTokenDetails
	if outputDetails != (dto.OutputTokenDetails{}) {
		dst.OutputTokensDetails = &outputDetails
	}
}
