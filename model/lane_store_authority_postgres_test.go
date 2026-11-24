package model

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func laneStoreTestRelease() LaneStoreReleaseV1 {
	return LaneStoreReleaseV1{StoreID: strings.Repeat("a", 32), ReleaseID: strings.Repeat("b", 32), BinarySHA256: strings.Repeat("c", 64), Epoch: 1}
}

func laneStoreFixture(t *testing.T, legacy bool) (*gorm.DB, LaneStoreQuoteInputV1) {
	t.Helper()
	db := laneStartupFixture(t, 0)
	r := LaneStoreQuoteInputV1{Release: laneStoreTestRelease(), RequestID: "synthetic-stage8-quote", UserID: 901, TokenID: 902,
		TokenFingerprint: BillingTokenFingerprint("syntheticstage8finitekey"), UserAuthVersion: 4, UserCacheRevision: 7, TokenCacheRevision: 5, Group: "default"}
	if legacy {
		require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Option{}, &BonusSourceState{}, &BillingFinalization{}))
		user := User{Id: r.UserID, Username: "synthetic-stage8", AffCode: "synthetic-s8", AuthVersion: r.UserAuthVersion, CacheRevision: r.UserCacheRevision,
			Status: common.UserStatusEnabled, Quota: 1000, QuotaPaid: 1000, Group: "default"}
		user.SetSetting(dto.UserSetting{BillingPreference: "wallet_only"})
		token := Token{Id: r.TokenID, UserId: r.UserID, Key: "syntheticstage8finitekey", Status: common.TokenStatusEnabled,
			CacheRevision: r.TokenCacheRevision, Group: r.Group, ExpiredTime: -1, RemainQuota: 1000, UsedQuota: 9}
		require.NoError(t, db.Create(&user).Error)
		require.NoError(t, db.Create(&token).Error)
		require.NoError(t, db.Create(&BillingFinalization{RequestID: "historic-ordinary-stage8", UserID: r.UserID, TokenID: r.TokenID,
			TokenFingerprint: r.TokenFingerprint, Source: "wallet", State: BillingSettled, ActualQuota: 9}).Error)
	}
	return db, r
}

func stage8Tx(t *testing.T, db *gorm.DB, fn func(*gorm.DB) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), laneBillingOwnerTimeout)
	defer cancel()
	return db.WithContext(ctx).Transaction(fn)
}

func captureStage8(t *testing.T, db *gorm.DB, in LaneStoreQuoteInputV1) LaneStoreQuoteAuthorityV1 {
	t.Helper()
	var proof LaneStoreQuoteAuthorityV1
	require.NoError(t, stage8Tx(t, db, func(tx *gorm.DB) error {
		var err error
		proof, err = CaptureLaneStoreQuoteAuthorityV1Tx(tx, in)
		return err
	}))
	return proof
}

func TestLaneStoreAuthorityDormantInstallPostgres(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "fresh"
		if legacy {
			name = "existing ordinary Key and settled owner"
		}
		t.Run(name, func(t *testing.T) {
			db, in := laneStoreFixture(t, legacy)
			require.NoError(t, CheckLaneStoreOrdinaryStartup(t.Context(), db))
			require.False(t, db.Migrator().HasTable(laneStoreAuthorityRelation), "startup must not install authority")
			require.False(t, LaneStoreRuntimeCapable)
			for range 2 {
				require.NoError(t, InstallLaneStoreAuthorityV1(t.Context(), db, in.Release))
				require.NoError(t, CheckLaneStoreOrdinaryStartup(t.Context(), db))
			}
			foreign := in.Release
			foreign.BinarySHA256 = strings.Repeat("d", 64)
			require.ErrorIs(t, InstallLaneStoreAuthorityV1(t.Context(), db, foreign), ErrLaneStoreAuthorityEvidence)
			require.ErrorIs(t, RevokeLaneStoreAuthorityV1(t.Context(), db, foreign), ErrLaneStoreAuthorityEvidence)
			if legacy {
				var user User
				var key Token
				var h BillingFinalization
				require.NoError(t, db.Take(&user, in.UserID).Error)
				require.NoError(t, db.Take(&key, in.TokenID).Error)
				require.NoError(t, db.Where("request_id = ?", "historic-ordinary-stage8").Take(&h).Error)
				assert.Equal(t, 1000, user.QuotaPaid)
				assert.Equal(t, 1000, key.RemainQuota)
				assert.Equal(t, 9, key.UsedQuota)
				assert.Equal(t, BillingSettled, h.State)
				assert.Equal(t, 9, h.ActualQuota)
			} else {
				assert.False(t, db.Migrator().HasTable(&User{}))
				assert.False(t, db.Migrator().HasTable(&BillingFinalization{}))
			}
			require.NoError(t, RevokeLaneStoreAuthorityV1(t.Context(), db, in.Release))
			require.NoError(t, CheckLaneStoreOrdinaryStartup(t.Context(), db))
			nested := db.Begin()
			require.NoError(t, nested.Error)
			require.ErrorIs(t, CheckLaneStoreOrdinaryStartup(t.Context(), nested), ErrLaneStoreAuthorityUnavailable)
			require.NoError(t, nested.Rollback().Error)
			require.ErrorIs(t, InstallLaneStoreAuthorityV1(t.Context(), db, in.Release), ErrLaneStoreAuthorityEvidence)
		})
	}
}

func TestLaneStoreAuthorityOrdinaryStartupRefusesEvidencePostgres(t *testing.T) {
	for _, tc := range []struct {
		name, ddl string
		installed bool
	}{
		{"partial manifest", `CREATE TABLE lane_store_authority_v1 (singleton smallint)`, false},
		{"visible orphan custody", `CREATE TABLE lane_billing_child_receipts_v1 (request_id text)`, false},
		{"spoofed relation type", `CREATE VIEW lane_store_authority_v1 AS SELECT 1 AS singleton`, false},
		{"active even with matching release", `UPDATE lane_store_authority_v1 SET state='active'`, true},
		{"authority row removed", `DELETE FROM lane_store_authority_v1`, true},
		{"authority with unexpected custody", `CREATE TABLE shared_wallet_shares (id integer)`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, in := laneStoreFixture(t, false)
			if tc.installed {
				require.NoError(t, InstallLaneStoreAuthorityV1(t.Context(), db, in.Release))
			}
			require.NoError(t, db.Exec(tc.ddl).Error)
			require.ErrorIs(t, CheckLaneStoreOrdinaryStartup(t.Context(), db), ErrLaneStoreAuthorityState)
			require.ErrorIs(t, InstallLaneStoreAuthorityV1(t.Context(), db, in.Release), ErrLaneStoreAuthorityState)
			assert.False(t, db.Migrator().HasTable(&User{}))
		})
	}
	t.Run("actual InitDB rejects before ordinary writes", func(t *testing.T) {
		db, in := laneStoreFixture(t, false)
		require.NoError(t, InstallLaneStoreAuthorityV1(t.Context(), db, in.Release))
		require.NoError(t, db.Exec(`UPDATE lane_store_authority_v1 SET state='active'`).Error)
		var schema string
		require.NoError(t, db.Raw("SELECT current_schema()").Row().Scan(&schema))
		dsn, err := url.Parse(os.Getenv("TEST_MODEL_POSTGRES_DSN"))
		require.NoError(t, err)
		q := dsn.Query()
		q.Set("search_path", schema)
		dsn.RawQuery = q.Encode()
		t.Setenv("SQL_DSN", dsn.String())
		require.ErrorIs(t, InitDB(), ErrLaneStoreAuthorityState)
		assert.False(t, db.Migrator().HasTable(&Option{}))
		assert.False(t, db.Migrator().HasTable(&User{}))
		assert.False(t, db.Migrator().HasTable(&BillingFinalization{}))
	})
}

func TestLaneStoreAuthorityOrphanInventoryAndSplitLogPostgres(t *testing.T) {
	for _, table := range []string{
		"lane_billing_child_receipts_v1", "lane_billing_key_shares_v1", "lane_billing_key_enrollments_v1",
		"lane_billing_key_transfers_v1", "shared_wallet_shares", "shared_wallet_transfers", "lane_billing_measured_dev_v1",
		"lane_billing_usage_outbox_dev_v1", "lane_billing_usage_destination_dev_v1", "lane_billing_usage_log_receipts_dev_v1",
	} {
		t.Run(table, func(t *testing.T) {
			db, _ := laneStoreFixture(t, false)
			require.NoError(t, db.Exec("CREATE TABLE "+table+" (incomplete text)").Error)
			require.ErrorIs(t, CheckLaneStoreOrdinaryStartup(t.Context(), db), ErrLaneStoreAuthorityState)
			require.ErrorIs(t, im21SchemaTransaction(db.WithContext(t.Context()), false, checkLaneStoreOrdinaryStartupTx), ErrLaneStoreAuthorityState)
			require.ErrorIs(t, im21SchemaTransaction(db.WithContext(t.Context()), true, checkLaneStoreOrdinaryStartupTx), ErrLaneStoreAuthorityState)
		})
	}
	t.Run("actual InitLogDB refuses before log migration", func(t *testing.T) {
		db, _ := laneStoreFixture(t, false)
		require.NoError(t, db.Exec("CREATE TABLE lane_billing_usage_log_receipts_dev_v1 (incomplete text)").Error)
		var schema string
		require.NoError(t, db.Raw("SELECT current_schema()").Row().Scan(&schema))
		dsn, err := url.Parse(os.Getenv("TEST_MODEL_POSTGRES_DSN"))
		require.NoError(t, err)
		q := dsn.Query()
		q.Set("search_path", schema)
		dsn.RawQuery = q.Encode()
		t.Setenv("LOG_SQL_DSN", dsn.String())
		require.ErrorIs(t, InitLogDB(), ErrLaneStoreAuthorityState)
		assert.False(t, db.Migrator().HasTable(&Log{}))
	})
}

func TestLaneStoreAuthorityRevalidatesOriginalPaidQuotePostgres(t *testing.T) {
	t.Setenv(UserGroupPricingEnabledEnv, "false")
	db, in := laneStoreFixture(t, true)
	require.NoError(t, InstallLaneStoreAuthorityV1(t.Context(), db, in.Release))
	proof := captureStage8(t, db, in)
	check := func(q LaneStoreQuoteAuthorityV1, cap int) error {
		return stage8Tx(t, db, func(tx *gorm.DB) error { return RevalidateLaneStoreQuoteAuthorityV1Tx(tx, q, cap) })
	}
	require.True(t, proof.MatchesPreparedRequest(in.RequestID, in.Group, in.GroupConfigRevision))
	require.NoError(t, check(proof, 25))
	require.ErrorIs(t, check(LaneStoreQuoteAuthorityV1{}, 25), ErrLaneStoreAuthorityEvidence)
	require.ErrorIs(t, check(proof, 1001), ErrLaneStoreAuthorityEvidence)
	_, err := CaptureLaneStoreQuoteAuthorityV1Tx(db, in)
	require.ErrorIs(t, err, ErrLaneStoreAuthorityUnavailable)
	for _, tc := range []struct {
		name, table, column string
		bad, old            any
	}{
		{"live key changed", "tokens", "key", "syntheticrotatedstage8key", "syntheticstage8finitekey"},
		{"same key revision changed", "tokens", "cache_revision", int64(6), int64(5)},
		{"key disabled", "tokens", "status", common.TokenStatusDisabled, common.TokenStatusEnabled},
		{"key expired", "tokens", "expired_time", int64(1), int64(-1)},
		{"key unlimited", "tokens", "unlimited_quota", true, false},
		{"user authority changed", "users", "auth_version", int64(5), int64(4)},
		{"reward came in", "users", "quota_reward", 1, 0},
		{"mixed policy", "users", "setting", `{"billing_preference":"wallet_first"}`, `{"billing_preference":"wallet_only"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := in.UserID
			if tc.table == "tokens" {
				id = in.TokenID
			}
			var oldSetting string
			if tc.column == "setting" {
				require.NoError(t, db.Table("users").Select("setting").Where("id = ?", id).Scan(&oldSetting).Error)
			}
			require.NoError(t, db.Table(tc.table).Where("id = ?", id).UpdateColumn(tc.column, tc.bad).Error)
			require.ErrorIs(t, check(proof, 25), ErrLaneStoreAuthorityEvidence)
			if tc.column == "setting" {
				tc.old = oldSetting
			}
			require.NoError(t, db.Table(tc.table).Where("id = ?", id).UpdateColumn(tc.column, tc.old).Error)
			require.NoError(t, check(proof, 25))
		})
	}
	require.NoError(t, db.Create(&BonusSourceState{LotID: 7, UserID: in.UserID, Available: 1}).Error)
	require.ErrorIs(t, check(proof, 25), ErrLaneStoreAuthorityEvidence)
	require.NoError(t, db.Where("lot_id = ?", 7).Delete(&BonusSourceState{}).Error)
	require.NoError(t, db.Create(&Option{Key: groupConfigVersionOptionKey, Value: "1"}).Error)
	require.ErrorIs(t, check(proof, 25), ErrLaneStoreAuthorityEvidence)
	require.NoError(t, db.Where("key = ?", groupConfigVersionOptionKey).Delete(&Option{}).Error)
	require.NoError(t, check(proof, 25))
	var laneOwners int64
	require.NoError(t, db.Model(&BillingFinalization{}).Where("source = ?", laneBillingOwnerSource).Count(&laneOwners).Error)
	assert.Zero(t, laneOwners)
}

func TestLaneStoreAuthorityEpochRevocationFencesInFlightPostgres(t *testing.T) {
	t.Setenv(UserGroupPricingEnabledEnv, "false")
	db, in := laneStoreFixture(t, true)
	require.NoError(t, InstallLaneStoreAuthorityV1(t.Context(), db, in.Release))
	proof := captureStage8(t, db, in)
	ctx, cancel := context.WithTimeout(t.Context(), laneBillingOwnerTimeout)
	defer cancel()
	unit := db.WithContext(ctx).Begin()
	require.NoError(t, unit.Error)
	defer unit.Rollback()
	require.NoError(t, RevalidateLaneStoreQuoteAuthorityV1Tx(unit, proof, 5))
	var holderPID int
	require.NoError(t, unit.Raw("SELECT pg_backend_pid()").Row().Scan(&holderPID))
	type result struct{ err error }
	finished := make(chan result, 1)
	go func() { finished <- result{RevokeLaneStoreAuthorityV1(ctx, db, in.Release)} }()
	require.Eventually(t, func() bool {
		var waiters int
		err := db.WithContext(ctx).Raw(`SELECT count(*) FROM pg_catalog.pg_stat_activity
			WHERE query LIKE '%lane_store_authority_v1%FOR UPDATE%' AND ? = ANY(pg_catalog.pg_blocking_pids(pid))`, holderPID).Row().Scan(&waiters)
		return err == nil && waiters == 1
	}, 2*time.Second, 20*time.Millisecond, "actual PostgreSQL epoch row lock must block revocation")
	require.NoError(t, unit.Rollback().Error)
	select {
	case result := <-finished:
		require.NoError(t, result.err)
	case <-ctx.Done():
		t.Fatal("revocation did not finish")
	}
	require.ErrorIs(t, stage8Tx(t, db, func(tx *gorm.DB) error { return RevalidateLaneStoreQuoteAuthorityV1Tx(tx, proof, 5) }), ErrLaneStoreAuthorityEvidence)
	var mode string
	var epoch int64
	require.NoError(t, db.Raw("SELECT state,epoch FROM lane_store_authority_v1").Row().Scan(&mode, &epoch))
	assert.Equal(t, laneStoreAuthorityRevoked, mode)
	assert.EqualValues(t, 2, epoch)
}
