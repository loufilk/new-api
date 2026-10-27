package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupModelSupportFailsClosedWithoutDatabaseOrFreshCache(t *testing.T) {
	oldDB, oldMemory := DB, common.MemoryCacheEnabled
	oldRefresh := channelCacheLastRefresh.Load()
	t.Cleanup(func() {
		DB, common.MemoryCacheEnabled = oldDB, oldMemory
		channelCacheLastRefresh.Store(oldRefresh)
	})
	DB, common.MemoryCacheEnabled = nil, false
	supported, err := HasConfiguredModelForGroups(context.Background(), []string{"selected"}, "requested-model")
	require.Error(t, err)
	assert.False(t, supported)

	common.MemoryCacheEnabled = true
	t.Setenv("CHANNEL_CACHE_MAX_STALE_SECONDS", "30")
	channelCacheLastRefresh.Store(time.Now().Add(-time.Minute).UnixNano())
	_, err = HasConfiguredModelForGroups(context.Background(), []string{"selected"}, "requested-model")
	require.ErrorIs(t, err, ErrChannelCacheStale)
}
