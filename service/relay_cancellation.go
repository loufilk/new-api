package service

import (
	"context"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ClientGoneErrorCode identifies downstream delivery failures, never an upstream
// account failure. 499 is diagnostic only: a disconnected client is not written to.
const ClientGoneErrorCode types.ErrorCode = "client_gone"
const StatusClientClosedRequest = 499

// RelayRequestCancellation checks the parent request, not a per-attempt timeout.
// A timed-out upstream may still be retried while this context remains live.
func RelayRequestCancellation(c *gin.Context) *types.NewAPIError {
	if c == nil || c.Request == nil || c.Request.Context().Err() == nil {
		return nil
	}
	err := c.Request.Context().Err()
	code, status := types.ErrorCode("request_deadline_exceeded"), http.StatusRequestTimeout
	if err == context.Canceled {
		return NewRelayClientGoneError(err)
	}
	return types.NewErrorWithStatusCode(err, code, status, types.ErrOptionWithSkipRetry())
}

// NewRelayClientGoneError classifies a downstream cancellation or delivery
// failure. It remains observable through request/termination logs, but is not
// an upstream channel error-table entry and must not consume retry budget.
func NewRelayClientGoneError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, ClientGoneErrorCode, StatusClientClosedRequest,
		types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
}
