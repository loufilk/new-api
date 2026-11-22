package cline

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const (
	ChannelName     = "cline"
	DefaultBaseURL  = "https://api.cline.bot/api/v1"
	chatPath        = "/v1/chat/completions"
	clientTypeKey   = "X-Client-Type"
	clineClientType = "cline-cli"
)

// Adaptor implements the Cline gateway as an OpenAI-compatible chat channel.
// The empty-content normalization, client identity header, and success/data
// envelope handling follow AxonHub's cline transformer (commit
// cb29b65d9adfb06f89bb1b467418e0816988f36c).
type Adaptor struct {
	openai.Adaptor
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.Adaptor.Init(info)
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if !supportsClineRelay(info) {
		return "", unsupportedRelayError(info)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(info.ChannelBaseUrl), "/")
	if baseURL == "" {
		return "", fmt.Errorf("cline channel base URL is empty")
	}
	// The default Cline base URL already ends in /v1 while the chat path is
	// /v1/chat/completions, so a naive join would produce /v1/v1.
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	return baseURL + chatPath, nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	if err := a.Adaptor.SetupRequestHeader(c, header, info); err != nil {
		return err
	}
	header.Set(clientTypeKey, clineClientType)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	streamOptions := request.StreamOptions
	converted, err := a.Adaptor.ConvertOpenAIRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	if !ok {
		return converted, nil
	}
	restoreClineStreamOptions(info, convertedRequest, streamOptions, false)
	normalizeClineEmptyMessageContent(convertedRequest)
	return convertedRequest, nil
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	converted, err := a.Adaptor.ConvertClaudeRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	if !ok {
		return converted, nil
	}
	restoreClineStreamOptions(info, convertedRequest, nil, true)
	normalizeClineEmptyMessageContent(convertedRequest)
	return convertedRequest, nil
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	converted, err := a.Adaptor.ConvertGeminiRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	if !ok {
		return converted, nil
	}
	restoreClineStreamOptions(info, convertedRequest, nil, true)
	normalizeClineEmptyMessageContent(convertedRequest)
	return convertedRequest, nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	if info == nil {
		return nil, fmt.Errorf("relay info is required")
	}
	// Cline only exposes chat completions upstream, so the Responses API is
	// bridged through the shared relaykit converter and the OpenAI response
	// handler pair. /v1/responses/compact stays unsupported.
	info.ConvOptions().ReplayResponsesReasoning = true
	result, err := service.ConvertRequestByID(c, info, relayconvert.ConverterOpenAIResponsesToOpenAIChat, request)
	if err != nil {
		return nil, err
	}
	chatRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, fmt.Errorf("expected OpenAI chat completions request, got %T", result.Value)
	}
	if info.SupportStreamOptions && info.IsStream {
		chatRequest.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	}
	return a.ConvertOpenAIRequest(c, info, chatRequest)
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if !supportsClineRelay(info) {
		return nil, unsupportedRelayError(info)
	}
	if stopped := service.RelayRequestCancellation(c); stopped != nil {
		return nil, stopped
	}
	body, err := normalizeClineRequestBody(requestBody)
	if err != nil {
		return nil, err
	}
	response, err := channel.DoApiRequest(a, c, info, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	result, responseErr := classifyClineHTTPError(response)
	if apiErr, ok := responseErr.(*types.NewAPIError); ok {
		return result, normalizeClineReadCancellation(c, apiErr)
	}
	return result, responseErr
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	if !supportsClineRelay(info) {
		return nil, unsupportedRelayAPIError(info)
	}
	if resp != nil && resp.Body != nil {
		if info.IsStream {
			if apiErr := prepareClineStreamResponse(resp, info); apiErr != nil {
				return nil, normalizeClineReadCancellation(c, apiErr)
			}
		} else if apiErr := prepareClineJSONResponse(resp); apiErr != nil {
			return nil, normalizeClineReadCancellation(c, apiErr)
		}
	}
	if info.RelayFormat == types.RelayFormatOpenAIResponses {
		if info.IsStream {
			return openai.OaiChatToResponsesStreamHandler(c, info, resp)
		}
		return openai.OaiChatToResponsesHandler(c, info, resp)
	}
	return a.Adaptor.DoResponse(c, resp, info)
}

func (a *Adaptor) GetModelList() []string {
	return nil
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

func supportsClineRelay(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	switch info.RelayFormat {
	case types.RelayFormatClaude, types.RelayFormatGemini:
		return true
	case types.RelayFormatOpenAI:
		return info.RelayMode == relayconstant.RelayModeChatCompletions
	case types.RelayFormatOpenAIResponses:
		return info.RelayMode == relayconstant.RelayModeResponses
	default:
		return false
	}
}

func unsupportedRelayError(info *relaycommon.RelayInfo) error {
	return unsupportedRelayAPIError(info)
}

func unsupportedRelayAPIError(info *relaycommon.RelayInfo) *types.NewAPIError {
	format := "unknown"
	mode := "unknown"
	if info != nil {
		format = string(info.RelayFormat)
		mode = strconv.Itoa(info.RelayMode)
	}
	return types.NewErrorWithStatusCode(
		fmt.Errorf("cline channels only support OpenAI-compatible chat completions (format=%s mode=%s)", format, mode),
		types.ErrorCodeInvalidApiType,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
}

func restoreClineStreamOptions(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest, previous *dto.StreamOptions, applyDefault bool) {
	if info == nil || request == nil || !info.SupportStreamOptions {
		return
	}
	if previous != nil {
		request.StreamOptions = previous
		return
	}
	if applyDefault && info.IsStream {
		request.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	}
}

func normalizeClineEmptyMessageContent(request *dto.GeneralOpenAIRequest) {
	for i := range request.Messages {
		message := &request.Messages[i]
		if !clineRequiresContent(message.Role) || !isEmptyClineContent(message.Content) {
			continue
		}
		message.Content = []map[string]any{{"type": "text", "text": ""}}
	}
}

func clineRequiresContent(role string) bool {
	switch role {
	case "user", "system", "developer":
		return true
	default:
		return false
	}
}

func isEmptyClineContent(content any) bool {
	switch value := content.(type) {
	case nil:
		return true
	case string:
		return value == ""
	case []any:
		if len(value) == 0 {
			return true
		}
		return len(value) == 1 && isClineEmptyTextPart(value[0])
	case []dto.MediaContent:
		if len(value) == 0 {
			return true
		}
		return len(value) == 1 && value[0].Type == "text" && value[0].Text == ""
	default:
		return false
	}
}

func isClineEmptyTextPart(part any) bool {
	switch value := part.(type) {
	case map[string]any:
		if value["type"] != "text" {
			return false
		}
		text, ok := value["text"]
		if !ok || text == nil {
			return true
		}
		textValue, ok := text.(string)
		return ok && textValue == ""
	case dto.MediaContent:
		return value.Type == "text" && value.Text == ""
	default:
		return false
	}
}

// normalizeClineReadCancellation only reclassifies a failed response-body read
// when the incoming request really ended. A genuine upstream 400/429/503 or
// semantic error keeps its original classification and diagnostics.
func normalizeClineReadCancellation(c *gin.Context, apiErr *types.NewAPIError) *types.NewAPIError {
	if apiErr != nil && apiErr.GetErrorCode() == types.ErrorCodeReadResponseBodyFailed {
		if stopped := service.RelayRequestCancellation(c); stopped != nil {
			return stopped
		}
	}
	return apiErr
}
