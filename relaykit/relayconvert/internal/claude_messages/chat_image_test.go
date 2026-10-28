package claudemessages

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeChatImageSourcesOnUserAndToolMessages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source *dto.ClaudeMessageSource
		want   string
	}{
		{"url", &dto.ClaudeMessageSource{Type: "url", Url: "https://example.test/image.png?signature=kept"}, "https://example.test/image.png?signature=kept"},
		{"base64", &dto.ClaudeMessageSource{Type: "base64", MediaType: "image/png", Data: "aGk="}, "data:image/png;base64,aGk="},
		{"legacy url", &dto.ClaudeMessageSource{Url: "https://example.test/a.png"}, "https://example.test/a.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			empty := ""
			image := dto.ClaudeMediaMessage{Type: "image", Source: tc.source}
			request := dto.ClaudeRequest{Model: "deepseek/deepseek-v4.1-flash", Messages: []dto.ClaudeMessage{
				{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: &empty}, image}},
			}}
			converted, err := ClaudeMessagesRequestToOpenAIChat(context.Background(), request, &convmeta.Values{})
			require.NoError(t, err)
			wire, err := kitutil.Marshal(converted)
			require.NoError(t, err)
			assert.Equal(t, `""`, gjson.GetBytes(wire, "messages.0.content.0.text").Raw)
			assert.Equal(t, tc.want, gjson.GetBytes(wire, "messages.0.content.1.image_url.url").Str)
			assert.NotContains(t, string(wire), "MimeType")
			text, media, err := claudeToolResultToChat([]dto.ClaudeMediaMessage{image})
			require.NoError(t, err)
			assert.Equal(t, "[image]", text)
			require.Len(t, media, 1)
			assert.Equal(t, tc.want, media[0].GetImageMedia().Url)
		})
	}
}

func TestClaudeChatRejectsMalformedImageSourcesWithoutPanicOrStringification(t *testing.T) {
	for _, source := range []*dto.ClaudeMessageSource{
		nil, {Type: "url"}, {Type: "file", Url: "file://private"},
		{Type: "base64", MediaType: "image/png", Data: ""},
		{Type: "base64", MediaType: "image/png", Data: map[string]any{"secret": "redacted"}},
		{Type: "base64", MediaType: "text/plain", Data: "secret"},
	} {
		_, err := claudeImageToChatURL(source)
		var apiErr *types.NewAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 400, apiErr.StatusCode)
		assert.True(t, types.IsSkipRetryError(apiErr))
		assert.NotContains(t, err.Error(), "secret")
		_, _, err = claudeToolResultToChat([]dto.ClaudeMediaMessage{{Type: "image", Source: source}})
		require.ErrorAs(t, err, &apiErr)
	}
}
