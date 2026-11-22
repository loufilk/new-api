package controller

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/request_plugin_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Commit a second writer's change after the edit has loaded its snapshot,
// before it acquires the row lock. The query callback runs after its rows are
// closed, so this interleaving also works with the single-connection fixture.
func commitAfterChannelEditSnapshot(t *testing.T, channelID int, changes map[string]any) *bool {
	t.Helper()
	database := model.DB
	fired := false
	callback := "channel_edit_snapshot:" + t.Name()
	require.NoError(t, database.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		stored, ok := tx.Statement.Dest.(*model.Channel)
		if fired || !ok || tx.Error != nil || stored.Id != channelID {
			return
		}
		fired = true
		require.NoError(t, database.Model(&model.Channel{}).Where("id = ?", channelID).Updates(changes).Error)
	}))
	t.Cleanup(func() { require.NoError(t, database.Callback().Query().Remove(callback)) })
	return &fired
}

func TestChannelEditRechecksPermissionsAfterConcurrentChange(t *testing.T) {
	for _, tc := range []struct {
		name           string
		field          string
		before         string
		after          string
		sensitiveWrite bool
		message        string
	}{
		{
			name: "request plugin binding", field: "setting", before: "{}",
			after: `{"request_plugins":["concurrent_guard"]}`, sensitiveWrite: true,
			message: "task_plugin.bind",
		},
		{
			name: "sensitive upstream address", field: "base_url",
			before: "https://old.example", after: "https://new.example",
			message: "Unauthorized, insufficient privileges",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupTaskPluginBindChannelTest(t)
			previousCache := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = false
			t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
			previousInstances, err := common.Marshal(request_plugin_setting.GetInstances())
			require.NoError(t, err)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{request_plugin_setting.InstancesOption: `[{"id":"concurrent_guard","name":"Guard","plugin":"filter"}]`}))
			t.Cleanup(func() {
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{request_plugin_setting.InstancesOption: string(previousInstances)}))
			})
			require.NoError(t, authz.SetUserPermissions(2, authz.PermissionsMap{
				authz.ResourceChannel:    {authz.ActionSensitiveWrite: tc.sensitiveWrite},
				authz.ResourceTaskPlugin: {authz.ActionBind: false},
			}))
			require.True(t, authz.Can(2, common.RoleAdminUser, authz.ChannelWrite))
			require.False(t, authz.Can(2, common.RoleAdminUser, authz.TaskPluginBind))
			setting, baseURL := "{}", "https://old.example"
			channel := model.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
				Name: "before-edit", Key: "static-key", Models: "gpt", Group: "default", Setting: &setting, BaseURL: &baseURL}
			require.NoError(t, channel.Insert())
			fired := commitAfterChannelEditSnapshot(t, channel.Id, map[string]any{tc.field: tc.after})
			body := fmt.Sprintf(`{"id":%d,"type":1,"name":"after-edit","models":"gpt","group":"default",%q:%q}`, channel.Id, tc.field, tc.before)
			response := putUpdateChannel(t, 2, common.RoleAdminUser, body)
			require.True(t, *fired, "the other writer must commit after the edit reads its original snapshot")
			assert.Contains(t, response.Body.String(), `"success":false`)
			assert.Contains(t, response.Body.String(), tc.message)
			stored, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, channel.Name, stored.Name, "a refused edit must not partly persist")
			if tc.field == "setting" {
				assert.Equal(t, []string{"concurrent_guard"}, stored.GetSetting().RequestPlugins)
			} else {
				require.NotNil(t, stored.BaseURL)
				assert.Equal(t, tc.after, *stored.BaseURL)
			}
		})
	}
}

func TestChannelEditChecksCredentialsAddedAfterSnapshot(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
	const pluginKey = "concurrent-credential-owner"
	channel := insertPluginChannel(t, pluginKey, "https://plugin.example", "", false)
	credential := pluginCredentialLine(t, pluginKey, "fresh-login")
	fired := commitAfterChannelEditSnapshot(t, channel.Id, map[string]any{"key": credential})
	// The channel had no login credential when this type change was first
	// checked. A login commits one before the edit obtains its row lock.
	body := fmt.Sprintf(`{"id":%d,"type":1,"name":"after-edit","models":"gpt","group":"default","setting":"{}"}`, channel.Id)
	response := putUpdateChannel(t, 1, common.RoleRootUser, body)
	require.True(t, *fired, "the concurrent login must commit before the edit locks the row")
	assert.Contains(t, response.Body.String(), `"success":false`)
	assert.Contains(t, response.Body.String(), "before changing the channel type or plugin")
	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, constant.ChannelTypeTaskPlugin, stored.Type)
	assert.Equal(t, pluginKey, stored.GetSetting().TaskPluginKey)
	assert.Equal(t, channel.Name, stored.Name)
	assert.Equal(t, credential, stored.Key, "a rejected type change must retain the newly logged-in credential")
}
