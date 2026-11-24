//go:build lane_billing_integration_dev

package model

import (
	"context"
	"crypto/sha256"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	fenceDevOldPassword   = "synthetic-old-scram-only-019"
	fenceDevNewPassword   = "synthetic-new-scram-only-028"
	fenceDevProbePassword = "synthetic-probe-scram-only-037"
)

func fenceDevSQLState(t *testing.T, err error, expected string) {
	t.Helper()
	var pg *pgconn.PgError
	require.ErrorAs(t, err, &pg)
	require.Equal(t, expected, pg.Code)
}

func fenceDevConnect(ctx context.Context, base *url.URL, role, password string) (*pgx.Conn, error) {
	u := *base
	u.User = url.UserPassword(role, password)
	q := u.Query()
	q.Set("search_path", fenceDevSchema)
	u.RawQuery = q.Encode()
	return pgx.Connect(ctx, u.String())
}

func fenceDevAdmin(t *testing.T, base *url.URL) *gorm.DB {
	t.Helper()
	u := *base
	q := u.Query()
	q.Set("search_path", fenceDevSchema)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	return db
}

func fenceDevAwaitQuiet(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.Eventually(t, func() bool {
		var n int
		return db.Raw("SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE datname=current_database() AND pid<>pg_catalog.pg_backend_pid()").Row().Scan(&n) == nil && n == 0
	}, 2*time.Second, 20*time.Millisecond)
}

func TestLaneWriterRoleFenceDevPostgres(t *testing.T) {
	raw, dataDir, nonce := os.Getenv("TEST_LANE_WRITER_FENCE_POSTGRES_DSN"), os.Getenv("TEST_LANE_WRITER_FENCE_PGDATA"), os.Getenv("TEST_LANE_WRITER_FENCE_NONCE")
	if raw == "" || dataDir == "" || nonce == "" {
		t.Skip("requires a dedicated loopback disposable PostgreSQL writer-fence runner")
	}
	u, err := url.Parse(raw)
	require.NoError(t, err)
	ip, err := netip.ParseAddr(u.Hostname())
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("127.0.0.1"), ip)
	require.Equal(t, "postgres", u.Scheme)
	require.Equal(t, "/"+fenceDevDB, u.Path)
	require.Equal(t, fenceDevAudit, u.User.Username())
	require.NotEmpty(t, u.Port())
	require.Equal(t, url.Values{"sslmode": {"disable"}}, u.Query())
	require.True(t, fenceDevDataPath.MatchString(dataDir))
	require.True(t, fenceDevNonce.MatchString(nonce))

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	db := fenceDevAdmin(t, u)
	var pgData, onServerNonce, database, version string
	require.NoError(t, db.Raw(`SELECT current_setting('data_directory'),current_setting('lane_fence.fixture_nonce',true),
  current_database(),current_setting('server_version_num')`).Row().Scan(&pgData, &onServerNonce, &database, &version))
	require.Equal(t, dataDir, pgData)
	require.Equal(t, nonce, onServerNonce, "never configure principals outside the purpose-created isolated cluster")
	require.Equal(t, fenceDevDB, database)
	require.Equal(t, "180006", version)
	var preexisting int
	require.NoError(t, db.Raw("SELECT count(*) FROM pg_catalog.pg_roles WHERE rolname IN ?", []string{fenceDevOwner, fenceDevOld, fenceDevNew, fenceDevProbe}).Row().Scan(&preexisting))
	require.Zero(t, preexisting)
	var unrelated bool
	require.NoError(t, db.Raw(`SELECT EXISTS (SELECT FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON c.relnamespace=n.oid
  WHERE n.nspname IN ('public', ?))`, fenceDevSchema).Row().Scan(&unrelated))
	require.False(t, unrelated)
	for _, sql := range []string{
		"CREATE ROLE " + fenceDevOwner + " NOLOGIN",
		"CREATE ROLE " + fenceDevOld + " LOGIN PASSWORD '" + fenceDevOldPassword + "'",
		"CREATE ROLE " + fenceDevNew + " LOGIN PASSWORD '" + fenceDevNewPassword + "'",
		"CREATE ROLE " + fenceDevProbe + " LOGIN PASSWORD '" + fenceDevProbePassword + "'",
		"REVOKE ALL ON DATABASE " + fenceDevDB + " FROM PUBLIC",
		"ALTER DATABASE " + fenceDevDB + " OWNER TO " + fenceDevOwner,
		"GRANT CONNECT ON DATABASE " + fenceDevDB + " TO " + fenceDevOld + "," + fenceDevNew + "," + fenceDevProbe,
		"CREATE SCHEMA " + fenceDevSchema + " AUTHORIZATION " + fenceDevOwner,
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"GRANT USAGE ON SCHEMA " + fenceDevSchema + " TO " + fenceDevOld + "," + fenceDevNew + "," + fenceDevProbe,
		"CREATE TABLE " + fenceDevSlot + " (id smallint PRIMARY KEY CHECK(id IN (1,2)),units bigint NOT NULL CHECK(units BETWEEN 0 AND 1000))",
		"ALTER TABLE " + fenceDevSlot + " OWNER TO " + fenceDevOwner,
		"INSERT INTO " + fenceDevSlot + " VALUES (1,5),(2,13)",
		"GRANT SELECT,UPDATE ON " + fenceDevSlot + " TO " + fenceDevOld,
		"GRANT SELECT(id,units),UPDATE(units) ON " + fenceDevSlot + " TO " + fenceDevNew,
		"GRANT SELECT ON " + fenceDevSlot + " TO " + fenceDevProbe,
	} {
		require.NoError(t, db.Exec(sql).Error)
	}
	release := laneStoreTestRelease()
	in := laneCustodyBaseFixture(t, db, release)
	require.NoError(t, InstallLaneStoreAuthorityV1(ctx, db, release))
	require.NoError(t, InstallLaneCustodyTransitionV1(ctx, db, db, in))
	for _, table := range []string{laneStoreAuthorityRelation, laneCustodyRelation, im21ReceiptTable} {
		require.NoError(t, db.Exec("ALTER TABLE "+table+" OWNER TO "+fenceDevOwner).Error)
	}

	digest, err := sealWriterFenceDev(ctx, db, in, false)
	require.ErrorIs(t, err, errFenceDevPolicy, "credential admission still open")
	require.Empty(t, digest)

	legacy, err := fenceDevConnect(ctx, u, fenceDevOld, fenceDevOldPassword)
	require.NoError(t, err)
	t.Cleanup(func() { _ = legacy.Close(context.Background()) })
	var oldPID int32
	require.NoError(t, legacy.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&oldPID))
	var backendStart time.Time
	require.NoError(t, db.Raw(`SELECT a.backend_start FROM pg_catalog.pg_stat_activity a JOIN pg_catalog.pg_roles r ON r.oid=a.usesysid
  WHERE a.pid=? AND a.datname=current_database() AND r.rolname=?`, oldPID, fenceDevOld).Row().Scan(&backendStart))
	_, err = legacy.Exec(ctx, "BEGIN")
	require.NoError(t, err)
	_, err = legacy.Exec(ctx, "UPDATE "+fenceDevSlot+" SET units=units+9 WHERE id=1")
	require.NoError(t, err)
	oldPrepared, err := fenceDevConnect(ctx, u, fenceDevOld, fenceDevOldPassword)
	require.NoError(t, err)
	t.Cleanup(func() { _ = oldPrepared.Close(context.Background()) })
	_, err = oldPrepared.Exec(ctx, "BEGIN")
	require.NoError(t, err)
	_, err = oldPrepared.Exec(ctx, "UPDATE "+fenceDevSlot+" SET units=units+11 WHERE id=2")
	require.NoError(t, err)
	_, err = oldPrepared.Exec(ctx, "PREPARE TRANSACTION 'synthetic_legacy_fence_unresolved'")
	require.NoError(t, err)
	require.NoError(t, oldPrepared.Close(ctx))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("ALTER ROLE " + fenceDevOld + " NOLOGIN").Error; err != nil {
			return err
		}
		return tx.Exec("REVOKE ALL ON DATABASE " + fenceDevDB + " FROM " + fenceDevOld).Error
	}))
	conn, err := fenceDevConnect(ctx, u, fenceDevOld, fenceDevOldPassword)
	require.Nil(t, conn)
	fenceDevSQLState(t, err, "28000")
	conn, err = fenceDevConnect(ctx, u, fenceDevNew, fenceDevOldPassword)
	require.Nil(t, conn)
	fenceDevSQLState(t, err, "28P01")
	var own int
	require.NoError(t, legacy.QueryRow(ctx, "SELECT units FROM "+fenceDevSlot+" WHERE id=1").Scan(&own))
	require.Equal(t, 14, own, "NOLOGIN does not terminate an already authenticated writer")
	_, err = sealWriterFenceDev(ctx, db, in, false)
	require.ErrorIs(t, err, errFenceDevSessions)
	require.False(t, db.Migrator().HasTable(fenceDevReceipt))
	var stopped bool
	require.NoError(t, db.Raw(`SELECT pg_catalog.pg_terminate_backend(a.pid,3000)
  FROM pg_catalog.pg_stat_activity a JOIN pg_catalog.pg_roles r ON r.oid=a.usesysid
  WHERE a.pid=? AND a.backend_start=? AND a.datname=current_database() AND r.rolname=?`,
		oldPID, backendStart, fenceDevOld).Row().Scan(&stopped))
	require.True(t, stopped, "positive termination timeout must observe exit")
	_, err = legacy.Exec(ctx, "COMMIT")
	require.Error(t, err)
	var after int
	require.NoError(t, db.Raw("SELECT units FROM "+fenceDevSlot+" WHERE id=1").Row().Scan(&after))
	require.Equal(t, 5, after, "old uncommitted write rolled back")
	fenceDevAwaitQuiet(t, db)

	_, err = sealWriterFenceDev(ctx, db, in, false)
	require.ErrorIs(t, err, errFenceDevPrepared, "2PC survived old socket exit")
	require.False(t, db.Migrator().HasTable(fenceDevReceipt))
	require.NoError(t, db.Exec("ROLLBACK PREPARED 'synthetic_legacy_fence_unresolved'").Error)
	require.NoError(t, db.Raw("SELECT units FROM "+fenceDevSlot+" WHERE id=2").Row().Scan(&after))
	require.Equal(t, 13, after, "unresolved old 2PC never became a new-lane write")
	require.NoError(t, db.Exec("REVOKE ALL ON "+fenceDevSlot+" FROM "+fenceDevOld).Error)

	probe, err := fenceDevConnect(ctx, u, fenceDevProbe, fenceDevProbePassword)
	require.NoError(t, err)
	_, err = sealWriterFenceDev(ctx, db, in, false)
	require.ErrorIs(t, err, errFenceDevSessions, "unclassified or idle backend still blocks the quiet boundary")
	require.NoError(t, probe.Close(ctx))
	fenceDevAwaitQuiet(t, db)
	for _, check := range []struct{ name, grant, undo string }{
		{"public database connection", "GRANT CONNECT ON DATABASE " + fenceDevDB + " TO PUBLIC", "REVOKE CONNECT ON DATABASE " + fenceDevDB + " FROM PUBLIC"},
		{"noinherit set/admin path", "GRANT " + fenceDevNew + " TO " + fenceDevProbe + " WITH ADMIN TRUE, INHERIT FALSE, SET TRUE", "REVOKE " + fenceDevNew + " FROM " + fenceDevProbe},
		{"column-specific bypass", "GRANT UPDATE(units) ON " + fenceDevSlot + " TO " + fenceDevProbe, "REVOKE UPDATE(units) ON " + fenceDevSlot + " FROM " + fenceDevProbe},
		{"delegatable new writer grant", "GRANT UPDATE(units) ON " + fenceDevSlot + " TO " + fenceDevNew + " WITH GRANT OPTION", "REVOKE GRANT OPTION FOR UPDATE(units) ON " + fenceDevSlot + " FROM " + fenceDevNew},
		{"delegate database admission", "GRANT CONNECT ON DATABASE " + fenceDevDB + " TO " + fenceDevNew + " WITH GRANT OPTION", "REVOKE GRANT OPTION FOR CONNECT ON DATABASE " + fenceDevDB + " FROM " + fenceDevNew},
		{"unreviewed rewrite policy", "CREATE RULE synthetic_extra_slot_rule AS ON UPDATE TO " + fenceDevSlot + " DO ALSO NOTHING", "DROP RULE synthetic_extra_slot_rule ON " + fenceDevSlot},
		{"unreviewed definer executable", "CREATE FUNCTION synthetic_extra_definer() RETURNS integer LANGUAGE sql SECURITY DEFINER AS 'SELECT 0'", "DROP FUNCTION synthetic_extra_definer()"},
	} {
		t.Run(check.name, func(t *testing.T) {
			require.NoError(t, db.Exec(check.grant).Error)
			defer func() { require.NoError(t, db.Exec(check.undo).Error) }()
			_, err := sealWriterFenceDev(ctx, db, in, false)
			require.ErrorIs(t, err, errFenceDevPolicy)
			require.False(t, db.Migrator().HasTable(fenceDevReceipt))
		})
	}

	digest, err = sealWriterFenceDev(ctx, db, in, false)
	require.NoError(t, err)
	require.True(t, laneStoreLowerHex(digest, 64))
	require.False(t, LaneStoreRuntimeCapable)
	require.ErrorIs(t, CheckLaneCustodyCapableStartupV1(ctx, db, db, in), ErrLaneCustodyFleetFenceRequired, "this sealed isolated result is not production authority")
	pool, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, pool.Close())
	db = fenceDevAdmin(t, u)
	reopened, err := sealWriterFenceDev(ctx, db, in, true)
	require.NoError(t, err)
	require.Equal(t, digest, reopened, "receipt survives a fresh authenticated audit connection")
	var evidence string
	require.NoError(t, db.Raw("SELECT evidence FROM "+fenceDevReceipt).Row().Scan(&evidence))
	require.Contains(t, evidence, `"MaxPreparedTransactions":4`)
	require.Contains(t, evidence, `"PreparedTransactions":0`)
	require.NotContains(t, evidence, fenceDevNewPassword)

	t.Run("role receipt is not payment ownership", func(t *testing.T) {
		// The real role receipt and the real no-custody catalogue agree.
		// They cannot be repurposed as the existing tagged custody issuer.
		authority, err := InstallLaneCustodyFixtureV1(ctx, db, db, in.Release, 1)
		require.ErrorIs(t, err, ErrLaneStoreAuthorityEvidence)
		require.Nil(t, authority)

		oldPrimary, oldLog := DB, LOG_DB
		DB, LOG_DB = db, db
		defer func() { DB, LOG_DB = oldPrimary, oldLog }()
		// Bypass no checks: pass a well-formed synthetic draft and signer,
		// but only the zero, non-issued scope available on this endpoint.
		draft := TopUp{UserId: 431, Amount: 1, Money: 1, TradeNo: "SYNTHETIC_FENCE_RECEIPT_NOT_ORDER",
			PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, QuotedWalletQuota: 7,
			QuotedPaymentMinor: 100, QuotedPaymentCurrency: "CNY", QuotedPaymentScope: "631210", QuotedPaymentEnvironment: "test"}
		bound := &BoundLaneEpayOrder{
			scope:       laneEpaySettlementScope{db, fenceDevSchema, draft.UserId, 1, draft.TradeNo},
			authority:   &LaneCustodyAuthorityV1{},
			orderSigner: &laneEpayOrderSigner{keyID: "isolated-signing-fixture", key: sha256.Sum256([]byte("synthetic-inert-signing-fixture"))},
		}
		created, err := createBoundLaneEpayOrder(ctx, bound, draft)
		require.ErrorIs(t, err, ErrLaneStoreAuthorityEvidence, "authority must fail before any attempt to adopt/create an order")
		require.Zero(t, created)
		observed, err := bound.Status(ctx)
		require.ErrorIs(t, err, ErrLaneStoreAuthorityEvidence)
		require.Zero(t, observed)
		require.False(t, db.Migrator().HasTable(&TopUp{}))
		require.False(t, db.Migrator().HasTable(&laneEpayOrderBindingV1{}))
	})

	for _, actor := range []struct{ role, password string }{{fenceDevProbe, fenceDevProbePassword}, {fenceDevNew, fenceDevNewPassword}} {
		c, err := fenceDevConnect(ctx, u, actor.role, actor.password)
		require.NoError(t, err)
		_, err = c.Exec(ctx, "UPDATE "+fenceDevReceipt+" SET digest='not trusted' WHERE singleton=1")
		fenceDevSQLState(t, err, "42501")
		_, err = c.Exec(ctx, "UPDATE "+laneCustodyRelation+" SET wallet_epoch=99")
		fenceDevSQLState(t, err, "42501")
		_, err = c.Exec(ctx, "UPDATE "+fenceDevSlot+" SET id=2 WHERE id=1")
		fenceDevSQLState(t, err, "42501")
		change, err := c.Exec(ctx, "UPDATE "+fenceDevSlot+" SET units=units+1 WHERE id=1")
		if actor.role == fenceDevNew {
			require.NoError(t, err)
			require.EqualValues(t, 1, change.RowsAffected())
			_, err = c.Exec(ctx, "UPDATE "+fenceDevSlot+" SET units=1001 WHERE id=1")
			fenceDevSQLState(t, err, "23514")
		} else {
			fenceDevSQLState(t, err, "42501")
		}
		require.NoError(t, c.Close(ctx))
	}
	fenceDevAwaitQuiet(t, db)
	require.NoError(t, db.Raw("SELECT units FROM "+fenceDevSlot+" WHERE id=1").Row().Scan(&after))
	require.Equal(t, 6, after)
	require.NoError(t, db.Exec("GRANT UPDATE(units) ON "+fenceDevSlot+" TO "+fenceDevProbe).Error)
	_, err = sealWriterFenceDev(ctx, db, in, true)
	require.ErrorIs(t, err, errFenceDevPolicy, "a restored privilege cannot be overridden by an old receipt")
	require.NoError(t, db.Exec("REVOKE UPDATE(units) ON "+fenceDevSlot+" FROM "+fenceDevProbe).Error)
	require.NoError(t, db.Exec("ALTER TABLE "+fenceDevReceipt+" SET UNLOGGED").Error)
	_, err = sealWriterFenceDev(ctx, db, in, true)
	require.ErrorIs(t, err, errFenceDevPolicy, "same-OID unlogged evidence is not a durable receipt")
	require.NoError(t, db.Exec("ALTER TABLE "+fenceDevReceipt+" SET LOGGED").Error)
	require.NoError(t, db.Exec("ALTER TABLE "+fenceDevSlot+" ADD CONSTRAINT synthetic_constraint_drift CHECK(units >= 0)").Error)
	_, err = sealWriterFenceDev(ctx, db, in, true)
	require.ErrorIs(t, err, errFenceDevReceipt, "same-OID constraint changes invalidate the saved physical scope")
	require.NoError(t, db.Exec("ALTER TABLE "+fenceDevSlot+" DROP CONSTRAINT synthetic_constraint_drift").Error)
	wrong := in
	wrong.Release.ReleaseID = strings.Repeat("1", 32)
	_, err = sealWriterFenceDev(ctx, db, wrong, true)
	require.ErrorIs(t, err, errFenceDevPolicy)
}
