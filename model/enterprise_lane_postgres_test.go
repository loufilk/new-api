//go:build enterprise_lane_lab

package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func enterpriseLaneFixture(t *testing.T) EnterpriseLaneIdentity {
	t.Helper()
	_, id := enterpriseLaneTokenFixture(t)
	require.NoError(t, DB.AutoMigrate(EnterpriseLaneModels()...))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id.UserID).Updates(map[string]any{"quota": 1300, "quota_paid": 1000, "quota_bonus": 200, "quota_reward": 100}).Error)
	t.Cleanup(func() {
		for _, row := range []any{&EnterpriseLaneRequest{}, &EnterpriseLane{}, &BalanceLedger{}, &BalanceLedgerOperation{}, &BonusSourceState{}, &BonusSourceLot{}, &BonusSourceAccount{}} {
			require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Where("user_id = ?", id.UserID).Delete(row).Error)
		}
	})
	return id
}

func checkLaneBaseAccounts(t *testing.T, id EnterpriseLaneIdentity, paid, remaining, used int) {
	t.Helper()
	var user User
	var token Token
	require.NoError(t, DB.First(&user, id.UserID).Error)
	require.NoError(t, DB.Unscoped().First(&token, id.TokenID).Error)
	assert.Equal(t, paid, user.QuotaPaid)
	assert.Equal(t, 200, user.QuotaBonus)
	assert.Equal(t, 100, user.QuotaReward)
	assert.Equal(t, paid+300, user.Quota)
	assert.Equal(t, remaining, token.RemainQuota)
	assert.Equal(t, used, token.UsedQuota)
}

func readLabLane(t *testing.T, id EnterpriseLaneIdentity) EnterpriseLane {
	t.Helper()
	var lane EnterpriseLane
	require.NoError(t, DB.Where("lane_key = ?", id.laneKey()).Take(&lane).Error)
	require.True(t, lane.valid())
	return lane
}

func TestEnterpriseLaneLabLifecyclePostgres(t *testing.T) {
	id := enterpriseLaneFixture(t)
	ctx := t.Context()

	// Failure on either original source leaves the other source and lane intact.
	_, err := CreateEnterpriseLane(ctx, DB, id, 1001, 100)
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
	_, err = CreateEnterpriseLane(ctx, DB, id, 100, 1001)
	require.ErrorIs(t, err, ErrBillingTokenInsufficient)
	checkLaneBaseAccounts(t, id, 1000, 1000, 100)
	var count int64
	require.NoError(t, DB.Model(&EnterpriseLane{}).Where("lane_key = ?", id.laneKey()).Count(&count).Error)
	assert.Zero(t, count)

	funded, err := CreateEnterpriseLane(ctx, DB, id, 300, 170)
	require.NoError(t, err)
	assert.Equal(t, 300, funded.PaidAvailable)
	assert.Equal(t, 170, funded.TokenAvailable)
	_, err = CreateEnterpriseLane(ctx, DB, id, 301, 170)
	require.ErrorIs(t, err, ErrEnterpriseLaneIdentity)
	checkLaneBaseAccounts(t, id, 700, 830, 100)

	id2 := id
	id2.Epoch++
	_, err = CreateEnterpriseLane(ctx, DB, id2, 10, 10)
	require.NoError(t, err)
	requestID := id.LaneID + "-one"
	_, err = ReserveEnterpriseLane(ctx, DB, id, requestID, 0)
	require.ErrorIs(t, err, ErrEnterpriseLaneCapacity)
	_, err = ReserveEnterpriseLane(ctx, DB, id, requestID, common.MaxQuota+1)
	require.ErrorIs(t, err, ErrEnterpriseLaneCapacity)
	held, err := ReserveEnterpriseLane(ctx, DB, id, requestID, 100)
	require.NoError(t, err)
	assert.Equal(t, 100, held.Held)
	_, err = ReserveEnterpriseLane(ctx, DB, id2, requestID, 10)
	require.ErrorIs(t, err, ErrEnterpriseLaneIdentity)
	_, err = ReserveEnterpriseLane(ctx, DB, id, requestID, 171)
	require.ErrorIs(t, err, ErrEnterpriseLaneCapacity)
	held, err = ReserveEnterpriseLane(ctx, DB, id, requestID, 150)
	require.NoError(t, err)
	assert.Equal(t, 150, held.Held)
	_, err = SettleEnterpriseLane(ctx, DB, id, requestID, 80)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, requestID)
	require.NoError(t, err)
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, requestID)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	_, err = RefundEnterpriseLane(ctx, DB, id, requestID)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	_, err = ReserveEnterpriseLane(ctx, DB, id, requestID, 170)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)

	otherID := id.LaneID + "-two"
	_, err = ReserveEnterpriseLane(ctx, DB, id, otherID, 20)
	require.NoError(t, err)
	_, err = StopEnterpriseLane(ctx, DB, id)
	require.NoError(t, err)
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, otherID)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	_, err = DrainEnterpriseLane(ctx, DB, id)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	for range 2 {
		refunded, err := RefundEnterpriseLane(ctx, DB, id, otherID)
		require.NoError(t, err)
		assert.Equal(t, EnterpriseLaneRefunded, refunded.State)
		assert.Equal(t, EnterpriseLaneRefundNeverDispatched, refunded.RefundProof)
	}
	for range 2 {
		settled, err := SettleEnterpriseLane(ctx, DB, id, requestID, 80)
		require.NoError(t, err)
		assert.Equal(t, EnterpriseLaneSettled, settled.State)
		assert.Equal(t, 80, settled.Actual)
	}
	_, err = SettleEnterpriseLane(ctx, DB, id, requestID, 81)
	require.ErrorIs(t, err, ErrEnterpriseLaneIdentity)
	checkLaneBaseAccounts(t, id, 690, 820, 100)

	for _, i := range []EnterpriseLaneIdentity{id, id, id2, id2} {
		_, err := StopEnterpriseLane(ctx, DB, i)
		require.NoError(t, err)
		released, err := DrainEnterpriseLane(ctx, DB, i)
		require.NoError(t, err)
		assert.Equal(t, EnterpriseLaneDrained, released.State)
		assert.Equal(t, released.PaidBudget-released.Spent, released.PaidReturned)
		assert.Equal(t, released.TokenBudget-released.Spent, released.TokenReturned)
	}
	checkLaneBaseAccounts(t, id, 920, 920, 180)
	var operations []BalanceLedger
	require.NoError(t, DB.Where("user_id = ? AND reference_type = ?", id.UserID, enterpriseLaneReference).Find(&operations).Error)
	require.Len(t, operations, 4, "two exact funding debits, two exact release credits")
	walletNet := 0
	for _, row := range operations {
		assert.Equal(t, string(AccountPaid), row.Account)
		walletNet += row.Delta
	}
	assert.Equal(t, -80, walletNet)
}

func TestEnterpriseLaneLabUnknownAndExcessNeverRefundPostgres(t *testing.T) {
	id := enterpriseLaneFixture(t)
	ctx := t.Context()
	_, err := CreateEnterpriseLane(ctx, DB, id, 200, 200)
	require.NoError(t, err)
	for _, scenario := range []string{"unknown", "overcap"} {
		rid := id.LaneID + "-" + scenario
		_, err := ReserveEnterpriseLane(ctx, DB, id, rid, 50)
		require.NoError(t, err)
		_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, rid)
		require.NoError(t, err)
		if scenario == "unknown" {
			owner, err := MarkEnterpriseLaneDeliveryUnknown(ctx, DB, id, rid)
			require.NoError(t, err)
			assert.Equal(t, EnterpriseLaneUnknown, owner.State)
			_, err = SettleEnterpriseLane(ctx, DB, id, rid, 0)
			require.ErrorIs(t, err, ErrEnterpriseLaneState)
		} else {
			owner, err := SettleEnterpriseLane(ctx, DB, id, rid, 60)
			require.ErrorIs(t, err, ErrEnterpriseLaneExposure)
			assert.Equal(t, EnterpriseLaneReview, owner.State)
			assert.Equal(t, 60, owner.Actual)
			_, err = SettleEnterpriseLane(ctx, DB, id, rid, 40)
			require.ErrorIs(t, err, ErrEnterpriseLaneIdentity)
		}
		_, err = RefundEnterpriseLane(ctx, DB, id, rid)
		require.ErrorIs(t, err, ErrEnterpriseLaneState)
	}
	_, err = StopEnterpriseLane(ctx, DB, id)
	require.NoError(t, err)
	_, err = DrainEnterpriseLane(ctx, DB, id)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	lane := readLabLane(t, id)
	assert.Equal(t, 100, lane.Held)
	assert.Equal(t, 2, lane.OpenRequests)
	assert.Zero(t, lane.Spent)
	checkLaneBaseAccounts(t, id, 800, 800, 100)
}

func TestEnterpriseLaneLabDrainRejectsDivergentReceiptsPostgres(t *testing.T) {
	id := enterpriseLaneFixture(t)
	ctx := t.Context()
	_, err := CreateEnterpriseLane(ctx, DB, id, 100, 100)
	require.NoError(t, err)
	rid := id.LaneID + "-drain"
	_, err = ReserveEnterpriseLane(ctx, DB, id, rid, 30)
	require.NoError(t, err)
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, rid)
	require.NoError(t, err)
	_, err = SettleEnterpriseLane(ctx, DB, id, rid, 20)
	require.NoError(t, err)
	_, err = StopEnterpriseLane(ctx, DB, id)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&EnterpriseLaneRequest{}).Where("request_id = ?", rid).Update("actual", 19).Error)
	_, err = DrainEnterpriseLane(ctx, DB, id)
	require.ErrorIs(t, err, ErrEnterpriseLaneIdentity, "algebraically valid lane counters alone do not prove what may be returned")
	checkLaneBaseAccounts(t, id, 900, 900, 100)
	require.NoError(t, DB.Model(&EnterpriseLaneRequest{}).Where("request_id = ?", rid).Update("actual", 20).Error)
	_, err = DrainEnterpriseLane(ctx, DB, id)
	require.NoError(t, err)
	checkLaneBaseAccounts(t, id, 980, 980, 120)
}

func TestEnterpriseLaneLabLostCommitAckPostgres(t *testing.T) {
	id := enterpriseLaneFixture(t)
	ctx := t.Context()
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	lost := DB.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{sqlDB}
	_, err = CreateEnterpriseLane(ctx, lost, id, 200, 200)
	require.ErrorContains(t, err, "synthetic lost acknowledgment")
	_, err = CreateEnterpriseLane(ctx, DB, id, 200, 200)
	require.NoError(t, err)
	checkLaneBaseAccounts(t, id, 800, 800, 100)

	rid := id.LaneID + "-ack"
	_, err = ReserveEnterpriseLane(ctx, lost, id, rid, 50)
	require.ErrorContains(t, err, "synthetic lost acknowledgment")
	_, err = ReserveEnterpriseLane(ctx, DB, id, rid, 50)
	require.NoError(t, err)
	assert.Equal(t, 50, readLabLane(t, id).Held)
	_, err = MarkEnterpriseLaneDispatched(ctx, lost, id, rid)
	require.ErrorContains(t, err, "synthetic lost acknowledgment")
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, rid)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	_, err = RefundEnterpriseLane(ctx, DB, id, rid)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)

	_, err = SettleEnterpriseLane(ctx, lost, id, rid, 40)
	require.ErrorContains(t, err, "synthetic lost acknowledgment")
	_, err = SettleEnterpriseLane(ctx, DB, id, rid, 41)
	require.ErrorIs(t, err, ErrEnterpriseLaneIdentity)
	_, err = SettleEnterpriseLane(ctx, DB, id, rid, 40)
	require.NoError(t, err)
	_, err = StopEnterpriseLane(ctx, DB, id)
	require.NoError(t, err)
	_, err = DrainEnterpriseLane(ctx, lost, id)
	require.ErrorContains(t, err, "synthetic lost acknowledgment")
	_, err = DrainEnterpriseLane(ctx, DB, id)
	require.NoError(t, err)
	checkLaneBaseAccounts(t, id, 960, 960, 140)
}

func TestEnterpriseLaneLabFiniteCapacityAndRootLocksPostgres(t *testing.T) {
	id := enterpriseLaneFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := CreateEnterpriseLane(ctx, DB, id, 200, 70)
	require.NoError(t, err)
	block := DB.WithContext(ctx).Begin()
	require.NoError(t, block.Error)
	defer block.Rollback()
	require.NoError(t, LockFinancialUserTx(block, id.UserID))
	_, err = lockEnterpriseLaneTokenTx(block, id)
	require.NoError(t, err)

	// A real competing SQL transaction holds both base rows throughout these
	// requests. Either hot path touching either row cannot pass this fixture.
	var wg sync.WaitGroup
	results := make(chan error, 12)
	start := make(chan struct{})
	for i := range cap(results) {
		wg.Go(func() {
			<-start
			_, err := ReserveEnterpriseLane(ctx, DB, id, fmt.Sprintf("%s-%d", id.LaneID, i), 10)
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, denied, first := 0, 0, -1
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrEnterpriseLaneCapacity):
			denied++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 7, accepted)
	assert.Equal(t, 5, denied)
	lane := readLabLane(t, id)
	assert.Equal(t, 0, lane.TokenAvailable)
	assert.Equal(t, 130, lane.PaidAvailable)
	assert.Equal(t, 70, lane.Held)
	assert.Equal(t, 7, lane.OpenRequests)
	for i := range 12 {
		var request EnterpriseLaneRequest
		result := DB.Where("request_id = ?", fmt.Sprintf("%s-%d", id.LaneID, i)).Limit(1).Find(&request)
		require.NoError(t, result.Error)
		if result.RowsAffected == 1 {
			first = i
			break
		}
	}
	require.GreaterOrEqual(t, first, 0)
	rid := fmt.Sprintf("%s-%d", id.LaneID, first)
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, rid)
	require.NoError(t, err)
	_, err = SettleEnterpriseLane(ctx, DB, id, rid, 7)
	require.NoError(t, err)
	require.NoError(t, block.Rollback().Error)
	lane = readLabLane(t, id)
	assert.Equal(t, 3, lane.TokenAvailable)
	assert.Equal(t, 133, lane.PaidAvailable)
	assert.Equal(t, 60, lane.Held)
	assert.Equal(t, 7, lane.Spent)
}
