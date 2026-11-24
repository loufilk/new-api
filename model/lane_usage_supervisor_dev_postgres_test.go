//go:build lane_billing_integration_dev

package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Only the local test fixture discovers a target. A future authorized runtime
// would have to supply the admitted pair, not bootstrap its own expected IDs.
func usageSupervisorEndpoint(t *testing.T, db *gorm.DB) LaneUsageEndpointDev {
	t.Helper()
	var e LaneUsageEndpointDev
	require.NoError(t, db.Raw(`SELECT host(inet_server_addr()), inet_server_port(), current_database(), current_schema(),
		(SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()),
		(SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=current_schema())`).Row().Scan(&e.Address, &e.Port, &e.Database, &e.Schema, &e.DatabaseOID, &e.SchemaOID))
	require.True(t, e.valid())
	return e
}

func usageSupervisor(t *testing.T, main, logs *gorm.DB, destination string, batch int) *LaneUsageSupervisorDev {
	t.Helper()
	w, err := NewLaneUsageSupervisorDev(main, logs, usageSupervisorEndpoint(t, main), usageSupervisorEndpoint(t, logs), destination, batch)
	require.NoError(t, err)
	return w
}

func TestLaneUsageSupervisorBoundedReportingPostgres(t *testing.T) {
	main, b, child, m := usageDevFixture(t)
	ctx := t.Context()
	logs, _ := splitUsageDevFixture(t)
	dest, err := InstallLaneBillingUsageLogDevSchema(ctx, logs)
	require.NoError(t, err)
	require.NoError(t, child.SettleMeasured(ctx, m))
	for _, id := range []string{"supervisor-second", "supervisor-third"} {
		f := b
		f.Owner.RequestID = id
		other, err := ReserveLaneBillingChildDev(ctx, main, LaneBillingChildDevBinding(f), 8)
		require.NoError(t, err)
		require.NoError(t, other.ClaimDispatch(ctx, f.FinalWireSHA256))
		measured := m
		measured.RequestID, measured.ActualQuota = id, 4
		require.NoError(t, other.SettleMeasured(ctx, measured))
	}
	wallet, key := childStoreBalances(t, main, b)
	raw := finiteKeyDevReadOriginal(t, main, finiteKeyDevIdentity(b))
	w := usageSupervisor(t, main, logs, dest, 1)
	_, err = w.Window(ctx, 9)
	require.ErrorIs(t, err, ErrLaneBillingOwnerUnavailable)
	first, err := w.Window(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 2, first.Pages)
	require.Equal(t, 2, first.Candidates)
	require.Equal(t, 2, first.Acknowledged)
	require.True(t, first.QueueObserved && first.Due)
	require.False(t, first.Deferred || first.Conflicts)
	// Second independent process: no reapply of the first two receipts.
	last, err := usageSupervisor(t, main, logs, dest, 2).Window(ctx, 8)
	require.NoError(t, err)
	require.Equal(t, 1, last.Pages)
	require.Equal(t, 1, last.Candidates)
	require.Equal(t, 1, last.Acknowledged)
	require.True(t, last.QueueObserved)
	require.False(t, last.Due || last.Deferred || last.Conflicts)
	usageDevStats(t, main, b, m.ActualQuota+8, 3)
	var count int64
	require.NoError(t, logs.Model(&Log{}).Where("type=?", LogTypeConsume).Count(&count).Error)
	require.EqualValues(t, 3, count)
	require.NoError(t, main.Model(&Log{}).Count(&count).Error)
	require.Zero(t, count)
	// Read through the ordinary user-id and original-Key log accessors, not
	// just direct SQL. Never return admin price or upstream diagnostics here.
	oldLogs := LOG_DB
	LOG_DB = logs
	t.Cleanup(func() { LOG_DB = oldLogs })
	byToken, err := GetLogByTokenId(b.Owner.TokenID)
	require.NoError(t, err)
	require.Len(t, byToken, 3)
	self, total, err := GetUserLogs(b.Owner.UserID, LogFilter{Type: LogTypeConsume, RequestId: m.RequestID}, 0, 5)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, self, 1)
	require.Equal(t, m.ActualQuota, self[0].Quota)
	require.Zero(t, self[0].ChannelId)
	require.NotContains(t, self[0].Other, b.FrozenPriceSHA256)
	require.NotContains(t, self[0].Other, b.Owner.TokenFingerprint)
	otherUser, total, err := GetUserLogs(b.Owner.UserID+901, LogFilter{RequestId: m.RequestID}, 0, 5)
	require.NoError(t, err)
	require.Empty(t, otherUser)
	require.Zero(t, total)
	walletAfter, keyAfter := childStoreBalances(t, main, b)
	require.Equal(t, wallet, walletAfter)
	require.Equal(t, key, keyAfter)
	rawAfter := finiteKeyDevReadOriginal(t, main, finiteKeyDevIdentity(b))
	require.Equal(t, raw.UsedQuota, rawAfter.UsedQuota)
	require.Equal(t, raw.RemainQuota, rawAfter.RemainQuota)
}

func TestLaneUsageSupervisorPinsMainAndDestinationPostgres(t *testing.T) {
	main, _, child, m := usageDevFixture(t)
	ctx := t.Context()
	logs, _ := splitUsageDevFixture(t)
	dest, err := InstallLaneBillingUsageLogDevSchema(ctx, logs)
	require.NoError(t, err)
	mScope, lScope := usageSupervisorEndpoint(t, main), usageSupervisorEndpoint(t, logs)
	for _, pending := range []bool{false, true} {
		if pending {
			require.NoError(t, child.SettleMeasured(ctx, m))
		}
		for _, swap := range []string{"main", "logs", "token"} {
			a, b, id := mScope, lScope, dest
			switch swap {
			case "main":
				a.SchemaOID++
			case "logs":
				b.SchemaOID++
			case "token":
				id = strings.Repeat("e", 64)
			}
			w, err := NewLaneUsageSupervisorDev(main, logs, a, b, id, 1)
			require.NoError(t, err)
			reports := 0
			err = w.Run(ctx, 100*time.Millisecond, func(out LaneUsageWindowDev, err error) {
				reports++
				require.ErrorIs(t, err, ErrLaneBillingOwnerUnavailable)
				require.Zero(t, out.Acknowledged)
				if swap == "main" {
					require.False(t, out.QueueObserved)
				}
			})
			require.ErrorIs(t, err, ErrLaneBillingOwnerUnavailable)
			require.Equal(t, 1, reports)
			if pending {
				row := readUsageOutbox(t, main, m.RequestID)
				require.Zero(t, row.Attempt)
				require.Empty(t, row.Destination)
			}
		}
	}
}

func TestLaneUsageSupervisorLeaseConflictStopPostgres(t *testing.T) {
	main, b, child, m := usageDevFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dest, err := InstallLaneBillingUsageLogDevSchema(ctx, main)
	require.NoError(t, err)
	require.NoError(t, child.SettleMeasured(ctx, m))
	w := usageSupervisor(t, main, main, dest, 3)
	// Simulate the prior process stopping after destination commit but before
	// main counters/ack. A new process must not claim to be drained meanwhile.
	claimed, err := w.main.claim(ctx, m.RequestID, dest, true)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	_, err = w.logs.writeLog(ctx, *claimed)
	require.NoError(t, err)
	win, err := w.Window(ctx, 8)
	require.NoError(t, err)
	require.True(t, win.QueueObserved && win.Deferred)
	require.False(t, win.Due || win.Conflicts)
	require.Equal(t, 1, win.Pages)
	require.Zero(t, win.Candidates)
	usageDevStats(t, main, b, 0, 0)
	// Orphan receipt: never adopt/recreate a deleted consume log.
	require.NoError(t, main.Where("request_id=?", m.RequestID).Delete(&Log{}).Error)
	dueUsageDev(t, main, m.RequestID)
	calls := 0
	err = w.Run(ctx, 100*time.Millisecond, func(out LaneUsageWindowDev, err error) {
		calls++
		require.True(t, out.QueueObserved && out.Conflicts)
		require.Zero(t, out.Acknowledged)
		_, busy := w.Window(ctx, 1)
		require.ErrorIs(t, busy, ErrLaneUsageSupervisorBusyDev)
		if calls == 1 {
			require.ErrorIs(t, err, ErrLaneBillingUsageProjectionConflict)
			return
		}
		require.NoError(t, err)
		require.Zero(t, out.Candidates)
		cancel()
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 2, calls)
	require.Equal(t, laneUsageConflict, readUsageOutbox(t, main, m.RequestID).State)
	usageDevStats(t, main, b, 0, 0)
}
