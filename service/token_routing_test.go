package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func tokenRoutingFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_SERVICE_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL fixture")
	}
	require.Equal(t, "postgres", model.DB.Dialector.Name())
	savedGroups, savedRatios, savedAuto := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString(), setting.AutoGroups2JsonString()
	savedLimit, savedRetries := setting.GetMaxTokenAutoGroups(), common.RetryTimes
	require.True(t, perf_metrics_setting.GetSetting().Enabled)
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(savedGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedRatios))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(savedAuto))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(strconv.Itoa(savedLimit)))
		common.RetryTimes = savedRetries
		require.NoError(t, model.DB.Where("channel_id BETWEEN ? AND ?", 94001, 94099).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Where("id BETWEEN ? AND ?", 94001, 94099).Delete(&model.Channel{}).Error)
		require.NoError(t, model.DB.Where("channel_id BETWEEN ? AND ?", 94001, 94099).Delete(&model.TokenRoutingMetric{}).Error)
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"route-a":"A","route-b":"B","route-c":"C","auto":"Auto"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"route-a":2,"route-b":1,"route-c":3,"private":0.01}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("8"))
	require.NoError(t, model.DB.AutoMigrate(&model.TokenRoutingMetric{}))
	common.RetryTimes = 0
}

func tokenRoutingContext(smart, once bool) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "route-a")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"route-a", "route-b", "route-c"})
	common.SetContextKey(c, constant.ContextKeyTokenSmartRouting, smart)
	common.SetContextKey(c, constant.ContextKeyTokenNoRetryWithinGroup, once)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "routing-fixture")
	return c
}

func tokenRoutingChannel(t *testing.T, id int, group string, priority int64) *model.Channel {
	t.Helper()
	weight := uint(1)
	ch := &model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Name: "synthetic-routing", Key: "synthetic-not-a-real-key", Status: common.ChannelStatusEnabled, Group: group, Models: "routing-fixture", Priority: &priority, Weight: &weight}
	require.NoError(t, model.DB.Create(ch).Error)
	require.NoError(t, model.DB.Create(&model.Ability{Group: group, Model: "routing-fixture", ChannelId: id, Enabled: true, Priority: &priority, Weight: weight}).Error)
	return ch
}

func TestTokenRoutingPostgresManualOneAttemptPerGroup(t *testing.T) {
	tokenRoutingFixture(t)
	tokenRoutingChannel(t, 94001, "route-a", 10)
	tokenRoutingChannel(t, 94002, "route-a", 0)
	tokenRoutingChannel(t, 94003, "route-b", 0)
	tokenRoutingChannel(t, 94004, "route-c", 0)
	c := tokenRoutingContext(false, true)
	// The explicit policy is independent of the old cross-group switch and
	// per-group retry budget. Manual order is not re-sorted by price.
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, false)
	require.Equal(t, 2, RelayRetryBudget(c))
	n := 0
	p := &RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "routing-fixture", Retry: &n, RequestPath: c.Request.URL.Path}
	for i, id := range []int{94001, 94003, 94004} {
		ch, g, err := CacheGetRandomSatisfiedChannel(p)
		require.NoError(t, err)
		require.NotNil(t, ch)
		require.Equal(t, id, ch.Id)
		require.Equal(t, []string{"route-a", "route-b", "route-c"}[i], g)
		AppendUsedChannel(c, ch.Id)
		require.Equal(t, i < 2, TokenRoutingRetryAvailable(c))
		p.IncreaseRetry()
	}
	ch, _, err := CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.Nil(t, ch, "must never select lower priority channel in an attempted group")
}

func TestTokenRoutingPostgresSingleGroupAndPinnedOriginStop(t *testing.T) {
	tokenRoutingFixture(t)
	for _, pin := range []bool{false, true} {
		c := tokenRoutingContext(false, true)
		if pin {
			GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 94001, Source: dto.PinSourceOriginTask, Rank: dto.PinRankOriginTask, RetryMode: dto.PinRetrySameChannel})
		} else {
			common.SetContextKey(c, constant.ContextKeyTokenGroup, "route-a")
			require.Zero(t, RelayRetryBudget(c))
		}
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "route-a")
		AppendUsedChannel(c, 94001)
		require.False(t, TokenRoutingRetryAvailable(c))
		err := types.NewError(errors.New("no channel key"), types.ErrorCodeChannelNoAvailableKey)
		require.Equal(t, "stop", DecideRelayRetry(c, err, 8).Action)
	}
	// Legacy keys keep same-channel pin behavior and global retry budget.
	c := tokenRoutingContext(false, false)
	common.RetryTimes = 3
	require.Equal(t, 3, RelayRetryBudget(c))
	require.True(t, TokenRoutingRetryAvailable(c))
}

func TestTokenRoutingPostgresSmartStrategiesAndLiveScope(t *testing.T) {
	tokenRoutingFixture(t)
	a := tokenRoutingChannel(t, 94001, "route-a", 0)
	b := tokenRoutingChannel(t, 94002, "route-b", 0)
	cc := tokenRoutingChannel(t, 94003, "route-c", 0)
	tokenRoutingChannel(t, 94004, "private", 999)
	candidates := []model.RoutingCandidate{{Channel: a, Group: "route-a"}, {Channel: b, Group: "route-b"}, {Channel: cc, Group: "route-c"}}
	metrics := []model.TokenRoutingMetric{{ChannelID: a.Id, Group: "route-a", RequestCount: 100, SuccessCount: 100, TotalLatencyMs: 100000}, {ChannelID: b.Id, Group: "route-b", RequestCount: 100, SuccessCount: 90, TotalLatencyMs: 45000}, {ChannelID: cc.Id, Group: "route-c", RequestCount: 100, SuccessCount: 80, TotalLatencyMs: 8000}}
	for strategy, id := range map[string]int{"auto": 94001, "success_rate": 94001, "price": 94002, "speed": 94003} {
		c := tokenRoutingContext(true, true)
		common.SetContextKey(c, constant.ContextKeyTokenRoutingStrategy, strategy)
		require.Equal(t, id, rankTokenRoutes(c, candidates, metrics).Channel.Id, strategy)
	}
	c := tokenRoutingContext(true, true)
	common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"private"}) // dormant draft must be ignored
	common.SetContextKey(c, constant.ContextKeyTokenRoutingStrategy, "price")
	n := 0
	p := &RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "routing-fixture", Retry: &n}
	ch, g, err := CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.Equal(t, 94002, ch.Id)
	require.Equal(t, "route-b", g)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"route-c":"C"}`))
	ch, g, err = CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.Equal(t, 94001, ch.Id)
	require.Equal(t, "route-a", g, "user's own group remains authorized but revoked route-b is excluded")
	require.NoError(t, model.DB.Model(b).Update("status", common.ChannelStatusManuallyDisabled).Error)
	current, err := model.GetTokenRoutingCandidates(context.Background(), []string{"route-b"}, "routing-fixture", nil)
	require.NoError(t, err)
	require.Empty(t, current)
}

func TestTokenRoutingPostgresNoReplayAfterOutputCancellationOrHandledError(t *testing.T) {
	tokenRoutingFixture(t)
	for _, which := range []string{"output", "cancel", "stream_handled", "skip_retry"} {
		t.Run(which, func(t *testing.T) {
			c := tokenRoutingContext(true, true)
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "route-a")
			AppendUsedChannel(c, 94001)
			err := types.NewError(errors.New("channel error"), types.ErrorCodeChannelNoAvailableKey)
			switch which {
			case "output":
				c.Writer.WriteHeaderNow()
			case "cancel":
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
			case "stream_handled":
				common.SetContextKey(c, constant.ContextKeyResponsesStreamErrorHandled, true)
			case "skip_retry":
				err = types.NewError(errors.New("unknown delivery"), types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
			}
			require.Equal(t, "stop", DecideRelayRetry(c, err, 8).Action)
		})
	}
}

func TestTokenRoutingPostgresMetricsAtomicDeduplicatedAndAdvisory(t *testing.T) {
	tokenRoutingFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			errs <- model.RecordTokenRoutingMetric(context.Background(), 94001, "routing-fixture", "route-a", true, 100*time.Millisecond)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	c := tokenRoutingContext(true, true)
	RequestPolicy(c).BeginAttempt(&model.Channel{Id: 94001}, "route-a")
	MarkRequestPolicySuccess(c, nil)
	MarkRequestPolicySuccess(c, nil)
	stats, err := model.GetTokenRoutingMetrics(context.Background(), "routing-fixture", []string{"route-a"})
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.EqualValues(t, 21, stats[0].RequestCount)
	require.EqualValues(t, 21, stats[0].SuccessCount)
	RequestPolicy(c).BeginAttempt(&model.Channel{Id: 94001}, "route-a")
	RecordPolicyFailure(c, 94001, types.NewError(errors.New("quota"), types.ErrorCodeInsufficientUserQuota), PolicyDecision{Action: "stop"})
	stats, err = model.GetTokenRoutingMetrics(context.Background(), "routing-fixture", []string{"route-a"})
	require.NoError(t, err)
	require.EqualValues(t, 21, stats[0].RequestCount, "local business rejection is not channel health")
	require.NoError(t, model.DB.Model(&model.TokenRoutingMetric{}).Where("channel_id = ?", 94001).Update("bucket_ts", time.Now().Add(-8*24*time.Hour).Unix()).Error)
	require.NoError(t, model.DeleteTokenRoutingMetricsBefore(time.Now().Add(-7*24*time.Hour).Unix()))
	var count int64
	require.NoError(t, model.DB.Model(&model.TokenRoutingMetric{}).Where("channel_id = ?", 94001).Count(&count).Error)
	require.Zero(t, count)
}

func TestTokenRoutingPostgresOnlyOneEligibleGroupPreservesFailure(t *testing.T) {
	tokenRoutingFixture(t)
	tokenRoutingChannel(t, 94001, "route-a", 0)
	c := tokenRoutingContext(false, true)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "route-a")
	AppendUsedChannel(c, 94001)
	require.False(t, TokenRoutingRetryAvailable(c), "other authorized groups without a matching channel are not fallbacks")
	apiErr := types.NewOpenAIError(errors.New("original upstream overload"), types.ErrorCodeBadResponseStatusCode, 503)
	require.Equal(t, "stop", DecideRelayRetry(c, apiErr, 8).Action)
}

func TestTokenRoutingPostgresBusinessRefusalDoesNotImproveHealth(t *testing.T) {
	tokenRoutingFixture(t)
	c := tokenRoutingContext(true, true)
	RequestPolicy(c).BeginAttempt(&model.Channel{Id: 94001}, "route-a")
	MarkRequestPolicySuccess(c, nil, &relaycommon.RelayInfo{PerformanceBusinessRejection: true})
	stats, err := model.GetTokenRoutingMetrics(context.Background(), "routing-fixture", []string{"route-a"})
	require.NoError(t, err)
	require.Empty(t, stats)
}
