//go:build lane_billing_integration_dev

package model

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func childStoreFixture(t *testing.T, releases ...LaneStoreReleaseV1) (*gorm.DB, laneBillingChildBinding, laneBillingChildBinding) {
	t.Helper()
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires disposable loopback PostgreSQL TEST_MODEL_POSTGRES_DSN")
	}
	schema := "lane_billing_dev_" + common.GetUUID()[:8]
	require.Regexp(t, `^lane_billing_dev_[a-f0-9]{8}$`, schema)
	require.NoError(t, DB.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Error)
	t.Cleanup(func() { require.NoError(t, DB.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error) })
	cfg, err := pgx.ParseConfig(os.Getenv("TEST_MODEL_POSTGRES_DSN"))
	require.NoError(t, err)
	cfg.RuntimeParams["search_path"] = schema
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	sqlDB := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	// Existing owner/treasury run before the dev-only child schema installer.
	require.ErrorIs(t, installLaneBillingChildDevSchema(t.Context(), DB), ErrLaneBillingOwnerUnavailable)
	require.ErrorIs(t, installLaneBillingChildDevSchema(t.Context(), db), ErrLaneBillingOwnerUnavailable)
	fixtures := append([]any{&BillingFinalization{}, &User{}, &Token{}, &BalanceLedger{}, &BalanceLedgerOperation{}}, SharedWalletTreasuryModels()...)
	fixtures = append(fixtures, BonusSourceModels()...)
	if len(releases) != 0 {
		require.Len(t, releases, 1)
		require.NoError(t, InstallLaneStoreAuthorityV1(t.Context(), db, releases[0]))
		fixtures = append(fixtures, &Option{})
	}
	require.NoError(t, db.AutoMigrate(fixtures...))
	for range 2 {
		require.NoError(t, installLaneBillingChildDevSchema(t.Context(), db))
	}
	b := laneBillingChildBinding{
		Owner:       LaneBillingOwnerIdentity{RequestID: "synthetic-child-first", LaneID: "wallet-slot-4", Epoch: 11, UserID: 101, TokenID: 501, TokenFingerprint: BillingTokenFingerprint("synthetic-original-key-1")},
		WalletEpoch: 7, WalletSlot: 4, KeyEpoch: 19, KeySlot: 5, ChannelID: 41,
		FundingPolicySHA256: strings.Repeat("a", 64), FrozenPriceSHA256: strings.Repeat("b", 64), RouteSHA256: strings.Repeat("c", 64), FinalWireSHA256: strings.Repeat("d", 64),
	}
	peer := b
	peer.Owner.RequestID, peer.Owner.TokenID, peer.Owner.TokenFingerprint = "synthetic-child-second", 502, BillingTokenFingerprint("synthetic-original-key-2")
	peer.KeyEpoch, peer.KeySlot = 20, 6
	// Use the real paid treasury for the one shared wallet grant. Keys are
	// pre-existing with settled history, but their separate slot enrollment is
	// intentionally seeded; this is not proof of a real Key transition.
	user := User{Id: b.Owner.UserID, Username: "synthetic-child-wallet", AffCode: "child-wallet", Status: common.UserStatusEnabled, Quota: 200, QuotaPaid: 200}
	require.NoError(t, db.Create(&user).Error)
	for i, key := range []laneBillingChildBinding{b, peer} {
		original := Token{Id: key.Owner.TokenID, UserId: user.Id, Key: fmt.Sprintf("synthetic-original-key-%d", i+1),
			Name: "existing synthetic finite key", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 300, UsedQuota: 20 + i}
		require.NoError(t, db.Create(&original).Error)
		require.Equal(t, key.Owner.TokenFingerprint, BillingTokenFingerprint(original.Key))
		history := BillingFinalization{RequestID: fmt.Sprintf("synthetic-old-%d", i+1), UserID: user.Id, Source: "wallet", TokenID: original.Id,
			TokenFingerprint: key.Owner.TokenFingerprint, State: BillingSettled, ActualQuota: original.UsedQuota}
		require.NoError(t, db.Create(&history).Error)
		require.NoError(t, db.Create(&laneBillingFiniteKeyShareV1{UserID: key.Owner.UserID, TokenID: key.Owner.TokenID, Epoch: key.KeyEpoch, Slot: key.KeySlot,
			TokenFingerprint: key.Owner.TokenFingerprint, Granted: 60, Available: 60, Version: 1}).Error)
	}
	grant, replay, err := ApplySharedWalletTransfer(t.Context(), db, SharedWalletTransfer{UserID: user.Id, Epoch: b.WalletEpoch, Slot: b.WalletSlot,
		Direction: "grant", OperationID: "synthetic-shared-wallet-grant", Amount: 100})
	require.NoError(t, err)
	assert.False(t, replay)
	assert.Equal(t, 100, grant.GrantedAfter)
	return db, b, peer
}

func childStoreBalances(t *testing.T, db *gorm.DB, b laneBillingChildBinding) (SharedWalletShare, laneBillingFiniteKeyShareV1) {
	t.Helper()
	var wallet SharedWalletShare
	var key laneBillingFiniteKeyShareV1
	require.NoError(t, db.Where("user_id = ? AND epoch = ? AND slot = ?", b.Owner.UserID, b.WalletEpoch, b.WalletSlot).Take(&wallet).Error)
	require.NoError(t, db.Where("user_id = ? AND token_id = ? AND epoch = ? AND slot = ?", b.Owner.UserID, b.Owner.TokenID, b.KeyEpoch, b.KeySlot).Take(&key).Error)
	require.True(t, wallet.valid())
	require.True(t, key.valid())
	return wallet, key
}

func childStoreEvidence(t *testing.T, db *gorm.DB, b laneBillingChildBinding, cap, actual int, state string) {
	t.Helper()
	var owner BillingFinalization
	var child laneBillingChildReceiptV1
	require.NoError(t, db.Where("request_id = ?", b.Owner.RequestID).Take(&owner).Error)
	assert.Equal(t, laneBillingOwnerState, owner.State)
	assert.Zero(t, owner.SourceHeld)
	assert.Zero(t, owner.TokenHeld)
	require.NoError(t, db.Where("request_id = ?", b.Owner.RequestID).Take(&child).Error)
	assert.Equal(t, b, child.binding())
	assert.Equal(t, state, child.State)
	assert.Equal(t, cap, child.Cap)
	assert.Equal(t, actual, child.Actual)
}

func TestLaneBillingConcreteChildLifecyclePostgres(t *testing.T) {
	db, first, second := childStoreFixture(t)
	ctx := t.Context()
	stores := []*laneBillingPGChildStore{{binding: first}, {binding: second}}
	// Same model functions are exercised as a real integration path, but no
	// public router or provider is wired. Direct store entry must fail closed.
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := stores[0].ReserveLaneBillingReceiptTx(tx, first.Owner, 45)
		return err
	})
	assert.ErrorIs(t, err, ErrLaneBillingOwnerEvidence)
	for i, s := range stores {
		cap := 45 - i*5
		res, err := ReserveLaneBillingOwner(ctx, db, s.binding.Owner, cap, s)
		require.NoError(t, err)
		assert.True(t, res.Fresh)
	}
	wallet, a := childStoreBalances(t, db, first)
	_, b := childStoreBalances(t, db, second)
	assert.Equal(t, 15, wallet.Available)
	assert.Equal(t, 85, wallet.Held)
	assert.Zero(t, wallet.Spent)
	assert.Equal(t, 15, a.Available)
	assert.Equal(t, 45, a.Held)
	assert.Equal(t, 20, b.Available)
	assert.Equal(t, 40, b.Held)
	childStoreEvidence(t, db, first, 45, 0, LaneBillingReceiptHeld)
	childStoreEvidence(t, db, second, 40, 0, LaneBillingReceiptHeld)

	wrong := first
	wrong.FundingPolicySHA256 = strings.Repeat("9", 64)
	_, err = ReserveLaneBillingOwner(ctx, db, first.Owner, 45, &laneBillingPGChildStore{binding: wrong})
	assert.ErrorIs(t, err, ErrLaneBillingOwnerEvidence)
	assert.ErrorIs(t, claimLaneBillingChildDispatch(ctx, db, wrong, first.FinalWireSHA256), ErrLaneBillingOwnerEvidence)
	assert.ErrorIs(t, claimLaneBillingChildDispatch(ctx, db, first, strings.Repeat("0", 64)), ErrLaneBillingOwnerEvidence)
	childStoreEvidence(t, db, first, 45, 0, LaneBillingReceiptHeld)
	require.NoError(t, claimLaneBillingChildDispatch(ctx, db, first, first.FinalWireSHA256))
	assert.ErrorIs(t, claimLaneBillingChildDispatch(ctx, db, first, first.FinalWireSHA256), errLaneBillingChildState)
	assert.ErrorIs(t, finishLaneBillingChild(ctx, db, first, true), errLaneBillingChildState)
	usage := strings.Repeat("e", 64)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	lost := db.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{sqlDB}
	assert.ErrorIs(t, freezeLaneBillingChildUsage(ctx, lost, first, 25, usage), ErrLaneBillingOwnerCommitUncertain)
	require.NoError(t, freezeLaneBillingChildUsage(ctx, db, first, 25, usage))
	assert.ErrorIs(t, freezeLaneBillingChildUsage(ctx, db, first, 26, usage), ErrLaneBillingOwnerEvidence)
	childStoreEvidence(t, db, first, 45, 25, laneBillingChildObserved)
	assert.ErrorIs(t, finishLaneBillingChild(ctx, lost, first, false), ErrLaneBillingOwnerCommitUncertain)
	require.NoError(t, finishLaneBillingChild(ctx, db, first, false))
	require.NoError(t, finishLaneBillingChild(ctx, db, first, false))
	require.NoError(t, finishLaneBillingChild(ctx, db, second, true))
	require.NoError(t, finishLaneBillingChild(ctx, db, second, true))
	assert.ErrorIs(t, claimLaneBillingChildDispatch(ctx, db, second, second.FinalWireSHA256), errLaneBillingChildState)
	childStoreEvidence(t, db, first, 45, 25, laneBillingChildSettled)
	childStoreEvidence(t, db, second, 40, 0, laneBillingChildRefunded)
	wallet, a = childStoreBalances(t, db, first)
	_, b = childStoreBalances(t, db, second)
	assert.Equal(t, 75, wallet.Available)
	assert.Zero(t, wallet.Held)
	assert.Equal(t, 25, wallet.Spent)
	assert.Equal(t, 35, a.Available)
	assert.Zero(t, a.Held)
	assert.Equal(t, 25, a.Spent)
	assert.Equal(t, 60, b.Available)
	assert.Zero(t, b.Held)
	assert.Zero(t, b.Spent)
	var walletCount, transfers int64
	require.NoError(t, db.Model(&SharedWalletShare{}).Count(&walletCount).Error)
	assert.EqualValues(t, 1, walletCount)
	require.NoError(t, db.Model(&SharedWalletTransfer{}).Count(&transfers).Error)
	assert.EqualValues(t, 1, transfers, "two Keys use a single funded wallet share")
	reclaim, replay, err := ApplySharedWalletTransfer(ctx, db, SharedWalletTransfer{UserID: first.Owner.UserID, Epoch: first.WalletEpoch, Slot: first.WalletSlot,
		Direction: "reclaim", OperationID: "synthetic-shared-wallet-return", Amount: 75})
	require.NoError(t, err)
	assert.False(t, replay)
	assert.Equal(t, 75, reclaim.ReturnedAfter)
	wallet, _ = childStoreBalances(t, db, first)
	assert.Zero(t, wallet.Available)
	assert.Equal(t, 25, wallet.Spent)
	assert.Equal(t, 75, wallet.Returned)
	var user User
	require.NoError(t, db.Select("quota_paid").First(&user, first.Owner.UserID).Error)
	assert.Equal(t, 175, user.QuotaPaid, "original 200 = liquid 175 + exactly 25 spent")
	for i, b := range []laneBillingChildBinding{first, second} {
		var original Token
		require.NoError(t, db.Select("remain_quota", "used_quota").First(&original, b.Owner.TokenID).Error)
		assert.Equal(t, 300, original.RemainQuota)
		assert.Equal(t, 20+i, original.UsedQuota, "projection back to original Key is outside this stage")
	}
	var history int64
	require.NoError(t, db.Model(&BillingFinalization{}).Where("state = ?", BillingSettled).Count(&history).Error)
	assert.EqualValues(t, 2, history, "pre-existing ordinary Key history remains")
}

func TestLaneBillingConcreteChildRollbackAndUncertaintyPostgres(t *testing.T) {
	db, first, second := childStoreFixture(t)
	ctx := t.Context()
	store := &laneBillingPGChildStore{binding: first}
	walletBefore, keyBefore := childStoreBalances(t, db, first)
	// 61 fits the shared wallet but not the finite Key. The wallet update has
	// already executed before testing the Key, so this proves both roll back.
	res, err := ReserveLaneBillingOwner(ctx, db, first.Owner, 61, store)
	assert.ErrorIs(t, err, ErrLaneBillingOwnerStore)
	assert.Empty(t, res)
	wallet, key := childStoreBalances(t, db, first)
	assert.Equal(t, walletBefore, wallet)
	assert.Equal(t, keyBefore, key)
	for _, entity := range []any{&BillingFinalization{}, &laneBillingChildReceiptV1{}} {
		var n int64
		require.NoError(t, db.Model(entity).Where("request_id = ?", first.Owner.RequestID).Count(&n).Error)
		assert.Zero(t, n)
	}

	sqlDB, err := db.DB()
	require.NoError(t, err)
	lost := db.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{sqlDB}
	res, err = ReserveLaneBillingOwner(ctx, lost, first.Owner, 45, store)
	assert.ErrorIs(t, err, ErrLaneBillingOwnerCommitUncertain)
	assert.Empty(t, res)
	res, err = ReserveLaneBillingOwner(ctx, db, first.Owner, 45, &laneBillingPGChildStore{binding: first})
	require.NoError(t, err)
	assert.False(t, res.Fresh)
	childStoreEvidence(t, db, first, 45, 0, LaneBillingReceiptHeld)
	assert.ErrorIs(t, claimLaneBillingChildDispatch(ctx, lost, first, first.FinalWireSHA256), ErrLaneBillingOwnerCommitUncertain)
	assert.ErrorIs(t, claimLaneBillingChildDispatch(ctx, db, first, first.FinalWireSHA256), errLaneBillingChildState)
	require.NoError(t, markLaneBillingChildUnknown(ctx, db, first))
	assert.ErrorIs(t, finishLaneBillingChild(ctx, db, first, true), errLaneBillingChildState)
	assert.ErrorIs(t, freezeLaneBillingChildUsage(ctx, db, first, 5, strings.Repeat("6", 64)), errLaneBillingChildState)
	childStoreEvidence(t, db, first, 45, 0, laneBillingChildUnknown)
	res, err = ReserveLaneBillingOwner(ctx, db, second.Owner, 30, &laneBillingPGChildStore{binding: second})
	require.NoError(t, err)
	assert.True(t, res.Fresh)
	require.NoError(t, claimLaneBillingChildDispatch(ctx, db, second, second.FinalWireSHA256))
	assert.ErrorIs(t, freezeLaneBillingChildUsage(ctx, db, second, 31, strings.Repeat("7", 64)), errLaneBillingChildExposure)
	assert.ErrorIs(t, finishLaneBillingChild(ctx, db, second, false), errLaneBillingChildState)
	childStoreEvidence(t, db, second, 30, 31, laneBillingChildReview)
	wallet, key = childStoreBalances(t, db, first)
	_, peer := childStoreBalances(t, db, second)
	assert.Equal(t, 25, wallet.Available)
	assert.Equal(t, 75, wallet.Held)
	assert.Zero(t, wallet.Spent)
	assert.Equal(t, 45, key.Held)
	assert.Equal(t, 30, peer.Held)
}

func TestLaneBillingConcreteChildSharedWalletRacePostgres(t *testing.T) {
	db, first, second := childStoreFixture(t)
	start := make(chan struct{})
	type attempt struct {
		binding laneBillingChildBinding
		result  LaneBillingOwnerReservation
		err     error
	}
	complete := make(chan attempt, 2)
	for _, b := range []laneBillingChildBinding{first, second} {
		go func() {
			<-start
			result, err := ReserveLaneBillingOwner(t.Context(), db, b.Owner, 60, &laneBillingPGChildStore{binding: b})
			complete <- attempt{b, result, err}
		}()
	}
	close(start)
	one, two := <-complete, <-complete
	if one.err != nil {
		one, two = two, one
	}
	require.NoError(t, one.err)
	assert.True(t, one.result.Fresh)
	assert.ErrorIs(t, two.err, ErrLaneBillingOwnerStore)
	assert.Empty(t, two.result)
	wallet, winningKey := childStoreBalances(t, db, one.binding)
	_, losingKey := childStoreBalances(t, db, two.binding)
	assert.Equal(t, 40, wallet.Available)
	assert.Equal(t, 60, wallet.Held)
	assert.Equal(t, 60, winningKey.Held)
	assert.Zero(t, losingKey.Held)
	assert.Equal(t, 60, losingKey.Available)
	childStoreEvidence(t, db, one.binding, 60, 0, LaneBillingReceiptHeld)
	for _, entity := range []any{&BillingFinalization{}, &laneBillingChildReceiptV1{}} {
		var n int64
		require.NoError(t, db.Model(entity).Where("request_id = ?", two.binding.Owner.RequestID).Count(&n).Error)
		assert.Zero(t, n)
	}
}
