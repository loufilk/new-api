package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelEditTransactionPreservesCredentialOnFailure(t *testing.T) {
	useScheduledChannelDatabase(t)
	channel := Channel{Type: 1, Name: "original", Key: "key-original", Models: "model-original", Group: "default", Status: common.ChannelStatusEnabled}
	require.NoError(t, channel.Insert())
	InitChannelCache()
	edited := Channel{Id: channel.Id, Name: "replacement", Models: "model-replacement", Group: "default"}
	failure := errors.New("ability persistence unavailable")
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register("channel_edit_ability_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Ability" {
			tx.AddError(failure)
		}
	}))
	err := edited.UpdateWithKey(func(stored *Channel) error {
		stored.Key = "key-replacement"
		return nil
	})
	require.ErrorIs(t, err, failure)
	require.NoError(t, DB.Callback().Create().Remove("channel_edit_ability_failure"))
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "key-original", stored.Key)
	assert.Equal(t, "original", stored.Name)
	var abilities []Ability
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).Find(&abilities).Error)
	require.Len(t, abilities, 1)
	assert.Equal(t, "model-original", abilities[0].Model)

	// A refresh that finishes before this edit must be the append baseline.
	_, err = UpdateChannelLocked(channel.Id, func(stored *Channel) error {
		stored.Key = "key-refreshed"
		return nil
	})
	require.NoError(t, err)
	edited = Channel{Id: channel.Id, Name: "replacement", Models: "model-replacement", Group: "default", ChannelInfo: ChannelInfo{IsMultiKey: true}}
	require.NoError(t, edited.UpdateWithKey(func(stored *Channel) error {
		assert.Equal(t, "key-refreshed", stored.Key)
		stored.Key += "\nkey-appended"
		return nil
	}))
	stored, err = GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "key-refreshed\nkey-appended", stored.Key)
	assert.Equal(t, 2, stored.ChannelInfo.MultiKeySize)
	assert.Equal(t, "replacement", stored.Name)
	abilities = nil
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).Find(&abilities).Error)
	require.Len(t, abilities, 1)
	assert.Equal(t, "model-replacement", abilities[0].Model)
}
