package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestLegacyQuotaNormalizationRejectsInvalidBalanceWithoutWrites(t *testing.T) {
	for _, tc := range []struct {
		name  string
		quota int
		err   error
	}{
		{name: "negative", quota: -1, err: ErrLedgerInvalidEntry},
		{name: "negative_wallet_boundary", quota: -common.MaxWalletQuota, err: ErrLedgerInvalidEntry},
		{name: "above_wallet_limit", quota: common.MaxWalletQuota + 1, err: ErrWalletQuotaLimitExceeded},
	} {
		for _, path := range []string{"backfill", "first_debit"} {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				setupLedgerTestState(t)
				const userID = 40901
				require.NoError(t, DB.Create(&User{Id: userID, Username: "invalid-legacy", AffCode: "invalid-legacy", Quota: tc.quota, Status: common.UserStatusEnabled}).Error)
				before := getUserBalances(t, userID)
				for range 2 {
					if path == "backfill" {
						affected, err := BackfillLegacyQuotaAccountsWithDB(DB)
						require.ErrorIs(t, err, tc.err)
						require.Zero(t, affected)
					} else {
						_, err := DebitQuota(LedgerEntry{UserId: userID, Delta: 1, Reason: LedgerReasonBillingPreConsume, IdempotencyKey: "invalid-legacy-first-debit"}, AccountPaid)
						require.ErrorIs(t, err, tc.err)
					}
					require.Equal(t, before, getUserBalances(t, userID), "rejection must not alter any legacy balance or user field")
					var operations, rows int64
					require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("user_id = ?", userID).Count(&operations).Error)
					require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ?", userID).Count(&rows).Error)
					require.Zero(t, operations, "rejection must not leave an idempotency header")
					require.Zero(t, rows, "rejection must not create usable paid credit")
				}
			})
		}
	}
}

func TestLegacyQuotaBackfillWalletBoundaryAndPerUserTransactions(t *testing.T) {
	setupLedgerTestState(t)
	for i, quota := range []int{common.MaxWalletQuota, -1, 17} {
		id := 40911 + i
		require.NoError(t, DB.Create(&User{Id: id, Username: fmt.Sprintf("legacy-boundary-%d", id), AffCode: fmt.Sprintf("legacy-boundary-%d", id), Quota: quota, Status: common.UserStatusEnabled}).Error)
	}

	affected, err := BackfillLegacyQuotaAccountsWithDB(DB)
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	require.Equal(t, int64(1), affected, "only the earlier valid user's transaction committed")
	first := getUserBalances(t, 40911)
	require.Equal(t, common.MaxWalletQuota, first.Quota)
	require.Equal(t, common.MaxWalletQuota, first.QuotaPaid)
	require.Zero(t, first.QuotaBonus)
	require.Zero(t, first.QuotaReward)
	require.Equal(t, int64(1), countLedgerRows(t, "migration:legacy_quota:40911"))
	for _, id := range []int{40912, 40913} {
		u := getUserBalances(t, id)
		require.Zero(t, u.QuotaPaid)
		require.Zero(t, u.QuotaBonus)
		require.Zero(t, u.QuotaReward)
		require.Zero(t, countLedgerRows(t, fmt.Sprintf("migration:legacy_quota:%d", id)))
		var operations int64
		require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("user_id = ?", id).Count(&operations).Error)
		require.Zero(t, operations)
	}
	require.Equal(t, -1, getUserBalances(t, 40912).Quota)
	require.Equal(t, 17, getUserBalances(t, 40913).Quota)

	// A retry remains blocked on the same invalid user and does not duplicate
	// the previously committed boundary-sized migration.
	affected, err = BackfillLegacyQuotaAccountsWithDB(DB)
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	require.Zero(t, affected)
	require.Equal(t, int64(1), countLedgerRows(t, "migration:legacy_quota:40911"))
}
