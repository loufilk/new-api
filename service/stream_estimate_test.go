package service

import (
	"github.com/QuantumNous/new-api/tokenkit"
	"net/http/httptest"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func feedInChunks(estimator *StreamingEstimateByModel, text string, sizes []int) {
	if len(sizes) == 0 {
		estimator.WriteString(text)
		return
	}
	offset := 0
	index := 0
	for offset < len(text) {
		size := sizes[index%len(sizes)]
		index++
		end := offset + size
		if end > len(text) {
			end = len(text)
		}
		estimator.WriteString(text[offset:end])
		offset = end
	}
}

func TestStreamingEstimateMatchesBatchAcrossModels(t *testing.T) {
	texts := map[string]string{
		"latin boundary":     "hello world version 3.5 abc123xyz",
		"cjk":                "你好世界こんにちは안녕하세요",
		"emoji":              "hello \U0001F600\U0001F300\u2600\u2702 world",
		"math":               "∑ ∫ ∂ √ ∞ ≤ ≥ x² y³ α + β",
		"url delims":         "https://api.example.com/v1/chat?model=gpt&n=1#frag%20x",
		"at and symbols":     "user@example.com, price $12.50 (a+b); done!",
		"reasoning":          "Planning file inspection\n\nClarifying environment task requirements",
		"output text":        "I’ll inspect the starter repository. What would you like me to build?",
		"tool name and args": "lookup{\"q\":\"x\",\"city\":\"Paris\",\"n\":3}",
		"mixed":              "Text 生成 hello 123 \U0001F600 ∑ https://x.com/a?b=1@c",
		"newlines":           "line1\nline2\tindented  spaced",
	}
	models := []string{"gpt-4o", "claude-3-5-sonnet", "gemini-1.5-pro", "unknown-model-xyz"}

	for _, model := range models {
		for name, text := range texts {
			want := tokenkit.Estimate(model, text)
			whole := NewStreamingEstimateByModel(model)
			whole.WriteString(text)
			require.Equal(t, want, whole.Tokens(), "model %s case %s whole", model, name)

			split := NewStreamingEstimateByModel(model)
			feedInChunks(split, text, []int{1, 2, 3, 5, 8})
			require.Equal(t, want, split.Tokens(), "model %s case %s split", model, name)

			bytewise := NewStreamingEstimateByModel(model)
			for i := 0; i < len(text); i++ {
				bytewise.Write([]byte{text[i]})
			}
			require.Equal(t, want, bytewise.Tokens(), "model %s case %s bytewise", model, name)
		}
	}
}

func TestStreamingEstimateLatinNumberBoundaryAcrossChunks(t *testing.T) {
	model := "gpt-4o"
	full := "abc123xyz 456def"
	want := tokenkit.Estimate(model, full)

	splits := [][]string{
		{"a", "bc", "123", "xyz", " ", "456", "def"},
		{"abc", "123xyz 456", "def"},
		{"abc123", "xyz 456def"},
		{full},
	}
	for _, parts := range splits {
		estimator := NewStreamingEstimateByModel(model)
		for _, part := range parts {
			estimator.WriteString(part)
		}
		require.Equal(t, want, estimator.Tokens())
	}
}

func TestStreamingEstimateByteSplitUTF8(t *testing.T) {
	models := []string{"gpt-4o", "claude-3-5-sonnet", "gemini-1.5-pro"}
	text := "a\u4e2d\U0001F600\u2211b"
	require.True(t, utf8.ValidString(text))

	for _, model := range models {
		want := tokenkit.Estimate(model, text)
		raw := []byte(text)
		for split := 1; split < len(raw); split++ {
			estimator := NewStreamingEstimateByModel(model)
			estimator.WriteString(string(raw[:split]))
			estimator.WriteString(string(raw[split:]))
			require.Equal(t, want, estimator.Tokens(), "model %s split %d", model, split)
		}

		singleBytes := NewStreamingEstimateByModel(model)
		for _, b := range raw {
			singleBytes.Write([]byte{b})
		}
		require.Equal(t, want, singleBytes.Tokens(), "model %s single bytes", model)
	}
}

func TestStreamingEstimateIncompleteTrailingBytesBuffered(t *testing.T) {
	model := "gpt-4o"
	emoji := "\U0001F600"
	raw := []byte("ab" + emoji + "cd")
	require.True(t, utf8.ValidString(string(raw)))

	estimator := NewStreamingEstimateByModel(model)
	estimator.WriteString(string(raw[:3]))
	require.NotNil(t, estimator)
	estimator.WriteString(string(raw[3:]))
	require.Equal(t, tokenkit.Estimate(model, string(raw)), estimator.Tokens())

	stringsEstimator := NewStreamingEstimateByModel(model)
	for _, part := range []string{"ab", emoji[:2], emoji[2:], "cd"} {
		stringsEstimator.WriteString(part)
	}
	require.Equal(t, tokenkit.Estimate(model, string(raw)), stringsEstimator.Tokens())
}

func TestStreamingEstimateFinalizesInvalidTrailingBytesLikeBatch(t *testing.T) {
	model := "gpt-4o"
	text := "valid 中文 " + string([]byte{0xe2, 0x82})
	estimator := NewStreamingEstimateByModel(model)
	estimator.WriteString(text)

	require.Equal(t, tokenkit.Estimate(model, text), estimator.Tokens())
	require.Equal(t, tokenkit.Estimate(model, text), estimator.Tokens())
	require.Zero(t, NewStreamingEstimateByModel(model).Tokens())
}

func TestStreamingEstimate2UsageMatchesResponseText2Usage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	model := "gpt-4o"
	text := "hello world 你好 \U0001F600 ∑ https://example.com/?a=1"
	promptTokens := 7

	estimator := NewStreamingEstimateByModel(model)
	feedInChunks(estimator, text, []int{2, 5})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	got := StreamingEstimate2Usage(c, estimator, model, promptTokens)
	require.NotNil(t, got)

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	want := ResponseText2Usage(c2, text, model, promptTokens)
	require.NotNil(t, want)

	assert.Equal(t, want.PromptTokens, got.PromptTokens)
	assert.Equal(t, want.CompletionTokens, got.CompletionTokens)
	assert.Equal(t, want.TotalTokens, got.TotalTokens)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
	assert.True(t, ValidUsage(got))
}
