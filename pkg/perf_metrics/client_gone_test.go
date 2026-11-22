package perfmetrics

import (
	"context"
	"io"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
)

func TestDownstreamWriteFailureIsNotUpstreamHealthFailure(t *testing.T) {
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	info.StreamStatus.MarkCancelled()
	local := types.NewErrorWithStatusCode(io.ErrClosedPipe, "client_gone", 499, types.ErrOptionWithSkipRetry())
	assert.Equal(t, OutcomeIgnored, ClassifyRelayOutcome(context.Background(), info, local))
	// Do not allow an upstream-provided error code to impersonate a local event.
	upstream := types.WithOpenAIError(types.OpenAIError{Code: "client_gone", Type: "server_error", Message: "upstream failure"}, 503)
	assert.Equal(t, OutcomeFailure, ClassifyRelayOutcome(context.Background(), info, upstream))
}
