package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func midjourneyRefundDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := withPaymentBindingDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Midjourney{}, &model.Channel{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.BillingFinalization{}, &model.Log{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldMemory, oldRedis, oldBatch, oldLog := common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, model.LOG_DB
	common.MemoryCacheEnabled = false
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	model.LOG_DB = db
	t.Cleanup(func() {
		common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, model.LOG_DB = oldMemory, oldRedis, oldBatch, oldLog
	})
	return db
}

func TestMidjourneyMissingChannelRequiresReviewWithoutRefund(t *testing.T) {
	db := midjourneyRefundDB(t)
	require.NoError(t, db.Create(&model.User{Id: 29501, Username: "mj-official-no-retry", Quota: 900, QuotaPaid: 900, UsedQuota: 100, RequestCount: 1}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 29503, UserId: 29501, Key: "synthetic-mj-official", RemainQuota: 900, UsedQuota: 100}).Error)
	task := &model.Midjourney{MjId: "mj-official-no-retry", UserId: 29501, TokenId: 29503, ChannelId: 29502, Quota: 100, Status: "IN_PROGRESS", Progress: "20%", WalletDebit: []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}}
	require.NoError(t, db.Create(task).Error)
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	require.NoError(t, db.First(task, task.Id).Error)
	require.Equal(t, "UNKNOWN", task.Status)
	assert.Equal(t, "20%", task.Progress)
	assert.Equal(t, 100, task.Quota)
	assert.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}, task.WalletDebit)
	assert.Equal(t, "Missing channel metadata; upstream delivery requires operator review", task.FailReason)
	assert.True(t, task.RequiresFinancialReview())
	assert.Zero(t, task.FinishTime)
	before := *task
	require.False(t, model.HasUnfinishedMidjourneyTasks())
	require.Empty(t, model.GetAllUnFinishTasks())
	for range 2 {
		summary := runMidjourneyTaskUpdateOnce(context.Background(), nil)
		assert.Zero(t, summary.UnfinishedTasks)
		assert.Zero(t, summary.ChannelsScanned)
		assert.Zero(t, summary.NullTasksFailed)
	}
	require.NoError(t, db.First(task, task.Id).Error)
	assert.Equal(t, before, *task)
	var user model.User
	require.NoError(t, db.First(&user, 29501).Error)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 900, user.QuotaPaid)
	assert.Zero(t, user.QuotaBonus)
	assert.Zero(t, user.QuotaReward)
	assert.Equal(t, 100, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	var token model.Token
	require.NoError(t, db.First(&token, 29503).Error)
	assert.Equal(t, 900, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
	var count int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&model.BillingFinalization{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestMidjourneyTransientChannelLookupCannotAuthorizeRefund(t *testing.T) {
	db := midjourneyRefundDB(t)
	require.NoError(t, db.Create(&model.User{Id: 29511, Username: "mj-lookup-failure", Quota: 900, QuotaPaid: 900}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 29512, Name: "existing-channel"}).Error)
	task := &model.Midjourney{UserId: 29511, MjId: "mj-lookup-failure", ChannelId: 29512, Quota: 100, Status: "IN_PROGRESS", Progress: "20%", WalletDebit: []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}}
	require.NoError(t, db.Create(task).Error)
	const hook = "test:mj-transient-lookup"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Channel" {
			tx.AddError(errors.New("temporary database lookup failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(hook) })
	for range 2 {
		runMidjourneyTaskUpdateOnce(context.Background(), nil)
	}
	require.NoError(t, db.First(task, task.Id).Error)
	assert.Equal(t, "IN_PROGRESS", task.Status)
	assert.Equal(t, "20%", task.Progress)
	assert.Equal(t, 100, task.Quota)
	var user model.User
	require.NoError(t, db.First(&user, 29511).Error)
	assert.Equal(t, 900, user.QuotaPaid)
	var count int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestMidjourneyEmptyUpstreamIDsHaveIndependentRefundReceipts(t *testing.T) {
	db := midjourneyRefundDB(t)
	require.NoError(t, db.Create(&model.User{Id: 29531, Username: "mj-empty-ids", Quota: 1000, QuotaPaid: 1000}).Error)
	tasks := make([]*model.Midjourney, 0, 2)
	for i := range 2 {
		info := &relaycommon.RelayInfo{UserId: 29531, RequestId: fmt.Sprintf("mj-empty-id-%d", i), UserQuota: 1000 - i*100, IsPlayground: true, BillingSource: service.BillingSourceWallet}
		info.UserSetting.QuotaWarningEnabled = common.GetPointer(false)
		task := &model.Midjourney{UserId: 29531, Status: "IN_PROGRESS", Progress: "20%"}
		prepared, err := service.PrepareMidjourneyTaskBilling(info, task, 100, true)
		require.NoError(t, err)
		require.True(t, prepared)
		require.NoError(t, db.Create(task).Error)
		billed, err := service.SettleMidjourneyTaskBilling(info, task, prepared)
		require.NoError(t, err)
		require.True(t, billed)
		assert.Empty(t, task.MjId)
		assert.Equal(t, 100, task.Quota)
		assert.True(t, task.BillingAccounted)
		assert.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}, task.WalletDebit)
		tasks = append(tasks, task)
	}
	require.NotEqual(t, tasks[0].Id, tasks[1].Id)
	var user model.User
	require.NoError(t, db.First(&user, 29531).Error)
	assert.Equal(t, 800, user.QuotaPaid)
	assert.Equal(t, 200, user.UsedQuota)
	assert.Equal(t, 2, user.RequestCount)
	for i, task := range tasks {
		task.Status, task.Progress = "FAILURE", "100%"
		won, err := task.UpdateWithStatus("IN_PROGRESS")
		require.NoError(t, err)
		require.True(t, won)
		require.True(t, service.RefundMidjourneyQuota(context.Background(), task, "explicit caller decision"))
		require.True(t, service.RefundMidjourneyQuota(context.Background(), task, "duplicate caller decision"))
		require.NoError(t, db.First(task, task.Id).Error)
		assert.Zero(t, task.Quota)
		assert.NotNil(t, task.WalletDebit)
		assert.Empty(t, task.WalletDebit)
		require.NoError(t, db.First(&user, 29531).Error)
		assert.Equal(t, 900+i*100, user.QuotaPaid)
		assert.Equal(t, 100-i*100, user.UsedQuota)
		if i == 0 {
			require.NoError(t, db.First(tasks[1], tasks[1].Id).Error)
			assert.Equal(t, "IN_PROGRESS", tasks[1].Status)
			assert.Equal(t, 100, tasks[1].Quota, "refunding one local task must not clear the other's receipt")
			assert.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}, tasks[1].WalletDebit)
		}
	}
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	require.NoError(t, db.First(&user, 29531).Error)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, user.QuotaPaid)
	assert.Zero(t, user.UsedQuota)
	assert.Equal(t, 2, user.RequestCount)
	var operations, pending int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&operations).Error)
	require.NoError(t, db.Model(&model.Midjourney{}).Where("quota <> 0").Count(&pending).Error)
	assert.EqualValues(t, 4, operations, "two real charges and two refunds; repeats must add no operations")
	var refunds []model.BalanceLedger
	require.NoError(t, db.Where("reason = ?", model.LedgerReasonTaskRefund).Order("id").Find(&refunds).Error)
	require.Len(t, refunds, 2)
	for i, refund := range refunds {
		assert.Equal(t, "mj_task", refund.ReferenceType)
		assert.Equal(t, fmt.Sprint(tasks[i].Id), refund.ReferenceId)
		assert.Equal(t, string(model.AccountPaid), refund.Account)
		assert.Equal(t, 100, refund.Delta)
	}
	assert.NotEqual(t, refunds[0].OperationId, refunds[1].OperationId)
	assert.Zero(t, pending)
	assert.False(t, model.HasUnfinishedMidjourneyTasks())
}

func TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts(t *testing.T) {
	for _, receipt := range []struct {
		name   string
		shares []model.LedgerShare
	}{
		{"legacy_null", nil}, {"legacy_empty", []model.LedgerShare{}},
	} {
		t.Run(receipt.name, func(t *testing.T) {
			db := midjourneyRefundDB(t)
			require.NoError(t, db.Create(&model.User{Id: 29541, Username: "mj-receipt-kind", Quota: 900, QuotaPaid: 900}).Error)
			task := &model.Midjourney{UserId: 29541, MjId: receipt.name, Quota: 100, Status: "FAILURE", Progress: "100%", WalletDebit: receipt.shares}
			require.NoError(t, db.Create(task).Error)
			before := *task
			assert.False(t, task.HasCurrentBillingIdentity())
			for range 2 {
				require.False(t, service.RefundMidjourneyQuota(context.Background(), task, "unproven legacy charge"))
			}
			runMidjourneyTaskUpdateOnce(context.Background(), nil)
			runMidjourneyTaskUpdateOnce(context.Background(), nil)
			var user model.User
			require.NoError(t, db.First(&user, 29541).Error)
			assert.Equal(t, 900, user.Quota)
			assert.Equal(t, 900, user.QuotaPaid)
			assert.Zero(t, user.UsedQuota)
			assert.Zero(t, user.RequestCount)
			require.NoError(t, db.First(task, task.Id).Error)
			assert.Equal(t, 100, task.Quota)
			assert.Equal(t, receipt.shares, task.WalletDebit)
			assert.Equal(t, before, *task)
			for _, entity := range []any{&model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.BillingFinalization{}, &model.Log{}} {
				var count int64
				require.NoError(t, db.Model(entity).Count(&count).Error)
				assert.Zero(t, count, "%T must not be fabricated for an unproven charge", entity)
			}
		})
	}
	t.Run("prepared_uncharged", func(t *testing.T) {
		db := midjourneyRefundDB(t)
		require.NoError(t, db.Create(&model.User{Id: 29541, Username: "mj-prepared-uncharged", Quota: 900, QuotaPaid: 900}).Error)
		info := &relaycommon.RelayInfo{UserId: 29541, RequestId: "mj-prepared-uncharged", UserQuota: 900, IsPlayground: true, BillingSource: service.BillingSourceWallet}
		info.UserSetting.QuotaWarningEnabled = common.GetPointer(false)
		task := &model.Midjourney{UserId: 29541, MjId: "mj-prepared-uncharged", Status: "IN_PROGRESS", Progress: "20%"}
		prepared, err := service.PrepareMidjourneyTaskBilling(info, task, 100, true)
		require.NoError(t, err)
		require.True(t, prepared)
		require.NoError(t, db.Create(task).Error)
		assert.True(t, task.HasCurrentBillingIdentity())
		assert.False(t, task.BillingAccounted)
		assert.NotNil(t, task.WalletDebit)
		assert.Empty(t, task.WalletDebit)
		task.Status, task.Progress = "FAILURE", "100%"
		won, err := task.UpdateWithStatus("IN_PROGRESS")
		require.NoError(t, err)
		require.True(t, won)
		for range 2 {
			require.True(t, service.RefundMidjourneyQuota(context.Background(), task, "canceled before charging"))
		}
		billed, err := service.SettleMidjourneyTaskBilling(info, task, prepared)
		require.NoError(t, err)
		assert.False(t, billed, "a canceled preparation must not be charged later")
		runMidjourneyTaskUpdateOnce(context.Background(), nil)
		runMidjourneyTaskUpdateOnce(context.Background(), nil)
		require.NoError(t, db.First(task, task.Id).Error)
		assert.Zero(t, task.Quota)
		assert.Equal(t, 100, task.BillingOriginalQuota)
		assert.False(t, task.BillingAccounted)
		assert.NotNil(t, task.WalletDebit)
		assert.Empty(t, task.WalletDebit)
		var user model.User
		require.NoError(t, db.First(&user, 29541).Error)
		assert.Equal(t, 900, user.Quota)
		assert.Equal(t, 900, user.QuotaPaid)
		assert.Zero(t, user.UsedQuota)
		assert.Zero(t, user.RequestCount)
		for _, entity := range []any{&model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Log{}} {
			var count int64
			require.NoError(t, db.Model(entity).Count(&count).Error)
			assert.Zero(t, count, "%T must not be emitted for an uncharged preparation", entity)
		}
		var owners int64
		require.NoError(t, db.Model(&model.BillingFinalization{}).Count(&owners).Error)
		assert.EqualValues(t, 1, owners)
	})
}

func TestMidjourneyCacheMissRequiresAuthoritativeAbsence(t *testing.T) {
	db := midjourneyRefundDB(t)
	require.NoError(t, db.Create(&model.User{Id: 29551, Username: "mj-cache-miss", Quota: 900, QuotaPaid: 900}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 29552, Name: "exists-on-other-node"}).Error)
	task := &model.Midjourney{UserId: 29551, MjId: "mj-cache-miss", ChannelId: 29552, Quota: 100, Status: "IN_PROGRESS", Progress: "20%", WalletDebit: []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}}
	require.NoError(t, db.Create(task).Error)
	const hook = "test:mj-snapshot-miss"
	confirmed := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Channel" {
			if len(tx.Statement.Omits) == 0 {
				tx.AddError(model.ErrChannelCacheMiss) // the cache-only lookup is unavailable
			} else {
				confirmed++
			} // the metadata-only authoritative lookup succeeds
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(hook) })
	for range 2 {
		runMidjourneyTaskUpdateOnce(context.Background(), nil)
	}
	require.Equal(t, 2, confirmed)
	require.NoError(t, db.First(task, task.Id).Error)
	assert.Equal(t, "IN_PROGRESS", task.Status)
	assert.Equal(t, 100, task.Quota)
	var user model.User
	require.NoError(t, db.First(&user, 29551).Error)
	assert.Equal(t, 900, user.QuotaPaid)
}
