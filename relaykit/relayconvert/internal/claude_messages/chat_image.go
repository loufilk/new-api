package claudemessages

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// claudeImageToChatURL preserves remote URL sources rather than constructing an
// empty data URL. The shared request boundary owns URL fetching/SSRF controls.
func claudeImageToChatURL(source *dto.ClaudeMessageSource) (string, error) {
	invalid := func(reason string) (string, error) {
		return "", types.WithOpenAIError(types.OpenAIError{
			Message: reason, Type: "invalid_request_error", Code: "invalid_image_source", Param: "source",
		}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if source == nil {
		return invalid("image source is required")
	}
	if source.Type == "url" || source.Type == "" && source.Url != "" && source.Data == nil {
		if strings.TrimSpace(source.Url) == "" {
			return invalid("image URL source requires a nonempty url")
		}
		return source.Url, nil
	}
	if source.Type != "base64" && source.Type != "" {
		return invalid("unsupported image source type")
	}
	data, ok := source.Data.(string)
	if !ok || strings.TrimSpace(data) == "" || !strings.HasPrefix(source.MediaType, "image/") {
		return invalid("base64 image source requires image media_type and nonempty string data")
	}
	return "data:" + source.MediaType + ";base64," + data, nil
}
