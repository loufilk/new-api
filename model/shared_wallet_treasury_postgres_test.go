package model

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestMain vets TEST_MODEL_POSTGRES_DSN for loopback and PG 18.6/18.7.
// The first subtest exercises the pre-upgrade schema; later subtests share the
// explicitly migrated schema but use different wallets.
func TestSharedWalletTreasuryPostgres(t *testing.T) {
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated loopback TEST_MODEL_POSTGRES_DSN")
	}
	db := DB
	ctx := t.Context()
	makeWallet := func(t *testing.T, paid int) User {
		t.Helper()
		suffix := common.GetUUID()
		u := User{Username: suffix[:16], AffCode: suffix[:6], Status: common.UserStatusEnabled,
			Quota: paid, QuotaPaid: paid, UsedQuota: 37, RequestCount: 5}
		require.NoError(t, db.Create(&u).Error)
		return u
	}
	readPaid := func(t *testing.T, id int) int {
		t.Helper()
		var u User
		require.NoError(t, db.Select("quota", "quota_paid").First(&u, id).Error)
		require.Equal(t, u.QuotaPaid, u.Quota)
		return u.QuotaPaid
	}
	readShare := func(t *testing.T, op SharedWalletTransfer) SharedWalletShare {
		t.Helper()
		var share SharedWalletShare
		require.NoError(t, db.Where("user_id = ? AND epoch = ? AND slot = ?", op.UserID, op.Epoch, op.Slot).Take(&share).Error)
		require.Equal(t, share.Granted, share.Available+share.Held+share.Spent+share.Returned)
		return share
	}
	assertEffects := func(t *testing.T, u User, paid int, transfers, ledgerOps, shares int64) {
		t.Helper()
		require.Equal(t, paid, readPaid(t, u.Id))
		for _, spec := range []struct {
			model any
			count int64
		}{
			{&SharedWalletTransfer{}, transfers}, {&SharedWalletShare{}, shares},
			{&BalanceLedgerOperation{}, ledgerOps},
		} {
			var n int64
			require.NoError(t, db.Model(spec.model).Where("user_id = ?", u.Id).Count(&n).Error)
			require.Equal(t, spec.count, n, "count for %T", spec.model)
		}
	}
	grant := func(u User, id string, slot, amount int) SharedWalletTransfer {
		return SharedWalletTransfer{UserID: u.Id, Epoch: 1, Slot: slot, Direction: "grant", OperationID: id, Amount: amount}
	}
	require.NoError(t, db.AutoMigrate(&BillingFinalization{}))
	t.Run("existing_three_keys_upgrade_and_reclaim", func(t *testing.T) {
		u := makeWallet(t, 1000)
		op := grant(u, "initial", 0, 700)
		keys := make([]Token, 3)
		owners := make([]BillingFinalization, len(keys))
		for i := range keys {
			keys[i] = Token{UserId: u.Id, Key: fmt.Sprintf("synthetic-%d-%d", u.Id, i), Status: common.TokenStatusEnabled,
				ExpiredTime: -1, RemainQuota: 500 - i, UsedQuota: 20 + i}
			require.NoError(t, db.Create(&keys[i]).Error)
			owners[i] = BillingFinalization{RequestID: fmt.Sprintf("treasury-%d-%d", u.Id, i), UserID: u.Id,
				TokenID: keys[i].Id, TokenFingerprint: BillingTokenFingerprint(keys[i].Key),
				Source: "wallet", State: BillingSettled, ActualQuota: keys[i].UsedQuota}
		}
		require.NoError(t, db.Create(&owners).Error)
		if !db.Migrator().HasTable(&SharedWalletTransfer{}) {
			_, _, err := ApplySharedWalletTransfer(ctx, db, op)
			require.Error(t, err, "base schema cannot allocate")
			require.Equal(t, 1000, readPaid(t, u.Id))
			var n int64
			require.NoError(t, db.Model(&BalanceLedgerOperation{}).Where("user_id = ?", u.Id).Count(&n).Error)
			require.Zero(t, n)
		}
		require.NoError(t, db.AutoMigrate(SharedWalletTreasuryModels()...))
		first, replay, err := ApplySharedWalletTransfer(ctx, db, op)
		require.NoError(t, err)
		require.False(t, replay)
		require.Positive(t, first.ShareVersion)
		assertEffects(t, u, 300, 1, 1, 1)
		for range 2 {
			require.NoError(t, db.AutoMigrate(SharedWalletTreasuryModels()...))
		}
		for _, edit := range []func(*SharedWalletTransfer){
			func(v *SharedWalletTransfer) { v.Direction = "reclaim" },
			func(v *SharedWalletTransfer) { v.Epoch++ },
			func(v *SharedWalletTransfer) { v.Slot++ },
			func(v *SharedWalletTransfer) { v.Amount++ },
		} {
			changed := op
			edit(&changed)
			_, _, err := ApplySharedWalletTransfer(ctx, db, changed)
			require.ErrorIs(t, err, ErrSharedWalletEvidence)
		}
		_, err = DebitQuota(LedgerEntry{UserId: u.Id, Delta: 301, Reason: LedgerReasonBillingPreConsume,
			ReferenceType: "relay", ReferenceId: "raw-only", IdempotencyKey: fmt.Sprintf("raw-only-%d", u.Id)}, AccountPaid)
		require.ErrorIs(t, err, ErrLedgerInsufficientBalance, "ordinary debit has no reclaim integration")
		assertEffects(t, u, 300, 1, 1, 1)

		// An existing held/spent split is a synthetic fixture, not a dispatch API.
		require.NoError(t, db.Model(&SharedWalletShare{}).Where("user_id = ?", u.Id).
			Updates(map[string]any{"available": 500, "held": 170, "spent": 30, "version": first.ShareVersion + 1}).Error)
		back := op
		back.Direction, back.OperationID, back.Amount = "reclaim", "return", 501
		_, _, err = ApplySharedWalletTransfer(ctx, db, back)
		require.ErrorIs(t, err, ErrSharedWalletCapacity, "cannot reclaim held or spent units")
		assertEffects(t, u, 300, 1, 1, 1)
		back.Amount = 500
		_, replay, err = ApplySharedWalletTransfer(ctx, db, back)
		require.NoError(t, err)
		require.False(t, replay)
		assertEffects(t, u, 800, 2, 2, 1)
		s := readShare(t, op)
		require.Equal(t, [5]int{700, 0, 170, 30, 500}, [5]int{s.Granted, s.Available, s.Held, s.Spent, s.Returned})
		got, replay, err := ApplySharedWalletTransfer(ctx, db, op)
		require.NoError(t, err)
		require.True(t, replay)
		require.Equal(t, first, got, "retry returns original proof, not newer share version")
		assertEffects(t, u, 800, 2, 2, 1)
		var rows []BalanceLedger
		require.NoError(t, db.Where("user_id = ?", u.Id).Order("id").Find(&rows).Error)
		require.Len(t, rows, 2)
		for i, original := range keys {
			var saved Token
			require.NoError(t, db.First(&saved, original.Id).Error)
			require.Equal(t, [2]int{original.RemainQuota, original.UsedQuota}, [2]int{saved.RemainQuota, saved.UsedQuota})
			var owner BillingFinalization
			require.NoError(t, db.Where("request_id = ?", owners[i].RequestID).Take(&owner).Error)
			require.Equal(t, owners[i], owner)
		}
		for i, op := range []SharedWalletTransfer{op, back} {
			require.Equal(t, "wallet_share_transfer", rows[i].ReferenceType)
			require.Equal(t, "wallet_share_"+op.Direction, rows[i].Reason)
			require.Equal(t, string(AccountPaid), rows[i].Account)
		}
		var userAfter User
		require.NoError(t, db.First(&userAfter, u.Id).Error)
		require.Equal(t, u.UsedQuota, userAfter.UsedQuota, "treasury transfer is not customer consumption")
		require.Equal(t, u.RequestCount, userAfter.RequestCount)
		var consumeLogs int64
		require.NoError(t, db.Model(&Log{}).Where("user_id = ? AND type = ?", u.Id, LogTypeConsume).Count(&consumeLogs).Error)
		require.Zero(t, consumeLogs)
		require.Equal(t, -700, rows[0].Delta)
		require.Equal(t, 500, rows[1].Delta)
	})

	t.Run("mismatched_process_dialect_cannot_disable_row_locks", func(t *testing.T) {
		u := makeWallet(t, 1000)
		op := grant(u, "wrong-process-dialect", 0, 600)
		common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
		defer common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
		_, _, err := ApplySharedWalletTransfer(ctx, db, op)
		require.ErrorIs(t, err, ErrSharedWalletScope)
		assertEffects(t, u, 1000, 0, 0, 0)
	})

	t.Run("rollback_failure_after_ledger_and_share_writes", func(t *testing.T) {
		for _, table := range []string{"shared_wallet_shares", "shared_wallet_transfers"} {
			t.Run(table, func(t *testing.T) {
				u := makeWallet(t, 1000)
				op := grant(u, "fault", 1, 600)
				fault := errors.New("synthetic treasury write failure")
				name := "test:treasury-fault-" + table
				cb := db.Callback().Create()
				require.NoError(t, cb.After("gorm:create").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
				_, _, err := ApplySharedWalletTransfer(ctx, db, op)
				require.NoError(t, cb.Remove(name))
				require.ErrorIs(t, err, fault)
				assertEffects(t, u, 1000, 0, 0, 0)
				_, _, err = ApplySharedWalletTransfer(ctx, db, op)
				require.NoError(t, err)
				assertEffects(t, u, 400, 1, 1, 1)
			})
		}
	})

	t.Run("commit_ack_lost_grant_and_reclaim", func(t *testing.T) {
		u := makeWallet(t, 1000)
		op := grant(u, "grant-lost", 0, 600)
		back := SharedWalletTransfer{UserID: u.Id, Epoch: op.Epoch, Slot: op.Slot,
			OperationID: "return-lost", Direction: "reclaim", Amount: 300}
		pool, err := db.DB()
		require.NoError(t, err)
		lost := db.Session(&gorm.Session{NewDB: true, Context: ctx})
		lost.Statement.ConnPool = loseQuotaCommitAck{pool}
		for i, v := range []SharedWalletTransfer{op, back} {
			_, _, err := ApplySharedWalletTransfer(ctx, lost, v)
			require.ErrorContains(t, err, "synthetic lost acknowledgment")
			receipt, replay, err := ApplySharedWalletTransfer(ctx, db, v)
			require.NoError(t, err)
			require.True(t, replay)
			require.EqualValues(t, i+1, receipt.ShareVersion)
		}
		assertEffects(t, u, 700, 2, 2, 1)
		require.Equal(t, 300, readShare(t, op).Available)
	})

	t.Run("orphan_evidence_is_not_adopted", func(t *testing.T) {
		u := makeWallet(t, 1000)
		op := grant(u, "only-ledger", 0, 100)
		_, err := DebitQuota(op.ledgerEntry(), AccountPaid)
		require.NoError(t, err)
		_, _, err = ApplySharedWalletTransfer(ctx, db, op)
		require.ErrorIs(t, err, ErrSharedWalletEvidence)
		assertEffects(t, u, 900, 0, 1, 0)

		good := grant(u, "valid", 1, 100)
		receipt, _, err := ApplySharedWalletTransfer(ctx, db, good)
		require.NoError(t, err)
		receipt.OperationID = "only-receipt"
		require.NoError(t, db.Create(&receipt).Error)
		onlyReceipt := good
		onlyReceipt.OperationID = receipt.OperationID
		_, _, err = ApplySharedWalletTransfer(ctx, db, onlyReceipt)
		require.ErrorIs(t, err, ErrSharedWalletEvidence)
		assertEffects(t, u, 800, 2, 2, 1)
	})

	t.Run("reclaim_rejects_unproven_share", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			corrupt string
		}{
			{"forged_share", ""},
			{"wrong_latest_operation", "UPDATE shared_wallet_shares SET last_operation_id = 'missing' WHERE user_id = ?"},
			{"wrong_cumulative_totals", "UPDATE shared_wallet_shares SET returned = 1, available = available - 1 WHERE user_id = ?"},
			{"missing_signed_ledger", "DELETE FROM balance_ledger_operations WHERE user_id = ?"},
			{"mismatched_ledger_reference", "UPDATE balance_ledger SET reference_id = 'not-the-original-checkpoint' WHERE user_id = ?"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				u := makeWallet(t, 1000)
				op := grant(u, "grant-before-reclaim", 0, 100)
				paid := 1000
				if tc.corrupt == "" {
					share := SharedWalletShare{UserID: u.Id, Epoch: op.Epoch, Slot: op.Slot,
						Granted: 100, Available: 100, Version: 1, LastOperationID: op.OperationID}
					require.NoError(t, db.Create(&share).Error)
				} else {
					_, _, err := ApplySharedWalletTransfer(ctx, db, op)
					require.NoError(t, err)
					paid = 900
					require.NoError(t, db.Exec(tc.corrupt, u.Id).Error)
				}
				reclaim := op
				reclaim.OperationID, reclaim.Direction = "new-reclaim", "reclaim"
				_, _, err := ApplySharedWalletTransfer(ctx, db, reclaim)
				require.ErrorIs(t, err, ErrSharedWalletEvidence)
				require.Equal(t, paid, readPaid(t, u.Id))
				var n int64
				require.NoError(t, db.Model(&SharedWalletTransfer{}).Where("user_id = ? AND direction = ?", u.Id, "reclaim").Count(&n).Error)
				require.Zero(t, n)
			})
		}
	})

	t.Run("lifetime_slot_grants_are_bounded_after_return", func(t *testing.T) {
		u := makeWallet(t, common.MaxWalletQuota)
		op := grant(u, "full-grant", 0, common.MaxWalletQuota)
		_, _, err := ApplySharedWalletTransfer(ctx, db, op)
		require.NoError(t, err)
		back := op
		back.OperationID, back.Direction = "full-return", "reclaim"
		_, _, err = ApplySharedWalletTransfer(ctx, db, back)
		require.NoError(t, err)
		previous := readShare(t, op)
		require.Equal(t, common.MaxWalletQuota, previous.Returned)
		require.Zero(t, previous.Available)
		refill := grant(u, "after-return", 0, 1)
		_, _, err = ApplySharedWalletTransfer(ctx, db, refill)
		require.ErrorIs(t, err, ErrSharedWalletCapacity)
		assertEffects(t, u, common.MaxWalletQuota, 2, 2, 1)
		require.Equal(t, previous, readShare(t, op))
	})

	t.Run("logical_wallet_does_not_exceed_safe_domain", func(t *testing.T) {
		u := makeWallet(t, common.MaxWalletQuota)
		op := grant(u, "fund", 0, 100)
		_, _, err := ApplySharedWalletTransfer(ctx, db, op)
		require.NoError(t, err)
		previous := readShare(t, op)
		// A legacy raw-only writer can ignore escrow. Returning or moving
		// quota must not proceed once that violates the combined domain.
		require.NoError(t, db.Model(&User{}).Where("id = ?", u.Id).
			Updates(map[string]any{"quota": common.MaxWalletQuota, "quota_paid": common.MaxWalletQuota}).Error)
		back := op
		back.OperationID, back.Direction, back.Amount = "return", "reclaim", 1
		for _, v := range []SharedWalletTransfer{back, grant(u, "new-slot", 1, 1)} {
			_, _, err := ApplySharedWalletTransfer(ctx, db, v)
			require.ErrorIs(t, err, ErrSharedWalletCapacity)
		}
		assertEffects(t, u, common.MaxWalletQuota, 1, 1, 1)
		require.Equal(t, previous, readShare(t, op))
	})

	t.Run("concurrent_exact_grant_creates_one_receipt", func(t *testing.T) {
		u := makeWallet(t, 1000)
		op := grant(u, "same-operation", 0, 600)
		type outcome struct {
			receipt  SharedWalletTransfer
			replayed bool
			err      error
		}
		results := make(chan outcome, 2)
		start := make(chan struct{})
		var workers sync.WaitGroup
		for range 2 {
			workers.Go(func() {
				<-start
				r, replayed, err := ApplySharedWalletTransfer(ctx, db, op)
				results <- outcome{r, replayed, err}
			})
		}
		close(start)
		workers.Wait()
		close(results)
		var original SharedWalletTransfer
		replays := 0
		for got := range results {
			require.NoError(t, got.err)
			if original.OperationID == "" {
				original = got.receipt
			} else {
				require.Equal(t, original, got.receipt)
			}
			if got.replayed {
				replays++
			}
		}
		require.Equal(t, 1, replays)
		assertEffects(t, u, 400, 1, 1, 1)
	})

	t.Run("allocators_share_one_paid_budget", func(t *testing.T) {
		u := makeWallet(t, 1000)
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, 2)
		for i := range 2 {
			wg.Go(func() {
				<-start
				_, _, err := ApplySharedWalletTransfer(ctx, db, grant(u, fmt.Sprintf("race-%d", i), i, 600))
				errs <- err
			})
		}
		close(start)
		wg.Wait()
		close(errs)
		success := 0
		for err := range errs {
			if err == nil {
				success++
			} else {
				require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
			}
		}
		require.Equal(t, 1, success)
		assertEffects(t, u, 400, 1, 1, 1)
	})
}

// Synthetic Dialectors have no connection pool; both rejected calls must
// return before any transaction or SQL attempt.
type treasuryScopeDialect struct {
	gorm.Dialector
	name string
}

func (d treasuryScopeDialect) Name() string { return d.name }

func TestSharedWalletTreasuryScopeNoIO(t *testing.T) {
	op := SharedWalletTransfer{UserID: 1, Epoch: 1, Direction: "grant", OperationID: "scope", Amount: 100}
	for _, tc := range []struct {
		dialect string
		wrapped bool
	}{
		{"sqlite", false},
		{"mysql", false},
		{"postgres", true},
	} {
		t.Run(tc.dialect, func(t *testing.T) {
			db := &gorm.DB{Config: &gorm.Config{Dialector: treasuryScopeDialect{name: tc.dialect}}, Statement: &gorm.Statement{}}
			// A PG DB without an owned transaction is rejected at the Tx seam;
			// only invoke the outer wrapper on explicitly unsupported dialects.
			if !tc.wrapped {
				receipt, replayed, err := ApplySharedWalletTransfer(t.Context(), db, op)
				require.ErrorIs(t, err, ErrSharedWalletScope)
				require.Empty(t, receipt)
				require.False(t, replayed)
			}
			receipt, replayed, touched, err := ApplySharedWalletTransferTx(db, op)
			require.ErrorIs(t, err, ErrSharedWalletScope)
			require.Empty(t, receipt)
			require.False(t, replayed)
			require.False(t, touched)
		})
	}
}
