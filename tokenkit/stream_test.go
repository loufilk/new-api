package tokenkit

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestStreamingEstimatePreservesChunkBoundaries(t *testing.T) {
	for _, model := range []string{"gpt-5", "claude-sonnet-4-6", "gemini-2.5-pro", "unknown"} {
		t.Run(model, func(t *testing.T) {
			text := "Hello123 中文🙂\nαβ=42 https://example.com"
			estimator := NewStreamingEstimate(model)
			for i := range len(text) {
				estimator.Write([]byte{text[i]})
				assert.Equal(t, Estimate(model, text[:i+1]), estimator.Tokens(), "UTF-8 snapshot at byte %d", i)
			}
			assert.Equal(t, Estimate(model, text), estimator.Tokens())
		})
	}
}
