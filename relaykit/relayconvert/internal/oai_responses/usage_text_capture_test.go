package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesToChatUsageTextCaptureModes(t *testing.T) {
	event := &dto.ResponsesStreamResponse{
		Type:  responsesEventOutputTextDelta,
		Delta: "hello world",
	}

	captured := NewResponsesToChatStreamState("gpt-test", false)
	_, err := ResponsesStreamEventToChatChunks(event, captured)
	require.NoError(t, err)
	assert.Equal(t, "hello world", captured.UsageText())

	streamed := NewResponsesToChatStreamStateWithoutUsageText("gpt-test", false)
	_, err = ResponsesStreamEventToChatChunks(event, streamed)
	require.NoError(t, err)
	assert.Empty(t, streamed.UsageText())
}
