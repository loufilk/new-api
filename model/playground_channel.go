package model

import (
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
)

// NewPlaygroundChannelFilter pairs advertised model metadata with an actual
// channel in the caller's selected groups. An endpoint inferred for a different
// channel is not evidence that this channel can handle it.
func NewPlaygroundChannelFilter(endpoint constant.EndpointType) func(*Channel, string) bool {
	var declared map[string]bool
	if endpoint == constant.EndpointTypeImageGeneration {
		declared = make(map[string]bool)
		for _, row := range GetPricing() {
			if row.declaredImageEndpoint {
				declared[row.ModelName] = true
			}
		}
	}
	return func(ch *Channel, name string) bool {
		return playgroundChannelSupports(ch, name, endpoint, declared[name])
	}
}
func playgroundChannelSupports(ch *Channel, name string, endpoint constant.EndpointType, declaredImage bool) bool {
	if ch == nil || ch.Status != common.ChannelStatusEnabled || name == "" {
		return false
	}
	path := "/v1/chat/completions"
	if endpoint == constant.EndpointTypeImageGeneration {
		path = "/v1/images/generations"
	} else if endpoint != constant.EndpointTypeOpenAI {
		return false
	}
	// The legacy channel getters repair malformed settings. Discovery is read-only.
	var settings relaydto.ChannelSettings
	if ch.Setting != nil && *ch.Setting != "" && common.UnmarshalJsonStr(*ch.Setting, &settings) != nil {
		return false
	}
	if ch.OtherSettings != "" {
		var other relaydto.ChannelOtherSettings
		if common.UnmarshalJsonStr(ch.OtherSettings, &other) != nil {
			return false
		}
	}
	target := name
	if raw := ch.GetModelMapping(); raw != "" && raw != "{}" {
		var mappings map[string]string
		if common.UnmarshalJsonStr(raw, &mappings) != nil {
			return false
		}
		var cycle bool
		target, cycle = followChannelModelMapping(mappings, name)
		if cycle {
			return false
		}
	}
	if endpoint == constant.EndpointTypeImageGeneration {
		// Image requests also enter the task-plugin protocol dispatcher.
		gen := jsplugin.DefaultRegistry.Generation()
		for _, candidate := range gen.LookupEndpointCandidates(http.MethodPost, path, target) {
			p := candidate.Plugin.Meta
			if ch.Type == constant.ChannelTypeTaskPlugin || ch.Type == constant.ChannelTypeNewAPI {
				if settings.BindsTaskPlugin(p.Key) {
					return true
				}
			} else if slices.Contains(p.ChannelTypes, ch.Type) {
				return true
			}
		}
	}
	if ok, _ := ChannelSatisfiesFilters(ch, name, []dto.ChannelFilter{{Kind: dto.FilterRequestPath, RequestPath: path}}); !ok {
		return false
	}
	if ch.Type == constant.ChannelTypeTaskPlugin {
		return providerChannelServes(ch, path, name)
	}
	if constant.IsAdvancedCustomChannel(ch.Type) {
		// The explicit per-model path already matched.
		return true
	}
	if ch.Type == constant.ChannelTypeCodex {
		// Only admitted above if the configured Chat→Responses bridge allows it.
		return endpoint == constant.EndpointTypeOpenAI
	}
	if endpoint == constant.EndpointTypeImageGeneration {
		apiType, _ := common.ChannelType2APIType(ch.Type)
		switch apiType {
		case constant.APITypeGemini, constant.APITypeVertexAi:
			return strings.HasPrefix(target, "imagen")
		case constant.APITypeXai:
			return strings.HasPrefix(target, "grok-imagine-image")
		case constant.APITypeOpenAI, constant.APITypeOpenRouter, constant.APITypeXinference, constant.APITypeSub2API, constant.APITypeNewAPI, constant.APITypeZhipuV4, constant.APITypeSiliconFlow, constant.APITypeVolcEngine, constant.APITypeJimeng, constant.APITypeMoonshot, constant.APITypeMiniMax, constant.APITypeReplicate:
			return declaredImage || common.IsImageGenerationModel(target)
		default:
			return false
		}
	}
	if endpoint == constant.EndpointTypeOpenAI {
		id := strings.ToLower(target)
		if common.IsImageGenerationModel(target) || strings.HasPrefix(id, "text-embedding-") || strings.HasPrefix(id, "embedding-") || strings.HasPrefix(id, "whisper-") || strings.HasPrefix(id, "tts-") {
			return false
		}
	}
	return slices.Contains(common.GetEndpointTypesByChannelType(ch.Type, target), endpoint)
}
