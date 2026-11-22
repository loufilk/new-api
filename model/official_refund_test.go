package model

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOfficialTokenRefundUsesBatchAndIndependentSQL(t *testing.T) {
	r := tokenMutationFixture(t, 100)
	oldBatch := common.BatchUpdateEnabled
	oldStores := make([]map[int]int, BatchUpdateTypeCount)
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		oldStores[i] = batchUpdateStores[i]
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatch
		for i := range BatchUpdateTypeCount {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = oldStores[i]
			batchUpdateLocks[i].Unlock()
		}
	})
	common.BatchUpdateEnabled = true
	require.NoError(t, IncreaseTokenQuotaForRefund(r.TokenID, "synthetic-deadline-token", 100))
	var token Token
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, 900, token.RemainQuota, "official batch refund is queued, not a synchronous private Tx")
	batchUpdateLocks[BatchUpdateTypeTokenQuota].Lock()
	queued := batchUpdateStores[BatchUpdateTypeTokenQuota][r.TokenID]
	batchUpdateLocks[BatchUpdateTypeTokenQuota].Unlock()
	assert.Equal(t, 100, queued)
	batchUpdate()
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	common.BatchUpdateEnabled = false
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", r.TokenID).Update("accessed_time", 0).Error)
	require.NoError(t, IncreaseTokenQuotaForRefund(r.TokenID, "ignored-without-redis", 0))
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Positive(t, token.AccessedTime, "official zero-delta SQL still updates accessed_time")
	require.Error(t, IncreaseTokenQuotaForRefund(r.TokenID, "", -1))
}

func TestOfficialTokenRefundUsesAsyncCacheWithoutPrivateFence(t *testing.T) {
	r := tokenMutationFixture(t, 100)
	server := useUserCacheMiniRedis(t)
	t.Cleanup(func() {
		require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
	})
	var token Token
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.NoError(t, IncreaseTokenQuotaForRefund(r.TokenID, token.Key, 100))
	require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
	require.False(t, server.Exists(getTokenCacheFenceKey(token.Key)), "refund API is official async delta, not private transactional invalidation")
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 1000, cached.RemainQuota)
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, 1000, token.RemainQuota)
}

// The original WAL/deferred partial-commit failure remains in the archived RED
// evidence. The same fixture must now commit the allowance and receipt together.
func TestOfficialSubscriptionRefundDatabaseBehavior(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			db := subscriptionPeriodDatabase(t, dialect.kind, dsn)
			require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(4)
			if dialect.kind == common.DatabaseTypeSQLite {
				var mode string
				require.NoError(t, db.Raw("PRAGMA journal_mode=WAL").Scan(&mode).Error)
				require.Equal(t, "wal", mode)
			}
			plan, sub := subscriptionPeriodSeed(t, db)
			plan.QuotaResetPeriod = SubscriptionResetNever
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Save(plan).Error)
			InvalidateSubscriptionPlanCache(plan.Id)
			res, err := PreConsumeUserSubscription("official-receipt", sub.UserId, "model", 0, 100)
			require.NoError(t, err)
			assert.Positive(t, res.QuotaPeriod)
			assert.EqualValues(t, 100, res.ReservedAmount)
			var before SubscriptionPreConsumeRecord
			require.NoError(t, db.Where("request_id = ?", "official-receipt").First(&before).Error)
			assert.EqualValues(t, 100, before.PreConsumed)
			assert.Equal(t, res.QuotaPeriod, before.QuotaPeriod)
			assert.EqualValues(t, 100, before.ReservedAmount)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			original := DB
			DB = db.WithContext(ctx)
			err = RefundSubscriptionPreConsume("official-receipt")
			DB = original
			var after SubscriptionPreConsumeRecord
			require.NoError(t, db.First(&after, before.Id).Error)
			require.NoError(t, db.First(sub, sub.Id).Error)
			require.NoError(t, err)
			assert.NoError(t, ctx.Err(), "the watchdog must not explain a refund failure")
			assert.Equal(t, "refunded", after.Status)
			assert.Zero(t, sub.AmountUsed)
			require.NoError(t, RefundSubscriptionPreConsume("official-receipt"))
			require.NoError(t, db.First(sub, sub.Id).Error)
			assert.Zero(t, sub.AmountUsed)
		})
	}
}
