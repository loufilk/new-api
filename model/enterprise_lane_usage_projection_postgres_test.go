//go:build enterprise_lane_lab

package model

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnterpriseLaneUsageProjectionPostgres(t *testing.T) {
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL fixture")
	}
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, DB.AutoMigrate(EnterpriseLaneModels()...))
	suffix := fmt.Sprintf("%016x", rand.Uint64())
	user := User{Username: "usage-" + suffix, AffCode: suffix[:6], Quota: 900, QuotaPaid: 900, UsedQuota: 10, RequestCount: 5}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{UserId: user.Id, Name: "fixture key", Key: "synthetic-" + suffix, RemainQuota: 500, UsedQuota: 7}
	require.NoError(t, DB.Create(&token).Error)
	channel := Channel{Name: "usage-" + suffix, Key: "synthetic", UsedQuota: 30}
	require.NoError(t, DB.Create(&channel).Error)
	t.Cleanup(func() {
		for _, row := range []any{&EnterpriseLaneUsageProjection{}, &EnterpriseLaneRequest{}, &Log{}, &Token{}} {
			require.NoError(t, DB.Unscoped().Where("user_id = ?", user.Id).Delete(row).Error)
		}
		require.NoError(t, DB.Unscoped().Delete(&channel).Error)
		require.NoError(t, DB.Unscoped().Delete(&user).Error)
	})
	identity := EnterpriseLaneIdentity{LaneID: "usage-" + suffix, Epoch: 2, UserID: user.Id, TokenID: token.Id, TokenFingerprint: BillingTokenFingerprint(token.Key)}
	usage := EnterpriseLaneUsage{ChannelID: channel.Id, PromptTokens: 80, CompletionTokens: 20, Model: "fixture-model", Group: "fixture-group", ObservedAt: common.GetTimestamp()}
	owners := []EnterpriseLaneRequest{
		{RequestID: identity.LaneID + "-a", EnterpriseLaneIdentity: identity, Held: 100, Actual: 0, State: EnterpriseLaneSettled, Usage: usage, UsageProjectionPending: true},
		{RequestID: identity.LaneID + "-b", EnterpriseLaneIdentity: identity, Held: 100, Actual: 8, State: EnterpriseLaneSettled, Usage: usage, UsageProjectionPending: true},
		{RequestID: identity.LaneID + "-c", EnterpriseLaneIdentity: identity, Held: 100, Actual: 13, State: EnterpriseLaneSettled, Usage: usage, UsageProjectionPending: true},
		{RequestID: identity.LaneID + "-d", EnterpriseLaneIdentity: identity, Held: 100, Actual: 70, State: EnterpriseLaneObserved, Usage: usage},
	}
	require.NoError(t, DB.Create(&owners).Error)
	ctx := t.Context()
	hold := DB.WithContext(ctx).Begin()
	require.NoError(t, hold.Error)
	defer hold.Rollback()
	_, err := lockEnterpriseLaneRequest(hold, identity, owners[0].RequestID)
	require.NoError(t, err)
	updates := map[string]int{}
	callback := "record_enterprise_lane_projection_grouped_updates"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
		updates[tx.Statement.Table]++
	}))
	n, err := ProjectEnterpriseLaneUsageBatch(ctx, DB, 2)
	require.NoError(t, DB.Callback().Update().Remove(callback))
	require.NoError(t, err)
	assert.Equal(t, 2, n, "skip the blocked older owner without waiting")
	assert.Equal(t, 1, updates["users"], "group the batch's deltas before locking the user")
	assert.Equal(t, 1, updates["channels"], "group the batch's deltas before locking the channel")
	require.ErrorIs(t, DrainEnterpriseLaneUsageProjection(ctx, DB), ErrEnterpriseLaneUsageProjection, "SKIP LOCKED zero is not proof of no pending owner")
	require.NoError(t, hold.Commit().Error)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).UpdateColumns(map[string]any{"key": "replacement-" + suffix, "name": "different key"}).Error)

	// Use a real PostgreSQL COMMIT while discarding the caller's acknowledgement.
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	lost := DB.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{sqlDB}
	n, err = ProjectEnterpriseLaneUsageBatch(ctx, lost, 1)
	require.ErrorContains(t, err, "synthetic lost acknowledgment")
	assert.Zero(t, n, "unknown ACK is not acknowledged progress")
	n, err = ProjectEnterpriseLaneUsageBatch(ctx, DB, MaxEnterpriseLaneUsageProjectionBatch)
	require.NoError(t, err)
	assert.Zero(t, n, "the ACK-loss retry cannot claim or apply the committed rows")
	_, err = ProjectEnterpriseLaneUsageBatch(ctx, DB, MaxEnterpriseLaneUsageProjectionBatch+1)
	require.ErrorIs(t, err, ErrEnterpriseLaneUsageProjection)
	t.Setenv("LOG_SQL_DSN", "intentionally unsupported split-log fixture")
	_, err = ProjectEnterpriseLaneUsageBatch(ctx, DB, 1)
	require.ErrorIs(t, err, ErrEnterpriseLaneUsageProjection)
	t.Setenv("LOG_SQL_DSN", "")

	var receipts []EnterpriseLaneUsageProjection
	require.NoError(t, DB.Where("user_id = ?", user.Id).Order("request_id").Find(&receipts).Error)
	var logs []Log
	require.NoError(t, DB.Where("user_id = ? AND type = ?", user.Id, LogTypeConsume).Order("request_id").Find(&logs).Error)
	require.Len(t, receipts, 3)
	require.Len(t, logs, 3)
	for i, receipt := range receipts {
		owner := owners[i]
		assert.Equal(t, owner.RequestID, receipt.RequestID)
		assert.Equal(t, owner.Actual, receipt.Actual)
		assert.Equal(t, usage, receipt.Usage)
		assert.Equal(t, owner.RequestID, logs[i].RequestId)
		assert.Equal(t, owner.Actual, logs[i].Quota)
		assert.Equal(t, user.Username, logs[i].Username)
		if i == 0 {
			assert.Empty(t, logs[i].TokenName, "do not label the original usage with a replacement credential")
		} else {
			assert.Equal(t, token.Name, logs[i].TokenName)
		}
		assert.Equal(t, usage.Group, logs[i].Group)
		assert.Equal(t, usage.ChannelID, logs[i].ChannelId)
		assert.Equal(t, usage.ObservedAt, logs[i].CreatedAt)
		assert.Equal(t, usage.PromptTokens, logs[i].PromptTokens)
		assert.Equal(t, usage.CompletionTokens, logs[i].CompletionTokens)
	}
	var actualUser User
	require.NoError(t, DB.First(&actualUser, user.Id).Error)
	assert.Equal(t, 31, actualUser.UsedQuota)
	assert.Equal(t, 8, actualUser.RequestCount)
	assert.Equal(t, user.Quota, actualUser.Quota)
	assert.Equal(t, user.QuotaPaid, actualUser.QuotaPaid)
	var actualToken Token
	require.NoError(t, DB.First(&actualToken, token.Id).Error)
	assert.Equal(t, token.RemainQuota, actualToken.RemainQuota)
	assert.Equal(t, token.UsedQuota, actualToken.UsedQuota)
	var actualChannel Channel
	require.NoError(t, DB.First(&actualChannel, channel.Id).Error)
	assert.EqualValues(t, 51, actualChannel.UsedQuota)

	// A late failure must leave the owner pending and roll back the receipt,
	// log and all grouped writes, even though the user UPDATE ran first.
	blocked := owners[1]
	blocked.RequestID = identity.LaneID + "-e"
	require.NoError(t, DB.Create(&blocked).Error)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).UpdateColumn("used_quota", int64(math.MaxInt64)).Error)
	n, err = ProjectEnterpriseLaneUsageBatch(ctx, DB, 2)
	require.ErrorIs(t, err, ErrEnterpriseLaneUsageProjection)
	assert.Zero(t, n)
	require.NoError(t, DB.First(&actualUser, user.Id).Error)
	assert.Equal(t, 31, actualUser.UsedQuota)
	assert.Equal(t, 8, actualUser.RequestCount)
	var pending EnterpriseLaneRequest
	require.NoError(t, DB.Where("request_id = ?", blocked.RequestID).Take(&pending).Error)
	assert.True(t, pending.UsageProjectionPending)
	var duplicate int64
	require.NoError(t, DB.Model(&EnterpriseLaneUsageProjection{}).Where("request_id = ?", blocked.RequestID).Count(&duplicate).Error)
	assert.Zero(t, duplicate)
	require.NoError(t, DB.Model(&Log{}).Where("request_id = ?", blocked.RequestID).Count(&duplicate).Error)
	assert.Zero(t, duplicate)

	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).UpdateColumn("used_quota", 51).Error)
	worker := lifecycle.NewGroup()
	require.True(t, worker.Go("test-enterprise-lane-usage-projection", RunEnterpriseLaneUsageProjection))
	join := func() {
		worker.BeginDrain()
		joinCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, worker.Wait(joinCtx))
	}
	t.Cleanup(join)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var owner EnterpriseLaneRequest
		assert.NoError(c, DB.WithContext(ctx).Where("request_id = ?", blocked.RequestID).Take(&owner).Error)
		assert.False(c, owner.UsageProjectionPending)
	}, 2*time.Second, 10*time.Millisecond)
	join()
	require.NoError(t, DrainEnterpriseLaneUsageProjection(ctx, DB))
	require.NoError(t, DB.First(&actualUser, user.Id).Error)
	assert.Equal(t, 39, actualUser.UsedQuota)
	assert.Equal(t, 9, actualUser.RequestCount)
}
