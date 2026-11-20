package privacy

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ClientOpenAIError preserves the native gateway error contract. Provider- and
// plugin-controlled diagnostics still go through the existing privacy boundary.
func ClientOpenAIError(c *gin.Context, apiErr *types.NewAPIError) types.OpenAIError {
	source := apiErr.ToOpenAIError(clientErrorUpstreamAddresses(c)...)
	if message, ok := nativeGatewayMessage(c, apiErr, source.Message); ok {
		source.Message = message
		return source
	}
	return SanitizeClientOpenAIError(c, apiErr.StatusCode, source)
}

// ClientClaudeError applies the same boundary to the native Claude envelope.
func ClientClaudeError(c *gin.Context, apiErr *types.NewAPIError) types.ClaudeError {
	source := apiErr.ToClaudeError(clientErrorUpstreamAddresses(c)...)
	if message, ok := nativeGatewayMessage(c, apiErr, source.Message); ok {
		source.Message = message
		return source
	}
	return SanitizeClientClaudeError(c, apiErr.StatusCode, source)
}

// ClientGeminiError returns the native Google JSON error envelope and shares
// the same privacy projection of the human-readable diagnostic.
func ClientGeminiError(c *gin.Context, apiErr *types.NewAPIError) gin.H {
	status := "INTERNAL"
	switch apiErr.StatusCode {
	case http.StatusBadRequest:
		status = "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		status = "UNAUTHENTICATED"
	case http.StatusForbidden:
		status = "PERMISSION_DENIED"
	case http.StatusNotFound:
		status = "NOT_FOUND"
	case http.StatusConflict:
		status = "ABORTED"
	case http.StatusTooManyRequests, http.StatusRequestEntityTooLarge:
		status = "RESOURCE_EXHAUSTED"
	case http.StatusNotImplemented:
		status = "UNIMPLEMENTED"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		status = "UNAVAILABLE"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		status = "DEADLINE_EXCEEDED"
	}
	return gin.H{"error": gin.H{"code": apiErr.StatusCode, "message": ClientOpenAIError(c, apiErr).Message, "status": status}}
}

func clientErrorUpstreamAddresses(c *gin.Context) []string {
	if c == nil {
		return nil
	}
	return common.UpstreamAddresses(c)
}

// IsNativeGatewayClientError distinguishes a typed local gateway error from
// a raw payload that merely claims to have the same error type or code.
func IsNativeGatewayClientError(apiErr *types.NewAPIError) bool {
	if apiErr.GetErrorType() != types.ErrorTypeNewAPIError ||
		apiErr.StatusCode < http.StatusBadRequest || apiErr.StatusCode >= http.StatusInternalServerError {
		return false
	}
	// Even a gateway wrapper can carry an upstream response body, an internal
	// infrastructure error or an administrator's plugin diagnostics.
	switch apiErr.GetErrorCode() {
	case types.ErrorCodeInvalidRequest, types.ErrorCodeBadRequestBody,
		types.ErrorCodeAccessDenied, types.ErrorCodeModelNotFound, types.ErrorCodeInsufficientUserQuota:
		return true
	default:
		return false
	}
}

func nativeGatewayMessage(c *gin.Context, apiErr *types.NewAPIError, message string) (string, bool) {
	if !Restricted(c) || !IsNativeGatewayClientError(apiErr) {
		return "", false
	}
	if c != nil {
		if key := common.GetContextKeyString(c, constant.ContextKeyChannelKey); key != "" {
			message = strings.ReplaceAll(message, key, "***")
		}
		if requestID := localRequestID(c); requestID != "" {
			suffix := common.MessageWithRequestId("", requestID)
			if !strings.HasSuffix(message, suffix) {
				message = common.MessageWithRequestId(message, requestID)
			}
		}
	}
	return message, true
}
