package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// A fixed route holds the cached channel; each smart route gets a separate DB
// snapshot. Both must advance the same per-channel cursor, even across refresh.
func TestTokenRoutingPostgresSmartPollingRotatesAcrossRequests(t *testing.T) {
	tokenRoutingFixture(t)
	ch := tokenRoutingChannel(t, 94001, "route-a", 10)
	info := model.ChannelInfo{IsMultiKey: true, MultiKeySize: 4, MultiKeyMode: constant.MultiKeyModePolling, MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}}
	require.NoError(t, model.DB.Model(ch).Updates(map[string]any{"key": "fixture-a\nfixture-b\nfixture-c\n ", "channel_info": info}).Error)
	old := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = old })
	require.NoError(t, model.RefreshChannelCache(context.Background()))
	for i, want := range []int{0, 2, 0, 2, 0, 2} {
		if i == 3 {
			require.NoError(t, model.RefreshChannelCache(context.Background()))
		}
		candidate, err := model.CacheGetChannel(ch.Id)
		require.NoError(t, err)
		if i%3 != 0 {
			c := tokenRoutingContext(true, false)
			candidate, _, err = selectTokenRoutingChannel(&RetryParam{Ctx: c, ModelName: "routing-fixture", TokenGroup: "auto", Retry: common.GetPointer(0)})
			require.NoError(t, err)
			require.NotNil(t, candidate)
		}
		_, index, apiErr := candidate.GetNextEnabledKey()
		require.Nil(t, apiErr)
		require.Equal(t, want, index, "request %d", i)
	}
}

func TestTokenRoutingPostgresSkipsDrainedMultiKeyChannel(t *testing.T) {
	tokenRoutingFixture(t)
	drained := tokenRoutingChannel(t, 94001, "route-a", 100)
	tokenRoutingChannel(t, 94002, "route-a", 1)
	info := model.ChannelInfo{
		IsMultiKey: true, MultiKeySize: 1, MultiKeyMode: constant.MultiKeyModePolling,
		MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
	}
	require.NoError(t, model.DB.Model(drained).Update("channel_info", info).Error)
	c := tokenRoutingContext(true, false)
	channel, group, err := selectTokenRoutingChannel(&RetryParam{
		Ctx: c, ModelName: "routing-fixture", TokenGroup: "auto", Retry: common.GetPointer(0),
	})
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 94002, channel.Id)
	require.Equal(t, "route-a", group)
}
