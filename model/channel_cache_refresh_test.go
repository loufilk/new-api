package model

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func channelRefreshFixture(t *testing.T) *Channel {
	t.Helper()
	oldDB, oldMemory := DB, common.MemoryCacheEnabled
	oldRoutes, oldChannels, oldConfigs := group2model2channels, channelsIDM, channel2advancedCustomConfig
	oldRevision, oldTime, oldFailures := channelCacheRevision, channelCacheLastRefresh.Load(), channelCacheRefreshFailures.Load()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cache.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB, common.MemoryCacheEnabled = db, true
	channelCacheLastRefresh.Store(0)
	t.Cleanup(func() {
		DB, common.MemoryCacheEnabled = oldDB, oldMemory
		channelSyncLock.Lock()
		group2model2channels, channelsIDM, channel2advancedCustomConfig = oldRoutes, oldChannels, oldConfigs
		channelCacheRevision = oldRevision
		channelSyncLock.Unlock()
		channelCacheLastRefresh.Store(oldTime)
		channelCacheRefreshFailures.Store(oldFailures)
		require.NoError(t, sqlDB.Close())
		InvalidatePricingCache()
		rebuildTaskAliasView()
	})
	channel := &Channel{Id: 945001, Name: "before", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Group: "default", Models: "test-model", Key: "key-a\nkey-b"}
	channel.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModePolling}
	require.NoError(t, channel.Insert())
	require.NoError(t, RefreshChannelCache(context.Background()))
	return channel
}

func TestChannelRefreshKeepsPreviousSnapshotOnEitherQueryFailure(t *testing.T) {
	for _, failedTable := range []string{"channels", "abilities"} {
		t.Run(failedTable, func(t *testing.T) {
			channel := channelRefreshFixture(t)
			before, err := CacheGetChannel(channel.Id)
			require.NoError(t, err)
			lastSuccess, oldFailures := ChannelCacheHealth()
			require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("name", "after").Error)
			const hook = "test:channel-refresh-query-failure"
			require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
				if tx.Statement.Table == failedTable {
					tx.AddError(errors.New("injected query failure"))
				}
			}))
			t.Cleanup(func() { _ = DB.Callback().Query().Remove(hook) })
			require.ErrorContains(t, RefreshChannelCache(context.Background()), "injected query failure")
			cached, err := CacheGetChannel(channel.Id)
			require.NoError(t, err)
			assert.Same(t, before, cached)
			assert.Equal(t, "before", cached.Name)
			afterSuccess, failures := ChannelCacheHealth()
			assert.Equal(t, lastSuccess, afterSuccess)
			assert.Equal(t, oldFailures+1, failures)
			require.NoError(t, DB.Callback().Query().Remove(hook))
			require.NoError(t, RefreshChannelCache(context.Background()))
			cached, err = CacheGetChannel(channel.Id)
			require.NoError(t, err)
			assert.Equal(t, "after", cached.Name)
		})
	}
}

func TestChannelRefreshRejectsInconsistentGroupsWithoutReplacingRoutes(t *testing.T) {
	channel := channelRefreshFixture(t)
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error)
	require.ErrorContains(t, RefreshChannelCache(context.Background()), "absent from abilities")
	selected, err := GetRandomSatisfiedChannel("default", "test-model", 0, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, channel.Id, selected.Id)
}

func TestChannelRefreshDoesNotOverwriteConcurrentDisable(t *testing.T) {
	channel := channelRefreshFixture(t)
	const hook = "test:channel-refresh-disable"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "abilities" {
			CacheUpdateChannelStatus(channel.Id, common.ChannelStatusAutoDisabled)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(hook) })
	require.ErrorContains(t, RefreshChannelCache(context.Background()), "state changed")
	selected, err := GetRandomSatisfiedChannel("default", "test-model", 0, nil)
	require.NoError(t, err)
	assert.Nil(t, selected, "a snapshot read before disable must not resurrect the channel")
}

func TestChannelRefreshRetainsPollingCursorAndBoundsStaleRouting(t *testing.T) {
	channel := channelRefreshFixture(t)
	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	key, _, apiErr := cached.GetNextEnabledKey()
	require.Nil(t, apiErr)
	assert.Equal(t, "key-a", key)
	require.NoError(t, RefreshChannelCache(context.Background()))
	cached, err = CacheGetChannel(channel.Id)
	require.NoError(t, err)
	key, _, apiErr = cached.GetNextEnabledKey()
	require.Nil(t, apiErr)
	assert.Equal(t, "key-b", key)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusAutoDisabled).Error)
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", false).Error)
	t.Setenv("CHANNEL_CACHE_MAX_STALE_SECONDS", "30")
	channelCacheLastRefresh.Store(time.Now().Add(-time.Minute).UnixNano())
	selected, err := GetRandomSatisfiedChannel("default", "test-model", 0, nil)
	require.ErrorIs(t, err, ErrChannelCacheStale)
	assert.Nil(t, selected, "an expired snapshot must not keep revoked routes usable")
	cached, err = CacheGetChannel(channel.Id)
	require.ErrorIs(t, err, ErrChannelCacheStale)
	assert.Nil(t, cached)
	info, err := CacheGetChannelInfo(channel.Id)
	require.ErrorIs(t, err, ErrChannelCacheStale)
	assert.Nil(t, info)
}

func TestChannelRefreshCancelledContextDoesNotPublish(t *testing.T) {
	channel := channelRefreshFixture(t)
	before, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, RefreshChannelCache(ctx))
	after, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Same(t, before, after)
}
