//go:build lane_billing_integration_dev

package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLaneWalletTipDevLogicalEvidencePostgres(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, *gorm.DB, LaneLogicalQuotaDevScope, SharedWalletShare)
	}{
		{"missing_local_slot", func(t *testing.T, db *gorm.DB, _ LaneLogicalQuotaDevScope, original SharedWalletShare) {
			require.NoError(t, db.Delete(&original).Error)
		}},
		{"self_consistent_stale_slot", func(t *testing.T, db *gorm.DB, _ LaneLogicalQuotaDevScope, original SharedWalletShare) {
			// A legitimate partial return leaves two immutable transfers. A
			// stale slot snapshot still has a matching *older* valid ledger tip.
			_, replay, err := ApplySharedWalletTransfer(t.Context(), db, SharedWalletTransfer{
				UserID: original.UserID, Epoch: original.Epoch, Slot: original.Slot,
				Direction: "reclaim", Amount: 15, OperationID: "tip-verified-partial-return",
			})
			require.NoError(t, err)
			require.False(t, replay)
			require.NoError(t, db.Save(&original).Error)
		}},
		{"transfer_only_older_epoch", func(t *testing.T, db *gorm.DB, scope LaneLogicalQuotaDevScope, original SharedWalletShare) {
			_, replay, err := ApplySharedWalletTransfer(t.Context(), db, SharedWalletTransfer{
				UserID: original.UserID, Epoch: scope.WalletEpoch - 1, Slot: 0,
				Direction: "grant", Amount: 1, OperationID: "tip-earlier-epoch",
			})
			require.NoError(t, err)
			require.False(t, replay)
			require.NoError(t, db.Where("user_id = ? AND epoch = ?", original.UserID, scope.WalletEpoch-1).Delete(&SharedWalletShare{}).Error)
		}},
		{"transfer_only_newer_epoch", func(t *testing.T, db *gorm.DB, scope LaneLogicalQuotaDevScope, original SharedWalletShare) {
			_, replay, err := ApplySharedWalletTransfer(t.Context(), db, SharedWalletTransfer{
				UserID: original.UserID, Epoch: scope.WalletEpoch + 1, Slot: SharedWalletMaxSlots - 1,
				Direction: "grant", Amount: 1, OperationID: "tip-later-epoch",
			})
			require.NoError(t, err)
			require.False(t, replay)
			require.NoError(t, db.Where("user_id = ? AND epoch = ?", original.UserID, scope.WalletEpoch+1).Delete(&SharedWalletShare{}).Error)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, a, b := laneLogicalQuotaFixture(t)
			initial := readLogicalQuotaFixture(t, db, a)
			require.Positive(t, initial.WalletPaidIdle)
			require.Positive(t, initial.KeyHeld)
			var wallet SharedWalletShare
			require.NoError(t, db.Where("user_id = ?", a.Key.UserID).Take(&wallet).Error)
			tc.change(t, db, a, wallet)
			var beforeRaw User
			require.NoError(t, db.First(&beforeRaw, a.Key.UserID).Error)
			// Both sibling views refuse the same broken wallet. No fabricated
			// zero or raw-only balance, and no best-effort reserve even when
			// this small amount would fit in ordinary raw balances alone.
			for _, scope := range []LaneLogicalQuotaDevScope{a, b} {
				view, err := ReadLaneLogicalQuotaDev(t.Context(), db, scope)
				require.ErrorIs(t, err, ErrSharedWalletEvidence)
				require.Zero(t, view)
			}
			const requestID = "tip-refuse-ordinary"
			_, attempt, err := ReserveLaneLogicalWalletFirstDev(t.Context(), db, a, requestID, 1)
			require.ErrorIs(t, err, ErrSharedWalletEvidence)
			require.False(t, attempt.Applied)
			var n int64
			require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", requestID).Count(&n).Error)
			require.Zero(t, n)
			var afterRaw User
			require.NoError(t, db.First(&afterRaw, a.Key.UserID).Error)
			require.Equal(t, beforeRaw.QuotaPaid, afterRaw.QuotaPaid)
		})
	}
}

// The generated probes must be backed by index range conditions, not an
// ORDER BY/LIMIT over the user's lifetime history. This is an indexability
// check against PG18.6, not a claimed default planner choice or latency budget.
func TestLaneWalletTipDevIndexablePostgres(t *testing.T) {
	db, a, _ := laneLogicalQuotaFixture(t)
	const index = "idx_lane_dev_wallet_transfer_slot_version"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("SET LOCAL enable_seqscan = off").Error)
		require.NoError(t, tx.Exec("SET LOCAL enable_bitmapscan = off").Error)
		explain := func(sql string, args ...any) string {
			rows, err := tx.Raw("EXPLAIN (COSTS OFF) "+sql, args...).Rows()
			require.NoError(t, err)
			defer rows.Close()
			var lines []string
			for rows.Next() {
				var line string
				require.NoError(t, rows.Scan(&line))
				lines = append(lines, line)
			}
			require.NoError(t, rows.Err())
			return strings.Join(lines, "\n")
		}
		tips := explain(laneWalletDevTipsSQL, SharedWalletMaxSlots-1, a.Key.UserID, a.WalletEpoch)
		require.Contains(t, tips, "Index Scan Backward using "+index, tips)
		require.Contains(t, tips, "slot = slots.slot", tips)
		edges := explain(laneWalletDevOutsideScopeSQL, a.Key.UserID, a.WalletEpoch, a.Key.UserID, a.WalletEpoch, SharedWalletMaxSlots)
		require.Equal(t, 2, strings.Count(edges, index), edges)
		require.Contains(t, edges, "ROW(epoch, slot) <", edges)
		require.Contains(t, edges, "ROW(epoch, slot) >=", edges)
		return nil
	}))
}
