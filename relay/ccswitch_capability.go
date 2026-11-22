package relay

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

const (
	CCSwitchAppClaude = "claude"
	CCSwitchAppCodex  = "codex"
	CCSwitchAppGemini = "gemini"
)

func NormalizeCCSwitchApp(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case CCSwitchAppClaude:
		return CCSwitchAppClaude, true
	case CCSwitchAppCodex:
		return CCSwitchAppCodex, true
	case CCSwitchAppGemini:
		return CCSwitchAppGemini, true
	default:
		return "", false
	}
}

func ccswitchRequestSpec(app, modelName string) (types.RelayFormat, int, string, bool) {
	switch app {
	case CCSwitchAppClaude:
		return types.RelayFormatClaude, relayconstant.RelayModeChatCompletions, "/v1/messages", true
	case CCSwitchAppCodex:
		return types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, "/v1/responses", true
	case CCSwitchAppGemini:
		modelPath := url.PathEscape(strings.TrimPrefix(strings.TrimSpace(modelName), "models/"))
		return types.RelayFormatGemini, relayconstant.RelayModeChatCompletions, "/v1beta/models/" + modelPath + ":generateContent", true
	default:
		return "", 0, "", false
	}
}

func readCCSwitchChannelSettings(channel *model.Channel) (dto.ChannelSettings, dto.ChannelOtherSettings, bool) {
	var setting dto.ChannelSettings
	if channel.Setting != nil && strings.TrimSpace(*channel.Setting) != "" {
		if err := common.Unmarshal([]byte(*channel.Setting), &setting); err != nil {
			return dto.ChannelSettings{}, dto.ChannelOtherSettings{}, false
		}
	}
	var other dto.ChannelOtherSettings
	if strings.TrimSpace(channel.OtherSettings) != "" {
		if err := common.UnmarshalJsonStr(channel.OtherSettings, &other); err != nil {
			return dto.ChannelSettings{}, dto.ChannelOtherSettings{}, false
		}
	}
	if preset := common.GetAdvancedCustomPreset(channel.Type); preset != nil {
		other.AdvancedCustom = preset
	}
	return setting, other, true
}

// SupportsCCSwitchModel performs a conversion-only probe against the channel's
// real adaptor. It never sends an upstream request. A model is advertised only
// when the adaptor can accept the application's wire protocol with the current
// channel configuration.
func SupportsCCSwitchModel(channel *model.Channel, app, modelName string) (supported bool) {
	defer func() {
		if recover() != nil {
			supported = false
		}
	}()
	if channel == nil || channel.Status != common.ChannelStatusEnabled || strings.TrimSpace(modelName) == "" {
		return false
	}
	app, ok := NormalizeCCSwitchApp(app)
	if !ok {
		return false
	}
	relayFormat, relayMode, requestPath, ok := ccswitchRequestSpec(app, modelName)
	if !ok {
		return false
	}
	apiType, _ := common.ChannelType2APIType(channel.Type)
	if apiType < 0 {
		return false
	}
	adaptor := GetAdaptor(apiType)
	if adaptor == nil {
		return false
	}
	channelSetting, channelOtherSettings, ok := readCCSwitchChannelSettings(channel)
	if !ok {
		return false
	}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayMode,
		OriginModelName: modelName,
		RequestURLPath:  requestPath,
		RelayFormat:     relayFormat,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          channel.Type,
			ChannelId:            channel.Id,
			ChannelBaseUrl:       channel.GetBaseURL(),
			ApiType:              apiType,
			ChannelSetting:       channelSetting,
			ChannelOtherSettings: channelOtherSettings,
			UpstreamModelName:    modelName,
			SupportStreamOptions: true,
		},
	}
	ctx := &gin.Context{
		Request: &http.Request{
			Method: http.MethodPost,
			URL:    &url.URL{Path: requestPath},
			Header: make(http.Header),
		},
	}
	adaptor.Init(info)

	var err error
	switch app {
	case CCSwitchAppClaude:
		_, err = adaptor.ConvertClaudeRequest(ctx, info, &dto.ClaudeRequest{Model: modelName})
	case CCSwitchAppCodex:
		_, err = adaptor.ConvertOpenAIResponsesRequest(ctx, info, dto.OpenAIResponsesRequest{
			Model: modelName,
			Input: json.RawMessage(`"ping"`),
		})
	case CCSwitchAppGemini:
		_, err = adaptor.ConvertGeminiRequest(ctx, info, &dto.GeminiChatRequest{})
	}
	return err == nil
}
