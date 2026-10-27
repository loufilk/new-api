package privacy

import "github.com/QuantumNous/new-api/relaykit/types"

// clientSafeErrorTypes are the categories an ordinary caller may observe.
// They are either protocol-standard classifications or new-api's own stable
// classifications, never free-form provider text.
var clientSafeErrorTypes = map[string]struct{}{
	string(types.ErrorTypeNewAPIError):     {},
	string(types.ErrorTypeOpenAIError):     {},
	string(types.ErrorTypeClaudeError):     {},
	string(types.ErrorTypeMidjourneyError): {},
	string(types.ErrorTypeGeminiError):     {},
	string(types.ErrorTypeRerankError):     {},
	string(types.ErrorTypeUpstreamError):   {},

	"invalid_request_error": {},
	"authentication_error":  {},
	"permission_error":      {},
	"not_found_error":       {},
	"rate_limit_error":      {},
	"api_error":             {},
	"overloaded_error":      {},
	"server_error":          {},
	"timeout_error":         {},
	"request_too_large":     {},
	"insufficient_quota":    {},
	"billing_error":         {},
}

// clientSafeErrorCodes are stable classifications from the OpenAI/Anthropic/
// new-api protocols. Provider-specific codes such as organization ids or
// request identifiers are not listed and are therefore dropped.
var clientSafeErrorCodes = map[string]struct{}{
	// new-api internal classifications.
	string(types.ErrorCodeInvalidRequest):               {},
	string(types.ErrorCodeSensitiveWordsDetected):       {},
	string(types.ErrorCodeViolationFeeGrokCSAM):         {},
	string(types.ErrorCodeCountTokenFailed):             {},
	string(types.ErrorCodeModelPriceError):              {},
	string(types.ErrorCodeInvalidApiType):               {},
	string(types.ErrorCodeJsonMarshalFailed):            {},
	string(types.ErrorCodeDoRequestFailed):              {},
	string(types.ErrorCodeGetChannelFailed):             {},
	string(types.ErrorCodeGenRelayInfoFailed):           {},
	string(types.ErrorCodeChannelNoAvailableKey):        {},
	string(types.ErrorCodeChannelParamOverrideInvalid):  {},
	string(types.ErrorCodeChannelHeaderOverrideInvalid): {},
	string(types.ErrorCodeChannelModelMappedError):      {},
	string(types.ErrorCodeChannelAwsClientError):        {},
	string(types.ErrorCodeChannelInvalidKey):            {},
	string(types.ErrorCodeChannelResponseTimeExceeded):  {},
	string(types.ErrorCodeReadRequestBodyFailed):        {},
	string(types.ErrorCodeConvertRequestFailed):         {},
	string(types.ErrorCodeAccessDenied):                 {},
	string(types.ErrorCodeBadRequestBody):               {},
	string(types.ErrorCodeReadResponseBodyFailed):       {},
	string(types.ErrorCodeBadResponseStatusCode):        {},
	string(types.ErrorCodeBadResponse):                  {},
	string(types.ErrorCodeBadResponseBody):              {},
	string(types.ErrorCodeEmptyResponse):                {},
	string(types.ErrorCodeAwsInvokeError):               {},
	string(types.ErrorCodeModelNotFound):                {},
	string(types.ErrorCodePromptBlocked):                {},
	string(types.ErrorCodeQueryDataError):               {},
	string(types.ErrorCodeUpdateDataError):              {},
	string(types.ErrorCodeInsufficientUserQuota):        {},
	string(types.ErrorCodePreConsumeTokenQuotaFailed):   {},

	// Protocol-standard codes.
	"invalid_api_key":            {},
	"invalid_organization":       {},
	"insufficient_quota":         {},
	"rate_limit_exceeded":        {},
	"context_length_exceeded":    {},
	"content_filter":             {},
	"content_policy_violation":   {},
	"string_above_max_length":    {},
	"invalid_value":              {},
	"missing_required_parameter": {},
	"unknown_parameter":          {},
	"unsupported_model":          {},
	"model_not_available":        {},
	"model_overloaded":           {},
	"too_many_requests":          {},
	"overloaded":                 {},
	"timeout_error":              {},
	"server_error":               {},
	"api_error":                  {},
	"authentication_error":       {},
	"permission_error":           {},
	"not_found_error":            {},
	"rate_limit_error":           {},
	"invalid_request_error":      {},
	"request_too_large":          {},
	"billing_hard_limit_reached": {},
	"image_generation_failed":    {},
	"task_timeout":               {},
	"request_cancelled":          {},
}

// clientSafeGoogleStatuses are the canonical google.rpc.Status codes carried by
// native Gemini errors.
var clientSafeGoogleStatuses = map[string]struct{}{
	"INVALID_ARGUMENT":    {},
	"UNAUTHENTICATED":     {},
	"PERMISSION_DENIED":   {},
	"NOT_FOUND":           {},
	"RESOURCE_EXHAUSTED":  {},
	"FAILED_PRECONDITION": {},
	"ABORTED":             {},
	"OUT_OF_RANGE":        {},
	"UNIMPLEMENTED":       {},
	"INTERNAL":            {},
	"UNAVAILABLE":         {},
	"DATA_LOSS":           {},
	"DEADLINE_EXCEEDED":   {},
	"CANCELLED":           {},
	"UNKNOWN":             {},
}
