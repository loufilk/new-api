package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicChannelResponseKeepsOperationalMetadataWithoutSecrets(t *testing.T) {
	origin := model.Channel{
		Id: 42, Name: "Search cluster", Models: "search-model", Group: "staging", Status: 3,
		Key:            "synthetic-provider-value",
		BaseURL:        new("https://upstream.invalid/api?profile=synthetic-endpoint-value"),
		HeaderOverride: new(`{"X-Operator":"synthetic-header-value"}`),
		ParamOverride:  new(`{"model_context":"synthetic-param-value"}`),
		Setting:        new(`{"Proxy":"http://operator:synthetic-proxy-value@proxy.invalid:8080","force_format":true,"task_plugin_key":"fixture"}`),
		OtherSettings:  `{"tool_loss_policy":"strict","advanced_custom":{"advanced_routes":[{"auth":{"value":"synthetic-route-value"}}]},"Plugin_Config":{"opaque":"synthetic-plugin-value"}}`,
		OtherInfo:      `{"status_reason":"synthetic-diagnostic-value","status_time":1700000000,"source":"ionet","deployment_id":"deployment-42","balance_detail":{"amount":10,"currency":"USD","error":"synthetic-balance-value","keys":{"0":{"amount":2,"error":"synthetic-key-balance-value"}}},"unrelated":"synthetic-info-value"}`,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{0: 3},
			MultiKeyDisabledReason: map[int]string{0: "synthetic-disabled-value"},
			MultiKeyDisabledTime:   map[int]int64{0: 1700000000},
		},
	}
	reply := publicChannel(&origin)
	assert.Equal(t, 42, reply.Id)
	assert.Equal(t, "Search cluster", reply.Name)
	assert.Equal(t, "search-model", reply.Models)
	assert.Equal(t, "staging", reply.Group)
	assert.Equal(t, map[int]int{0: 3}, reply.ChannelInfo.MultiKeyStatusList)
	assert.Nil(t, reply.ChannelInfo.MultiKeyDisabledReason)
	assert.Nil(t, reply.BaseURL)
	require.NotNil(t, reply.Setting)
	assert.JSONEq(t, `{"force_format":true,"task_plugin_key":"fixture"}`, *reply.Setting)
	assert.JSONEq(t, `{"tool_loss_policy":"strict"}`, reply.OtherSettings)
	assert.JSONEq(t, `{"status_reason":"Upstream unavailable","status_time":1700000000,"source":"ionet","deployment_id":"deployment-42"}`, reply.OtherInfo)
	require.NotNil(t, reply.BalanceDetail)
	require.NotNil(t, reply.BalanceDetail.Amount)
	assert.Equal(t, float64(10), *reply.BalanceDetail.Amount)
	assert.Equal(t, "Balance check failed", reply.BalanceDetail.Error)
	assert.Equal(t, "Balance check failed", reply.BalanceDetail.Keys["0"].Error)
	encoded, err := common.Marshal(reply)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "synthetic-")
	assert.Contains(t, string(encoded), `"key":""`)
	assert.Contains(t, string(encoded), `"param_override":null`)
	assert.Contains(t, string(encoded), `"header_override":null`)
	assert.Equal(t, "synthetic-provider-value", origin.Key)
	assert.NotEmpty(t, origin.ChannelInfo.MultiKeyDisabledReason)
}

func TestPublicChannelReadKeepsOrdinaryRoutingConfiguration(t *testing.T) {
	origin := model.Channel{
		BaseURL:       new("https://upstream.invalid/api"),
		Setting:       new(`{"proxy":"http://proxy.invalid:8080","http_protocol":"http1"}`),
		OtherSettings: `{"advanced_custom":{"advanced_routes":[{"incoming_path":"/v1/models","upstream_path":"/api/models","auth":{"value":"{api_key}"}}]},"vertex_project_id":"fixture"}`,
	}
	reply := publicChannel(&origin)
	assert.Equal(t, origin.BaseURL, reply.BaseURL)
	assert.JSONEq(t, *origin.Setting, *reply.Setting)
	assert.JSONEq(t, origin.OtherSettings, reply.OtherSettings)
}

func TestPublicChannelRoundTripRetainsWithheldFields(t *testing.T) {
	origin := model.Channel{
		BaseURL:        new("https://upstream.invalid/api?profile=synthetic-endpoint-value"),
		ParamOverride:  new(`{"model_context":"held"}`),
		HeaderOverride: new(`{"X-Operator":"held"}`),
		Setting:        new(`{"proxy":"http://operator:fixture@proxy.invalid:8080","force_format":true}`),
		OtherSettings:  `{"plugin_config":{"opaque":"fixture"},"advanced_custom":{"advanced_routes":[{"upstream_path":"/api/models?profile=fixture"}]},"tool_loss_policy":"safe"}`,
	}
	patch := PatchChannel{Channel: model.Channel{
		Name: "Updated name", BaseURL: new(""), ParamOverride: new(""), HeaderOverride: new(""),
		Setting:       new(`{"proxy":"","force_format":false}`),
		OtherSettings: `{"tool_loss_policy":"strict"}`,
		OtherInfo:     `{"status_time":1700000000}`,
	}}
	fields := map[string]any{"name": "Updated name", "base_url": "", "param_override": "", "header_override": "", "setting": *patch.Setting, "settings": patch.OtherSettings, "other_info": patch.OtherInfo}
	preserved := preserveChannelReadOnlySecrets(&patch, &origin, fields)
	assert.Equal(t, "Updated name", patch.Name)
	assert.Equal(t, origin.BaseURL, patch.BaseURL)
	assert.Nil(t, patch.ParamOverride)
	assert.Nil(t, patch.HeaderOverride)
	assert.NotContains(t, fields, "param_override")
	assert.NotContains(t, fields, "header_override")
	assert.NotContains(t, fields, "other_info")
	assert.Empty(t, patch.OtherInfo)
	assert.JSONEq(t, `{"proxy":"http://operator:fixture@proxy.invalid:8080","force_format":false}`, *patch.Setting)
	assert.JSONEq(t, `{"plugin_config":{"opaque":"fixture"},"advanced_custom":{"advanced_routes":[{"upstream_path":"/api/models?profile=fixture"}]},"tool_loss_policy":"strict"}`, patch.OtherSettings)
	assert.False(t, preserved.conflicts(&origin, &origin))
	locked := origin
	locked.Setting = new(`{"proxy":"http://new-operator:fixture@proxy.invalid:8080"}`)
	assert.True(t, preserved.conflicts(&origin, &locked))

	patch.ParamOverride = new("{}")
	patch.HeaderOverride = new("{}")
	patch.Setting = new(`{"proxy":null}`)
	patch.OtherSettings = `{"plugin_config":null,"advanced_custom":null}`
	fields["param_override"], fields["header_override"] = "{}", "{}"
	preserveChannelReadOnlySecrets(&patch, &origin, fields)
	assert.Equal(t, "{}", *patch.ParamOverride)
	assert.Equal(t, "{}", *patch.HeaderOverride)
	assert.JSONEq(t, `{"proxy":null}`, *patch.Setting)
	assert.JSONEq(t, `{"plugin_config":null,"advanced_custom":null}`, patch.OtherSettings)
}

func TestPublicChannelKeyStatusOnlyExposesOperationalFields(t *testing.T) {
	info := model.ChannelInfo{
		MultiKeyStatusList:     map[int]int{1: common.ChannelStatusAutoDisabled, 2: common.ChannelStatusManuallyDisabled},
		MultiKeyDisabledReason: map[int]string{1: "fixture diagnostic details", 2: model.ChannelStatusReasonPluginNeedsLogin},
		MultiKeyDisabledTime:   map[int]int64{1: 1700000000, 2: 1700000001},
	}
	active := publicChannelKeyStatus(0, info)
	assert.Equal(t, KeyStatus{Index: 0, Status: common.ChannelStatusEnabled}, active)
	disabled := publicChannelKeyStatus(1, info)
	assert.Equal(t, KeyStatus{Index: 1, Status: common.ChannelStatusAutoDisabled, DisabledTime: 1700000000, Reason: "Upstream unavailable"}, disabled)
	login := publicChannelKeyStatus(2, info)
	assert.Equal(t, model.ChannelStatusReasonPluginNeedsLogin, login.Reason)
	wire, err := common.Marshal(disabled)
	require.NoError(t, err)
	assert.Contains(t, string(wire), `"key_preview":""`)
}
