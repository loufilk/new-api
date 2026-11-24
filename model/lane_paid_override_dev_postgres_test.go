//go:build lane_billing_integration_dev

package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func applyTestLanePaidOverride(t *testing.T, db *gorm.DB, entry LedgerEntry, epoch int64) (*LedgerResult, error) {
	t.Helper()
	var schema string
	require.NoError(t, db.Raw("SELECT current_schema()").Scan(&schema).Error)
	var r *LedgerResult
	err := db.WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
		require.NoError(t, laneBillingFiniteKeyDevScopeTx(tx))
		var err error
		r, err = applyLanePaidOverrideTx(tx, lanePaidOverrideCustody{
			tx: tx, userID: entry.UserId, schema: schema, walletEpoch: epoch,
			operatorID: entry.OperatorId, requestID: entry.ReferenceId, target: entry.Delta,
		}, entry)
		return err
	})
	return r, err
}

func TestLanePaidOverrideUsesCanonicalExactLedgerPostgres(t *testing.T) {
	db, owner, _ := childStoreFixture(t)
	entry := LedgerEntry{UserId: owner.Owner.UserID, OperatorId: 800, Account: AccountPaid, Delta: 45,
		Reason: LedgerReasonAdminOverride, ReferenceType: "admin_quota", ReferenceId: "exact", IdempotencyKey: "admin_quota:800:exact"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := CreditQuotaTx(tx, LedgerEntry{UserId: entry.UserId, Account: AccountReward, Delta: 7,
			Reason: LedgerReasonAdminAdd, IdempotencyKey: "fixture-reward"})
		return err
	}))
	var before User
	require.NoError(t, db.First(&before, entry.UserId).Error)
	var old SharedWalletShare
	require.NoError(t, db.Where("user_id = ?", entry.UserId).First(&old).Error)
	require.Positive(t, old.Available)
	require.Less(t, entry.Delta, old.Available, "fixture exposes raw, idle-retaining, and lowering-target errors")

	// The old writer must still refuse, even if the requested value equals raw
	// or uses the very same idempotency key intended for the scoped writer.
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := OverrideQuotaTx(tx, entry)
		return err
	})
	laneOverrideDenied(t, err)
	r, err := applyTestLanePaidOverride(t, db, entry, owner.WalletEpoch)
	require.NoError(t, err)
	require.False(t, r.Replayed)
	require.Equal(t, before.Quota+old.Available, r.TotalBefore)
	require.Equal(t, entry.Delta+before.QuotaReward, r.TotalAfter)
	require.Len(t, r.Rows, 1)
	require.Equal(t, entry.Delta-(before.QuotaPaid+old.Available), r.Rows[0].Delta)
	require.Equal(t, entry.Delta, r.Rows[0].BalanceAfter)
	var after User
	require.NoError(t, db.First(&after, entry.UserId).Error)
	require.Equal(t, entry.Delta, after.QuotaPaid)
	require.Equal(t, entry.Delta+before.QuotaReward, after.Quota)
	require.Equal(t, before.QuotaReward, after.QuotaReward)
	require.Equal(t, before.QuotaBonus, after.QuotaBonus)
	var share SharedWalletShare
	require.NoError(t, db.Where("user_id = ?", entry.UserId).First(&share).Error)
	require.Zero(t, share.Available)
	require.Equal(t, old.Returned+old.Available, share.Returned)
	require.Equal(t, old.Spent, share.Spent)

	replay, err := applyTestLanePaidOverride(t, db, entry, owner.WalletEpoch)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, r.TotalBefore, replay.TotalBefore)
	require.Equal(t, r.Rows, replay.Rows)
	changed := entry
	changed.Delta++
	_, err = applyTestLanePaidOverride(t, db, changed, owner.WalletEpoch)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)
	_, err = applyTestLanePaidOverride(t, db, entry, owner.WalletEpoch+1)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)
	var n int64
	require.NoError(t, db.Model(&SharedWalletTransfer{}).Where("user_id = ? AND direction = 'reclaim'", entry.UserId).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestLanePaidOverrideRejectsWriteWithWrongScopeOrHoldPostgres(t *testing.T) {
	db, owner, _ := childStoreFixture(t)
	entry := LedgerEntry{UserId: owner.Owner.UserID, OperatorId: 800, Account: AccountPaid, Delta: 45,
		Reason: LedgerReasonAdminOverride, ReferenceType: "admin_quota", ReferenceId: "held", IdempotencyKey: "admin_quota:800:held"}
	tx := db.Begin()
	require.NoError(t, tx.Error)
	_, err := applyLanePaidOverrideTx(db, lanePaidOverrideCustody{tx: tx}, entry)
	require.ErrorIs(t, err, ErrLanePaidOverrideAuthority)
	require.NoError(t, tx.Rollback().Error)
	var old SharedWalletShare
	require.NoError(t, db.Where("user_id = ?", entry.UserId).First(&old).Error)
	require.Positive(t, old.Available)
	require.NoError(t, db.Model(&SharedWalletShare{}).Where("user_id = ?", entry.UserId).
		Updates(map[string]any{"available": old.Available - 1, "held": 1}).Error)
	_, err = applyTestLanePaidOverride(t, db, entry, owner.WalletEpoch)
	require.ErrorIs(t, err, ErrLanePaidOverrideHeld)

	require.NoError(t, db.Model(&SharedWalletShare{}).Where("user_id = ?", entry.UserId).
		Updates(map[string]any{"available": old.Available, "held": 0}).Error)
	// Inject a bad latest-row operation; the older valid ledger is still in
	// the store but must not be selected to make the available amount look safe.
	bad := SharedWalletTransfer{UserID: entry.UserId, Epoch: owner.WalletEpoch, Slot: old.Slot,
		OperationID: "orphan-tip", Direction: "grant", Amount: 1, ShareVersion: old.Version + 1}
	require.NoError(t, db.Create(&bad).Error)
	_, err = applyTestLanePaidOverride(t, db, entry, owner.WalletEpoch)
	require.ErrorIs(t, err, ErrSharedWalletEvidence)
	var n int64
	require.NoError(t, db.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", entry.IdempotencyKey).Count(&n).Error)
	require.Zero(t, n)
	var after SharedWalletShare
	require.NoError(t, db.Where("user_id = ?", entry.UserId).First(&after).Error)
	require.Equal(t, old, after)
}

func TestLanePaidOverrideRollsBackReclaimWithWriterPostgres(t *testing.T) {
	db, owner, _ := childStoreFixture(t)
	entry := LedgerEntry{UserId: owner.Owner.UserID, OperatorId: 800, Account: AccountPaid, Delta: 45,
		Reason: LedgerReasonAdminOverride, ReferenceType: "admin_quota", ReferenceId: "rollback", IdempotencyKey: "admin_quota:800:rollback"}
	var u User
	require.NoError(t, db.First(&u, entry.UserId).Error)
	var s SharedWalletShare
	require.NoError(t, db.Where("user_id = ?", entry.UserId).First(&s).Error)
	rollback := errors.New("synthetic outer rollback")
	err := db.Transaction(func(tx *gorm.DB) error {
		var schema string
		require.NoError(t, tx.Raw("SELECT current_schema()").Scan(&schema).Error)
		require.True(t, common.UsingMainDatabase(common.DatabaseTypePostgreSQL))
		_, err := applyLanePaidOverrideTx(tx, lanePaidOverrideCustody{
			tx: tx, userID: entry.UserId, schema: schema, walletEpoch: owner.WalletEpoch,
			operatorID: entry.OperatorId, requestID: entry.ReferenceId, target: entry.Delta,
		}, entry)
		require.NoError(t, err)
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	var current User
	require.NoError(t, db.First(&current, entry.UserId).Error)
	require.Equal(t, u.QuotaPaid, current.QuotaPaid)
	var retained SharedWalletShare
	require.NoError(t, db.Where("user_id = ?", entry.UserId).First(&retained).Error)
	require.Equal(t, s, retained)
	var n int64
	require.NoError(t, db.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", entry.IdempotencyKey).Count(&n).Error)
	require.Zero(t, n)
}

func TestLanePaidOverrideKeepsOrdinaryNamespacePostgres(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires isolated loopback PostgreSQL")
	}
	require.False(t, LaneStoreRuntimeCapable)
	// The ordinary test-main schema contains no fixture custody tables.
	require.False(t, DB.Migrator().HasTable(&SharedWalletShare{}))
	u := User{Username: "ordinary-paid-absolute", AffCode: "ordinary-absolute", Status: common.UserStatusEnabled, QuotaPaid: 20, Quota: 20}
	require.NoError(t, DB.Create(&u).Error)
	observed, err := AdjustUserQuota(u.Id, common.RoleRootUser, "override", 50, LedgerEntry{
		OperatorId: 800, ReferenceType: "admin_quota", ReferenceId: "ordinary", IdempotencyKey: "admin_quota:800:ordinary",
	})
	require.NoError(t, err)
	require.False(t, observed.Replayed)
	require.Equal(t, 20, observed.Before)
	require.Equal(t, 50, observed.After)
}

func TestLanePaidOverrideReplayRequiresExactDetailPostgres(t *testing.T) {
	for _, mode := range []string{"missing", "wrong_account", "wrong_metadata", "unexpected_noop_detail", "genuine_noop"} {
		t.Run(mode, func(t *testing.T) {
			db, owner, _ := childStoreFixture(t)
			entry := LedgerEntry{UserId: owner.Owner.UserID, OperatorId: 800, Account: AccountPaid, Delta: 45,
				Reason: LedgerReasonAdminOverride, ReferenceType: "admin_quota", ReferenceId: "replay-evidence", IdempotencyKey: "admin_quota:800:replay-evidence"}
			noOp := mode == "genuine_noop" || mode == "unexpected_noop_detail"
			if noOp {
				entry.Delta = 200 // full raw+idle, not the raw balance
			}
			initial, err := applyTestLanePaidOverride(t, db, entry, owner.WalletEpoch)
			require.NoError(t, err)
			_, err = applyTestLanePaidOverride(t, db, entry, owner.WalletEpoch)
			require.NoError(t, err)
			if noOp {
				require.Empty(t, initial.Rows)
			} else {
				require.Len(t, initial.Rows, 1)
			}
			switch mode {
			case "missing":
				require.NoError(t, db.Exec("DELETE FROM balance_ledger WHERE id = ?", initial.Rows[0].Id).Error)
			case "wrong_account":
				require.NoError(t, db.Exec("UPDATE balance_ledger SET account = ? WHERE id = ?", AccountReward, initial.Rows[0].Id).Error)
			case "wrong_metadata":
				require.NoError(t, db.Exec("UPDATE balance_ledger SET metadata = ? WHERE id = ?", "tampered-evidence", initial.Rows[0].Id).Error)
			case "unexpected_noop_detail":
				var row BalanceLedgerOperation
				require.NoError(t, db.Where("idempotency_key = ?", entry.IdempotencyKey).Take(&row).Error)
				require.NoError(t, db.Create(&BalanceLedger{OperationId: row.Id, UserId: row.UserId, Account: string(AccountPaid),
					BalanceBefore: 200, BalanceAfter: 200, TotalBefore: 200, TotalAfter: 200,
					Reason: entry.Reason, ReferenceType: entry.ReferenceType, ReferenceId: entry.ReferenceId, IdempotencyKey: entry.IdempotencyKey, OperatorId: entry.OperatorId}).Error)
			}
			var old SharedWalletShare
			require.NoError(t, db.Where("user_id = ?", entry.UserId).Take(&old).Error)
			onReplay, err := applyTestLanePaidOverride(t, db, entry, owner.WalletEpoch)
			if mode == "genuine_noop" {
				require.NoError(t, err)
				require.True(t, onReplay.Replayed)
				require.Empty(t, onReplay.Rows)
			} else {
				require.ErrorIs(t, err, ErrSharedWalletEvidence)
				require.Nil(t, onReplay)
			}
			var after SharedWalletShare
			require.NoError(t, db.Where("user_id = ?", entry.UserId).Take(&after).Error)
			require.Equal(t, old, after)
		})
	}
}
