// Package privacy contains the client-facing output boundary. It rewrites the
// client-requested model name and replaces upstream diagnostics with generic,
// allowlisted error categories for ordinary API callers. All patching works on
// the raw JSON text with gjson/sjson so unknown fields, tool arguments, user
// content, and numeric lexemes survive unchanged.
package privacy

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Restricted reports whether c is a public relay request whose response must
// only contain the client-requested model and generic errors. It is true for
// every relay caller regardless of role, because administrator diagnostics
// live in the log's admin_info scope rather than in the API response. The only
// exception is an administrator channel test: its synthetic probe has no
// end-user caller and its whole purpose is upstream diagnostics.
func Restricted(c *gin.Context) bool {
	if c == nil {
		return true
	}
	return !common.GetContextKeyBool(c, constant.ContextKeyChannelTest)
}

// RequestedModel returns the model name the client asked for. It is the only
// model identifier an ordinary caller may observe.
func RequestedModel(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
}

// SanitizeClientPayload sanitizes a client-bound payload using the current
// request context. Error messages are rebuilt from the safe category and the
// local request id; upstream text is never preserved by inspection.
func SanitizeClientPayload(c *gin.Context, payload []byte) []byte {
	if c == nil {
		return SanitizePayload(payload, "", true)
	}
	return sanitizePayload(payload, RequestedModel(c), Restricted(c), c.GetString(common.RequestIdKey))
}

// SanitizePayload rewrites recognized protocol identity fields to requested
// and, when restricted, replaces recognized error fields with generic safe
// categories. It is a no-op for non-JSON payloads and unknown shapes.
func SanitizePayload(payload []byte, requested string, restricted bool) []byte {
	return sanitizePayload(payload, requested, restricted, "")
}

func sanitizePayload(payload []byte, requested string, restricted bool, requestID string) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) || !gjson.ParseBytes(payload).IsObject() {
		return payload
	}
	result := payload
	if requested != "" {
		result = rewriteModelFields(result, requested)
	}
	if restricted {
		result = scrubErrorFields(result, requestID)
	}
	return result
}

// SanitizeClientOpenAIError converts an error into its client-facing form.
// Public relay callers receive a safe category and generic message carrying the
// local request id; an administrator channel test keeps the original error.
func SanitizeClientOpenAIError(c *gin.Context, status int, source types.OpenAIError) types.OpenAIError {
	if !Restricted(c) {
		return source
	}
	safe := types.OpenAIError{
		Message: SafeErrorText(status, source.Type),
		Type:    safeErrorType(source.Type, status),
		Code:    safeErrorCode(source.Code),
	}
	if requestID := localRequestID(c); requestID != "" {
		safe.Message = common.MessageWithRequestId(safe.Message, requestID)
	}
	return safe
}

// SanitizeClientClaudeError converts a Claude-shaped error into its
// client-facing form with the same audience rules as SanitizeClientOpenAIError.
func SanitizeClientClaudeError(c *gin.Context, status int, source types.ClaudeError) types.ClaudeError {
	if !Restricted(c) {
		return source
	}
	safe := types.ClaudeError{
		Message: SafeErrorText(status, source.Type),
		Type:    safeErrorType(source.Type, status),
	}
	if requestID := localRequestID(c); requestID != "" {
		safe.Message = common.MessageWithRequestId(safe.Message, requestID)
	}
	return safe
}

// localRequestID returns the local request correlation id, or "" when no
// context is available.
func localRequestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetString(common.RequestIdKey)
}

// SafeErrorText returns the generic message for an error with the given HTTP
// status and safe category. Responses and ordinary-user log projections use it
// so neither upstream text nor local diagnostics reach unprivileged callers.
func SafeErrorText(status int, category string) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid request"
	case http.StatusUnauthorized:
		return "authentication failed"
	case http.StatusForbidden:
		return "access denied"
	case http.StatusNotFound:
		return "not found"
	case http.StatusRequestTimeout:
		return "request timeout"
	case http.StatusConflict:
		return "request conflict"
	case http.StatusRequestEntityTooLarge:
		return "request too large"
	case http.StatusTooManyRequests:
		return "too many requests"
	}
	if status >= 500 {
		return "upstream request failed"
	}
	if status >= 400 {
		return "invalid request"
	}
	return safeCategoryText(category)
}

// IsSafeErrorCode reports whether code is a stable classification that cannot
// leak provider-specific identity or diagnostics.
func IsSafeErrorCode(code string) bool {
	_, ok := clientSafeErrorCodes[code]
	return ok
}

func safeErrorType(value string, status int) string {
	if _, ok := clientSafeErrorTypes[value]; ok && value != "" {
		return value
	}
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "server_error"
	case status >= 400:
		return "invalid_request_error"
	default:
		return "upstream_error"
	}
}

func safeErrorCode(code any) any {
	var text string
	switch value := code.(type) {
	case nil:
		return nil
	case string:
		text = value
	case types.ErrorCode:
		text = string(value)
	case float64:
		if value >= 100 && value <= 599 {
			return code
		}
		return nil
	case float32:
		if value >= 100 && value <= 599 {
			return code
		}
		return nil
	case int:
		if value >= 100 && value <= 599 {
			return code
		}
		return nil
	case int32:
		if value >= 100 && value <= 599 {
			return code
		}
		return nil
	case int64:
		if value >= 100 && value <= 599 {
			return code
		}
		return nil
	default:
		return nil
	}
	if IsSafeErrorCode(text) {
		return text
	}
	return nil
}

func safeCategoryText(category string) string {
	switch category {
	case "invalid_request_error", "invalid_request", "bad_request_body",
		"context_length_exceeded", "invalid_value", "missing_required_parameter",
		"unknown_parameter", "string_above_max_length", "unsupported_model",
		"model_not_available", "content_filter", "content_policy_violation",
		"request_too_large", "billing_hard_limit_reached":
		return "invalid request"
	case "authentication_error", "invalid_api_key", "invalid_organization":
		return "authentication failed"
	case "permission_error", "access_denied":
		return "access denied"
	case "not_found_error", "model_not_found":
		return "not found"
	case "rate_limit_error", "rate_limit_exceeded", "insufficient_quota",
		"too_many_requests", "model_overloaded", "overloaded":
		return "too many requests"
	case "server_error", "api_error", "upstream_error", "timeout_error":
		return "upstream request failed"
	default:
		return "request failed"
	}
}

// clientModelPaths are the protocol identity fields a client may observe. Only
// an existing string value is replaced, so malformed or unrelated shapes stay
// untouched and sjson never fabricates nested objects.
var clientModelPaths = []string{
	"model",         // OpenAI chat/embeddings/audio, Claude, converted protocols
	"modelVersion",  // native Gemini
	"message.model", // Claude message_start
	"response.model",
	"session.model", // Realtime session.created/session.updated
}

func rewriteModelFields(payload []byte, requested string) []byte {
	result := payload
	for _, path := range clientModelPaths {
		current := gjson.GetBytes(result, path)
		if current.Type != gjson.String || current.Str == "" || current.Str == requested {
			continue
		}
		patched, err := sjson.SetBytes(result, path, requested)
		if err != nil {
			continue
		}
		result = patched
	}
	return result
}

func isErrorEventType(eventType string) bool {
	switch eventType {
	case "error", "response.error", "response.failed":
		return true
	default:
		return false
	}
}

func scrubErrorFields(payload []byte, requestID string) []byte {
	result := payload
	if value := gjson.GetBytes(result, "response.error"); value.Exists() && value.Type != gjson.Null {
		result = replaceErrorValue(result, "response.error", value, requestID)
	}
	if value := gjson.GetBytes(result, "error"); value.Exists() && value.Type != gjson.Null {
		result = replaceErrorValue(result, "error", value, requestID)
	}
	if isErrorEventType(gjson.GetBytes(result, "type").Str) {
		result = scrubFlatErrorFields(result, requestID)
	}
	return result
}

func replaceErrorValue(payload []byte, path string, value gjson.Result, requestID string) []byte {
	if value.Type == gjson.String {
		patched, err := sjson.SetBytes(payload, path, scopedErrorText(genericErrorText("", value.Str, 0, ""), requestID))
		if err != nil {
			return payload
		}
		return patched
	}
	if !value.IsObject() {
		patched, err := sjson.SetBytes(payload, path, scopedErrorText("request failed", requestID))
		if err != nil {
			return payload
		}
		return patched
	}
	patched, err := sjson.SetRawBytes(payload, path, sanitizedErrorObject(value, requestID))
	if err != nil {
		return payload
	}
	return patched
}

// scopedErrorText appends the local request correlation id to a generic message.
func scopedErrorText(message string, requestID string) string {
	if requestID == "" {
		return message
	}
	return common.MessageWithRequestId(message, requestID)
}

// sanitizedErrorObject rebuilds an error object from the allowlisted category
// fields only. Provider-supplied param/metadata/details and every unrecognized
// key are dropped.
func sanitizedErrorObject(value gjson.Result, requestID string) []byte {
	typeValue := gjson.Get(value.Raw, "type").Str
	codeResult := gjson.Get(value.Raw, "code")
	statusValue := gjson.Get(value.Raw, "status").Str
	codeText := ""
	codeNumber := 0
	if codeResult.Type == gjson.String {
		codeText = codeResult.Str
	} else if codeResult.Type == gjson.Number {
		codeNumber = int(codeResult.Int())
	}

	message := scopedErrorText(genericErrorText(typeValue, codeText, codeNumber, statusValue), requestID)
	messageField := "message"
	if !gjson.Get(value.Raw, "message").Exists() && typeValue == "" && codeText == "" && statusValue == "" {
		// A native Gemini error identifies itself with a numeric code and status.
		// Keep that recognized shape and put the category in "status" instead of
		// fabricating a "message" field the upstream never sent.
		messageField = "status"
	}

	sanitized := []byte("{}")
	sanitized, _ = sjson.SetBytes(sanitized, messageField, message)
	if _, ok := clientSafeErrorTypes[typeValue]; ok && typeValue != "" {
		sanitized, _ = sjson.SetBytes(sanitized, "type", typeValue)
	}
	if codeResult.Exists() {
		switch codeResult.Type {
		case gjson.String:
			if IsSafeErrorCode(codeResult.Str) {
				sanitized, _ = sjson.SetBytes(sanitized, "code", codeResult.Str)
			}
		case gjson.Number:
			if number := codeResult.Int(); number >= 100 && number <= 599 {
				sanitized, _ = sjson.SetRawBytes(sanitized, "code", []byte(codeResult.Raw))
			}
		}
	}
	if _, ok := clientSafeGoogleStatuses[statusValue]; ok && statusValue != "" && messageField == "message" {
		sanitized, _ = sjson.SetBytes(sanitized, "status", statusValue)
	}
	return sanitized
}

// scrubFlatErrorFields handles Responses/Claude error events whose diagnostics
// live in flat top-level fields rather than a nested error object. The top
// level is rebuilt from the recognized envelope fields, so provider-supplied
// metadata, identifiers, and unknown keys cannot leak. Fields that were absent
// stay absent so successful frame shapes are not fabricated, and correlation
// fields (sequence number, event/response/stream ids, status) are preserved.
func scrubFlatErrorFields(payload []byte, requestID string) []byte {
	value := gjson.ParseBytes(payload)
	codeResult := value.Get("code")
	codeText := ""
	if codeResult.Type == gjson.String {
		codeText = codeResult.Str
	}

	sanitized := []byte("{}")
	if eventType := value.Get("type"); eventType.Exists() {
		sanitized, _ = sjson.SetRawBytes(sanitized, "type", []byte(eventType.Raw))
	}
	if status := value.Get("status"); status.Exists() {
		sanitized, _ = sjson.SetRawBytes(sanitized, "status", []byte(status.Raw))
	}
	if codeResult.Exists() {
		switch codeResult.Type {
		case gjson.String:
			if IsSafeErrorCode(codeResult.Str) {
				sanitized, _ = sjson.SetBytes(sanitized, "code", codeResult.Str)
			}
		case gjson.Number:
			if number := codeResult.Int(); number >= 100 && number <= 599 {
				sanitized, _ = sjson.SetRawBytes(sanitized, "code", []byte(codeResult.Raw))
			}
		}
	}
	if message := value.Get("message"); message.Exists() {
		sanitized, _ = sjson.SetBytes(sanitized, "message",
			scopedErrorText(genericErrorText(value.Get("type").Str, codeText, 0, ""), requestID))
	}
	for _, key := range []string{"sequence_number", "response_id", "event_id", "stream_id"} {
		if correlation := value.Get(key); correlation.Exists() {
			sanitized, _ = sjson.SetRawBytes(sanitized, key, []byte(correlation.Raw))
		}
	}
	// The nested values were already replaced in place by scrubErrorFields.
	for _, key := range []string{"error", "response"} {
		if nested := value.Get(key); nested.Exists() {
			sanitized, _ = sjson.SetRawBytes(sanitized, key, []byte(nested.Raw))
		}
	}
	return sanitized
}

func genericErrorText(typeValue, codeValue string, codeNumber int, statusValue string) string {
	if text := safeCategoryText(typeValue); text != "request failed" {
		return text
	}
	if text := safeCategoryText(codeValue); text != "request failed" {
		return text
	}
	if text := safeGoogleStatusText(statusValue); text != "" {
		return text
	}
	if codeNumber >= 400 && codeNumber <= 599 {
		return SafeErrorText(codeNumber, "")
	}
	return "upstream request failed"
}

func safeGoogleStatusText(status string) string {
	switch status {
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION", "OUT_OF_RANGE", "UNIMPLEMENTED":
		return "invalid request"
	case "UNAUTHENTICATED":
		return "authentication failed"
	case "PERMISSION_DENIED":
		return "access denied"
	case "NOT_FOUND":
		return "not found"
	case "RESOURCE_EXHAUSTED":
		return "too many requests"
	case "DEADLINE_EXCEEDED":
		return "request timeout"
	case "INTERNAL", "UNAVAILABLE", "UNKNOWN", "ABORTED", "DATA_LOSS":
		return "upstream request failed"
	case "CANCELLED":
		return "request cancelled"
	default:
		return ""
	}
}
