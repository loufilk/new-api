package cline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// normalizeClineRequestBody runs on the final wire body, after model mapping and
// parameter overrides. Raw messages retain vendor fields and numeric precision;
// only their content is normalized. No URLs are fetched here.
func normalizeClineRequestBody(reader io.Reader) ([]byte, error) {
	limit := int64(constant.MaxRequestBodyMB) << 20
	if limit <= 0 {
		limit = 128 << 20
	}
	if reader == nil {
		return nil, clineContentError("messages", "request body is missing")
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read Cline request body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, types.NewErrorWithStatusCode(common.ErrRequestBodyTooLarge,
			types.ErrorCodeInvalidRequest, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
	}
	return normalizeClineWireContent(body)
}

func clineContentError(path, reason string) *types.NewAPIError {
	// Paths and fixed reasons only: never embed a prompt, URL or base64 payload.
	return types.WithOpenAIError(types.OpenAIError{
		Message: "Cline content conversion: " + path + ": " + reason,
		Type:    "invalid_request_error", Code: "cline_unsupported_content", Param: path,
	}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func normalizeClineWireContent(body []byte) ([]byte, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, clineContentError("messages", "request must be a JSON object")
	}
	raw := gjson.GetBytes(body, "messages")
	if !raw.IsArray() {
		return nil, clineContentError("messages", "expected a message array")
	}
	if clinePlainTextWire(raw) {
		return body, nil
	}
	var messages []map[string]json.RawMessage
	if err := common.Unmarshal([]byte(raw.Raw), &messages); err != nil || len(messages) == 0 {
		return nil, clineContentError("messages", "expected a nonempty array of message objects")
	}
	out := make([]map[string]json.RawMessage, 0, len(messages))
	var pendingMedia []json.RawMessage
	for i, message := range messages {
		path := fmt.Sprintf("messages.%d.content", i)
		var role string
		if err := common.Unmarshal(message["role"], &role); err != nil ||
			!slices.Contains([]string{"user", "assistant", "system", "developer", "tool"}, role) {
			return nil, clineContentError(fmt.Sprintf("messages.%d.role", i), "unsupported message role")
		}
		if role != "tool" && len(pendingMedia) > 0 {
			media, err := common.Marshal(pendingMedia)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]json.RawMessage{"role": json.RawMessage(`"user"`), "content": media})
			pendingMedia = nil
		}
		content := bytes.TrimSpace(message["content"])
		if len(content) == 0 || bytes.Equal(content, []byte("null")) {
			switch role {
			case "user", "system", "developer":
				message["content"] = json.RawMessage(`[{"type":"text","text":""}]`)
			case "tool":
				return nil, clineContentError(path, "tool content must be text or supported content parts")
			}
			out = append(out, message)
			continue
		}
		if content[0] == '"' {
			if clineRequiresContent(role) && gjson.ParseBytes(content).Str == "" {
				message["content"] = json.RawMessage(`[{"type":"text","text":""}]`)
			}
			out = append(out, message)
			continue
		}
		if content[0] != '[' {
			return nil, clineContentError(path, "expected a string or content-part array")
		}
		var parts []map[string]json.RawMessage
		if err := common.Unmarshal(content, &parts); err != nil {
			return nil, clineContentError(path, "each content part must be an object")
		}
		normalized := make([]json.RawMessage, 0, len(parts))
		var texts, labels []string
		for j, part := range parts {
			partPath := fmt.Sprintf("%s.%d", path, j)
			kind, err := normalizeClineContentPart(part, partPath)
			if err != nil {
				return nil, err
			}
			isMedia := kind == "image_url" || kind == "input_audio" || kind == "file"
			if isMedia && role != "user" && role != "tool" {
				return nil, clineContentError(partPath, "media is supported only on user messages or tool results")
			}
			if kind == "refusal" && role != "assistant" {
				return nil, clineContentError(partPath, "refusal parts require the assistant role")
			}
			encoded, err := common.Marshal(part)
			if err != nil {
				return nil, err
			}
			if role == "tool" {
				if kind == "text" {
					var text string
					if err := common.Unmarshal(part["text"], &text); err != nil {
						return nil, err
					}
					texts = append(texts, text)
				} else {
					pendingMedia = append(pendingMedia, encoded)
					label := "[" + strings.TrimSuffix(strings.TrimPrefix(kind, "input_"), "_url") + "]"
					if !slices.Contains(labels, label) {
						labels = append(labels, label)
					}
				}
			} else {
				normalized = append(normalized, encoded)
			}
		}
		if role == "tool" {
			text := strings.Join(texts, "\n")
			if text == "" && len(labels) > 0 {
				text = strings.Join(labels, " ")
			}
			encoded, err := common.Marshal(text)
			if err != nil {
				return nil, err
			}
			message["content"] = encoded
		} else {
			if len(normalized) == 0 {
				normalized = append(normalized, json.RawMessage(`{"type":"text","text":""}`))
			}
			encoded, err := common.Marshal(normalized)
			if err != nil {
				return nil, err
			}
			message["content"] = encoded
		}
		out = append(out, message)
	}
	if len(pendingMedia) > 0 {
		media, err := common.Marshal(pendingMedia)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]json.RawMessage{"role": json.RawMessage(`"user"`), "content": media})
	}
	encoded, err := common.Marshal(out)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "messages", encoded)
}

func normalizeClineContentPart(part map[string]json.RawMessage, path string) (string, error) {
	var kind string
	if part == nil || common.Unmarshal(part["type"], &kind) != nil {
		return "", clineContentError(path+".type", "expected a content type string")
	}
	switch kind {
	case "text", "input_text", "output_text":
		kind = "text"
		part["type"] = json.RawMessage(`"text"`)
		if _, exists := part["text"]; !exists {
			// Older typed converters used omitempty for a required empty string.
			part["text"] = json.RawMessage(`""`)
		}
		value := gjson.ParseBytes(part["text"])
		if value.Type != gjson.String {
			return "", clineContentError(path+".text", "expected a string")
		}
	case "image_url", "input_image":
		kind = "image_url"
		part["type"] = json.RawMessage(`"image_url"`)
		var image map[string]json.RawMessage
		value := gjson.ParseBytes(part["image_url"])
		if value.Type == gjson.String {
			image = map[string]json.RawMessage{"url": part["image_url"]}
		} else if value.IsObject() {
			if err := common.Unmarshal(part["image_url"], &image); err != nil {
				return "", err
			}
		} else {
			return "", clineContentError(path+".image_url", "expected a URL string or an object with url")
		}
		if _, exists := image["url"]; !exists {
			return "", clineContentError(path+".image_url.url", "a URL is required; file_id references cannot be resolved by this channel")
		}
		var address string
		if common.Unmarshal(image["url"], &address) != nil || strings.TrimSpace(address) == "" {
			return "", clineContentError(path+".image_url.url", "expected a nonempty URL string")
		}
		parsed, err := url.Parse(address)
		remote := err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
		data := strings.HasPrefix(address, "data:image/") && strings.Contains(address, ";base64,") && !strings.HasSuffix(address, ";base64,")
		if !remote && !data {
			return "", clineContentError(path+".image_url.url", "expected an HTTP(S) image URL or a nonempty image data URL")
		}
		if detail, exists := part["detail"]; exists {
			if _, nested := image["detail"]; !nested {
				image["detail"] = detail
			}
			delete(part, "detail")
		}
		if detail, exists := image["detail"]; exists {
			var mode string
			if common.Unmarshal(detail, &mode) != nil || !slices.Contains([]string{"auto", "low", "high", "original"}, mode) {
				return "", clineContentError(path+".image_url.detail", "unsupported image detail value")
			}
		}
		delete(image, "MimeType") // old DTO's internal bookkeeping, never a wire field
		encoded, err := common.Marshal(image)
		if err != nil {
			return "", err
		}
		part["image_url"] = encoded
	case "input_audio":
		value := gjson.ParseBytes(part["input_audio"])
		if !value.IsObject() || value.Get("data").Type != gjson.String || value.Get("data").Str == "" ||
			!slices.Contains([]string{"wav", "mp3"}, value.Get("format").Str) {
			return "", clineContentError(path+".input_audio", "expected audio data and a supported format")
		}
	case "file":
		value := gjson.ParseBytes(part["file"])
		if !value.IsObject() || value.Get("file_data").Type != gjson.String || value.Get("file_data").Str == "" {
			return "", clineContentError(path+".file", "inline file_data is required; provider-local file references cannot be resolved")
		}
	case "refusal":
		if gjson.ParseBytes(part["refusal"]).Type != gjson.String {
			return "", clineContentError(path+".refusal", "expected a string")
		}
	case "input_video", "video_url":
		return "", clineContentError(path+".type", "video content is not supported by the Cline Chat bridge; use a video-capable native channel")
	default:
		return "", clineContentError(path+".type", "unsupported content part; no content was discarded")
	}
	return kind, nil
}
