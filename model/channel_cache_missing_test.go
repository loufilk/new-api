package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelCacheMissingHasTypedAbsence(t *testing.T) {
	existing := channelRefreshFixture(t)
	_, err := CacheGetChannel(existing.Id + 1)
	require.ErrorIs(t, err, ErrChannelCacheMiss)
	require.NotErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = CacheGetChannelInfo(existing.Id + 1)
	require.ErrorIs(t, err, ErrChannelCacheMiss)
	require.NotErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestChannelCacheMissIsNotAuthoritativeAbsence(t *testing.T) {
	existing := channelRefreshFixture(t)
	remote := &Channel{Id: existing.Id + 1, Name: "added-on-other-node"}
	require.NoError(t, DB.Create(remote).Error) // no local cache refresh
	_, err := CacheGetChannel(remote.Id)
	require.ErrorIs(t, err, ErrChannelCacheMiss)
	loaded, err := GetChannelById(remote.Id, false)
	require.NoError(t, err)
	require.Equal(t, remote.Id, loaded.Id)
}
