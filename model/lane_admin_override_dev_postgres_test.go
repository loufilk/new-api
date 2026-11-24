//go:build lane_billing_integration_dev

package model

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func laneOverrideDenied(t *testing.T, err error) {
	t.Helper()
	var pg *pgconn.PgError
	require.ErrorAs(t, err, &pg)
	require.Equal(t, "23514", pg.Code)
	require.Equal(t, "lane_dev_custody_override_v1", pg.ConstraintName)
}

func TestLaneAdminOverridePublicEntriesPostgres(t *testing.T) {
	db, _, _ := childStoreFixture(t)
	old := DB
	DB = db
	t.Cleanup(func() { DB = old })
	ctx := t.Context()
	u := User{Id: 401, Username: "synthetic-override", AffCode: "ovr401", Status: common.UserStatusEnabled, QuotaPaid: 100, Quota: 100}
	require.NoError(t, db.Create(&u).Error)
	entry := LedgerEntry{UserId: u.Id, Account: AccountPaid, Delta: 100, Reason: LedgerReasonAdminOverride, IdempotencyKey: "historical"}
	r, err := OverrideQuota(entry)
	require.NoError(t, err)
	require.False(t, r.Replayed)
	op := SharedWalletTransfer{UserID: u.Id, Epoch: 3, Slot: 6, Direction: "grant", Amount: 70, OperationID: "grant-override"}
	_, _, err = ApplySharedWalletTransfer(ctx, db, op)
	require.NoError(t, err)
	var schema string
	require.NoError(t, db.Raw("SELECT current_schema()").Scan(&schema).Error)
	cfg, err := pgx.ParseConfig(os.Getenv("TEST_MODEL_POSTGRES_DSN"))
	require.NoError(t, err)
	cfg.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*cfg)
	defer pool.Close()
	second, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{})
	require.NoError(t, err)

	r, err = OverrideQuota(entry)
	require.NoError(t, err)
	require.True(t, r.Replayed)
	require.Equal(t, 100, r.TotalAfter) // historical response, not today's raw 30
	changed := entry
	changed.Delta = 50
	_, err = OverrideQuota(changed)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)
	for _, value := range []int{0, 30, 50, 100} {
		changed.Delta, changed.IdempotencyKey = value, fmt.Sprintf("denied-%d", value)
		_, err = OverrideQuota(changed)
		laneOverrideDenied(t, err)
		// No opt-in GORM context, request flag or same connection required.
		err = second.Transaction(func(tx *gorm.DB) error {
			_, e := OverrideQuotaTx(tx, changed)
			return e
		})
		laneOverrideDenied(t, err)
		_, err = AdjustUserQuota(u.Id, common.RoleRootUser, "override", value, LedgerEntry{IdempotencyKey: changed.IdempotencyKey})
		laneOverrideDenied(t, err)
	}
	changed.Account = AccountReward // no account name on the immutable header
	_, err = OverrideQuota(changed)
	laneOverrideDenied(t, err)
	var stored User
	require.NoError(t, db.First(&stored, u.Id).Error)
	require.Equal(t, 30, stored.QuotaPaid)
	var share SharedWalletShare
	require.NoError(t, db.Where("user_id = ?", u.Id).Take(&share).Error)
	require.Equal(t, 70, share.Available)
	var n int64
	require.NoError(t, db.Model(&BalanceLedgerOperation{}).Where("idempotency_key LIKE 'denied-%'").Count(&n).Error)
	require.Zero(t, n)
	// Even a remaining transfer without its slot must not reopen overrides.
	require.NoError(t, db.Where("user_id = ?", u.Id).Delete(&SharedWalletShare{}).Error)
	_, err = OverrideQuota(changed)
	laneOverrideDenied(t, err)
	// Ordinary control wallet and non-absolute writes still work.
	u.Id, u.Username, u.AffCode = 402, "synthetic-override-other", "ovr402"
	require.NoError(t, db.Create(&u).Error)
	changed.UserId, changed.Account, changed.IdempotencyKey = u.Id, AccountPaid, "no-custody"
	r, err = OverrideQuota(changed)
	require.NoError(t, err)
	require.Equal(t, changed.Delta, r.TotalAfter)
	credit := LedgerEntry{UserId: 401, Account: AccountPaid, Delta: 2, Reason: LedgerReasonAdminAdd, IdempotencyKey: "ordinary-add"}
	r, err = CreditQuota(credit)
	require.NoError(t, err)
	require.Equal(t, 32, r.TotalAfter)
}

func TestLaneAdminOverrideSerializesFirstGrantPostgres(t *testing.T) {
	db, _, _ := childStoreFixture(t)
	u := User{Id: 403, Username: "synthetic-override-race", AffCode: "ovr403", Status: common.UserStatusEnabled, QuotaPaid: 100, Quota: 100}
	require.NoError(t, db.Create(&u).Error)
	grant := db.Begin()
	require.NoError(t, grant.Error)
	defer grant.Rollback()
	_, _, _, err := ApplySharedWalletTransferTx(grant, SharedWalletTransfer{UserID: u.Id, Epoch: 3, Slot: 6, Direction: "grant", Amount: 70, OperationID: "first-grant"})
	require.NoError(t, err)

	attempt := db.WithContext(t.Context()).Begin()
	require.NoError(t, attempt.Error)
	defer attempt.Rollback()
	require.NoError(t, attempt.Exec("SET LOCAL lock_timeout='5s'").Error)
	var pid int
	require.NoError(t, attempt.Raw("SELECT pg_backend_pid()").Scan(&pid).Error)
	done := make(chan error, 1)
	go func() {
		_, err := OverrideQuotaTx(attempt, LedgerEntry{UserId: u.Id, Account: AccountPaid, Delta: 50, Reason: LedgerReasonAdminOverride, IdempotencyKey: "concurrent-set"})
		done <- err
	}()
	// Establish that the ordinary writer actually reached and blocked on the
	// canonical lock; simply sleeping would test scheduler ordering instead.
	require.Eventually(t, func() bool {
		var blocked bool
		return db.Raw("SELECT cardinality(pg_blocking_pids(?)) > 0", pid).Scan(&blocked).Error == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, grant.Commit().Error)
	laneOverrideDenied(t, <-done)
	require.Error(t, attempt.Commit().Error) // even ignoring the guard cannot commit writes
	var stored User
	require.NoError(t, db.First(&stored, u.Id).Error)
	require.Equal(t, 30, stored.QuotaPaid)
}
