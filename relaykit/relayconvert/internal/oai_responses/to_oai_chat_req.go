package oairesponses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/internal/convdiag"
	relaymedia "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/media"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const (
	responsesInputTypeFunctionCall       = "function_call"
	responsesInputTypeFunctionCallOutput = "function_call_output"
	responsesInputTypeCustomToolCall     = "custom_tool_call"
	responsesInputTypeCustomToolOutput   = "custom_tool_call_output"
)

const (
	ResponsesInputTypeFunctionCall       = responsesInputTypeFunctionCall
	ResponsesInputTypeFunctionCallOutput = responsesInputTypeFunctionCallOutput
	ResponsesInputTypeCustomToolCall     = responsesInputTypeCustomToolCall
	ResponsesInputTypeCustomToolOutput   = responsesInputTypeCustomToolOutput
)

func ResponsesRequestToChatCompletionsRequest(ctx context.Context, req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	return ResponsesRequestToChatCompletionsRequestWithOptions(ctx, req, nil)
}

func ResponsesRequestToChatCompletionsRequestWithOptions(ctx context.Context, req *dto.OpenAIResponsesRequest, options *convmeta.Options) (*dto.GeneralOpenAIRequest, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}
	if err := validateResponsesRequestChatUnsupportedFields(req); err != nil {
		return nil, err
	}

	replayReasoning := options != nil && options.ReplayResponsesReasoning
	messages, err := responsesRequestMessagesToChat(ctx, req, replayReasoning)
	if err != nil {
		return nil, err
	}

	tools, err := responsesRequestToolsToChat(req.Tools)
	if err != nil {
		return nil, err
	}

	toolChoice, err := responsesRequestToolChoiceToChat(req.ToolChoice)
	if err != nil {
		return nil, err
	}

	responseFormat, err := responsesRequestTextToChatResponseFormat(req.Text)
	if err != nil {
		return nil, err
	}

	out := &dto.GeneralOpenAIRequest{
		Model:                req.Model,
		Messages:             messages,
		Stream:               req.Stream,
		StreamOptions:        req.StreamOptions,
		MaxCompletionTokens:  req.MaxOutputTokens,
		Temperature:          req.Temperature,
		TopP:                 req.TopP,
		TopLogProbs:          req.TopLogProbs,
		ResponseFormat:       responseFormat,
		Tools:                tools,
		ToolChoice:           toolChoice,
		User:                 req.User,
		Store:                req.Store,
		Metadata:             req.Metadata,
		SafetyIdentifier:     req.SafetyIdentifier,
		PromptCacheRetention: req.PromptCacheRetention,
		EnableThinking:       req.EnableThinking,
		ThinkingBudget:       req.ThinkingBudget,
	}

	out.FrequencyPenalty, err = responsesRawFloat(req.FrequencyPenalty)
	if err != nil {
		return nil, fmt.Errorf("invalid frequency_penalty: %w", err)
	}
	out.PresencePenalty, err = responsesRawFloat(req.PresencePenalty)
	if err != nil {
		return nil, fmt.Errorf("invalid presence_penalty: %w", err)
	}

	reasoningIntent, diagnostics, err := reasoning.FromOpenAIResponses(req)
	if err != nil {
		return nil, reasoning.AsClientError(err)
	}
	convdiag.Add(ctx, diagnostics...)
	if err := reasoning.ApplyToOpenAIChat(out, reasoningIntent); err != nil {
		return nil, reasoning.AsClientError(err)
	}
	if req.ServiceTier != "" {
		out.ServiceTier, _ = kitutil.Marshal(req.ServiceTier)
	}
	if len(req.ParallelToolCalls) > 0 && kitutil.GetJsonType(req.ParallelToolCalls) == "boolean" {
		var parallelToolCalls bool
		if err := kitutil.Unmarshal(req.ParallelToolCalls, &parallelToolCalls); err == nil {
			out.ParallelTooCalls = &parallelToolCalls
		}
	}
	if len(req.PromptCacheKey) > 0 && kitutil.GetJsonType(req.PromptCacheKey) == "string" {
		var promptCacheKey string
		if err := kitutil.Unmarshal(req.PromptCacheKey, &promptCacheKey); err == nil {
			out.PromptCacheKey = promptCacheKey
		}
	}

	return out, nil
}

func validateResponsesRequestChatUnsupportedFields(req *dto.OpenAIResponsesRequest) error {
	unsupported := make([]string, 0, 4)
	if rawJSONPresent(req.Conversation) {
		unsupported = append(unsupported, "conversation")
	}
	if strings.TrimSpace(req.PreviousResponseID) != "" {
		unsupported = append(unsupported, "previous_response_id")
	}
	if rawJSONPresent(req.Prompt) {
		unsupported = append(unsupported, "prompt")
	}
	if rawJSONPresent(req.ContextManagement) {
		unsupported = append(unsupported, "context_management")
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("responses to chat conversion does not support stateful fields: %s", strings.Join(unsupported, ", "))
	}
	return nil
}

func ValidateRequestChatUnsupportedFields(req *dto.OpenAIResponsesRequest) error {
	return validateResponsesRequestChatUnsupportedFields(req)
}

func responsesRequestMessagesToChat(ctx context.Context, req *dto.OpenAIResponsesRequest, replayReasoning bool) ([]dto.Message, error) {
	messages := make([]dto.Message, 0)
	if rawJSONPresent(req.Instructions) {
		instructions, err := responsesJSONString(req.Instructions)
		if err != nil {
			return nil, fmt.Errorf("invalid instructions: %w", err)
		}
		if strings.TrimSpace(instructions) != "" {
			messages = append(messages, dto.Message{Role: "system", Content: instructions})
		}
	}

	if !rawJSONPresent(req.Input) {
		return messages, nil
	}

	switch kitutil.GetJsonType(req.Input) {
	case "string":
		input, err := responsesJSONString(req.Input)
		if err != nil {
			return nil, fmt.Errorf("invalid input string: %w", err)
		}
		messages = append(messages, dto.Message{Role: "user", Content: input})
		return messages, nil
	case "array":
		var items []map[string]any
		if err := kitutil.Unmarshal(req.Input, &items); err != nil {
			return nil, fmt.Errorf("invalid input array: %w", err)
		}
		// Chat Completions requires the tool messages answering one assistant tool_calls
		// batch to stay contiguous, so media hoisted out of tool output items is
		// held back and emitted as a single user message once the batch ends.
		var pendingMedia []any
		pendingCalls := make(map[string]struct{})
		for i, item := range items {
			itemType := strings.TrimSpace(kitutil.Interface2String(item["type"]))
			if replayReasoning {
				path := fmt.Sprintf("input.%d", i)
				switch itemType {
				case "reasoning":
					text, err := responsesReasoningHistoryText(ctx, item, path)
					if err != nil {
						return nil, err
					}
					if text == "" {
						continue
					}
					if len(pendingCalls) > 0 && len(messages) > 0 && messages[len(messages)-1].Role == "tool" {
						return nil, responsesHistoryError(path, "reasoning between parallel tool results cannot be represented by Chat Completions")
					}
					if len(pendingMedia) > 0 {
						messages = append(messages, dto.Message{Role: "user", Content: pendingMedia})
						pendingMedia = nil
					}
					if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
						messages = append(messages, dto.Message{Role: "assistant"})
					}
					last := &messages[len(messages)-1]
					var reasoning strings.Builder
					appendSeparatedText(&reasoning, last.GetReasoningContent())
					appendSeparatedText(&reasoning, text)
					last.ReasoningContent = kitutil.GetPointer(reasoning.String())
					continue
				case responsesInputTypeFunctionCall, responsesInputTypeCustomToolCall:
					pendingCalls[responsesCallID(item)] = struct{}{}
				case responsesInputTypeFunctionCallOutput, responsesInputTypeCustomToolOutput:
					delete(pendingCalls, strings.TrimSpace(kitutil.Interface2String(item["call_id"])))
				case "", "message":
				default:
					return nil, responsesHistoryError(path+".type", "input item cannot be represented by Chat Completions")
				}
			}
			if len(pendingMedia) > 0 && itemType != responsesInputTypeFunctionCallOutput && itemType != responsesInputTypeCustomToolOutput {
				messages = append(messages, dto.Message{Role: "user", Content: pendingMedia})
				pendingMedia = nil
			}
			nextMessages, media, err := responsesInputItemToChatMessages(ctx, item, messages, replayReasoning)
			if err != nil {
				return nil, err
			}
			messages = nextMessages
			pendingMedia = append(pendingMedia, media...)
		}
		if len(pendingMedia) > 0 {
			messages = append(messages, dto.Message{Role: "user", Content: pendingMedia})
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported responses input type %q", kitutil.GetJsonType(req.Input))
	}
}

// responsesInputItemToChatMessages appends the Chat messages for one Responses input item.
// The second result carries media content parts hoisted out of a tool output item,
// already in Chat shape; the caller decides where that user message lands.
func responsesInputItemToChatMessages(ctx context.Context, item map[string]any, messages []dto.Message, replayReasoning bool) ([]dto.Message, []any, error) {
	itemType := strings.TrimSpace(kitutil.Interface2String(item["type"]))
	switch itemType {
	case "reasoning":
		// Reasoning history is assistant metadata, never an empty user turn.
		return messages, nil, nil
	case responsesInputTypeFunctionCall:
		toolCall, err := responsesFunctionCallItemToChatToolCall(item)
		if err != nil {
			return nil, nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil, nil
	case responsesInputTypeCustomToolCall:
		toolCall, err := responsesCustomToolCallItemToChatToolCall(item)
		if err != nil {
			return nil, nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil, nil
	case responsesInputTypeFunctionCallOutput, responsesInputTypeCustomToolOutput:
		callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
		content, media, err := responsesToolOutputToChat(ctx, item["output"])
		if err != nil {
			return nil, nil, err
		}
		return append(messages, dto.Message{Role: "tool", ToolCallId: callID, Content: content}), media, nil
	}

	role := strings.TrimSpace(kitutil.Interface2String(item["role"]))
	if role == "" {
		role = "user"
	}
	content, err := responsesInputContentToChatContent(ctx, item["content"])
	if err != nil {
		return nil, nil, err
	}
	if replayReasoning && role == "assistant" {
		switch content.(type) {
		case string, []any:
		default:
			return nil, nil, responsesHistoryError("input.content", "expected assistant text or content parts")
		}
	}
	if replayReasoning && role == "assistant" && len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
		// Responses splits one assistant turn into reasoning, commentary and
		// call items. Keep them on the same Chat message, including parallel
		// calls separated by commentary; never merge across a tool/user turn.
		last := &messages[len(messages)-1]
		if last.Content == nil || last.Content == "" {
			last.Content = content
		} else if oldText, ok := last.Content.(string); ok {
			if newText, ok := content.(string); ok {
				var text strings.Builder
				appendSeparatedText(&text, oldText)
				appendSeparatedText(&text, newText)
				last.Content = text.String()
			} else {
				last.Content = append([]any{map[string]any{"type": "text", "text": oldText}}, content.([]any)...)
			}
		} else {
			parts := last.Content.([]any)
			if text, ok := content.(string); ok {
				last.Content = append(parts, map[string]any{"type": "text", "text": text})
			} else {
				last.Content = append(parts, content.([]any)...)
			}
		}
		return messages, nil, nil
	}
	return append(messages, dto.Message{Role: role, Content: content}), nil, nil
}

// responsesReasoningHistoryText reads only explicit plaintext fields. A
// reasoning summary is not a replacement for provider-specific encrypted state.
func responsesReasoningHistoryText(ctx context.Context, item map[string]any, path string) (string, error) {
	if encrypted := item["encrypted_content"]; encrypted != nil && encrypted != "" {
		return "", responsesHistoryError(path+".encrypted_content", "encrypted reasoning cannot be replayed through Chat Completions; use a native Responses channel")
	}
	for _, field := range []string{"content", "summary"} {
		raw := item[field]
		if raw == nil {
			continue
		}
		parts, ok := raw.([]any)
		if !ok {
			return "", responsesHistoryError(path+"."+field, "expected reasoning text parts")
		}
		partType := "reasoning_text"
		if field == "summary" {
			partType = "summary_text"
		}
		var text strings.Builder
		for j, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok || part["type"] != partType {
				return "", responsesHistoryError(fmt.Sprintf("%s.%s.%d", path, field, j), "unsupported reasoning text part")
			}
			value, ok := part["text"].(string)
			if !ok {
				return "", responsesHistoryError(fmt.Sprintf("%s.%s.%d.text", path, field, j), "expected text")
			}
			appendSeparatedText(&text, value)
		}
		if text.Len() > 0 {
			if field == "summary" {
				convdiag.Add(ctx, types.ConversionDiagnostic{
					Code: "reasoning_summary_replayed", Path: path + ".summary",
					Message:  "Reasoning summary replayed as reasoning_content; native summaries may omit reasoning state",
					Severity: types.ConversionDiagnosticWarning,
				})
			}
			return text.String(), nil
		}
	}
	return "", nil
}

func responsesHistoryError(path, reason string) *types.NewAPIError {
	return types.WithOpenAIError(types.OpenAIError{
		Message: "Responses history conversion: " + reason,
		Type:    "invalid_request_error", Code: "responses_history_unsupported", Param: path,
	}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func responsesInputContentToChatContent(ctx context.Context, content any) (any, error) {
	if content == nil {
		return "", nil
	}

	switch value := content.(type) {
	case string:
		return value, nil
	case []any:
		return responsesContentPartsToChatContent(ctx, value)
	case []map[string]any:
		parts := make([]any, 0, len(value))
		for _, part := range value {
			parts = append(parts, part)
		}
		return responsesContentPartsToChatContent(ctx, parts)
	default:
		return content, nil
	}
}

func responsesContentPartsToChatContent(ctx context.Context, parts []any) (any, error) {
	chatParts := make([]any, 0, len(parts))
	var textOnly strings.Builder
	onlyText := true
	droppedFile := false

	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			onlyText = false
			chatParts = append(chatParts, rawPart)
			continue
		}

		partType := strings.TrimSpace(kitutil.Interface2String(part["type"]))
		switch partType {
		case "input_text", "output_text", "text":
			text := kitutil.Interface2String(part["text"])
			textOnly.WriteString(text)
			chatParts = append(chatParts, map[string]any{
				"type": dto.ContentTypeText,
				"text": text,
			})
		case "input_image":
			imageURL, err := responsesImagePartToChatImageURL(part)
			if err != nil {
				return nil, err
			}
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeImageURL,
				"image_url": imageURL,
			})
		case "input_file":
			onlyText = false
			chatPart, reason := responsesFilePartToChat(ctx, part)
			if reason != "" {
				convdiag.Add(ctx, types.ConversionDiagnostic{
					Code:     "unsupported_media_type",
					Path:     "input.content",
					Message:  reason + "; the file was omitted",
					Severity: types.ConversionDiagnosticError,
				})
				droppedFile = true
				continue
			}
			chatParts = append(chatParts, chatPart)
		case "input_audio":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":        dto.ContentTypeInputAudio,
				"input_audio": responsesPartPayload(part, "input_audio"),
			})
		case "input_video":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeVideoUrl,
				"video_url": responsesVideoPartToChatVideoURL(part),
			})
		default:
			onlyText = false
			chatParts = append(chatParts, part)
		}
	}

	if onlyText {
		return textOnly.String(), nil
	}
	if len(chatParts) == 0 && droppedFile {
		// Chat rejects an empty content array; name what was omitted instead.
		return "[file]", nil
	}
	return chatParts, nil
}

func responsesFunctionCallItemToChatToolCall(item map[string]any) (dto.ToolCallRequest, error) {
	name := responsesCallName(item)
	if name == "" {
		return dto.ToolCallRequest{}, errors.New("function_call item is missing name")
	}
	return dto.ToolCallRequest{
		ID:   responsesCallID(item),
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      name,
			Arguments: responsesArgumentsString(item["arguments"]),
		},
	}, nil
}

// responsesCustomToolCallItemToChatToolCall encodes a custom tool call the same
// way its definition reaches Chat Completions: a function call whose arguments
// carry the raw input under the single "input" key.
func responsesCustomToolCallItemToChatToolCall(item map[string]any) (dto.ToolCallRequest, error) {
	name := responsesCallName(item)
	if name == "" {
		return dto.ToolCallRequest{}, errors.New("custom_tool_call item is missing name")
	}
	arguments, err := kitutil.Marshal(map[string]string{
		convmeta.CustomToolInputArgument: responsesArgumentsString(item["input"]),
	})
	if err != nil {
		return dto.ToolCallRequest{}, err
	}
	return dto.ToolCallRequest{
		ID:   responsesCallID(item),
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      name,
			Arguments: string(arguments),
		},
	}, nil
}

func appendToolCallToLastAssistant(messages []dto.Message, toolCall dto.ToolCallRequest) []dto.Message {
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		messages = append(messages, dto.Message{Role: "assistant"})
	}

	idx := len(messages) - 1
	toolCalls := messages[idx].ParseToolCalls()
	toolCalls = append(toolCalls, toolCall)
	toolCallsRaw, _ := kitutil.Marshal(toolCalls)
	messages[idx].ToolCalls = toolCallsRaw
	return messages
}

func responsesRequestToolsToChat(raw json.RawMessage) ([]dto.ToolCallRequest, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}

	var tools []json.RawMessage
	if err := kitutil.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("invalid tools: %w", err)
	}

	out := make([]dto.ToolCallRequest, 0, len(tools))
	for _, rawTool := range tools {
		var tool map[string]any
		if err := kitutil.Unmarshal(rawTool, &tool); err != nil {
			return nil, fmt.Errorf("invalid tools: %w", err)
		}
		toolType := strings.TrimSpace(kitutil.Interface2String(tool["type"]))
		if toolType == "function" {
			out = append(out, dto.ToolCallRequest{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        strings.TrimSpace(kitutil.Interface2String(tool["name"])),
					Description: kitutil.Interface2String(tool["description"]),
					Parameters:  kitutil.ObjectMember(rawTool, "parameters"),
				},
			})
			continue
		}

		out = append(out, dto.ToolCallRequest{
			Type:   toolType,
			Custom: rawTool,
		})
	}
	return out, nil
}

func responsesRequestToolChoiceToChat(raw json.RawMessage) (any, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}
	if kitutil.GetJsonType(raw) == "string" {
		var choice string
		if err := kitutil.Unmarshal(raw, &choice); err != nil {
			return nil, fmt.Errorf("invalid tool_choice: %w", err)
		}
		return choice, nil
	}

	var choice map[string]any
	if err := kitutil.Unmarshal(raw, &choice); err != nil {
		return nil, fmt.Errorf("invalid tool_choice: %w", err)
	}
	if kitutil.Interface2String(choice["type"]) == "function" {
		name := strings.TrimSpace(kitutil.Interface2String(choice["name"]))
		if name != "" {
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}, nil
		}
	}
	return choice, nil
}

func RequestToolChoiceToChat(raw json.RawMessage) (any, error) {
	return responsesRequestToolChoiceToChat(raw)
}

func responsesRequestTextToChatResponseFormat(raw json.RawMessage) (*dto.ResponseFormat, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}

	var textConfig map[string]any
	if err := kitutil.Unmarshal(raw, &textConfig); err != nil {
		return nil, fmt.Errorf("invalid text config: %w", err)
	}
	format, ok := textConfig["format"].(map[string]any)
	if !ok {
		return nil, nil
	}

	formatType := strings.TrimSpace(kitutil.Interface2String(format["type"]))
	if formatType == "" {
		return nil, nil
	}

	out := &dto.ResponseFormat{Type: formatType}
	if formatType == "json_schema" {
		// The client's bytes keep the schema's property order.
		out.JsonSchema = kitutil.ObjectMember(raw, "format")
	}
	return out, nil
}

func RequestTextToChatResponseFormat(raw json.RawMessage) (*dto.ResponseFormat, error) {
	return responsesRequestTextToChatResponseFormat(raw)
}

// responsesImagePartToChatImageURL converts a Responses input_image part into
// the Chat Completions wire shape. The Responses API carries image_url as a
// plain string, while Chat Completions requires an object with a url field;
// strict upstreams reject the bare string with "Invalid input" against
// messages.N.content. A string URL is therefore wrapped, and an already
// object-shaped image_url passes through unchanged.
func responsesImagePartToChatImageURL(part map[string]any) (map[string]any, error) {
	imageURL := map[string]any{}
	switch value := part["image_url"].(type) {
	case string:
		imageURL["url"] = value
	case map[string]any:
		for key, value := range value {
			imageURL[key] = value
		}
	case nil:
		if value, exists := part["url"]; exists {
			imageURL["url"] = value
		}
	}
	address, ok := imageURL["url"].(string)
	if !ok || strings.TrimSpace(address) == "" {
		return nil, types.WithOpenAIError(types.OpenAIError{
			Message: "Responses image requires a nonempty image_url; image file_id references cannot be converted to Chat Completions",
			Type:    "invalid_request_error", Code: "unsupported_image_reference", Param: "input_image.image_url",
		}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if detail, exists := part["detail"]; exists {
		if _, nested := imageURL["detail"]; !nested {
			imageURL["detail"] = detail
		}
	}
	// Responses-only values such as "original" are rejected by strict Chat
	// upstreams. Preserve the supported Chat detail values and omit the rest.
	switch imageURL["detail"] {
	case "auto", "low", "high":
	default:
		delete(imageURL, "detail")
	}
	return imageURL, nil
}

// responsesFilePartToChat maps a Responses input_file onto Chat Completions,
// whose file part takes only PDF data or an uploaded file_id and no file URL.
// Files given by data or URL are loaded through the media resolver: PDF
// becomes a Chat file part with inline data and text files become a text
// part. A non-empty reason means Chat cannot carry the file.
func responsesFilePartToChat(ctx context.Context, part map[string]any) (map[string]any, string) {
	fields := part
	if nested, ok := part["file"].(map[string]any); ok {
		fields = nested
	}
	filename := strings.TrimSpace(kitutil.Interface2String(fields["filename"]))
	source := ContentPartToFileSource(part)
	if source == nil {
		fileID := strings.TrimSpace(kitutil.Interface2String(fields["file_id"]))
		if fileID == "" {
			return nil, "input_file carries no file_data, file_url, or file_id"
		}
		file := map[string]any{"file_id": fileID}
		if filename != "" {
			file["filename"] = filename
		}
		return map[string]any{"type": dto.ContentTypeFile, "file": file}, ""
	}
	base64Data, mimeType, err := relaymedia.ResolveBase64Data(ctx, source, "formatting Responses input file for Chat")
	if err != nil {
		return nil, fmt.Sprintf("input_file could not be loaded: %s", err.Error())
	}
	mediaType, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(mimeType)), ";")
	mediaType = strings.TrimSpace(mediaType)
	switch {
	case mediaType == "application/pdf":
		if filename == "" {
			filename = "document.pdf"
		}
		return map[string]any{"type": dto.ContentTypeFile, "file": map[string]any{
			"filename":  filename,
			"file_data": "data:application/pdf;base64," + base64Data,
		}}, ""
	case relaymedia.IsTextMimeType(mediaType):
		text, ok := relaymedia.DecodeText(base64Data)
		if !ok {
			return nil, fmt.Sprintf("input_file of MIME type %q could not be decoded as UTF-8 text", mimeType)
		}
		return map[string]any{"type": dto.ContentTypeText, "text": text}, ""
	}
	return nil, fmt.Sprintf("OpenAI Chat Completions file parts accept only PDF, not %q", mimeType)
}

func responsesVideoPartToChatVideoURL(part map[string]any) any {
	if videoURL, ok := part["video_url"]; ok {
		if videoURLMap, ok := videoURL.(map[string]any); ok {
			if url := kitutil.Interface2String(videoURLMap["url"]); url != "" {
				return url
			}
		}
		return videoURL
	}
	if url := kitutil.Interface2String(part["url"]); url != "" {
		return url
	}
	return responsesPartPayload(part, "video_url")
}

func responsesPartPayload(part map[string]any, key string) any {
	if value, ok := part[key]; ok {
		return value
	}
	payload := make(map[string]any, len(part))
	for k, value := range part {
		if k == "type" {
			continue
		}
		payload[k] = value
	}
	return payload
}

func responsesCallID(item map[string]any) string {
	callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
	if callID != "" {
		return callID
	}
	return strings.TrimSpace(kitutil.Interface2String(item["id"]))
}

func CallID(item map[string]any) string {
	return responsesCallID(item)
}

// responsesCallName is the upstream function name of a function_call or
// custom_tool_call item: a call made in a tool namespace uses the name its
// flattened definition was sent with.
func responsesCallName(item map[string]any) string {
	name := strings.TrimSpace(kitutil.Interface2String(item["name"]))
	if name == "" {
		return ""
	}
	return convmeta.NamespacedToolName(strings.TrimSpace(kitutil.Interface2String(item["namespace"])), name)
}

func responsesArgumentsString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := kitutil.Marshal(v)
		if err != nil {
			return kitutil.Interface2String(v)
		}
		return string(raw)
	}
}

func responseToolOutputToChatContent(value any) any {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := kitutil.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(raw)
	}
}

// responsesToolOutputToChat maps a function_call_output payload onto Chat Completions, where a
// tool message may only carry text. A Responses content-part array keeps its text on the tool
// message and returns the media parts in Chat shape for the caller to hoist into a user message;
// stringifying them instead would hand base64 image data to the upstream text tokenizer. Any
// other payload shape (string, object, plain JSON array) keeps the historical stringified form.
func responsesToolOutputToChat(ctx context.Context, value any) (any, []any, error) {
	rawParts, ok := value.([]any)
	if !ok || len(rawParts) == 0 {
		return responseToolOutputToChatContent(value), nil, nil
	}

	texts := make([]string, 0, len(rawParts))
	mediaParts := make([]any, 0, len(rawParts))
	labels := make([]string, 0, len(rawParts))
	for _, rawPart := range rawParts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			return responseToolOutputToChatContent(value), nil, nil
		}
		partType := strings.TrimSpace(kitutil.Interface2String(part["type"]))
		switch partType {
		case "input_text", "output_text", "text":
			if text := kitutil.Interface2String(part["text"]); text != "" {
				texts = append(texts, text)
			}
		case "input_image", "input_file", "input_audio", "input_video":
			mediaParts = append(mediaParts, part)
			label := "[" + strings.TrimPrefix(partType, "input_") + "]"
			if !slices.Contains(labels, label) {
				labels = append(labels, label)
			}
		default:
			return responseToolOutputToChatContent(value), nil, nil
		}
	}
	if len(mediaParts) == 0 {
		return strings.Join(texts, "\n"), nil, nil
	}

	converted, err := responsesContentPartsToChatContent(ctx, mediaParts)
	if err != nil {
		return nil, nil, err
	}
	media, _ := converted.([]any)
	if len(texts) == 0 {
		// Upstreams reject empty tool content; the media itself rides on the hoisted user message.
		return strings.Join(labels, " "), media, nil
	}
	return strings.Join(texts, "\n"), media, nil
}

func responsesRawFloat(raw json.RawMessage) (*float64, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}
	var value float64
	if err := kitutil.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func responsesJSONString(raw json.RawMessage) (string, error) {
	if kitutil.GetJsonType(raw) != "string" {
		return string(raw), nil
	}
	var value string
	if err := kitutil.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func rawJSONPresent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	return kitutil.GetJsonType(raw) != "null"
}

func JSONString(raw json.RawMessage) (string, error) {
	return responsesJSONString(raw)
}

func RawJSONPresent(raw json.RawMessage) bool {
	return rawJSONPresent(raw)
}
