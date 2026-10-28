package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func configureRealtimeConsistency(t *testing.T, s *BillingSession) {
	t.Helper()
	old := ratio_setting.ModelRatio2JSONString()
	ratios := ratio_setting.GetModelRatioCopy()
	ratios[s.relayInfo.GetBillingModelName()] = 1
	data, err := common.Marshal(ratios)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(data)))
	oldLogs := common.LogConsumeEnabled
	common.LogConsumeEnabled = false // This fixture asserts authority, not optional display DBs.
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(old))
		common.LogConsumeEnabled = oldLogs
	})
	// This owned child observes financial callbacks only. Its real user starts
	// at 1000; disable optional low-quota notifications, whose rate limiter
	// intentionally owns a permanent cleanup goroutine unrelated to billing.
	s.relayInfo.UserQuota = 1000
	s.relayInfo.UserSetting.QuotaWarningThreshold = -1
	s.relayInfo.UserSetting.QuotaWarningEnabled = boolPtr(false)
	s.relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1
	s.relayInfo.StartTime = time.Now()
	s.relayInfo.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 93204}
	require.NoError(t, model.DB.Create(&model.Channel{Id: 93204, Type: 1, Name: "realtime-consistency"}).Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Delete(&model.Channel{}, 93204).Error) })
}

func realtimeTextUsage(tokens int) *dto.RealtimeUsage {
	return &dto.RealtimeUsage{TotalTokens: tokens, InputTokens: tokens, InputTokenDetails: dto.InputTokenDetails{TextTokens: tokens}}
}

func requireRealtimeConsistency(t *testing.T, s *BillingSession, source string, actual, count int) {
	t.Helper()
	wallet := 1000 - actual
	if source == BillingSourceSubscription {
		wallet = 1000
		var sub model.UserSubscription
		require.NoError(t, model.DB.First(&sub, 93203).Error)
		require.EqualValues(t, 500+actual, sub.AmountUsed)
	}
	assertSettlementBalances(t, wallet, 1000-actual)
	var user model.User
	var token model.Token
	var channel model.Channel
	require.NoError(t, model.DB.First(&user, 93201).Error)
	require.NoError(t, model.DB.First(&token, 93202).Error)
	require.NoError(t, model.DB.First(&channel, 93204).Error)
	require.Equal(t, actual, user.UsedQuota)
	require.Equal(t, count, user.RequestCount)
	require.Equal(t, actual, token.UsedQuota)
	require.EqualValues(t, actual, channel.UsedQuota)
	owner := lifecycleOwner(t, s.relayInfo.RequestId)
	require.Equal(t, model.BillingSettled, owner.State)
	require.Equal(t, actual, owner.ActualQuota)
}

func TestRealtimeBillingLifecycle(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			for _, scenario := range []string{"aggregate", "segment_rollback", "segment_lost_ack", "final_lost_ack", "partial_refund", "zero_usage", "zero_price", "fixed_price", "missing_total"} {
				t.Run(scenario, func(t *testing.T) {
					s, _ := settlementDeadlineSession(t, "realtime-lifecycle", true, source)
					configureRealtimeConsistency(t, s)
					c := testGinContext(t)
					usage := realtimeTextUsage(20)
					actual, count := 20, 1
					switch scenario {
					case "zero_usage":
						usage = realtimeTextUsage(0)
						actual, count = 0, 0
					case "zero_price":
						s.relayInfo.PriceData.GroupRatioInfo.GroupRatio = 0
						actual = 0
					case "fixed_price":
						s.relayInfo.PriceData.UsePrice = true
						s.relayInfo.PriceData.ModelPrice = 50 / common.QuotaPerUnit
						actual = 50
					case "missing_total":
						usage.TotalTokens = 0
					}
					if scenario == "segment_rollback" {
						_, hits, remove := failLifecycleWrite(t, &model.Token{}, false)
						require.Error(t, PreWssConsumeQuota(c, s.relayInfo, usage))
						require.EqualValues(t, 1, hits.Load())
						remove()
						requireLifecycleAccounting(t, source, 100)
					} else if scenario == "segment_lost_ack" {
						flag := terminalCommitFault(t, 2)
						require.ErrorContains(t, PreWssConsumeQuota(c, s.relayInfo, usage), "synthetic committed ACK lost")
						require.Zero(t, flag.Load())
						requireLifecycleAccounting(t, source, 120)
						require.NoError(t, PreWssConsumeQuota(c, s.relayInfo, usage), "same unresolved segment is neither re-observed nor re-debited")
						requireLifecycleAccounting(t, source, 120)
						require.EqualValues(t, 1, s.relayInfo.WssPostConsumeSequence)
					} else {
						require.NoError(t, PreWssConsumeQuota(c, s.relayInfo, usage))
					}
					if scenario == "aggregate" {
						require.NoError(t, PreWssConsumeQuota(c, s.relayInfo, realtimeTextUsage(30)))
						usage, actual = realtimeTextUsage(50), 50
					}
					if scenario == "partial_refund" {
						s.Refund(c)
					} else {
						if scenario == "final_lost_ack" {
							terminalCommitFault(t, 2, 1) // Skip the frozen-intent COMMIT; lose the money ACK.
						}
						PostWssConsumeQuota(c, s.relayInfo, s.relayInfo.OriginModelName, usage, nil)
					}
					requireRealtimeConsistency(t, s, source, actual, count)
					// Reload of the original authority, not a second compensating fee.
					s.Refund(c)
					PostWssConsumeQuota(c, s.relayInfo, s.relayInfo.OriginModelName, usage, nil)
					requireRealtimeConsistency(t, s, source, actual, count)
					waitRefundCallback(t, s)
				})
			}
		})
	}
}

func TestRealtimeBillingTrustedObservedOnly(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	s, _ := settlementDeadlineSession(t, "realtime-trusted-baseline", true, BillingSourceWallet)
	require.NoError(t, s.Settle(0))
	c, info := lifecycleRequest(t, s, "realtime-trusted-zero")
	operation_setting.GetQuotaSetting().TrustQuotaUSD = .000001
	info.UserQuota = 1000
	s, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, apiErr)
	info.Billing = s
	configureRealtimeConsistency(t, s)
	require.True(t, s.trusted)
	require.Zero(t, s.GetPreConsumedQuota())
	require.NoError(t, PreWssConsumeQuota(c, info, realtimeTextUsage(20)))
	require.True(t, s.trusted, "observed consumption never changes the prediction trust policy")
	require.Equal(t, 20, s.GetPreConsumedQuota())
	PostWssConsumeQuota(c, info, info.OriginModelName, realtimeTextUsage(20), nil)
	requireRealtimeConsistency(t, s, BillingSourceWallet, 20, 1)
	waitRefundCallback(t, s)
}

func TestSubscriptionReservationOldPeriodSnapshotUnknown(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, phase := range []string{"fresh_replay", "reserve_lost_ack"} {
		t.Run(phase, func(t *testing.T) {
			s, _ := settlementDeadlineSession(t, "period-snapshot", true, BillingSourceSubscription)
			if phase == "reserve_lost_ack" {
				flag := terminalCommitFault(t, 2)
				require.ErrorContains(t, s.Reserve(200), "synthetic committed ACK lost")
				require.Zero(t, flag.Load())
			}
			var original model.UserSubscription
			require.NoError(t, model.DB.First(&original, 93203).Error)
			_, err := model.AdminResetUserSubscriptionsByPlan(s.relayInfo.UserId, original.PlanId, false)
			require.NoError(t, err)
			// Real usage in the NEW period must not become this old hold's snapshot.
			require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", original.Id).Update("amount_used", 17).Error)
			if phase == "fresh_replay" {
				c, info := lifecycleRequest(t, s, s.relayInfo.RequestId)
				recovered, apiErr := NewBillingSession(c, info, 100)
				require.Nil(t, apiErr)
				s = recovered
			} else {
				require.NoError(t, s.Reserve(200))
			}
			require.True(t, s.relayInfo.SubscriptionSnapshotUnknown)
			require.Zero(t, s.relayInfo.SubscriptionAmountTotal)
			require.Zero(t, s.relayInfo.SubscriptionAmountUsedAfterPreConsume)
			require.Equal(t, original.QuotaPeriod, s.relayInfo.SubscriptionQuotaPeriod)
			other := model.NewLogOther()
			appendBillingInfo(s.relayInfo, other)
			require.Equal(t, true, other.Snapshot()["subscription_snapshot_unknown"])
			require.NotContains(t, other.Snapshot(), "subscription_total")
			require.NotContains(t, other.Snapshot(), "subscription_remain")
			require.NoError(t, s.Settle(20))
			var current model.UserSubscription
			require.NoError(t, model.DB.First(&current, original.Id).Error)
			require.EqualValues(t, 17, current.AmountUsed)
			require.Equal(t, original.QuotaPeriod+1, current.QuotaPeriod)
		})
	}
}
