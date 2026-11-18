package model

import (
	"github.com/QuantumNous/new-api/setting/request_plugin_setting"
	"gorm.io/gorm"
)

// Request plugin scopes share the group namespace lock with renames. Only
// the plugin settings API may save full instances; the group settings API
// may rewrite their group references but cannot replace their configuration.
func updateRequestPluginInstancesOption(value string) error {
	groups, err := groupRenameOptionGroupKeys(request_plugin_setting.InstancesOption, value)
	if err != nil {
		return err
	}
	groupRenameRuntimeApplyMutex.Lock()
	defer groupRenameRuntimeApplyMutex.Unlock()
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		if err := validateGroupNamesForWriteTx(tx, groups...); err != nil {
			return err
		}
		return upsertGroupRenameOption(tx, request_plugin_setting.InstancesOption, value)
	})
	if err != nil {
		return err
	}
	BumpSyncTopic(SyncTopicOptions)
	return updateOptionMap(request_plugin_setting.InstancesOption, value)
}
