package privacy

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// ClientErrorCode applies the administrator's visibility policy. HTTP transport
// status is deliberately unaffected; 413 diagnostics remain private in all modes.
func ClientErrorCode(status int, code any) any {
	if status == http.StatusRequestEntityTooLarge || !operation_setting.GetLogSetting().ClientErrorCodesEnabled {
		return nil
	}
	return safeErrorCode(code)
}

// ClientErrorText classifies 400/429 diagnostics into fixed, actionable messages.
// Never echo upstream text, parameters, URLs, credentials or provider identities.
// Unknown messages fail closed to the original generic category.
func ClientErrorText(status int, category string, code any, diagnostic string) string {
	if status != http.StatusBadRequest && status != http.StatusTooManyRequests {
		return SafeErrorText(status, category)
	}
	if len(diagnostic) > 8192 {
		diagnostic = diagnostic[:8192]
	}
	text := strings.ToLower(diagnostic)
	if safe := safeErrorCode(code); safe != nil {
		text = fmt.Sprint(safe) + " " + text
	}
	if status == http.StatusTooManyRequests {
		switch {
		case strings.Contains(text, "concurrency"), strings.Contains(text, "concurrent"):
			return "too many concurrent requests; reduce parallel requests and retry later"
		case strings.Contains(text, "token") && (strings.Contains(text, "rate") || strings.Contains(text, "per minute")):
			return "token rate limit exceeded; reduce request size or frequency and retry later"
		default:
			return "too many requests"
		}
	}
	switch {
	case strings.Contains(text, "context_length_exceeded"), strings.Contains(text, "context window"), strings.Contains(text, "context length exceeded"), strings.Contains(text, "input is too long"):
		return "context length exceeded; shorten the conversation or reduce the output limit"
	case strings.Contains(text, "tool") && (strings.Contains(text, "tool_result") || strings.Contains(text, "tool_call_id") || strings.Contains(text, "invalid tool conversation") || strings.Contains(text, "tool_history_mismatch") || strings.Contains(text, "tool history")):
		return "invalid tool conversation; match each tool result to its tool call and preserve message order"
	case strings.Contains(text, "tool_schema_rejected"), strings.Contains(text, "tool schema"), strings.Contains(text, "invalid tool definition"):
		return "invalid tool definition; check tool names and JSON schemas"
	case strings.Contains(text, "thinking"), strings.Contains(text, "reasoning_controls_rejected"), strings.Contains(text, "reasoning controls"):
		return "invalid thinking configuration; check the thinking mode, budget and compatible message format"
	case strings.Contains(text, "image") && (strings.Contains(text, "format") || strings.Contains(text, "invalid")):
		return "invalid image input; check the image encoding, format and size"
	case strings.Contains(text, "missing_required_parameter"):
		return "missing required parameter; check the request schema"
	case strings.Contains(text, "unknown_parameter"):
		return "unsupported request parameter; check the model's supported parameters"
	default:
		return SafeErrorText(status, category)
	}
}

// errorStatus derives only standard classifications for streaming error payloads,
// where an HTTP error status is unavailable after the stream has started.
func errorStatus(category, code string, number int) int {
	if number >= 400 && number <= 599 {
		return number
	}
	if category == "request_too_large" || code == "request_too_large" {
		return 413
	}
	if category == "rate_limit_error" || code == "rate_limit_exceeded" || code == "too_many_requests" {
		return 429
	}
	switch code {
	case "tool_history_mismatch", "tool_schema_rejected", "missing_thinking_signature", "thinking_signature_rejected":
		return 400
	}
	if category == "invalid_request_error" || code == "context_length_exceeded" || code == "invalid_request" || code == "missing_required_parameter" || code == "unknown_parameter" || code == "reasoning_controls_rejected" {
		return 400
	}
	return 0
}
