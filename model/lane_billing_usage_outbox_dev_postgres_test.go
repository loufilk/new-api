//go:build lane_billing_integration_dev

package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func usageDevFixture(t *testing.T) (*gorm.DB, laneBillingChildBinding, *LaneBillingChildDev, LaneBillingMeasuredDev) {
	t.Helper()
	db, binding, _ := finiteKeyDevFixture(t)
	if !common.LogConsumeEnabled {
		common.LogConsumeEnabled = true
		t.Cleanup(func() { common.LogConsumeEnabled = false })
	}
	require.NoError(t, db.AutoMigrate(&Channel{}, &Log{}))
	require.NoError(t, db.Create(&Channel{Id: binding.ChannelID, Name: "synthetic usage channel"}).Error)
	id := finiteKeyDevIdentity(binding)
	require.NoError(t, EnrollLaneBillingFiniteKeyDev(t.Context(), db, id))
	_, _, err := ApplyLaneBillingFiniteKeyDevTransfer(t.Context(), db, LaneBillingFiniteKeyTransfer{
		Identity: id, OperationID: "usage-fixture-key-grant", Slot: binding.KeySlot, Direction: "grant", Amount: 70})
	require.NoError(t, err)
	child, err := ReserveLaneBillingChildDev(t.Context(), db, LaneBillingChildDevBinding(binding), 40)
	require.NoError(t, err)
	require.NoError(t, child.ClaimDispatch(t.Context(), binding.FinalWireSHA256))
	return db, binding, child, LaneBillingMeasuredDev{
		RequestID: binding.Owner.RequestID, ChannelID: binding.ChannelID, Group: "synthetic-group", Model: "synthetic-model",
		PromptTokens: 21, CompletionTokens: 6, CacheReadTokens: 3, CacheWriteTokens: 5, CacheWrite12hTokens: 2, ActualQuota: 29, UsageSHA256: strings.Repeat("f", 64),
	}
}

func readUsageOutbox(t *testing.T, db *gorm.DB, id string) laneBillingUsageOutboxDev {
	t.Helper()
	var row laneBillingUsageOutboxDev
	require.NoError(t, db.Where("request_id = ?", id).Take(&row).Error)
	return row
}

func usageDevStats(t *testing.T, db *gorm.DB, b laneBillingChildBinding, actual, requests int) {
	t.Helper()
	var user User
	var channel Channel
	require.NoError(t, db.First(&user, b.Owner.UserID).Error)
	require.NoError(t, db.First(&channel, b.ChannelID).Error)
	assert.Equal(t, actual, user.UsedQuota)
	assert.Equal(t, requests, user.RequestCount)
	assert.EqualValues(t, actual, channel.UsedQuota)
}

func dueUsageDev(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	require.NoError(t, db.Model(&laneBillingUsageOutboxDev{}).Where("request_id = ?", id).UpdateColumn("due_at", 0).Error)
}

func usageDevScopedConnection(t *testing.T, databaseDSN, schema string) *gorm.DB {
	t.Helper()
	cfg, err := pgx.ParseConfig(databaseDSN)
	require.NoError(t, err)
	addr, err := netip.ParseAddr(cfg.Host)
	require.NoError(t, err)
	require.True(t, addr.IsLoopback(), "use only a disposable loopback PostgreSQL")
	if schema != "" {
		cfg.RuntimeParams["search_path"] = schema
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = pool.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{})
	require.NoError(t, err)
	return db
}

func splitUsageDevFixture(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("TEST_LANE_USAGE_LOG_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires separately disposable TEST_LANE_USAGE_LOG_POSTGRES_DSN")
	}
	parent, err := pgx.ParseConfig(os.Getenv("TEST_MODEL_POSTGRES_DSN"))
	require.NoError(t, err)
	separate, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	require.NotEqual(t, parent.Database, separate.Database, "split test uses a distinct PostgreSQL database")
	admin := usageDevScopedConnection(t, dsn, "")
	schema := "lane_billing_dev_" + common.GetUUID()[:12]
	require.Regexp(t, `^lane_billing_dev_[a-f0-9]{12}$`, schema)
	require.NoError(t, admin.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error) })
	logDB := usageDevScopedConnection(t, dsn, schema)
	require.NoError(t, logDB.AutoMigrate(&Log{}))
	return logDB, schema
}

type laneUsageLoseNthAck struct {
	*sql.DB
	at    int32
	begun atomic.Int32
}
type laneUsageSelectiveCommit struct {
	*sql.Tx
	lose bool
}

func (p *laneUsageLoseNthAck) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &laneUsageSelectiveCommit{Tx: tx, lose: p.begun.Add(1) == p.at}, nil
}
func (tx *laneUsageSelectiveCommit) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if tx.lose {
		return errors.New("synthetic committed without acknowledgement")
	}
	return nil
}

func TestLaneBillingUsageOutboxAtomicSpendPostgres(t *testing.T) {
	db, b, child, m := usageDevFixture(t)
	walletBefore, keyBefore := childStoreBalances(t, db, b)
	const hook = "test:deny-usage-outbox"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == (laneBillingUsageOutboxDev{}).TableName() {
			tx.AddError(errors.New("synthetic outbox write failure"))
		}
	}))
	require.ErrorIs(t, child.SettleMeasured(t.Context(), m), ErrLaneBillingOwnerStore)
	require.NoError(t, db.Callback().Create().Remove(hook))
	childStoreEvidence(t, db, b, 40, m.ActualQuota, laneBillingChildObserved)
	walletAfter, keyAfter := childStoreBalances(t, db, b)
	assert.Equal(t, walletBefore, walletAfter)
	assert.Equal(t, keyBefore, keyAfter)
	var count int64
	require.NoError(t, db.Model(&laneBillingUsageOutboxDev{}).Count(&count).Error)
	assert.Zero(t, count)
	var measured LaneBillingMeasuredDev
	require.NoError(t, db.First(&measured, "request_id = ?", m.RequestID).Error)
	assert.False(t, measured.Ready)

	// Lose the second, balance+outbox COMMIT acknowledgement, not the already
	// observed first transaction. An exact settlement replay cannot spend twice.
	pool, err := db.DB()
	require.NoError(t, err)
	lostDB := db.Session(&gorm.Session{NewDB: true, Context: t.Context()})
	lostDB.Statement.ConnPool = &laneUsageLoseNthAck{DB: pool, at: 2}
	lostChild := *child
	lostChild.db = lostDB
	require.ErrorIs(t, lostChild.SettleMeasured(t.Context(), m), ErrLaneBillingOwnerCommitUncertain)
	o := readUsageOutbox(t, db, m.RequestID)
	assert.Equal(t, laneUsagePending, o.State)
	assert.Equal(t, b.Owner.UserID, o.Payload.UserID)
	assert.Equal(t, b.Owner.TokenID, o.Payload.TokenID)
	assert.Equal(t, b.KeyEpoch, o.Payload.KeyEpoch)
	assert.Equal(t, b.FrozenPriceSHA256, o.Payload.PriceSHA256)
	require.NoError(t, child.SettleMeasured(t.Context(), m))
	assert.Equal(t, o, readUsageOutbox(t, db, m.RequestID))
	walletAfter, keyAfter = childStoreBalances(t, db, b)
	assert.Equal(t, m.ActualQuota, walletAfter.Spent)
	assert.Equal(t, m.ActualQuota, keyAfter.Spent)
	usageDevStats(t, db, b, 0, 0)
	require.NoError(t, db.Where("request_id = ?", m.RequestID).Delete(&laneBillingUsageOutboxDev{}).Error)
	require.ErrorIs(t, child.SettleMeasured(t.Context(), m), ErrLaneBillingOwnerEvidence, "do not backfill a legacy settled owner")
	walletFinal, keyFinal := childStoreBalances(t, db, b)
	assert.Equal(t, walletAfter, walletFinal)
	assert.Equal(t, keyAfter, keyFinal)
}

func TestLaneBillingUsageOutboxSharedProjectionPostgres(t *testing.T) {
	db, b, child, m := usageDevFixture(t)
	ctx := t.Context()
	_, err := InstallLaneBillingUsageLogDevSchema(ctx, DB)
	require.ErrorIs(t, err, ErrLaneBillingOwnerUnavailable, "ordinary schema cannot be altered")
	destination, err := InstallLaneBillingUsageLogDevSchema(ctx, db)
	require.NoError(t, err)
	again, err := InstallLaneBillingUsageLogDevSchema(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, destination, again)
	n, err := ProjectLaneBillingUsageDev(ctx, db, db, destination, 4)
	require.NoError(t, err)
	assert.Zero(t, n, "dispatched alone is not settled")
	require.NoError(t, child.SettleMeasured(ctx, m))
	wallet, key := childStoreBalances(t, db, b)
	// One real backend holds the due row; the second worker may not wait,
	// steal its attempt, or lose time scanning more than its selected page.
	locked, unlock, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var row laneBillingUsageOutboxDev
			if err := lockForUpdate(tx).Where("request_id = ?", m.RequestID).Take(&row).Error; err != nil {
				return err
			}
			close(locked)
			select {
			case <-unlock:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-locked:
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	skipped, skipErr := (laneUsageStoreDev{db: db}).claim(ctx, m.RequestID, destination, true)
	close(unlock)
	require.NoError(t, <-done)
	require.NoError(t, skipErr)
	require.Nil(t, skipped)
	// Supersede the first attempt using the same destination. Projection of
	// the stale attempt may find/create the same log, but cannot apply counters.
	first, err := (laneUsageStoreDev{db: db}).claim(ctx, m.RequestID, destination, true)
	require.NoError(t, err)
	require.NotNil(t, first)
	blocked, err := (laneUsageStoreDev{db: db}).claim(ctx, m.RequestID, destination, true)
	require.NoError(t, err)
	require.Nil(t, blocked)
	dueUsageDev(t, db, m.RequestID)
	second, err := (laneUsageStoreDev{db: db}).claim(ctx, m.RequestID, destination, true)
	require.NoError(t, err)
	require.NotNil(t, second)
	missing, err := (laneUsageStoreDev{db: db}).finish(ctx, *first, nil, false)
	require.ErrorIs(t, err, ErrLaneBillingOwnerEvidence)
	assert.False(t, missing)
	oldProof, err := (laneUsageStoreDev{db: db}).writeLog(ctx, *first)
	require.NoError(t, err)
	applied, err := (laneUsageStoreDev{db: db}).finish(ctx, *first, oldProof, false)
	require.NoError(t, err)
	assert.False(t, applied)
	usageDevStats(t, db, b, 0, 0)
	proof, err := (laneUsageStoreDev{db: db}).writeLog(ctx, *second)
	require.NoError(t, err)
	assert.Equal(t, oldProof, proof)
	pool, err := db.DB()
	require.NoError(t, err)
	lost := db.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{pool}
	applied, err = (laneUsageStoreDev{db: lost}).finish(ctx, *second, proof, false)
	require.ErrorIs(t, err, ErrLaneBillingOwnerCommitUncertain)
	assert.False(t, applied)
	n, err = ProjectLaneBillingUsageDev(ctx, db, db, destination, 4)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, laneUsageProjected, readUsageOutbox(t, db, m.RequestID).State)
	usageDevStats(t, db, b, m.ActualQuota, 1)
	var rows []Log
	require.NoError(t, db.Where("request_id = ?", m.RequestID).Find(&rows).Error)
	require.Len(t, rows, 1)
	log := rows[0]
	assert.Equal(t, b.Owner.TokenID, log.TokenId)
	assert.Equal(t, b.Owner.UserID, log.UserId)
	assert.Equal(t, m.ChannelID, log.ChannelId)
	assert.Equal(t, m.PromptTokens, log.PromptTokens)
	assert.Equal(t, m.ActualQuota, log.Quota)
	assert.Contains(t, log.Other, b.FrozenPriceSHA256)
	assert.NotContains(t, log.Other, b.Owner.TokenFingerprint)
	assert.Empty(t, log.TokenName)
	assert.Empty(t, log.Ip)
	walletAfter, keyAfter := childStoreBalances(t, db, b)
	assert.Equal(t, wallet, walletAfter)
	assert.Equal(t, key, keyAfter)
}

func TestLaneBillingUsageOutboxSplitRetryPostgres(t *testing.T) {
	db, b, child, m := usageDevFixture(t)
	ctx := t.Context()
	logDB, schema := splitUsageDevFixture(t)
	destination, err := InstallLaneBillingUsageLogDevSchema(ctx, logDB)
	require.NoError(t, err)
	require.NoError(t, child.SettleMeasured(ctx, m))
	old := usageDevScopedConnection(t, os.Getenv("TEST_LANE_USAGE_LOG_POSTGRES_DSN"), schema)
	closed, err := old.DB()
	require.NoError(t, err)
	require.NoError(t, closed.Close())
	n, err := ProjectLaneBillingUsageDev(ctx, db, old, destination, 2)
	require.Error(t, err)
	assert.Zero(t, n)
	row := readUsageOutbox(t, db, m.RequestID)
	assert.Equal(t, laneUsagePending, row.State)
	assert.Empty(t, row.Destination, "unreachable target must not pin a guessed identity")
	assert.Positive(t, row.DueAt)
	usageDevStats(t, db, b, 0, 0)
	n, err = ProjectLaneBillingUsageDev(ctx, db, logDB, destination, 2)
	require.NoError(t, err)
	assert.Zero(t, n, "delayed retry must not hammer the log DB")

	dueUsageDev(t, db, m.RequestID)
	n, err = ProjectLaneBillingUsageDev(ctx, db, logDB, strings.Repeat("a", 64), 2)
	require.ErrorIs(t, err, ErrLaneBillingOwnerUnavailable)
	assert.Zero(t, n)
	assert.Empty(t, readUsageOutbox(t, db, m.RequestID).Destination, "mismatched reachable target cannot poison the first lease")
	pool, err := logDB.DB()
	require.NoError(t, err)
	lost := logDB.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = &laneUsageLoseNthAck{DB: pool, at: 2} // first target probe is read only
	n, err = ProjectLaneBillingUsageDev(ctx, db, lost, destination, 2)
	require.ErrorIs(t, err, ErrLaneBillingOwnerCommitUncertain)
	assert.Zero(t, n)
	usageDevStats(t, db, b, 0, 0)
	var count int64
	require.NoError(t, logDB.Model(&Log{}).Where("request_id = ?", m.RequestID).Count(&count).Error)
	assert.EqualValues(t, 1, count)

	dueUsageDev(t, db, m.RequestID)
	var mainSchema string
	require.NoError(t, db.Raw("SELECT current_schema()").Scan(&mainSchema).Error)
	newMain := usageDevScopedConnection(t, os.Getenv("TEST_MODEL_POSTGRES_DSN"), mainSchema)
	newTarget := usageDevScopedConnection(t, os.Getenv("TEST_LANE_USAGE_LOG_POSTGRES_DSN"), schema)
	n, err = ProjectLaneBillingUsageDev(ctx, newMain, newTarget, strings.Repeat("a", 64), 2)
	require.ErrorIs(t, err, ErrLaneBillingOwnerUnavailable)
	assert.Zero(t, n)
	n, err = ProjectLaneBillingUsageDev(ctx, newMain, newTarget, destination, 2)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	usageDevStats(t, db, b, m.ActualQuota, 1)
	require.NoError(t, logDB.Model(&Log{}).Where("request_id = ?", m.RequestID).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&Log{}).Where("request_id = ?", m.RequestID).Count(&count).Error)
	assert.Zero(t, count, "ordinary primary log must not obscure the split destination")
}

func TestLaneBillingUsageOutboxConflictsPostgres(t *testing.T) {
	for _, kind := range []string{"foreign_log", "missing_destination_log", "corrupt_terminal", "missing_channel", "malformed_channel_counter"} {
		t.Run(kind, func(t *testing.T) {
			db, b, child, m := usageDevFixture(t)
			ctx := t.Context()
			destination, err := InstallLaneBillingUsageLogDevSchema(ctx, db)
			require.NoError(t, err)
			require.NoError(t, child.SettleMeasured(ctx, m))
			switch kind {
			case "foreign_log":
				require.NoError(t, db.Create(&Log{RequestId: m.RequestID, Type: LogTypeConsume, ChannelId: b.ChannelID, UserId: b.Owner.UserID, TokenId: b.Owner.TokenID, Quota: m.ActualQuota}).Error)
			case "missing_destination_log":
				claim, err := (laneUsageStoreDev{db: db}).claim(ctx, m.RequestID, destination, true)
				require.NoError(t, err)
				require.NotNil(t, claim)
				_, err = (laneUsageStoreDev{db: db}).writeLog(ctx, *claim)
				require.NoError(t, err)
				require.NoError(t, db.Where("request_id = ?", m.RequestID).Delete(&Log{}).Error)
				dueUsageDev(t, db, m.RequestID)
			case "corrupt_terminal":
				require.NoError(t, db.Model(&LaneBillingMeasuredDev{}).Where("request_id = ?", m.RequestID).UpdateColumn("ready", false).Error)
			case "missing_channel":
				require.NoError(t, db.Delete(&Channel{}, b.ChannelID).Error)
			case "malformed_channel_counter":
				require.NoError(t, db.Model(&Channel{}).Where("id = ?", b.ChannelID).UpdateColumn("used_quota", -7).Error)
			}
			n, err := ProjectLaneBillingUsageDev(ctx, db, db, destination, 2)
			require.ErrorIs(t, err, ErrLaneBillingUsageProjectionConflict)
			assert.Zero(t, n)
			assert.Equal(t, laneUsageConflict, readUsageOutbox(t, db, m.RequestID).State)
			n, err = ProjectLaneBillingUsageDev(ctx, db, db, destination, 2)
			require.NoError(t, err)
			assert.Zero(t, n)
			var user User
			require.NoError(t, db.First(&user, b.Owner.UserID).Error)
			assert.Zero(t, user.RequestCount)
			assert.Zero(t, user.UsedQuota)
			if kind == "malformed_channel_counter" {
				var channel Channel
				require.NoError(t, db.First(&channel, b.ChannelID).Error)
				assert.EqualValues(t, -7, channel.UsedQuota, "malformed counter is retained for review; user update rolled back")
			}
			wallet, key := childStoreBalances(t, db, b)
			assert.Equal(t, m.ActualQuota, wallet.Spent)
			assert.Equal(t, m.ActualQuota, key.Spent)
		})
	}
}
