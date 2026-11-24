package model

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLaneOrdinaryMarkerCompatibilityPostgres(t *testing.T) {
	for _, tc := range []struct {
		name, mutation           string
		prepare, revoke, blocked bool
	}{
		{name: "ordinary existing wallet and finite Key"},
		{name: "prepared no custody", prepare: true},
		{name: "revoked once after preparation", prepare: true, revoke: true},
		{name: "orphan catalogue", prepare: true, mutation: "DROP TABLE lane_store_authority_v1", blocked: true},
		{name: "fixture despite missing custody tables", prepare: true, mutation: "UPDATE lane_custody_catalog_v1 SET profile='isolated_fixture',wallet_epoch=1", blocked: true},
		{name: "wrong store", prepare: true, mutation: "UPDATE lane_custody_catalog_v1 SET store_id=repeat('9',32)", blocked: true},
		{name: "restored schema OID", prepare: true, mutation: "UPDATE lane_custody_catalog_v1 SET schema_oid=schema_oid+1", blocked: true},
		{name: "revoked generation gap", prepare: true, revoke: true, mutation: "UPDATE lane_store_authority_v1 SET epoch=epoch+1", blocked: true},
		{name: "invalid base declaration", prepare: true, mutation: "UPDATE lane_custody_catalog_v1 SET migration_run_id='missing-base'", blocked: true},
		{name: "empty catalogue", prepare: true, mutation: "DELETE FROM lane_custody_catalog_v1", blocked: true},
		{name: "unlogged marker", prepare: true, mutation: "ALTER TABLE lane_custody_catalog_v1 SET UNLOGGED", blocked: true},
		{name: "row-filtered marker", prepare: true, mutation: "ALTER TABLE lane_custody_catalog_v1 ENABLE ROW LEVEL SECURITY", blocked: true},
		{name: "child of a different table", prepare: true, mutation: "CREATE TABLE inheritance_parent (singleton smallint NOT NULL); ALTER TABLE lane_custody_catalog_v1 INHERIT inheritance_parent", blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, quote := laneStoreFixture(t, true)
			if tc.prepare {
				in := laneCustodyBaseFixture(t, db, quote.Release)
				require.NoError(t, InstallLaneStoreAuthorityV1(t.Context(), db, in.Release))
				require.NoError(t, InstallLaneCustodyTransitionV1(t.Context(), db, db, in))
				if tc.revoke {
					require.NoError(t, RevokeLaneStoreAuthorityV1(t.Context(), db, in.Release))
				}
			}
			if tc.mutation != "" {
				require.NoError(t, db.Exec(tc.mutation).Error)
			}
			for _, err := range []error{
				CheckLaneStoreOrdinaryStartup(t.Context(), db),
				im21SchemaTransaction(db.WithContext(t.Context()), false, checkLaneStoreOrdinaryStartupTx),
				im21SchemaTransaction(db.WithContext(t.Context()), true, checkLaneStoreOrdinaryStartupTx),
			} {
				if tc.blocked {
					require.ErrorIs(t, err, ErrLaneStoreAuthorityState)
				} else {
					require.NoError(t, err)
				}
			}
			var wallet User
			var key Token
			var prior BillingFinalization
			require.NoError(t, db.First(&wallet, quote.UserID).Error)
			require.NoError(t, db.First(&key, quote.TokenID).Error)
			require.NoError(t, db.Where("request_id = ?", "historic-ordinary-stage8").Take(&prior).Error)
			require.Equal(t, []int{1000, 1000, 1000, 9, 9}, []int{wallet.Quota, wallet.QuotaPaid, key.RemainQuota, key.UsedQuota, prior.ActualQuota})
			require.Equal(t, BillingSettled, prior.State)
			require.False(t, db.Migrator().HasTable("shared_wallet_shares"))
			require.Equal(t, tc.prepare, db.Migrator().HasTable(laneCustodyRelation))
		})
	}
}

func TestLaneOrdinaryOrphanMarkerStopsStartupPostgres(t *testing.T) {
	dsn := func(t *testing.T, db *gorm.DB) string {
		t.Helper()
		var schema string
		require.NoError(t, db.Raw("SELECT current_schema()").Row().Scan(&schema))
		u, err := url.Parse(os.Getenv("TEST_MODEL_POSTGRES_DSN"))
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String()
	}
	for _, target := range []string{"ordinary primary", "ordinary split log", "managed primary migrate", "managed split log serve", "managed split log migrate"} {
		t.Run(target, func(t *testing.T) {
			primary, _ := laneStoreFixture(t, false)
			log := primary
			bad := primary
			if strings.Contains(target, "split log") {
				log, _ = laneStoreFixture(t, false)
				bad = log
				t.Setenv("LOG_SQL_DSN", dsn(t, log))
			} else {
				t.Setenv("LOG_SQL_DSN", "")
			}
			t.Setenv("SQL_DSN", dsn(t, primary))
			require.NoError(t, bad.Exec("CREATE TABLE lane_custody_catalog_v1 (partial text)").Error)
			// Existing split-migrator fail-closed contract: a separately
			// committed primary receipt invalidation precedes checking LOG.
			require.NoError(t, writeIM21Receipt(primary, "primary", strings.Repeat("a", 64), "previous:run"))
			previousDB, previousLog, master := DB, LOG_DB, common.IsMasterNode
			common.IsMasterNode = true
			t.Cleanup(func() {
				for _, db := range []*gorm.DB{DB, LOG_DB} {
					if db != nil && db != previousDB && db != previousLog {
						if pool, err := db.DB(); err == nil {
							_ = pool.Close()
						}
					}
				}
				DB, LOG_DB, common.IsMasterNode = previousDB, previousLog, master
			})
			t.Setenv("CORE_IM21_RELEASE_ID", strings.Repeat("a", 32))
			switch target {
			case "ordinary primary":
				require.ErrorIs(t, InitDB(), ErrLaneStoreAuthorityState)
			case "ordinary split log":
				require.ErrorIs(t, InitLogDB(), ErrLaneStoreAuthorityState)
			case "managed split log serve":
				require.ErrorIs(t, InitIM21Databases(IM21Serve), ErrLaneStoreAuthorityState)
			default:
				require.ErrorIs(t, InitIM21Databases(IM21Migrate), ErrLaneStoreAuthorityState)
			}
			var receipt int64
			require.NoError(t, primary.Table(im21ReceiptTable).Where("role = 'primary' AND run_id = 'previous:run'").Count(&receipt).Error)
			if target == "managed split log migrate" {
				require.Zero(t, receipt)
			} else {
				require.EqualValues(t, 1, receipt)
			}
			require.False(t, primary.Migrator().HasTable(&User{}))
			require.False(t, primary.Migrator().HasTable(&Option{}))
			require.False(t, log.Migrator().HasTable(&Log{}))
		})
	}
}
