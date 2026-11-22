package service

import (
	"context"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestResponsesPreludeInterruptedIsNotUsageEvidence(t *testing.T) {
	for _, end := range []relaycommon.StreamEndReason{relaycommon.StreamEndReasonClientGone, relaycommon.StreamEndReasonTimeout, relaycommon.StreamEndReasonScannerErr, relaycommon.StreamEndReasonEOF, relaycommon.StreamEndReasonDone} {
		t.Run(string(end), func(t *testing.T) {
			info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
			info.SetEstimatePromptTokens(100)
			a := NewResponsesUsageAccumulator(info)
			a.Observe(&dto.ResponsesStreamResponse{Type: "response.created"}, nil)
			a.Observe(&dto.ResponsesStreamResponse{Type: "response.in_progress"}, nil)
			info.StreamStatus.SetEndReason(end, context.Canceled)
			require.Nil(t, a.Finish(), "a prelude cannot authorize estimated prompt or per-request billing")
			require.Nil(t, a.Finish(), "repeated Finish must not revive an estimate")
		})
	}
	t.Run("completed_retains_normal_policy", func(t *testing.T) {
		info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
		info.SetEstimatePromptTokens(100)
		a := NewResponsesUsageAccumulator(info)
		a.Observe(&dto.ResponsesStreamResponse{Type: "response.completed"}, nil)
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)
		usage := a.Finish()
		require.NotNil(t, usage)
		require.Equal(t, 100, usage.PromptTokens)
	})
}
