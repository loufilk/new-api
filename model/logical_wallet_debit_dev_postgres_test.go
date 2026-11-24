//go:build lane_billing_integration_dev

package model

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestWalletPaidIdleDebitPolicyPostgres(t *testing.T) {
	for _, tc := range []struct {
		name       string
		order      []LedgerAccount
		amount     int
		expired    bool
		wantBonus  int
		bonusAfter int
	}{
		{"eligible_uses_bonus_before_reward_and_paid", []LedgerAccount{AccountBonus, AccountReward, AccountPaid}, 200, false, 40, 0},
		{"noneligible_preserves_bonus", []LedgerAccount{AccountReward, AccountPaid}, 160, false, 0, 40},
		{"expired_bonus_is_removed_before_reclaim", []LedgerAccount{AccountBonus, AccountReward, AccountPaid}, 160, true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, binding, _ := finiteKeyDevFixture(t)
			ctx := t.Context()
			id := finiteKeyDevIdentity(binding)
			scope := LaneLogicalQuotaDevScope{Key: id, WalletEpoch: binding.WalletEpoch}
			require.NoError(t, EnrollLaneBillingFiniteKeyDev(ctx, db, id))
			_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, LaneBillingFiniteKeyTransfer{
				Identity: id, OperationID: "ordered-key-grant", Slot: binding.KeySlot, Direction: "grant", Amount: 70,
			})
			require.NoError(t, err)
			child := &laneBillingEnrolledPGChildStore{laneBillingPGChildStore{binding: binding}}
			_, err = ReserveLaneBillingOwner(ctx, db, binding.Owner, 25, child)
			require.NoError(t, err)
			require.NoError(t, claimEnrolledLaneBillingChildDispatch(ctx, db, binding, binding.FinalWireSHA256))
			require.NoError(t, markLaneBillingChildUnknown(ctx, db, binding))

			currentClock := bonusSourceNow
			t.Cleanup(func() { bonusSourceNow = currentClock })
			now := int64(2_000_000_000)
			bonusSourceNow = func() int64 { return now }
			expires := int64(0)
			if tc.expired {
				expires = now + 1
			}
			require.NoError(t, db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				_, err := CreditQuotaTx(tx, LedgerEntry{UserId: id.UserID, Account: AccountBonus, Delta: 40,
					Reason: "campaign_grant", IdempotencyKey: "actual-ordered-bonus",
					bonusGrant: &bonusSourceGrant{CampaignID: 1, ClaimID: 1, ExpiresAt: expires}})
				if err != nil {
					return err
				}
				_, err = CreditQuotaTx(tx, LedgerEntry{UserId: id.UserID, Account: AccountReward, Delta: 25,
					Reason: LedgerReasonAdminAdd, IdempotencyKey: "actual-ordered-reward"})
				return err
			}))
			now += 2
			request := "ordinary-policy-order"
			auth := BillingFinalization{RequestID: request, UserID: id.UserID, Source: "wallet", TokenID: id.TokenID, TokenFingerprint: id.TokenFingerprint}
			apply := func(tx *gorm.DB, owner *BillingFinalization) error {
				s, err := readLaneLogicalQuotaDevTx(tx, scope, true)
				if err != nil {
					return err
				}
				entry := LedgerEntry{UserId: id.UserID, Delta: tc.amount, Reason: LedgerReasonBillingPreConsume,
					ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:preconsume:" + request}
				result, err := debitQuotaTx(tx, entry, &walletPaidIdleDebit{
					tx: tx, userID: id.UserID, epoch: scope.WalletEpoch, request: request, slots: s.wallet,
				}, tc.order...)
				if err != nil {
					return err
				}
				require.False(t, result.Replayed)
				for _, row := range result.Rows {
					switch LedgerAccount(row.Account) {
					case AccountPaid:
						owner.Paid -= row.Delta
					case AccountBonus:
						owner.Bonus -= row.Delta
					case AccountReward:
						owner.Reward -= row.Delta
					}
				}
				owner.SourceHeld, owner.TokenHeld = tc.amount, tc.amount
				return ReserveBillingTokenTx(tx, owner, tc.amount, false)
			}
			inspect := func(tx *gorm.DB, owner BillingFinalization) error { return validateBillingSourceTx(tx, &owner) }
			receipt, attempt, err := reserveBillingFinalizationWithDB(ctx, db, auth, nil, tc.amount, laneBillingFiniteKeyDevScopeTx, apply, inspect)
			require.NoError(t, err)
			require.True(t, attempt.Applied)
			require.Equal(t, BillingHeld, receipt.State)
			require.Equal(t, 135, receipt.Paid)
			require.Equal(t, 25, receipt.Reward)
			require.Equal(t, tc.wantBonus, receipt.Bonus)
			require.Equal(t, tc.amount, receipt.TokenHeld)
			view := readLogicalQuotaFixture(t, db, scope)
			require.Equal(t, 40, view.WalletPaidAvailable, "only 35 idle was reclaimed; held 25 is unavailable")
			require.Equal(t, tc.bonusAfter, view.WalletBonusRaw)
			require.Zero(t, view.WalletRewardRaw)
			require.Equal(t, 25, view.KeyHeld, "unknown child was not spent or returned")
			var wallet SharedWalletShare
			require.NoError(t, db.Where("user_id = ? AND epoch = ?", id.UserID, scope.WalletEpoch).Take(&wallet).Error)
			require.Equal(t, 35, wallet.Returned)
			require.Equal(t, 25, wallet.Held)
			_, redo, err := reserveBillingFinalizationWithDB(ctx, db, auth, nil, tc.amount, laneBillingFiniteKeyDevScopeTx, apply, inspect)
			require.NoError(t, err)
			require.False(t, redo.Applied)
			require.Equal(t, view, readLogicalQuotaFixture(t, db, scope))
		})
	}
}

func TestWalletPaidIdleDebitOrdinaryNoSchemaPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_MODEL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated TEST_MODEL_POSTGRES_DSN")
	}
	schema := "ordinary_wallet_order_" + common.GetUUID()[:8]
	require.NoError(t, DB.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	t.Cleanup(func() { require.NoError(t, DB.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error) })
	db := usageDevScopedConnection(t, dsn, schema)
	fixtures := append([]any{&User{}, &BalanceLedger{}, &BalanceLedgerOperation{}}, BonusSourceModels()...)
	require.NoError(t, db.AutoMigrate(fixtures...))
	require.False(t, db.Migrator().HasTable(&SharedWalletShare{}))
	require.False(t, db.Migrator().HasTable(&laneBillingFiniteKeyShareV1{}))
	user := User{Id: 125, Username: "ordinary-order", AffCode: "ordered-no-schema", Status: common.UserStatusEnabled, Quota: 120, QuotaPaid: 90, QuotaReward: 30}
	require.NoError(t, db.Create(&user).Error)
	const rollbackOnly = "rollback baseline observation"
	// Compare with the exact former path using the same locked snapshot,
	// frozen ledger key and rolled-back effects, so counts cannot be masked
	// by a changed cache or escrow schema.
	observe := func(ordinary bool) ([]string, *LedgerResult) {
		t.Helper()
		var queries []string
		var result *LedgerResult
		q := db.Session(&gorm.Session{Logger: laneOrdinarySQLCapture{Interface: logger.Default, queries: &queries}})
		err := q.Transaction(func(tx *gorm.DB) error {
			order := []LedgerAccount{AccountReward, AccountPaid}
			entry := LedgerEntry{UserId: user.Id, Delta: 75, Reason: LedgerReasonBillingPreConsume,
				RequestId: "ordinary-no-schema", ReferenceType: "relay", ReferenceId: "ordinary-no-schema",
				IdempotencyKey: "billing:preconsume:ordinary-no-schema", DebitOrder: order}
			var err error
			if ordinary {
				result, err = DebitQuotaTx(tx, entry, order...)
			} else {
				result, err = applyLedgerTx(tx, entry, entry.IdempotencyKey, ledgerOpDebit, func(_ *gorm.DB, u *User) ([]accountDelta, error) {
					return planDebit(u, entry.Delta, order)
				})
			}
			if err != nil {
				return err
			}
			return errors.New(rollbackOnly)
		})
		require.EqualError(t, err, rollbackOnly)
		return queries, result
	}
	oldQueries, old := observe(false)
	newQueries, newResult := observe(true)
	require.NotEmpty(t, oldQueries)
	require.Len(t, newQueries, len(oldQueries))
	require.Equal(t, old.TotalBefore, newResult.TotalBefore)
	require.Equal(t, old.TotalAfter, newResult.TotalAfter)
	require.Equal(t, 45, newResult.TotalAfter)
	require.Len(t, newResult.Rows, 2)
	for i, row := range newResult.Rows {
		require.Equal(t, old.Rows[i].Account, row.Account)
		require.Equal(t, old.Rows[i].Delta, row.Delta)
		require.Equal(t, old.Rows[i].BalanceAfter, row.BalanceAfter)
		require.Equal(t, old.Rows[i].IdempotencyKey, row.IdempotencyKey)
	}
	for _, query := range newQueries {
		require.NotContains(t, query, "shared_wallet")
		require.NotContains(t, query, "lane_billing")
	}
}
