//go:build enterprise_lane_lab

package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestEnterpriseLaneGateway32FundingAndConservationPostgres(t *testing.T) {
	id := enterpriseLaneFixture(t)
	require.NoError(t, DB.AutoMigrate(&BillingFinalization{}))
	ctx := t.Context()
	const (
		count       = 32
		paidPerLane = 18_750_000
		keyPerLane  = 18_750_000
		starting    = 800_000_000
		actual      = 3
	)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id.UserID).Update("status", common.UserStatusEnabled).Error)
	setBalances := func(wallet, key int) {
		require.NoError(t, DB.Model(&User{}).Where("id = ?", id.UserID).Updates(map[string]any{"quota_paid": wallet, "quota": wallet + 300}).Error)
		require.NoError(t, DB.Model(&Token{}).Where("id = ?", id.TokenID).
			Updates(map[string]any{"group": "default", "remain_quota": key, "used_quota": 0}).Error)
	}
	requireNoFunding := func() {
		var rows int64
		require.NoError(t, DB.Model(&EnterpriseLane{}).Where("token_id = ?", id.TokenID).Count(&rows).Error)
		require.Zero(t, rows)
		require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ? AND reference_type = ?", id.UserID, enterpriseLaneReference).Count(&rows).Error)
		require.Zero(t, rows)
	}
	launch := func(n, paid, key int, stop bool) ([]EnterpriseLaneIdentity, error) {
		return BootstrapEnterpriseLaneGateway(ctx, DB, id.TokenID, n, paid, key, stop)
	}

	// Wallet failure occurs on the final debit: the outer transaction must
	// roll back the other 31 funded partitions, including Key and ledger.
	setBalances(count*paidPerLane-1, starting)
	_, err := launch(count, paidPerLane, keyPerLane, false)
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
	requireNoFunding()
	checkLaneBaseAccounts(t, id, count*paidPerLane-1, starting, 0)

	setBalances(starting, count*keyPerLane-1)
	_, err = launch(count, paidPerLane, keyPerLane, false)
	require.ErrorIs(t, err, ErrEnterpriseLaneCapacity)
	requireNoFunding()
	checkLaneBaseAccounts(t, id, starting, count*keyPerLane-1, 0)

	setBalances(starting, starting)
	for _, bad := range []struct{ n, paid, key int }{
		{count + 1, paidPerLane, keyPerLane},
		{count, common.MaxWalletQuota/count + 1, keyPerLane},
		{count, paidPerLane, common.MaxWalletQuota/count + 1},
	} {
		_, err = launch(bad.n, bad.paid, bad.key, false)
		require.ErrorIs(t, err, ErrEnterpriseLaneIdentity)
	}
	requireNoFunding()
	checkLaneBaseAccounts(t, id, starting, starting, 0)

	lanes, err := launch(count, paidPerLane, keyPerLane, false)
	require.NoError(t, err)
	require.Len(t, lanes, count)
	replay, err := launch(count, paidPerLane, keyPerLane, false)
	require.NoError(t, err)
	require.Equal(t, lanes, replay)
	_, err = launch(16, paidPerLane, keyPerLane, false)
	require.ErrorIs(t, err, ErrEnterpriseLaneState, "a live bundle cannot be resized/reopened on restart")
	checkLaneBaseAccounts(t, id, starting-count*paidPerLane, starting-count*keyPerLane, 0)

	paid, key, spent := 0, 0, 0
	for slot, laneID := range lanes {
		require.Equal(t, fmt.Sprintf("gateway-lab-%d-%02d", id.TokenID, slot), laneID.LaneID)
		require.EqualValues(t, 1, laneID.Epoch)
		require.Equal(t, id.UserID, laneID.UserID)
		require.Equal(t, id.TokenID, laneID.TokenID)
		require.Equal(t, id.TokenFingerprint, laneID.TokenFingerprint)
		lane := readLabLane(t, laneID)
		require.Equal(t, paidPerLane, lane.PaidAvailable)
		require.Equal(t, keyPerLane, lane.TokenAvailable)
		request := fmt.Sprintf("%s-use", laneID.LaneID)
		_, err := ReserveEnterpriseLane(ctx, DB, laneID, request, 7)
		require.NoError(t, err)
		_, err = MarkEnterpriseLaneDispatched(ctx, DB, laneID, request)
		require.NoError(t, err)
		for range 2 {
			owner, err := SettleEnterpriseLane(ctx, DB, laneID, request, actual)
			require.NoError(t, err)
			require.Equal(t, EnterpriseLaneSettled, owner.State)
			require.Equal(t, actual, owner.Actual)
		}
		lane = readLabLane(t, laneID)
		paid += lane.PaidAvailable
		key += lane.TokenAvailable
		spent += lane.Spent
		require.Zero(t, lane.Held)
		require.Zero(t, lane.OpenRequests)
	}
	require.Equal(t, 600_000_000, paid+spent)
	require.Equal(t, 600_000_000, key+spent)
	require.Equal(t, count*actual, spent)

	_, err = launch(count, paidPerLane, keyPerLane, true)
	require.NoError(t, err)
	_, err = launch(count, paidPerLane, keyPerLane, false)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	for _, laneID := range lanes {
		_, err := ReserveEnterpriseLane(ctx, DB, laneID, laneID.LaneID+"-stopped", 1)
		require.ErrorIs(t, err, ErrEnterpriseLaneState)
		for range 2 {
			lane, err := DrainEnterpriseLane(ctx, DB, laneID)
			require.NoError(t, err)
			require.Equal(t, EnterpriseLaneDrained, lane.State)
			require.Equal(t, paidPerLane-actual, lane.PaidReturned)
			require.Equal(t, keyPerLane-actual, lane.TokenReturned)
		}
	}
	checkLaneBaseAccounts(t, id, starting-count*actual, starting-count*actual, count*actual)
	var entries []BalanceLedger
	require.NoError(t, DB.Where("user_id = ? AND reference_type = ?", id.UserID, enterpriseLaneReference).Find(&entries).Error)
	require.Len(t, entries, 2*count)
	seen := map[string]int{}
	for _, entry := range entries {
		require.Equal(t, string(AccountPaid), entry.Account)
		require.Contains(t, []string{enterpriseLaneFund, enterpriseLaneReturn}, entry.Reason)
		seen[entry.ReferenceId] += entry.Delta
	}
	require.Len(t, seen, count)
	for _, amount := range seen {
		require.Equal(t, -actual, amount)
	}
}
