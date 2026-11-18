package controller

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelEditFailurePreservesCredentialAndConfiguration(t *testing.T) {
	for _, failure := range []string{"renamed group", "ability write"} {
		t.Run(failure, func(t *testing.T) {
			setupTaskPluginBindChannelTest(t)
			previousCache := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = false
			t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
			require.NoError(t, model.DB.AutoMigrate(&model.Option{}, &model.SystemTask{}))
			require.NoError(t, model.DB.Create(&model.Option{Key: "GroupRatio", Value: `{"new-group":1}`}).Error)
			require.NoError(t, model.DB.Create(&model.Option{Key: "GroupRenameAliases", Value: `{"old-group":"new-group"}`}).Error)
			channel := model.Channel{Type: 1, Status: common.ChannelStatusEnabled, Name: "before-edit", Key: "original-credential", Models: "old-model", Group: "new-group"}
			require.NoError(t, channel.Insert())
			group := "old-group"
			if failure == "ability write" {
				group = channel.Group
				require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register("channel_edit_ability_failure", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Ability" {
						tx.AddError(errors.New("ability write failed"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Create().Remove("channel_edit_ability_failure")) })
			}
			response, raw := callChannelAPI(t, UpdateChannel, http.MethodPut, "/api/channel", fmt.Sprintf(`{"id":%d,"type":1,"name":"after-edit","key":"replacement-credential","models":"new-model","group":%q}`, channel.Id, group))
			require.False(t, response.Success, raw)
			stored, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, channel.Name, stored.Name)
			assert.Equal(t, channel.Group, stored.Group)
			assert.Equal(t, channel.Key, stored.Key, "a failed edit must preserve the live credential")
			var abilities []model.Ability
			require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).Find(&abilities).Error)
			require.Len(t, abilities, 1)
			assert.Equal(t, "old-model", abilities[0].Model)
		})
	}
}
