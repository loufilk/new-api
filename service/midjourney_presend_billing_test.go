package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMidjourneyHoldHandoffPostgres(t *testing.T) {
	openTaskPollingSQLFixture(t, "postgres")
	require.NoError(t, model.DB.AutoMigrate(&model.BillingFinalization{}, &model.Midjourney{}, &model.Model{}, &model.Vendor{}, &model.ModelBonusEligibleAudit{}, &model.Ability{}, &model.TopUp{}, &model.ReferralCommission{}, &model.Redemption{}, &model.SubscriptionOrder{}, &model.SubscriptionPlan{}, &model.SystemTask{}, &model.SystemTaskLock{}))
	if dsn := os.Getenv("TEST_REDIS_DSN"); dsn != "" {
		options, err := redis.ParseURL(dsn)
		require.NoError(t, err)
		client := redis.NewClient(options)
		require.NoError(t, client.Ping(t.Context()).Err())
		version, err := client.Info(t.Context(), "server").Result()
		require.NoError(t, err)
		require.Contains(t, version, "redis_version:8.8.0")
		oldClient, oldEnabled := common.RDB, common.RedisEnabled
		common.RDB, common.RedisEnabled = client, true
		t.Cleanup(func() { common.RDB, common.RedisEnabled = oldClient, oldEnabled; require.NoError(t, client.Close()) })
	}
	for _, failure := range []string{"none", "rollback", "lost_ack"} {
		t.Run(failure, func(t *testing.T) {
			truncate(t)
			const userID, tokenID, channelID = 4651, 4652, 4653
			seedBonusUser(t, userID, 100, 200)
			require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{"quota_reward": 50, "quota": 350}).Error)
			seedToken(t, tokenID, userID, "synthetic-mj-held-token", 1000)
			seedChannel(t, channelID)
			seedBonusEligibleModel(t, "mj_imagine", boolPtr(true))
			info := &relaycommon.RelayInfo{UserId: userID, TokenId: tokenID, TokenKey: "synthetic-mj-held-token", RequestId: "mj-handoff-" + failure, OriginModelName: "mj_imagine", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channelID}}
			info.UserSetting.QuotaWarningEnabled = boolPtr(false)
			require.NoError(t, ReserveMidjourneyBilling(testGinContext(t), info, 180))
			assertUserBalances(t, userID, 0, 170, 0)
			assert.Equal(t, 820, getTokenRemainQuota(t, tokenID))
			owner := lifecycleOwner(t, info.RequestId)
			require.Equal(t, model.BillingHeld, owner.State)
			require.Equal(t, 180, owner.SourceHeld)
			require.Equal(t, 180, owner.TokenHeld)
			task := &model.Midjourney{UserId: userID, MjId: "accepted-" + failure, ChannelId: channelID, Status: "IN_PROGRESS", Progress: "20%"}
			prepared, err := PrepareMidjourneyTaskBilling(info, task, 180, true)
			require.NoError(t, err)
			require.True(t, prepared)
			var stop func()
			if failure == "rollback" {
				const hook = "test:mj-handoff-rollback"
				require.NoError(t, model.DB.Callback().Create().After("gorm:create").Register(hook, func(tx *gorm.DB) {
					if tx.Statement.Table == "midjourneys" {
						tx.AddError(errors.New("synthetic insertion rollback"))
					}
				}))
				stop = func() { require.NoError(t, model.DB.Callback().Create().Remove(hook)) }
			} else if failure == "lost_ack" {
				initialReservationCommitLoss(t, 1)
			}
			applied, err := PersistMidjourneyBilling(info, task)
			if failure != "none" {
				require.Error(t, err)
				require.False(t, applied)
				info.Billing.Refund(testGinContext(t))
				waitRefundCallback(t, info.Billing.(*BillingSession))
				assertUserBalances(t, userID, 0, 170, 0)
				assert.Equal(t, 820, getTokenRemainQuota(t, tokenID))
				if stop != nil {
					stop()
				}
				applied, err = PersistMidjourneyBilling(info, task)
				require.NoError(t, err)
				assert.Equal(t, failure == "rollback", applied)
			} else {
				require.NoError(t, err)
				require.True(t, applied)
			}
			require.True(t, task.BillingAccounted)
			owner = lifecycleOwner(t, info.RequestId)
			require.Equal(t, model.BillingTaskOwned, owner.State)
			require.EqualValues(t, task.Id, owner.TaskID)
			require.Equal(t, 180, owner.SourceHeld, "owner retains original held evidence after handoff")
			replay := *task
			applied, err = PersistMidjourneyBilling(info, &replay)
			require.NoError(t, err)
			require.False(t, applied)
			used, count := getUserUsageAccounting(t, userID)
			assert.Equal(t, 180, used)
			assert.Equal(t, 1, count)
			task.Status = "FAILURE"
			won, err := task.UpdateWithStatus("IN_PROGRESS")
			require.NoError(t, err)
			require.True(t, won)
			require.True(t, RefundMidjourneyQuota(context.Background(), task, "trusted upstream terminal failure"))
			require.True(t, RefundMidjourneyQuota(context.Background(), task, "replay"))
			assertUserBalances(t, userID, 100, 200, 50)
			assert.Equal(t, 1000, getTokenRemainQuota(t, tokenID))
			used, count = getUserUsageAccounting(t, userID)
			assert.Zero(t, used)
			assert.Equal(t, 1, count)
		})
	}
	t.Run("concurrent_holds_cannot_overspend", func(t *testing.T) {
		truncate(t)
		seedUser(t, 4661, 100)
		seedToken(t, 4662, 4661, "synthetic-concurrent-mj", 1000)
		seedBonusEligibleModel(t, "mj_imagine", boolPtr(false))
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for i := range 2 {
			info := &relaycommon.RelayInfo{UserId: 4661, TokenId: 4662, TokenKey: "synthetic-concurrent-mj", RequestId: fmt.Sprintf("mj-race-%d", i), OriginModelName: "mj_imagine"}
			c := testGinContext(t)
			wg.Go(func() { results <- ReserveMidjourneyBilling(c, info, 80) })
		}
		wg.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			}
		}
		assert.Equal(t, 1, success)
		assert.Equal(t, 20, getUserQuota(t, 4661))
		assert.Equal(t, 920, getTokenRemainQuota(t, 4662))
	})
}
