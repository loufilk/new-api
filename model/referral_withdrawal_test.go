package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type referralWithdrawalFixture struct {
	admin, alice, bob AuthSessionIdentity
	config            ReferralWithdrawalConfig
}

func setupReferralWithdrawalTest(t *testing.T) referralWithdrawalFixture {
	t.Helper()
	require.NotEmpty(t, os.Getenv("TEST_MODEL_POSTGRES_DSN"), "requires disposable PostgreSQL 18.6")
	require.True(t, common.UsingMainDatabase(common.DatabaseTypePostgreSQL))
	setupLedgerTestState(t)
	for _, table := range []string{"referral_withdrawal_receipts", "referral_withdrawal_events", "referral_withdrawals", "referral_withdrawal_configs", "referral_commissions", "user_sessions"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	identities := []AuthSessionIdentity{}
	for i, role := range []int{common.RoleRootUser, common.RoleCommonUser, common.RoleCommonUser} {
		id := 98110 + i
		user := User{Id: id, Username: fmt.Sprintf("withdrawal-%d", id), AffCode: fmt.Sprintf("withdrawal-%d", id), Role: role, Status: common.UserStatusEnabled, AuthVersion: 1, AffQuota: 5_000_000, AffHistoryQuota: 5_000_000, UsedQuota: 10_000_000}
		require.NoError(t, DB.Create(&user).Error)
		identity := AuthSessionIdentity{UserID: id, SessionID: fmt.Sprintf("withdrawal-session-%d", id), UserAuthVersion: 1, SessionVersion: 1}
		require.NoError(t, DB.Create(&UserSession{SID: identity.SessionID, UserID: id, UserAuthVersion: 1, Version: 1, Status: UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "test-only", LoginMethod: "password"}).Error)
		identities = append(identities, identity)
	}
	config := DefaultReferralWithdrawalConfig()
	config.Enabled, config.MinWithdrawalQuota, config.MinConsumedQuota, config.BSCFeeBPS = true, 500_000, 1_000_000, 100
	saved, err := SaveReferralWithdrawalConfig(context.Background(), identities[0], config)
	require.NoError(t, err)
	return referralWithdrawalFixture{identities[0], identities[1], identities[2], *saved}
}

func withdrawalInput(config ReferralWithdrawalConfig, amount int) ReferralWithdrawalInput {
	return ReferralWithdrawalInput{AmountQuota: amount, Network: "BSC", Address: "0x1111111111111111111111111111111111111111", PolicyRevision: config.Revision}
}

func advanceWithdrawal(t *testing.T, actor AuthSessionIdentity, row *ReferralWithdrawal, action, key, hash string) *ReferralWithdrawal {
	t.Helper()
	result, err := ReviewReferralWithdrawal(context.Background(), actor, row.ID, key, ReferralWithdrawalAction{ExpectedRevision: row.Revision, Action: action, TransactionHash: hash, Note: "Reviewed payment"}, true)
	require.NoError(t, err)
	return result
}

func TestReferralWithdrawalPostgresLifecycleAndSnapshot(t *testing.T) {
	f := setupReferralWithdrawalTest(t)
	ctx := context.Background()
	require.NoError(t, DB.Create(&ReferralCommission{UserId: f.alice.UserID, SourceUserId: f.bob.UserID, ReferenceType: "topup", ReferenceId: "not-matured", RewardQuota: 1_000_000, Status: ReferralCommissionStatusFrozen, MaturesAt: time.Now().Add(time.Hour).Unix()}).Error)
	row, err := CreateReferralWithdrawal(ctx, f.alice, "create-one", withdrawalInput(f.config, 1_000_000))
	require.NoError(t, err)
	assert.EqualValues(t, 2_000_000, row.GrossUSDTMicro)
	assert.EqualValues(t, 20_000, row.FeeUSDTMicro)
	assert.EqualValues(t, 1_980_000, row.NetUSDTMicro)
	summary, err := GetReferralWithdrawalSummary(ctx, f.alice)
	require.NoError(t, err)
	assert.EqualValues(t, 3_000_000, summary.AvailableQuota)
	assert.EqualValues(t, 1_000_000, summary.ReservedQuota)
	assert.Equal(t, 5_000_000, getUserBalances(t, f.alice.UserID).AffQuota, "reservation does not debit the gross reward balance")
	replay, err := CreateReferralWithdrawal(ctx, f.alice, "create-one", withdrawalInput(f.config, 1_000_000))
	require.NoError(t, err)
	assert.Equal(t, row.ID, replay.ID)
	_, err = CreateReferralWithdrawal(ctx, f.alice, "create-one", withdrawalInput(f.config, 2_000_000))
	require.ErrorIs(t, err, ErrReferralWithdrawalConflict)
	changed := f.config
	changed.QuotaPerUSDT, changed.BSCFeeBPS = 1_000_000, 500
	_, err = SaveReferralWithdrawalConfig(ctx, f.admin, changed)
	require.NoError(t, err)
	row = advanceWithdrawal(t, f.admin, row, "approve", "approve-one", "")
	row = advanceWithdrawal(t, f.admin, row, "begin_payment", "begin-one", "")
	_, err = ReviewReferralWithdrawal(ctx, f.admin, row.ID, "reject-paying", ReferralWithdrawalAction{ExpectedRevision: row.Revision, Action: "reject", Note: "Try release"}, true)
	require.ErrorIs(t, err, ErrReferralWithdrawalConflict)
	payment := ReferralWithdrawalAction{ExpectedRevision: row.Revision, Action: "paid", TransactionHash: "0x" + strings.Repeat("a", 64), Note: "Verified on chain"}
	row, err = ReviewReferralWithdrawal(ctx, f.admin, row.ID, "paid-one", payment, true)
	require.NoError(t, err)
	assert.Equal(t, ReferralWithdrawalPaid, row.Status)
	assert.EqualValues(t, 1_980_000, row.NetUSDTMicro)
	assert.Equal(t, 4_000_000, getUserBalances(t, f.alice.UserID).AffQuota)
	assert.Zero(t, getUserBalances(t, f.alice.UserID).QuotaPaid, "withdrawal never credits the spendable wallet")
	replay, err = ReviewReferralWithdrawal(ctx, f.admin, row.ID, "paid-one", payment, true)
	require.NoError(t, err)
	assert.Equal(t, row.Revision, replay.Revision)
	payment.TransactionHash = "0x" + strings.Repeat("b", 64)
	_, err = ReviewReferralWithdrawal(ctx, f.admin, row.ID, "paid-one", payment, true)
	require.ErrorIs(t, err, ErrReferralWithdrawalConflict)
	detail, err := GetReferralWithdrawal(ctx, f.alice, row.ID, false)
	require.NoError(t, err)
	require.Len(t, detail.Events, 4)
	assert.Equal(t, -1_000_000, detail.Events[3].AmountDelta)
	assert.Equal(t, -1_000_000, detail.Events[3].FrozenDelta)
	assert.ErrorIs(t, DB.Model(&detail.Events[0]).Update("note", "changed").Error, ErrLedgerImmutable)
	assert.ErrorIs(t, DB.Delete(&detail.Events[0]).Error, ErrLedgerImmutable)
	summary, err = GetReferralWithdrawalSummary(ctx, f.alice)
	require.NoError(t, err)
	assert.Zero(t, summary.ReservedQuota)
	assert.EqualValues(t, 3_000_000, summary.AvailableQuota)
}

func TestReferralWithdrawalPostgresConcurrentReservationAndWalletTransfer(t *testing.T) {
	f := setupReferralWithdrawalTest(t)
	ctx := context.Background()
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.alice.UserID).Update("aff_quota", 1_000_000).Error)
	const workers = 6
	rows := make(chan *ReferralWithdrawal, workers)
	failures := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			row, err := CreateReferralWithdrawal(ctx, f.alice, "same-concurrent", withdrawalInput(f.config, 700_000))
			rows <- row
			failures <- err
		})
	}
	group.Wait()
	close(rows)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var row *ReferralWithdrawal
	for current := range rows {
		if row != nil {
			assert.Equal(t, row.ID, current.ID)
		}
		row = current
	}
	_, err := TransferAffQuota(f.alice.UserID, 400_000, "blocked-by-reservation")
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
	assert.Zero(t, getUserBalances(t, f.alice.UserID).QuotaPaid, "failed transfer rolls back its wallet credit and ledger")
	var transferErr, cancelErr error
	group.Go(func() {
		_, cancelErr = ReviewReferralWithdrawal(ctx, f.alice, row.ID, "cancel-concurrent", ReferralWithdrawalAction{ExpectedRevision: 1, Action: "cancel"}, false)
	})
	group.Go(func() { _, transferErr = TransferAffQuota(f.alice.UserID, 600_000, "transfer-with-cancel") })
	group.Wait()
	require.NoError(t, cancelErr)
	if transferErr != nil {
		require.ErrorIs(t, transferErr, ErrLedgerInsufficientBalance)
		_, transferErr = TransferAffQuota(f.alice.UserID, 600_000, "transfer-with-cancel")
	}
	require.NoError(t, transferErr)
	user := getUserBalances(t, f.alice.UserID)
	assert.Equal(t, 400_000, user.AffQuota)
	assert.Equal(t, 600_000, user.QuotaPaid)
	assertQuotaInvariant(t, user)
	_, err = CreateReferralWithdrawal(ctx, f.alice, "too-much-after-transfer", withdrawalInput(f.config, 500_000))
	require.ErrorIs(t, err, ErrReferralWithdrawalIneligible)
}

func TestReferralWithdrawalPostgresReservationRaceAndRejection(t *testing.T) {
	f := setupReferralWithdrawalTest(t)
	ctx := context.Background()
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.alice.UserID).Update("aff_quota", 1_000_000).Error)
	var group sync.WaitGroup
	var createErr, transferErr error
	var row *ReferralWithdrawal
	group.Go(func() {
		row, createErr = CreateReferralWithdrawal(ctx, f.alice, "race-create", withdrawalInput(f.config, 750_000))
	})
	group.Go(func() { _, transferErr = TransferAffQuota(f.alice.UserID, 750_000, "race-transfer") })
	group.Wait()
	assert.NotEqual(t, createErr == nil, transferErr == nil, "only one destination can consume the same available quota")
	if createErr != nil {
		require.ErrorIs(t, createErr, ErrReferralWithdrawalIneligible)
		return
	}
	require.ErrorIs(t, transferErr, ErrLedgerInsufficientBalance)
	row = advanceWithdrawal(t, f.admin, row, "approve", "race-approve", "")
	_, err := ReviewReferralWithdrawal(ctx, f.alice, row.ID, "cancel-approved", ReferralWithdrawalAction{ExpectedRevision: row.Revision, Action: "cancel"}, false)
	require.ErrorIs(t, err, ErrReferralWithdrawalConflict)
	var rejectionErr error
	group.Go(func() {
		_, rejectionErr = ReviewReferralWithdrawal(ctx, f.admin, row.ID, "race-reject", ReferralWithdrawalAction{ExpectedRevision: row.Revision, Action: "reject", Note: "Destination needs correction"}, true)
	})
	group.Go(func() { _, transferErr = TransferAffQuota(f.alice.UserID, 750_000, "transfer-with-reject") })
	group.Wait()
	require.NoError(t, rejectionErr)
	if transferErr != nil {
		require.ErrorIs(t, transferErr, ErrLedgerInsufficientBalance)
		_, transferErr = TransferAffQuota(f.alice.UserID, 750_000, "transfer-with-reject")
	}
	require.NoError(t, transferErr)
	assert.Equal(t, 250_000, getUserBalances(t, f.alice.UserID).AffQuota)
}

func TestReferralWithdrawalPostgresGuardsAndReceiptUniqueness(t *testing.T) {
	f := setupReferralWithdrawalTest(t)
	ctx := context.Background()
	row, err := CreateReferralWithdrawal(ctx, f.alice, "guard-create", withdrawalInput(f.config, 1_000_000))
	require.NoError(t, err)
	_, err = GetReferralWithdrawal(ctx, f.bob, row.ID, false)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = ReviewReferralWithdrawal(ctx, f.bob, row.ID, "cross-user", ReferralWithdrawalAction{ExpectedRevision: 1, Action: "cancel"}, false)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = ReviewReferralWithdrawal(ctx, f.alice, row.ID, "self-admin", ReferralWithdrawalAction{ExpectedRevision: 1, Action: "approve"}, true)
	require.ErrorIs(t, err, ErrReferralWithdrawalForbidden)
	_, err = ReviewReferralWithdrawal(ctx, f.admin, row.ID, "skip-to-payment", ReferralWithdrawalAction{ExpectedRevision: 1, Action: "paid", TransactionHash: strings.Repeat("c", 64)}, true)
	require.ErrorIs(t, err, ErrReferralWithdrawalConflict)
	_, err = ReviewReferralWithdrawal(ctx, f.admin, row.ID, "stale-version", ReferralWithdrawalAction{ExpectedRevision: 4, Action: "approve"}, true)
	require.ErrorIs(t, err, ErrReferralWithdrawalConflict)
	row = advanceWithdrawal(t, f.admin, row, "approve", "guard-approve", "")
	row = advanceWithdrawal(t, f.admin, row, "begin_payment", "guard-begin", "")
	row = advanceWithdrawal(t, f.admin, row, "paid", "guard-paid", strings.Repeat("c", 64))
	second, err := CreateReferralWithdrawal(ctx, f.alice, "guard-second", withdrawalInput(f.config, 1_000_000))
	require.NoError(t, err)
	second = advanceWithdrawal(t, f.admin, second, "approve", "second-approve", "")
	second = advanceWithdrawal(t, f.admin, second, "begin_payment", "second-begin", "")
	_, err = ReviewReferralWithdrawal(ctx, f.admin, second.ID, "duplicate-receipt", ReferralWithdrawalAction{ExpectedRevision: second.Revision, Action: "paid", TransactionHash: row.TransactionHash}, true)
	require.ErrorIs(t, err, ErrReferralWithdrawalReceipt)
	assert.Equal(t, 4_000_000, getUserBalances(t, f.alice.UserID).AffQuota)
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", f.alice.SessionID).Update("status", UserSessionStatusRevoked).Error)
	_, err = CreateReferralWithdrawal(ctx, f.alice, "revoked-create", withdrawalInput(f.config, 1_000_000))
	require.ErrorIs(t, err, ErrUserSessionInactive)
	_, err = GetReferralWithdrawal(ctx, f.alice, row.ID, false)
	require.ErrorIs(t, err, ErrUserSessionInactive)
	bad := f.bob
	bad.SessionID = "pat:5"
	_, err = GetReferralWithdrawalSummary(ctx, bad)
	require.ErrorIs(t, err, ErrUserSessionInactive)
	_, err = SaveReferralWithdrawalConfig(ctx, f.bob, f.config)
	require.ErrorIs(t, err, ErrReferralWithdrawalForbidden)
}

func TestReferralWithdrawalPostgresMigrationAndAtomicAudit(t *testing.T) {
	f := setupReferralWithdrawalTest(t)
	ctx := context.Background()
	before := getUserBalances(t, f.alice.UserID)
	// Fresh installation of the added schema over an existing ordinary wallet.
	require.NoError(t, DB.Migrator().DropTable(ReferralWithdrawalModels()...))
	require.NoError(t, DB.AutoMigrate(ReferralWithdrawalModels()...))
	config := DefaultReferralWithdrawalConfig()
	config.Enabled, config.MinWithdrawalQuota = true, 500_000
	saved, err := SaveReferralWithdrawalConfig(ctx, f.admin, config)
	require.NoError(t, err)
	row, err := CreateReferralWithdrawal(ctx, f.alice, "migration-existing", withdrawalInput(*saved, 1_000_000))
	require.NoError(t, err)
	// Upgrade and repeated migration retain liabilities, snapshots and keys.
	for range 2 {
		require.NoError(t, DB.AutoMigrate(ReferralWithdrawalModels()...))
	}
	stored, err := GetReferralWithdrawal(ctx, f.alice, row.ID, false)
	require.NoError(t, err)
	assert.Equal(t, row.AmountQuota, stored.AmountQuota)
	assert.Equal(t, row.Status, stored.Status)
	assert.Equal(t, before.AffQuota, getUserBalances(t, f.alice.UserID).AffQuota)
	replay, err := CreateReferralWithdrawal(ctx, f.alice, "migration-existing", withdrawalInput(*saved, 1_000_000))
	require.NoError(t, err)
	assert.Equal(t, row.ID, replay.ID)
	require.NoError(t, DB.Migrator().DropTable(&ReferralWithdrawalEvent{}))
	_, err = CreateReferralWithdrawal(ctx, f.alice, "audit-failure", withdrawalInput(*saved, 1_000_000))
	require.Error(t, err)
	var count int64
	require.NoError(t, DB.Model(&ReferralWithdrawal{}).Where("request_key = ?", "audit-failure").Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.AutoMigrate(&ReferralWithdrawalEvent{}))
}

func TestReferralWithdrawalAddressAndArithmeticValidation(t *testing.T) {
	f := setupReferralWithdrawalTest(t)
	for _, address := range []string{"0x1111111111111111111111111111111111111111", "0x52908400098527886E0F7030069857D2E4169EE7"} {
		assert.True(t, validReferralEVMAddress(address))
	}
	for _, address := range []string{"0x" + strings.Repeat("0", 40), "0x52908400098527886e0F7030069857D2E4169EE7", "0x1234", "0x" + strings.Repeat("g", 40)} {
		assert.False(t, validReferralEVMAddress(address))
	}
	assert.True(t, validReferralTronAddress("TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"))
	assert.False(t, validReferralTronAddress("TJRabPrwbZy45sbavfcjinPJC18kjpRTv9"))
	for _, amount := range []int{-1, 0, common.MaxWalletQuota + 1} {
		_, err := CreateReferralWithdrawal(context.Background(), f.alice, "invalid-amount", withdrawalInput(f.config, amount))
		require.ErrorIs(t, err, ErrReferralWithdrawalInvalid)
	}
	input := withdrawalInput(f.config, 500_002)
	f.config.QuotaPerUSDT = 3
	_, err := referralWithdrawalQuote(f.config, input)
	require.ErrorIs(t, err, ErrReferralWithdrawalInvalid)
	f.config.QuotaPerUSDT = 500_000
	f.config.MinWithdrawalQuota = 1
	f.config.BSCFeeBPS = 1
	quote, err := referralWithdrawalQuote(f.config, withdrawalInput(f.config, 1))
	require.NoError(t, err)
	assert.EqualValues(t, 2, quote.GrossUSDTMicro)
	assert.EqualValues(t, 1, quote.FeeUSDTMicro)
	f.config.QuotaPerUSDT = 1
	_, err = referralWithdrawalQuote(f.config, withdrawalInput(f.config, common.MaxWalletQuota))
	require.True(t, errors.Is(err, ErrReferralWithdrawalInvalid))
}

func TestReferralWithdrawalPostgresDeletionPreservesUnsettledLiabilities(t *testing.T) {
	f := setupReferralWithdrawalTest(t)
	ctx := context.Background()
	row, err := CreateReferralWithdrawal(ctx, f.alice, "delete-guard-create", withdrawalInput(f.config, 1_000_000))
	require.NoError(t, err)
	for _, stage := range []string{"pending", "approve", "begin_payment"} {
		if stage != "pending" {
			row = advanceWithdrawal(t, f.admin, row, stage, "delete-guard-"+stage, "")
		}
		_, err = DeleteUserForSession(f.alice)
		require.ErrorIs(t, err, ErrReferralWithdrawalUnsettled, stage)
		owner := getUserBalances(t, f.alice.UserID)
		_, err = DeleteUserByAdministrator(f.admin, owner, true)
		require.ErrorIs(t, err, ErrReferralWithdrawalUnsettled, stage)
		_, err = DeleteUserByAdministrator(f.admin, owner, false)
		require.ErrorIs(t, err, ErrReferralWithdrawalUnsettled, stage)
		assert.EqualValues(t, 1, getUserBalances(t, f.alice.UserID).AuthVersion, "failed deletion leaves authentication and payout identity intact")
	}
	row = advanceWithdrawal(t, f.admin, row, "paid", "delete-guard-paid", strings.Repeat("e", 64))
	owner := getUserBalances(t, f.alice.UserID)
	_, err = DeleteUserByAdministrator(f.admin, owner, true)
	require.NoError(t, err)
	var count int64
	require.NoError(t, DB.Model(&ReferralWithdrawalReceipt{}).Where("withdrawal_id = ?", row.ID).Count(&count).Error)
	assert.EqualValues(t, 1, count, "settled payment evidence survives account removal")
	other, err := CreateReferralWithdrawal(ctx, f.bob, "delete-after-reject", withdrawalInput(f.config, 1_000_000))
	require.NoError(t, err)
	_, err = ReviewReferralWithdrawal(ctx, f.admin, other.ID, "delete-after-reject-action", ReferralWithdrawalAction{ExpectedRevision: 1, Action: "reject", Note: "User requested closure"}, true)
	require.NoError(t, err)
	_, err = DeleteUserForSession(f.bob)
	require.NoError(t, err)
}

func TestReferralWithdrawalPostgresPermissionEditInvalidatesSession(t *testing.T) {
	f := setupReferralWithdrawalTest(t)
	actor := getUserBalances(t, f.admin.UserID)
	actor.AdminPermissions = map[string]map[string]bool{"referral_withdrawals": {"pay": false}}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return actor.EditWithTx(tx, false) }))
	assert.EqualValues(t, 2, getUserBalances(t, f.admin.UserID).AuthVersion)
	assert.ErrorIs(t, ValidateAuthSessionWithTx(DB, f.admin), ErrUserSessionInactive)
}
