package controller

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// channelPublicResponse is an explicit read contract. A new database field
// must not become part of the channel listing just by being added to Channel.
type channelPublicResponse struct {
	Id                 int                         `json:"id"`
	Type               int                         `json:"type"`
	Key                string                      `json:"key"` // legacy clients expect this field; always empty
	OpenAIOrganization *string                     `json:"openai_organization"`
	TestModel          *string                     `json:"test_model"`
	Status             int                         `json:"status"`
	Name               string                      `json:"name"`
	Weight             *uint                       `json:"weight"`
	CreatedTime        int64                       `json:"created_time"`
	TestTime           int64                       `json:"test_time"`
	ResponseTime       int                         `json:"response_time"`
	BaseURL            *string                     `json:"base_url"`
	Other              string                      `json:"other"`
	Balance            float64                     `json:"balance"`
	BalanceUpdatedTime int64                       `json:"balance_updated_time"`
	Models             string                      `json:"models"`
	Group              string                      `json:"group"`
	UsedQuota          int64                       `json:"used_quota"`
	ModelMapping       *string                     `json:"model_mapping"`
	StatusCodeMapping  *string                     `json:"status_code_mapping"`
	Priority           *int64                      `json:"priority"`
	AutoBan            *int                        `json:"auto_ban"`
	OtherInfo          string                      `json:"other_info"`
	Tag                *string                     `json:"tag"`
	Setting            *string                     `json:"setting"`
	ParamOverride      *string                     `json:"param_override"`  // legacy shape; always null
	HeaderOverride     *string                     `json:"header_override"` // legacy shape; always null
	Remark             *string                     `json:"remark"`
	ChannelInfo        model.ChannelInfo           `json:"channel_info"`
	OtherSettings      string                      `json:"settings"`
	BalanceDetail      *model.ChannelBalanceDetail `json:"balance_detail,omitempty"`
}

var channelSettingReadSecrets = []string{"proxy"}
var channelOtherSettingReadSecrets = []string{"advanced_custom", "plugin_config"}

func publicChannel(channel *model.Channel) *channelPublicResponse {
	if channel == nil {
		return nil
	}
	info := channel.ChannelInfo
	info.MultiKeyDisabledReason = nil
	info.MultiKeyDisabledTime = nil
	var setting *string
	if channel.Setting != nil {
		value := publicChannelConfig(*channel.Setting, channelSettingReadSecrets, hiddenChannelSetting)
		setting = &value
	}
	baseURL := channel.BaseURL
	if baseURL != nil && channelURLHasCredentials(*baseURL) {
		baseURL = nil
	}
	return &channelPublicResponse{
		Id: channel.Id, Type: channel.Type, OpenAIOrganization: channel.OpenAIOrganization,
		TestModel: channel.TestModel, Status: channel.Status, Name: channel.Name, Weight: channel.Weight,
		CreatedTime: channel.CreatedTime, TestTime: channel.TestTime, ResponseTime: channel.ResponseTime,
		BaseURL: baseURL, Other: channel.Other, Balance: channel.Balance,
		BalanceUpdatedTime: channel.BalanceUpdatedTime, Models: channel.Models, Group: string(channel.Group),
		UsedQuota: channel.UsedQuota, ModelMapping: channel.ModelMapping, StatusCodeMapping: channel.StatusCodeMapping,
		Priority: channel.Priority, AutoBan: channel.AutoBan, OtherInfo: publicChannelOtherInfo(channel),
		Tag: channel.Tag, Setting: setting, Remark: channel.Remark, ChannelInfo: info,
		OtherSettings: publicChannelConfig(channel.OtherSettings, channelOtherSettingReadSecrets, hiddenChannelOtherSetting),
		BalanceDetail: publicChannelBalanceDetail(channel.GetBalanceDetail()),
	}
}

func publicChannels(channels []*model.Channel) []*channelPublicResponse {
	if channels == nil {
		return nil
	}
	result := make([]*channelPublicResponse, len(channels))
	for i, channel := range channels {
		result[i] = publicChannel(channel)
	}
	return result
}

func channelURLHasCredentials(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != ""
}

func readChannelConfig(raw string) map[string]json.RawMessage {
	if strings.TrimSpace(raw) == "" {
		return map[string]json.RawMessage{}
	}
	var config map[string]json.RawMessage
	if common.Unmarshal([]byte(raw), &config) != nil || config == nil {
		return nil
	}
	return config
}

func hiddenChannelSetting(key string, value json.RawMessage) bool {
	var proxy string
	return key == "proxy" && (common.Unmarshal(value, &proxy) != nil || channelURLHasCredentials(proxy))
}

func hiddenChannelOtherSetting(key string, value json.RawMessage) bool {
	switch key {
	case "plugin_config":
		// Plugins define arbitrary property names, including secret settings.
		return len(value) > 0
	case "advanced_custom":
		var config struct {
			Routes []struct {
				UpstreamPath string `json:"upstream_path"`
				Auth         *struct {
					Value string `json:"value"`
				} `json:"auth"`
			} `json:"advanced_routes"`
		}
		if common.Unmarshal(value, &config) != nil {
			return true
		}
		for _, route := range config.Routes {
			if channelURLHasCredentials(route.UpstreamPath) ||
				(route.Auth != nil && route.Auth.Value != "" && route.Auth.Value != "{api_key}") {
				return true
			}
		}
	}
	return false
}

func publicChannelConfig(raw string, fields []string, hidden func(string, json.RawMessage) bool) string {
	config := readChannelConfig(raw)
	if config == nil {
		// Never echo opaque/malformed data from storage.
		return ""
	}
	changed := false
	for name, value := range config {
		if field := channelConfigFieldName(name, fields); field != "" && hidden(field, value) {
			delete(config, name)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	encoded, err := common.Marshal(config)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// The typed JSON decoders for channel settings accept case variants too.
func channelConfigFieldName(name string, fields []string) string {
	for _, field := range fields {
		if strings.EqualFold(name, field) {
			return field
		}
	}
	return ""
}

// preserveHiddenChannelConfig seals the fields unavailable to a read→edit
// client. The caller checks that the snapshot used for this merge still
// matches the row it locked before committing.
func preserveHiddenChannelConfig(oldRaw, newRaw string, fields []string, hidden func(string, json.RawMessage) bool) (string, bool) {
	oldConfig, newConfig := readChannelConfig(oldRaw), readChannelConfig(newRaw)
	if oldConfig == nil || newConfig == nil {
		return newRaw, false
	}
	preserved := false
	for oldName, old := range oldConfig {
		field := channelConfigFieldName(oldName, fields)
		if field == "" || !hidden(field, old) {
			continue
		}
		emptyNames := make([]string, 0, 1)
		replacement := false
		for name, next := range newConfig {
			if !strings.EqualFold(name, field) {
				continue
			}
			// Explicit null is distinguished from older forms' placeholders.
			if field != "proxy" || string(next) != `""` {
				if field != "plugin_config" || string(next) != "{}" {
					replacement = true
					break
				}
			}
			emptyNames = append(emptyNames, name)
		}
		if replacement {
			continue
		}
		for _, name := range emptyNames {
			delete(newConfig, name)
		}
		newConfig[oldName] = old
		preserved = true
	}
	if !preserved {
		return newRaw, false
	}
	encoded, err := common.Marshal(newConfig)
	if err != nil {
		// Existing data should be retained rather than lost on a failed merge.
		return oldRaw, true
	}
	canonicalOld, err := common.Marshal(oldConfig)
	if err == nil && bytes.Equal(canonicalOld, encoded) {
		return oldRaw, true
	}
	return string(encoded), true
}

// The status UI identifies credentials by index only. This projection never
// receives the credential or anything derived from it.
func publicChannelKeyStatus(index int, info model.ChannelInfo) KeyStatus {
	status, found := info.MultiKeyStatusList[index]
	if !found {
		status = common.ChannelStatusEnabled
	}
	result := KeyStatus{Index: index, Status: status}
	if status != common.ChannelStatusEnabled {
		result.DisabledTime = info.MultiKeyDisabledTime[index]
		result.Reason = safeChannelStatusReason(info.MultiKeyDisabledReason[index])
	}
	return result
}

func safeChannelStatusReason(reason string) string {
	switch reason {
	case "":
		return ""
	case model.ChannelStatusReasonAllKeysDisabled, model.ChannelStatusReasonPluginLoginRequired, model.ChannelStatusReasonPluginNeedsLogin:
		return reason
	default:
		return "Upstream unavailable"
	}
}

func publicChannelOtherInfo(channel *model.Channel) string {
	if channel.OtherInfo == "" {
		return ""
	}
	old := channel.GetOtherInfo()
	info := make(map[string]any)
	if time, ok := old["status_time"].(float64); ok {
		info["status_time"] = time
	}
	if source, ok := old["source"].(string); ok && source == "ionet" {
		info["source"] = source
		if deployment, ok := old["deployment_id"].(string); ok {
			info["deployment_id"] = deployment
		}
	}
	if reason, ok := old["status_reason"].(string); ok {
		info["status_reason"] = safeChannelStatusReason(reason)
	}
	if base, ok := old["base_url"].(string); ok && !channelURLHasCredentials(base) {
		info["base_url"] = base
	}
	encoded, err := common.Marshal(info)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// The balance query's diagnostics are for operators' logs, not channel reads.
func publicChannelBalanceDetail(detail *model.ChannelBalanceDetail) *model.ChannelBalanceDetail {
	if detail == nil {
		return nil
	}
	public := *detail
	if public.Error != "" {
		public.Error = "Balance check failed"
	}
	if detail.Keys != nil {
		public.Keys = make(map[string]model.ChannelBalanceDetail, len(detail.Keys))
		for index, key := range detail.Keys {
			public.Keys[index] = *publicChannelBalanceDetail(&key)
		}
	}
	return &public
}

type channelPreservedReadSecrets struct {
	baseURL, setting, otherSettings bool
}

func preserveChannelReadOnlySecrets(patch *PatchChannel, stored *model.Channel, fields map[string]any) (preserved channelPreservedReadSecrets) {
	// Status and balance writers own this column. Never replay the reduced
	// other_info response into storage from a legacy whole-record editor.
	patch.OtherInfo = ""
	delete(fields, "other_info")
	// The old form sends an empty string for a withheld override. An explicit
	// empty object (the effective no-override value) is still a replacement.
	if patch.ParamOverride == nil || strings.TrimSpace(*patch.ParamOverride) == "" {
		patch.ParamOverride = nil
		delete(fields, "param_override")
	}
	if patch.HeaderOverride == nil || strings.TrimSpace(*patch.HeaderOverride) == "" {
		patch.HeaderOverride = nil
		delete(fields, "header_override")
	}
	if _, supplied := fields["base_url"]; supplied &&
		stored.BaseURL != nil && channelURLHasCredentials(*stored.BaseURL) &&
		(patch.BaseURL == nil || strings.TrimSpace(*patch.BaseURL) == "") {
		patch.BaseURL = stored.BaseURL
		preserved.baseURL = true
	}
	if patch.Setting != nil && stored.Setting != nil {
		raw, kept := preserveHiddenChannelConfig(*stored.Setting, *patch.Setting, channelSettingReadSecrets, hiddenChannelSetting)
		patch.Setting = &raw
		preserved.setting = kept
	}
	if _, supplied := fields["settings"]; supplied && patch.OtherSettings != "" {
		patch.OtherSettings, preserved.otherSettings = preserveHiddenChannelConfig(stored.OtherSettings, patch.OtherSettings, channelOtherSettingReadSecrets, hiddenChannelOtherSetting)
	}
	return preserved
}

func (p channelPreservedReadSecrets) conflicts(old, locked *model.Channel) bool {
	return (p.baseURL && !equalStringPtr(old.BaseURL, locked.BaseURL)) ||
		(p.setting && !equalStringPtr(old.Setting, locked.Setting)) ||
		(p.otherSettings && old.OtherSettings != locked.OtherSettings)
}
