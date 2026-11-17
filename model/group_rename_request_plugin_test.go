package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/request_plugin_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestPluginScopeRenameDatabaseMatrix(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			useGroupRenameDatabase(t, dialect)
			captureGroupRenameRuntimeState(t)
			seedGroupRenameOption(t, "GroupRatio", `{"old":1,"keep":1}`)
			original := `[{"id":"rp_filter","groups":["old"],"config":{"threshold":9007199254740993},"future_field":{"keep":true}}]`
			seedGroupRenameOption(t, request_plugin_setting.InstancesOption, original)
			_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
			require.NoError(t, err)
			var option Option
			require.NoError(t, DB.Where(&Option{Key: request_plugin_setting.InstancesOption}).First(&option).Error)
			assert.JSONEq(t, `[{"id":"rp_filter","groups":["new"],"config":{"threshold":9007199254740993},"future_field":{"keep":true}}]`, option.Value)
			assert.Contains(t, option.Value, "9007199254740993", "a group rename must preserve exact plugin config numbers")
			require.Len(t, request_plugin_setting.GetInstances(), 1)
			assert.Equal(t, []string{"new"}, request_plugin_setting.GetInstances()[0].Groups)
			require.ErrorIs(t, UpdateOption(request_plugin_setting.InstancesOption, original), ErrGroupReferenceRenamed)
			require.ErrorIs(t, UpdateOption("GroupRatio", `{"keep":1}`), ErrGroupRenameReferencedGroup)
			require.NoError(t, UpdateOption(request_plugin_setting.InstancesOption, `[]`))
			require.NoError(t, UpdateOption("GroupRatio", `{"keep":1}`))
		})
	}
}

func TestGroupRenameMigratesRequestPluginScopes(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8,"keep":1}`)
	seedGroupRenameOption(t, request_plugin_setting.InstancesOption,
		`[{"id":"rp_safety","name":"Group safety filter","plugin":"filter","enabled":true,"global":false,"groups":["old","keep"],"models":["gpt"],"config":{"threshold":0.125}}]`)
	result, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotContains(t, result.AppliedOptions, request_plugin_setting.InstancesOption, "group responses must not expose plugin configuration")
	var instances []request_plugin_setting.Instance
	loadGroupRenameOptionJSON(t, request_plugin_setting.InstancesOption, &instances)
	require.Len(t, instances, 1)
	assert.Equal(t, []string{"new", "keep"}, instances[0].Groups)
	assert.JSONEq(t, `{"threshold":0.125}`, string(instances[0].Config))
	assert.Equal(t, []string{"gpt"}, instances[0].Models)
	require.Len(t, request_plugin_setting.GetInstances(), 1)
	assert.Equal(t, []string{"new", "keep"}, request_plugin_setting.GetInstances()[0].Groups)
}

func TestRequestPluginScopesRejectRetiredGroupOnSave(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1,"keep":1}`)
	original := `[{"id":"rp_safety","name":"Filter","plugin":"filter","enabled":true,"groups":["old"],"config":{}}]`
	seedGroupRenameOption(t, request_plugin_setting.InstancesOption, original)
	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	err = UpdateOption(request_plugin_setting.InstancesOption, original)
	require.ErrorIs(t, err, ErrGroupReferenceRenamed)
	var instances []request_plugin_setting.Instance
	loadGroupRenameOptionJSON(t, request_plugin_setting.InstancesOption, &instances)
	require.Len(t, instances, 1)
	assert.Equal(t, []string{"new"}, instances[0].Groups)
}

func TestGroupRemovalRejectsRequestPluginReference(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1,"keep":1}`)
	seedGroupRenameOption(t, request_plugin_setting.InstancesOption, `[{"id":"rp_safety","groups":["old"]}]`)
	err := UpdateOption("GroupRatio", `{"keep":1}`)
	require.ErrorIs(t, err, ErrGroupRenameReferencedGroup)
}

func TestGroupRenameAPIRejectsFullPluginConfigurationEdits(t *testing.T) {
	setupGroupRenameTest(t)
	_, err := ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{
		request_plugin_setting.InstancesOption: `[{"id":"rp_safety","groups":[]}]`,
	}})
	require.ErrorIs(t, err, ErrGroupRenameInvalidOption)
}
