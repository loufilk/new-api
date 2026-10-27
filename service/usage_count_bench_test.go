package service

import (
	"github.com/QuantumNous/new-api/tokenkit"
	"strings"
	"testing"
)

func benchmarkTextLarge() string {
	var builder strings.Builder
	builder.Grow(280000)
	chunk := "hello world version 3.5 你好世界 \U0001F600 ∑ https://api.example.com/v1/chat?model=gpt "
	for builder.Len() < 270000 {
		builder.WriteString(chunk)
	}
	return builder.String()
}

func BenchmarkChatViaResponsesFallbackUsage(b *testing.B) {
	text := benchmarkTextLarge()
	models := []string{"gpt-4o", "claude-3-5-sonnet"}

	runes := []rune(text)
	const chunkRunes = 32
	var chunks []string
	for offset := 0; offset < len(runes); offset += chunkRunes {
		end := offset + chunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[offset:end]))
	}

	for _, model := range models {
		name := strings.Split(model, "-")[0]
		b.Run("old_builder_"+name+"_large", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for i := 0; i < b.N; i++ {
				var builder strings.Builder
				builder.Grow(len(text))
				builder.WriteString(text)
				_ = tokenkit.Estimate(model, builder.String())
			}
		})
		b.Run("new_streaming_"+name+"_large", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for i := 0; i < b.N; i++ {
				estimator := NewStreamingEstimateByModel(model)
				for _, chunk := range chunks {
					estimator.WriteString(chunk)
				}
				_ = estimator.Tokens()
			}
		})
	}
}
