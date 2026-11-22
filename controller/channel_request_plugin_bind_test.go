package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/request_plugin_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestPluginChannelChangesRequireBindPermission(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	previous, err := common.Marshal(request_plugin_setting.GetInstances())
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{request_plugin_setting.InstancesOption: `[{"id":"rp_first","name":"First","plugin":"filter"},{"id":"rp_second","name":"Second","plugin":"filter"}]`}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{request_plugin_setting.InstancesOption: string(previous)}))
	})
	require.NoError(t, authz.SetUserPermissions(2, authz.PermissionsMap{
		authz.ResourceChannel:    {authz.ActionSensitiveWrite: true},
		authz.ResourceTaskPlugin: {authz.ActionBind: false},
	}))
	require.True(t, authz.Can(2, common.RoleAdminUser, authz.ChannelWrite))
	require.True(t, authz.Can(2, common.RoleAdminUser, authz.ChannelSensitiveWrite))
	require.False(t, authz.Can(2, common.RoleAdminUser, authz.TaskPluginBind))
	setting := `{"request_plugins":["rp_first","rp_second"]}`
	channel := model.Channel{Type: 1, Status: common.ChannelStatusEnabled, Name: "guarded", Models: "gpt", Group: "default", Key: "credential", Setting: &setting}
	require.NoError(t, channel.Insert())
	for _, tc := range []struct {
		name    string
		setting string
		allowed bool
	}{
		{name: "unchanged bindings", setting: setting, allowed: true},
		{name: "removed bindings", setting: "{}"},
		{name: "reordered bindings", setting: `{"request_plugins":["rp_second","rp_first"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"id":%d,"type":1,"name":"edited","models":"gpt","group":"default","setting":%q}`, channel.Id, tc.setting)
			response := putUpdateChannel(t, 2, common.RoleAdminUser, body)
			var result struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.Equal(t, tc.allowed, result.Success, response.Body.String())
			stored, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, []string{"rp_first", "rp_second"}, stored.GetSetting().RequestPlugins)
		})
	}
	t.Run("create bound channel", func(t *testing.T) {
		body := fmt.Sprintf(`{"mode":"single","channel":{"type":1,"name":"bound","key":"credential","models":"gpt","group":"default","setting":%q}}`, setting)
		response := postAddChannel(t, 2, common.RoleAdminUser, body)
		assert.Contains(t, response.Body.String(), `"success":false`)
		assert.Contains(t, response.Body.String(), "task_plugin.bind")
		response = postAddChannel(t, 1, common.RoleRootUser, body)
		assert.Contains(t, response.Body.String(), `"success":true`)
	})
	t.Run("copy bound channel", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", 2)
		c.Set("role", common.RoleAdminUser)
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(channel.Id)}}
		c.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/channel/copy/%d", channel.Id), nil)
		CopyChannel(c)
		assert.Contains(t, recorder.Body.String(), `"success":false`)
		assert.Contains(t, recorder.Body.String(), "task_plugin.bind")
	})
}
