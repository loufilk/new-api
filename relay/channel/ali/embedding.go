package ali

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// Bailian multimodal embedding models (qwen3-vl-embedding, qwen2.5-vl-embedding,
// tongyi-embedding-vision-*, multimodal-embedding-v1) are not served in
// compatible mode; they only work on the native multimodal-embedding endpoint.
// Matching the name families also covers dated snapshots.
func isMultimodalEmbeddingModel(model string) bool {
	return strings.Contains(model, "vl-embedding") ||
		strings.Contains(model, "embedding-vision") ||
		strings.HasPrefix(model, "multimodal-embedding")
}

type AliMultimodalContent struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
	Video string `json:"video,omitempty"`
}

type AliMultimodalEmbeddingRequest struct {
	Model string `json:"model"`
	Input struct {
		Contents []AliMultimodalContent `json:"contents"`
	} `json:"input"`
	Parameters *AliMultimodalEmbeddingParameters `json:"parameters,omitempty"`
}

type AliMultimodalEmbeddingParameters struct {
	Dimension *int `json:"dimension,omitempty"`
}

type AliMultimodalEmbeddingItem struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type AliMultimodalEmbeddingResponse struct {
	Output struct {
		Embeddings []AliMultimodalEmbeddingItem `json:"embeddings"`
	} `json:"output"`
	Usage     AliUsage `json:"usage"`
	RequestId string   `json:"request_id"`
	AliError
}

func convertToMultimodalEmbeddingRequest(request dto.EmbeddingRequest) (*AliMultimodalEmbeddingRequest, error) {
	var items []any
	switch input := request.Input.(type) {
	case nil:
		return nil, fmt.Errorf("embedding input is empty")
	case string:
		items = []any{input}
	case []any:
		items = input
	default:
		return nil, fmt.Errorf("unsupported embedding input type: %T", request.Input)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("embedding input is empty")
	}

	aliReq := &AliMultimodalEmbeddingRequest{Model: request.Model}
	aliReq.Input.Contents = make([]AliMultimodalContent, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			aliReq.Input.Contents = append(aliReq.Input.Contents, AliMultimodalContent{Text: text})
			continue
		}
		part, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unsupported embedding input item type: %T", item)
		}
		partType, _ := part["type"].(string)
		var content AliMultimodalContent
		switch partType {
		case "":
			return nil, fmt.Errorf("multimodal input item missing required 'type' field")
		case dto.ContentTypeText:
			content.Text, _ = part[dto.ContentTypeText].(string)
		case dto.ContentTypeImageURL:
			url, err := parseURLField(part, dto.ContentTypeImageURL)
			if err != nil {
				return nil, err
			}
			content.Image = url
		case dto.ContentTypeVideoUrl:
			url, err := parseURLField(part, dto.ContentTypeVideoUrl)
			if err != nil {
				return nil, err
			}
			content.Video = url
		default:
			return nil, fmt.Errorf("unsupported multimodal input type: %s", partType)
		}
		aliReq.Input.Contents = append(aliReq.Input.Contents, content)
	}
	if request.Dimensions != nil {
		aliReq.Parameters = &AliMultimodalEmbeddingParameters{Dimension: request.Dimensions}
	}
	return aliReq, nil
}

func parseURLField(m map[string]any, key string) (string, error) {
	urlObj, ok := m[key].(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid %s format", key)
	}
	url, _ := urlObj["url"].(string)
	if url == "" {
		return "", fmt.Errorf("%s.url is required", key)
	}
	return url, nil
}

func MultimodalEmbeddingHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*types.NewAPIError, *dto.Usage) {
	responseBody, err := io.ReadAll(resp.Body)
	service.CloseResponseBodyGracefully(resp)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError), nil
	}

	var aliResponse AliMultimodalEmbeddingResponse
	if err = common.Unmarshal(responseBody, &aliResponse); err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError), nil
	}
	// The native endpoint can answer 200 with an error code in the body; the
	// client must not receive that failure as a 200.
	if aliResponse.Code != "" {
		return types.WithOpenAIError(types.OpenAIError{
			Message: aliResponse.Message,
			Type:    aliResponse.Code,
			Param:   aliResponse.RequestId,
			Code:    aliResponse.Code,
		}, http.StatusInternalServerError), nil
	}

	data := make([]dto.EmbeddingResponseItem, 0, len(aliResponse.Output.Embeddings))
	for _, embedding := range aliResponse.Output.Embeddings {
		data = append(data, dto.EmbeddingResponseItem{
			Object:    "embedding",
			Index:     embedding.Index,
			Embedding: embedding.Embedding,
		})
	}

	// The vl-embedding models and multimodal-embedding-v1 put text in
	// input_tokens and image and video tokens in image_tokens;
	// tongyi-embedding-vision counts all input in input_tokens and splits it in
	// input_tokens_details. Only input is priced, so output_tokens (also part of
	// total_tokens) is never billed. A response without usage falls back to the
	// request estimate, as in compatible mode.
	u := aliResponse.Usage
	var usage dto.Usage
	var prompt, text, image float64
	if details := u.InputTokensDetails; details != nil {
		prompt = float64(u.InputTokens)
		text = float64(details.TextTokens)
		image = float64(details.ImageTokens)
	} else {
		// Sum only after conversion, so an untrusted pair of signed integers
		// cannot wrap into a negative billable total.
		text = float64(u.InputTokens)
		image = float64(u.ImageTokens)
		prompt = max(0, text) + max(0, image)
	}
	if prompt <= 0 {
		if prompt < 0 {
			common.SysError(common.LogText("Ali multimodal embedding returned negative token usage"))
		}
		prompt = float64(info.GetEstimatePromptTokens())
	}
	for _, token := range []struct {
		value  float64
		target *int
	}{
		{prompt, &usage.PromptTokens},
		{text, &usage.PromptTokensDetails.TextTokens},
		{image, &usage.PromptTokensDetails.ImageTokens},
	} {
		if token.value < 0 {
			common.SysError(common.LogText("Ali multimodal embedding returned negative token usage"))
			*token.target = 0
			continue
		}
		count, clamp := common.QuotaFromFloatChecked(token.value)
		*token.target = count
		if clamp != nil && info.QuotaClamp == nil {
			info.QuotaClamp = clamp
		}
	}
	// Detail counts are a partition of the billable input, not additional
	// tokens. Inconsistent or saturated provider details cannot exceed it.
	if usage.PromptTokensDetails.ImageTokens > usage.PromptTokens ||
		usage.PromptTokensDetails.TextTokens > usage.PromptTokens-min(usage.PromptTokensDetails.ImageTokens, usage.PromptTokens) {
		common.SysError(common.LogText("Ali multimodal embedding token details exceed total input usage"))
	}
	usage.PromptTokensDetails.ImageTokens = min(usage.PromptTokensDetails.ImageTokens, usage.PromptTokens)
	usage.PromptTokensDetails.TextTokens = min(usage.PromptTokensDetails.TextTokens, usage.PromptTokens-usage.PromptTokensDetails.ImageTokens)
	usage.TotalTokens = usage.PromptTokens

	embeddingResponse := dto.EmbeddingResponse{
		Object: "list",
		Data:   data,
		Model:  info.UpstreamModelName,
		Usage:  usage,
	}

	jsonResponse, err := common.Marshal(embeddingResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	service.IOCopyBytesGracefully(c, resp, jsonResponse)
	return nil, &usage
}
