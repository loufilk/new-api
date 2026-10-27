package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenGroupPolicyExactSelection(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","retired":"Retired","auto":"Auto"}`))
	for _, tc := range []struct {
		name, owner, group string
		err                error
	}{
		{"implicit unconfigured owner", "legacy", "", nil},
		{"explicit unconfigured owner", "legacy", "legacy", ErrTokenGroupDeprecated},
		{"own group", "svip", "svip", nil},
		{"public group", "default", "vip", nil},
		{"unknown group", "default", "secret", ErrTokenGroupForbidden},
		{"retired public group", "default", "retired", ErrTokenGroupDeprecated},
		{"auto without ratio", "default", "auto", nil},
		{"no whitespace normalization", "default", " vip ", ErrTokenGroupForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.ErrorIs(t, ValidateTokenGroup(tc.owner, tc.group), tc.err) })
	}
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	assert.ErrorIs(t, ValidateTokenGroup("default", "auto"), ErrTokenGroupForbidden)
}

func TestTokenEffectiveGroupScopeLegacySnapshots(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","svip":"SVIP","auto":"Auto"}`))
	for _, raw := range []string{"", "[]", "null"} {
		groups, err := GetTokenEffectiveGroups("default", &model.Token{Group: "auto", AutoGroups: raw})
		require.NoError(t, err)
		assert.Equal(t, []string{"vip", "default", "svip"}, groups, "global inheritance is not limited by per-token max")
	}
	groups, err := GetTokenEffectiveGroups("default", &model.Token{Group: "auto", AutoGroups: `["revoked","vip","default","svip"]`})
	require.NoError(t, err)
	assert.Equal(t, []string{"vip", "default"}, groups)
	groups, err = GetTokenEffectiveGroups("default", &model.Token{Group: "auto", AutoGroups: `["revoked"]`})
	require.NoError(t, err)
	assert.Empty(t, groups, "no fallback when all custom members are revoked")
	groups, err = GetTokenEffectiveGroups("default", &model.Token{Group: "auto", AutoGroups: " "})
	require.Error(t, err)
	assert.Empty(t, groups, "malformed whitespace cannot inherit global Auto")
	groups, err = GetTokenEffectiveGroups("legacy", &model.Token{})
	require.NoError(t, err)
	assert.Equal(t, []string{"legacy"}, groups)
	groups, err = GetTokenEffectiveGroups("default", &model.Token{Group: " vip "})
	require.NoError(t, err)
	assert.Empty(t, groups)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	for _, raw := range []string{"", `["vip"]`} {
		groups, err = GetTokenEffectiveGroups("default", &model.Token{Group: "auto", AutoGroups: raw})
		require.NoError(t, err)
		assert.Empty(t, groups)
	}
}

func TestTokenGroupPolicySpecialRules(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	previous := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.MarshalJSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.UnmarshalJSON([]byte(previous)))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.UnmarshalJSON([]byte(`{"enterprise":{"-:vip":"Hidden","+:auto":"Auto","+:svip":"Private"},"default":{"-:default":"Hidden"}}`)))
	assert.ErrorIs(t, ValidateTokenGroup("enterprise", "vip"), ErrTokenGroupForbidden)
	assert.NoError(t, ValidateTokenGroup("enterprise", "svip"))
	assert.NoError(t, ValidateTokenGroup("enterprise", "auto"))
	assert.NoError(t, ValidateTokenGroup("default", "vip"), "enterprise denial is scoped")
	assert.ErrorIs(t, ValidateTokenGroup("default", "auto"), ErrTokenGroupForbidden)
	assert.NoError(t, ValidateTokenGroup("default", "default"), "own-group restoration is retained")
}
