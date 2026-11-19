package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestTokenRoutingPostgresOriginTaskUsesCurrentKeyAndConcreteGroup(t *testing.T) {
	tokenRoutingFixture(t)
	savedCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = savedCache })
	ch := tokenRoutingChannel(t, 94001, "route-b", 0)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "route-a", Model: "routing-fixture", ChannelId: ch.Id, Enabled: true}).Error)
	for _, tc := range []struct {
		name, origin, scope, wantGroup string
		allowed                        bool
	}{
		{"same upstream billed in original group", "route-b", "auto", "route-b", true},
		{"historical unknown group resolves current", "", "route-a", "route-a", true},
		{"different concrete key", "route-b", "route-a", "", true},
		{"model unavailable to current key", "route-b", "auto", "", false},
		{"current scope no longer has ability", "", "route-c", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tokenRoutingContext(false, false)
			common.SetContextKey(c, constant.ContextKeyTokenGroup, tc.scope)
			common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
			common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"routing-fixture": tc.allowed})
			common.SetContextKey(c, constant.ContextKeyOriginTasks, []*model.Task{{Group: tc.origin, ChannelId: ch.Id}})
			GetChannelConstraints(c).AddPin(dto.ChannelPin{Source: dto.PinSourceOriginTask, ChannelId: ch.Id, Rank: dto.PinRankOriginTask, RetryMode: dto.PinRetrySameChannel})
			got, group, err := SelectChannelForRequest(c, "routing-fixture", &RetryParam{Ctx: c, ModelName: "routing-fixture", TokenGroup: tc.scope})
			if tc.wantGroup == "" {
				require.Nil(t, got)
				require.NotNil(t, err)
				require.Equal(t, http.StatusForbidden, err.StatusCode)
				return
			}
			require.Nil(t, err)
			require.Equal(t, ch.Id, got.Id)
			require.Equal(t, tc.wantGroup, group)
			require.Equal(t, group, common.GetContextKeyString(c, constant.ContextKeyAutoGroup))
		})
	}
}

func TestTokenRoutingPostgresLegacyZeroBudgetAffinityMovesForward(t *testing.T) {
	tokenRoutingFixture(t)
	savedCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = savedCache })
	first := tokenRoutingChannel(t, 94001, "route-a", 0)
	affinity := operation_setting.GetChannelAffinitySetting()
	saved := *affinity
	t.Cleanup(func() { *affinity = saved })
	rule := operation_setting.ChannelAffinityRule{Name: "routing-test-zero-budget", ModelRegex: []string{"^routing-fixture$"}, KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}}, IncludeRuleName: true, SessionMode: "prefer"}
	affinity.Enabled = true
	affinity.Rules = []operation_setting.ChannelAffinityRule{rule}
	cache := getChannelAffinityCache()
	key := buildChannelAffinityCacheKeySuffix(rule, "routing-fixture", "auto", "synthetic-session")
	require.NoError(t, cache.SetWithTTL(key, first.Id, time.Minute))
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{key}) })
	for _, next := range []bool{false, true} {
		if next {
			tokenRoutingChannel(t, 94002, "route-b", 0)
		}
		c := tokenRoutingContext(false, false)
		c.Request.Header.Set("X-Session", "synthetic-session")
		common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, true)
		p := &RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "routing-fixture"}
		ch, g, err := SelectChannelForRequest(c, p.ModelName, p)
		require.Nil(t, err)
		require.Equal(t, first.Id, ch.Id)
		require.Equal(t, "route-a", g)
		require.Equal(t, 1, c.GetInt(ginKeyChannelAffinityRetryOffset), "fixture must hit the affinity path")
		AppendUsedChannel(c, ch.Id)
		require.Equal(t, next, TokenRoutingRetryAvailable(c), "a transition must not count the exhausted affinity group")
		if next {
			// The WS selector can receive a zero per-group counter again. No
			// attempted route may regain affinity merely because the counter reset.
			ch, g, err = SelectChannelForRequest(c, p.ModelName, p)
			require.Nil(t, err)
			require.Equal(t, "route-b", g)
			require.Equal(t, 94002, ch.Id)
		}
	}
}

func TestTokenRoutingSmartSameChannelRetryNeedsNoUnrelatedCandidate(t *testing.T) {
	c := tokenRoutingContext(true, false)
	GetChannelConstraints(c).AddPin(dto.ChannelPin{Source: dto.PinSourceOriginTask, ChannelId: 42, Rank: dto.PinRankOriginTask, RetryMode: dto.PinRetrySameChannel})
	AppendUsedChannel(c, 42)
	require.True(t, TokenRoutingRetryAvailable(c))
	common.SetContextKey(c, constant.ContextKeyTokenNoRetryWithinGroup, true)
	require.False(t, TokenRoutingRetryAvailable(c))
	common.SetContextKey(c, constant.ContextKeyTokenNoRetryWithinGroup, false)
	c.Writer.WriteHeaderNow()
	require.False(t, TokenRoutingRetryAvailable(c))
}
