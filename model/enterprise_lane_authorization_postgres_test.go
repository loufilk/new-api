//go:build enterprise_lane_lab

package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnterpriseLaneGatewayBootstrapAndStopPostgres(t *testing.T) {
	tok, id := enterpriseLaneTokenFixture(t)
	require.NoError(t, DB.AutoMigrate(append(EnterpriseLaneModels(), &BillingFinalization{})...))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id.UserID).Updates(map[string]any{"quota": 1000, "quota_paid": 1000, "status": common.UserStatusEnabled}).Error)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", tok.Id).Updates(map[string]any{"group": "default", "used_quota": 0}).Error)
	t.Cleanup(func() {
		for _, row := range []any{&EnterpriseLaneRequest{}, &EnterpriseLane{}, &BalanceLedger{}, &BalanceLedgerOperation{}, &BonusSourceState{}, &BonusSourceLot{}, &BonusSourceAccount{}, &BillingFinalization{}} {
			require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Where("user_id = ?", id.UserID).Delete(row).Error)
		}
	})
	ctx := t.Context()
	require.NoError(t, CheckEnterpriseLaneGatewayDisabled(ctx, DB))
	_, err := BootstrapEnterpriseLaneGateway(ctx, DB, tok.Id, 2, 600, 400, false)
	require.Error(t, err)
	require.NoError(t, CheckEnterpriseLaneGatewayDisabled(ctx, DB), "partial startup funding must roll back")
	var original Token
	require.NoError(t, DB.First(&original, tok.Id).Error)
	require.Equal(t, 1000, original.RemainQuota)
	require.Zero(t, original.UsedQuota)
	var initial User
	require.NoError(t, DB.First(&initial, id.UserID).Error)
	require.Equal(t, 1000, initial.QuotaPaid)

	legacy := BillingFinalization{RequestID: id.LaneID + "-legacy", UserID: id.UserID, TokenID: tok.Id, State: BillingHeld}
	require.NoError(t, DB.Create(&legacy).Error)
	_, err = BootstrapEnterpriseLaneGateway(ctx, DB, tok.Id, 2, 500, 500, false)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	require.NoError(t, DB.Delete(&legacy).Error)
	lanes, err := BootstrapEnterpriseLaneGateway(ctx, DB, tok.Id, 2, 500, 500, false)
	require.NoError(t, err)
	require.Len(t, lanes, 2)
	replayed, err := BootstrapEnterpriseLaneGateway(ctx, DB, tok.Id, 2, 500, 500, false)
	require.NoError(t, err)
	require.Equal(t, lanes, replayed)
	require.ErrorIs(t, CheckEnterpriseLaneGatewayDisabled(ctx, DB), ErrEnterpriseLaneState)
	var active Token
	require.NoError(t, DB.First(&active, tok.Id).Error)
	require.Zero(t, active.RemainQuota)
	require.Zero(t, active.UsedQuota, "escrow is not spend")
	require.NoError(t, AuthorizeEnterpriseLaneGateway(ctx, DB, lanes[0], active.CacheRevision))
	require.ErrorIs(t, AuthorizeEnterpriseLaneGateway(ctx, DB, lanes[0], active.CacheRevision-1), ErrEnterpriseLaneIdentity)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", tok.Id).Update("status", common.TokenStatusDisabled).Error)
	require.ErrorIs(t, AuthorizeEnterpriseLaneGateway(ctx, DB, lanes[0], active.CacheRevision), ErrEnterpriseLaneIdentity)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", tok.Id).Update("status", common.TokenStatusEnabled).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id.UserID).Update("status", common.UserStatusDisabled).Error)
	require.ErrorIs(t, AuthorizeEnterpriseLaneGateway(ctx, DB, lanes[0], active.CacheRevision), ErrEnterpriseLaneIdentity)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id.UserID).Update("status", common.UserStatusEnabled).Error)

	a, b := id.LaneID+"-a", id.LaneID+"-b"
	_, err = ReserveEnterpriseLane(ctx, DB, lanes[0], a, 40)
	require.NoError(t, err)
	_, err = ReserveEnterpriseLane(ctx, DB, lanes[1], b, 40)
	require.NoError(t, err)
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, lanes[0], a)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", tok.Id).Update("key", "synthetic-replacement-before-stop").Error)
	var wg sync.WaitGroup
	release := make(chan struct{})
	dispatchErr := make(chan error, 1)
	wg.Go(func() {
		<-release
		_, err := MarkEnterpriseLaneDispatched(ctx, DB, lanes[1], b)
		dispatchErr <- err
	})
	close(release)
	_, err = BootstrapEnterpriseLaneGateway(ctx, DB, tok.Id, 2, 500, 500, true)
	require.NoError(t, err)
	wg.Wait()
	for _, lane := range lanes {
		require.Equal(t, EnterpriseLaneStopped, readLabLane(t, lane).State)
		_, err := ReserveEnterpriseLane(ctx, DB, lane, id.LaneID+"-disabled", 1)
		require.ErrorIs(t, err, ErrEnterpriseLaneState)
	}
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, lanes[1], b)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	_, err = SettleEnterpriseLane(ctx, DB, lanes[0], a, 30)
	require.NoError(t, err, "stop cannot erase already dispatched work")
	if err := <-dispatchErr; err == nil {
		_, err = SettleEnterpriseLane(ctx, DB, lanes[1], b, 35)
		require.NoError(t, err)
	} else {
		require.ErrorIs(t, err, ErrEnterpriseLaneState)
		_, err = RefundEnterpriseLane(ctx, DB, lanes[1], b)
		require.NoError(t, err)
	}
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", tok.Id).Update("key", tok.Key).Error)
	_, err = BootstrapEnterpriseLaneGateway(ctx, DB, tok.Id, 2, 500, 500, false)
	require.ErrorIs(t, err, ErrEnterpriseLaneState, "stopped escrow cannot reopen on a launch opt-in")
	require.ErrorIs(t, CheckEnterpriseLaneGatewayDisabled(ctx, DB), ErrEnterpriseLaneState)
	for _, lane := range lanes {
		_, err = DrainEnterpriseLane(ctx, DB, lane)
		require.NoError(t, err)
	}
	require.NoError(t, CheckEnterpriseLaneGatewayDisabled(ctx, DB))
}
