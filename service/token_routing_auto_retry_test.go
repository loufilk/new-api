package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestTokenRoutingPostgresAutoCrossGroupRetryWithZeroLocalRetries(t *testing.T) {
	tokenRoutingFixture(t)
	savedCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = savedCache })
	tokenRoutingChannel(t, 94001, "route-a", 0)
	tokenRoutingChannel(t, 94002, "route-b", 0)
	tokenRoutingChannel(t, 94003, "route-b", 10)
	c := tokenRoutingContext(false, false)
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, true)

	// The distributor selects before the controller creates its retry state.
	first, group, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "routing-fixture"})
	require.NoError(t, err)
	require.Equal(t, 94001, first.Id)
	require.Equal(t, "route-a", group)
	AppendUsedChannel(c, first.Id)
	retry := &RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "routing-fixture"}
	budget := RelayRetryBudget(c)
	upstreamErr := types.NewOpenAIError(errors.New("upstream overload"), types.ErrorCodeBadResponseStatusCode, 503)
	require.Equal(t, "retry", DecideRelayRetry(c, upstreamErr, budget-retry.GetRetry()).Action,
		"zero retries within a group must still allow the explicitly enabled next group")

	retry.IncreaseRetry()
	second, group, err := CacheGetRandomSatisfiedChannel(retry)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, 94003, second.Id, "a new group must begin at its highest priority")
	require.Equal(t, "route-b", group)
	AppendUsedChannel(c, second.Id)
	require.Equal(t, "stop", DecideRelayRetry(c, upstreamErr, budget-retry.GetRetry()).Action,
		"route-c cannot serve this model; preserve the last upstream error instead of selecting an empty group")
}

func TestTokenRoutingPostgresAutoRetryRespectsDisabledCrossGroupSwitch(t *testing.T) {
	tokenRoutingFixture(t)
	savedCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = savedCache })
	common.RetryTimes = 1
	tokenRoutingChannel(t, 94001, "route-a", 0)
	tokenRoutingChannel(t, 94002, "route-b", 0)
	c := tokenRoutingContext(false, false)
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, false)
	retry := &RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "routing-fixture"}
	first, group, err := CacheGetRandomSatisfiedChannel(retry)
	require.NoError(t, err)
	require.Equal(t, 94001, first.Id)
	require.Equal(t, "route-a", group)
	AppendUsedChannel(c, first.Id)

	// A channel can be disabled after a retryable failure and before selection.
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", first.Id).Update("enabled", false).Error)
	retry.IncreaseRetry()
	next, _, err := CacheGetRandomSatisfiedChannel(retry)
	require.NoError(t, err)
	require.Nil(t, next, "a disabled cross-group switch must not silently route to route-b")

	initial := tokenRoutingContext(false, false)
	available, group, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: initial, TokenGroup: "auto", ModelName: "routing-fixture"})
	require.NoError(t, err)
	require.Equal(t, 94002, available.Id)
	require.Equal(t, "route-b", group, "initial selection can skip a group that has no matching channel")
}

func TestTokenRoutingPostgresAutoRetryStopsAfterFinalGroup(t *testing.T) {
	tokenRoutingFixture(t)
	savedCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = savedCache })
	common.RetryTimes = 1
	tokenRoutingChannel(t, 94001, "route-a", 0)
	tokenRoutingChannel(t, 94002, "route-b", 0)
	c := tokenRoutingContext(false, false)
	common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"route-a", "route-b"})
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, true)
	retry := &RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "routing-fixture"}
	budget := RelayRetryBudget(c)
	upstreamErr := types.NewOpenAIError(errors.New("upstream overload"), types.ErrorCodeBadResponseStatusCode, 503)
	for attempt, expected := range []int{94001, 94001, 94002, 94002} {
		channel, _, err := CacheGetRandomSatisfiedChannel(retry)
		require.NoError(t, err)
		require.NotNil(t, channel)
		require.Equal(t, expected, channel.Id)
		AppendUsedChannel(c, channel.Id)
		decision := DecideRelayRetry(c, upstreamErr, budget-retry.GetRetry())
		if attempt == 3 {
			require.Equal(t, "stop", decision.Action, "exhausted groups must retain the upstream failure")
		} else {
			require.Equal(t, "retry", decision.Action)
		}
		retry.IncreaseRetry()
	}
}
