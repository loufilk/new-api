package main

import (
	"crypto/sha256"
	"database/sql"
	"debug/buildinfo"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const fixtureDB = "lane_precustody_audit_test"
const fixtureSchema = "lane_audit_store"

func fixtureInventory() inventoryV1 {
	maxPrepared := 4
	return inventoryV1{
		Version: 1, Topology: "one_direct_shared_main_log_primary",
		InventorySHA256: strings.Repeat("a", 64), SourceRevision: strings.Repeat("b", 40),
		WritableEndpoints: []endpointV1{{Label: "lab-shared", Store: model.LaneOperatorStoreV1{
			SystemID: "876543210", Timeline: 1, VersionNum: 180006,
			ServerAddr: "127.0.0.1", ServerPort: 28838, Database: fixtureDB, Schema: fixtureSchema,
			DatabaseOID: 10, SchemaOID: 11, AuthorityOID: 12, CatalogOID: 13, MigrationOID: 14, MaxPrepared: &maxPrepared,
			Auditor:        model.LaneOperatorRoleV1{Name: "lane_auditor", OID: 21},
			Owner:          model.LaneOperatorRoleV1{Name: "lane_owner", OID: 22},
			RetiredWriters: []model.LaneOperatorRoleV1{{Name: "lane_old", OID: 23}},
			NextWriters:    []model.LaneOperatorRoleV1{{Name: "lane_next", OID: 24}},
			ControlAdmins:  []model.LaneOperatorRoleV1{{Name: "agent", OID: 25}},
			Transition: model.LaneCustodyTransitionV1{
				Release: model.LaneStoreReleaseV1{StoreID: strings.Repeat("c", 32), ReleaseID: strings.Repeat("d", 32),
					BinarySHA256: strings.Repeat("e", 64), Epoch: 1},
				BaseReleaseID: strings.Repeat("f", 32), BaseBinarySHA256: strings.Repeat("1", 64),
				MigrationRunID: strings.Repeat("f", 32) + ":synthetic-admission-v1",
			},
		}}},
	}
}

func TestPreCustodyInventoryIsExplicit(t *testing.T) {
	in := fixtureInventory()
	raw, err := common.Marshal(in)
	require.NoError(t, err)
	_, err = decodeInventory(raw)
	require.NoError(t, err)
	for name, alter := range map[string]func(*inventoryV1){
		"multiple-writable":         func(p *inventoryV1) { p.WritableEndpoints = append(p.WritableEndpoints, p.WritableEndpoints[0]) },
		"missing-writable":          func(p *inventoryV1) { p.WritableEndpoints = nil },
		"dedicated-log":             func(p *inventoryV1) { p.Topology = "split_main_log" },
		"unknown-prepared-mode":     func(p *inventoryV1) { p.WritableEndpoints[0].Store.MaxPrepared = nil },
		"role-alias":                func(p *inventoryV1) { p.WritableEndpoints[0].Store.NextWriters[0] = p.WritableEndpoints[0].Store.Owner },
		"invalid-physical-address":  func(p *inventoryV1) { p.WritableEndpoints[0].Store.ServerAddr = "0.0.0.0" },
		"unmeasured-source":         func(p *inventoryV1) { p.SourceRevision = "" },
		"missing-external-evidence": func(p *inventoryV1) { p.InventorySHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			p := fixtureInventory()
			alter(&p)
			raw, err := common.Marshal(p)
			require.NoError(t, err)
			_, err = decodeInventory(raw)
			require.ErrorIs(t, err, errInventory)
		})
	}
	_, err = decodeInventory([]byte(strings.Replace(string(raw), "{", `{"unknown":1,`, 1)))
	require.ErrorIs(t, err, errInventory)
	_, err = decodeInventory(append(raw, []byte("{}")...))
	require.ErrorIs(t, err, errInventory)
	for _, bad := range []string{
		strings.Replace(string(raw), `"Epoch":1`, `"Epoch":1,"Epoch":1`, 1),
		strings.Replace(string(raw), `"Epoch":1`, `"epoch":1`, 1),
		strings.Replace(string(raw), `"Epoch":1`, `"Epoch":1,"epoch":1`, 1),
	} {
		require.NotEqual(t, string(raw), bad)
		_, err = decodeInventory([]byte(bad))
		require.ErrorIs(t, err, errInventory)
	}
}

func TestPreCustodyRejectsRedirectAndUnmeasuredSource(t *testing.T) {
	p := fixtureInventory().WritableEndpoints[0].Store
	for _, suffix := range []string{
		"?sslmode=disable", "?sslmode=require&sslrootcert=/does/not/exist",
		"?sslmode=verify-full&sslrootcert=/does/not/exist&hostaddr=127.0.0.2",
		"?sslmode=verify-full&sslrootcert=/does/not/exist&sslmode=disable",
	} {
		_, err := directConfig("postgres://lane_auditor:fixture-redacted@127.0.0.1:28838/"+fixtureDB+suffix, p)
		require.ErrorIs(t, err, errDirect)
	}
	v := strings.Repeat("b", 40)
	info := &debug.BuildInfo{Path: auditModule, Main: debug.Module{Path: auditModule},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: v}, {Key: "vcs.modified", Value: "false"}}}
	require.Equal(t, v, cleanRevision(info, auditModule))
	require.Empty(t, cleanRevision(info, auditModule+"/cmd/lane-precustody-audit"))
	info.Settings[1].Value = "true"
	require.Empty(t, cleanRevision(info, auditModule))
	require.False(t, matchesCandidate(t.TempDir(), v, strings.Repeat("a", 64)))
}

func TestPreCustodyDirectTLSPostgres(t *testing.T) {
	path := os.Getenv("TEST_LANE_PRECUSTODY_PGDATA")
	if path == "" {
		t.Skip("requires dedicated loopback PostgreSQL 18.6 TLS fixture")
	}
	port, err := strconv.Atoi(os.Getenv("TEST_LANE_PRECUSTODY_PORT"))
	require.NoError(t, err)
	require.Greater(t, port, 1023)
	require.Less(t, port, 65536)
	require.Regexp(t, `^/tmp/lane-precustody-audit\.[A-Za-z0-9]+/pg$`, path)
	nonce, secret := os.Getenv("TEST_LANE_PRECUSTODY_NONCE"), os.Getenv("TEST_LANE_PRECUSTODY_PASSWORD")
	goodHex := regexp.MustCompile(`^[a-f0-9]{32}$`)
	require.True(t, goodHex.MatchString(nonce) && goodHex.MatchString(secret), "invalid disposable fixture guard")
	cert := filepath.Join(filepath.Dir(path), "server.crt")
	dsnFor := func(user, host, database string) string {
		u := url.URL{Scheme: "postgresql", User: url.UserPassword(user, secret), Host: host + ":" + strconv.Itoa(port), Path: "/" + database,
			RawQuery: url.Values{"sslmode": {"verify-full"}, "sslrootcert": {cert}}.Encode()}
		return u.String()
	}
	admin := func(database string) *sql.DB {
		conf, err := pgx.ParseConfig(dsnFor("agent", "127.0.0.1", database))
		require.True(t, err == nil, "local fixture settings")
		db := stdlib.OpenDB(*conf)
		t.Cleanup(func() { _ = db.Close() })
		var ok bool
		err = db.QueryRow(`SELECT current_setting('data_directory')=$1
		 AND current_setting('lane_audit.fixture_nonce',true)=$2 AND inet_server_addr()='127.0.0.1'`, path, nonce).Scan(&ok)
		require.True(t, err == nil && ok, "only fresh, nonce-matched loopback PostgreSQL is allowed")
		return db
	}
	execSQL := func(db *sql.DB, q string) {
		_, err := db.Exec(q)
		require.NoError(t, err)
	}
	in := fixtureInventory()
	auditBin, candidatePath := os.Getenv("TEST_LANE_PRECUSTODY_AUDIT_BIN"), os.Getenv("TEST_LANE_PRECUSTODY_CANDIDATE")
	require.True(t, filepath.Dir(auditBin) == filepath.Dir(path) && filepath.Dir(candidatePath) == filepath.Dir(path),
		"requires local verifier and offline candidate fixture binaries")
	candidateInfo, err := buildinfo.ReadFile(candidatePath)
	require.NoError(t, err)
	in.SourceRevision = cleanRevision(candidateInfo, auditModule)
	require.NotEmpty(t, in.SourceRevision)
	candidate, err := os.Open(candidatePath)
	require.NoError(t, err)
	h := sha256.New()
	_, err = io.Copy(h, candidate)
	require.NoError(t, err)
	require.NoError(t, candidate.Close())
	p := &in.WritableEndpoints[0].Store
	p.Transition.Release.BinarySHA256 = hex.EncodeToString(h.Sum(nil))
	p.ServerPort = port
	setup := admin(fixtureDB)
	for _, q := range []string{
		"CREATE ROLE lane_owner NOLOGIN",
		"CREATE ROLE lane_old NOLOGIN",
		"CREATE ROLE lane_next NOLOGIN",
		"CREATE ROLE lane_unknown LOGIN",
		"CREATE ROLE lane_auditor LOGIN PASSWORD '" + secret + "'",
		"GRANT pg_read_all_stats TO lane_auditor WITH INHERIT TRUE, SET FALSE, ADMIN FALSE",
		"GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_system() TO lane_auditor",
		"GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_checkpoint() TO lane_auditor",
		"GRANT SELECT(oid) ON pg_catalog.pg_subscription TO lane_auditor",
		"ALTER DATABASE " + fixtureDB + " OWNER TO lane_owner",
		"REVOKE ALL ON DATABASE " + fixtureDB + " FROM PUBLIC",
		"GRANT CONNECT ON DATABASE " + fixtureDB + " TO lane_auditor",
		"CREATE SCHEMA " + fixtureSchema + " AUTHORIZATION lane_owner",
		"GRANT USAGE ON SCHEMA " + fixtureSchema + " TO lane_auditor",
		"CREATE TABLE " + fixtureSchema + ".wallet_stub (units bigint NOT NULL)",
		"ALTER TABLE " + fixtureSchema + ".wallet_stub OWNER TO lane_owner",
		"CREATE TABLE " + fixtureSchema + ".core_im21_schema_receipts (role text PRIMARY KEY,binary_sha256 text NOT NULL,run_id text NOT NULL)",
	} {
		execSQL(setup, q)
	}
	_, err = setup.Exec("INSERT INTO "+fixtureSchema+".core_im21_schema_receipts VALUES ('primary',$1,$2),('log',$1,$2)",
		p.Transition.BaseBinarySHA256, p.Transition.MigrationRunID)
	require.NoError(t, err)
	conf, err := pgx.ParseConfig(dsnFor("agent", "127.0.0.1", fixtureDB))
	require.True(t, err == nil, "local fixture settings")
	conf.RuntimeParams["search_path"] = fixtureSchema
	bridge := stdlib.OpenDB(*conf)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: bridge}), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, model.InstallLaneStoreAuthorityV1(t.Context(), gdb, p.Transition.Release))
	require.NoError(t, model.InstallLaneCustodyTransitionV1(t.Context(), gdb, gdb, p.Transition))
	require.NoError(t, bridge.Close())
	for _, name := range []string{"lane_store_authority_v1", "lane_custody_catalog_v1", "core_im21_schema_receipts"} {
		execSQL(setup, "ALTER TABLE "+fixtureSchema+"."+name+" OWNER TO lane_owner")
		execSQL(setup, "GRANT SELECT ON "+fixtureSchema+"."+name+" TO lane_auditor")
	}
	err = setup.QueryRow(`SELECT s.system_identifier::text,c.timeline_id::bigint,
		(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),$1::regnamespace::oid::bigint,
		to_regclass($1 || '.lane_store_authority_v1')::oid::bigint,to_regclass($1 || '.lane_custody_catalog_v1')::oid::bigint,
		to_regclass($1 || '.core_im21_schema_receipts')::oid::bigint
		FROM pg_catalog.pg_control_system() s CROSS JOIN pg_catalog.pg_control_checkpoint() c`, fixtureSchema).
		Scan(&p.SystemID, &p.Timeline, &p.DatabaseOID, &p.SchemaOID, &p.AuthorityOID, &p.CatalogOID, &p.MigrationOID)
	require.NoError(t, err)
	for _, r := range []*model.LaneOperatorRoleV1{&p.Auditor, &p.Owner, &p.RetiredWriters[0], &p.NextWriters[0], &p.ControlAdmins[0]} {
		err = setup.QueryRow("SELECT oid::bigint FROM pg_catalog.pg_roles WHERE rolname=$1", r.Name).Scan(&r.OID)
		require.NoError(t, err)
	}
	require.NoError(t, setup.Close())
	check := func(v inventoryV1, dsn string) (receiptV1, error) {
		raw, err := common.Marshal(v)
		require.NoError(t, err)
		v, err = decodeInventory(raw)
		require.NoError(t, err)
		return audit(t.Context(), v, raw, dsn)
	}
	scoped := dsnFor(p.Auditor.Name, "lane-audit.test", fixtureDB) // dial pinned IPv4, verify certificate DNS SAN
	t.Run("built CLI measures exact offline Go app artifact and writes only receipt", func(t *testing.T) {
		raw, err := common.Marshal(in)
		require.NoError(t, err)
		invPath := filepath.Join(t.TempDir(), "inventory.json")
		require.NoError(t, os.WriteFile(invPath, raw, 0600))
		argv := []string{"-inventory", invPath, "-candidate", candidatePath}
		cmd := exec.CommandContext(t.Context(), auditBin, argv...)
		for _, variable := range os.Environ() {
			if !strings.HasPrefix(variable, "PG") {
				cmd.Env = append(cmd.Env, variable)
			}
		}
		cmd.Env = append(cmd.Env, auditDSNEnv+"="+scoped)
		out, err := cmd.Output()
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("built CLI failed: %s", exit.Stderr)
		}
		require.NoError(t, err)
		var got receiptV1
		require.NoError(t, common.DecodeJsonStrict(strings.NewReader(string(out)), &got))
		require.Equal(t, in.SourceRevision, got.SourceRevision)
		require.Equal(t, p.Transition.Release.BinarySHA256, got.CandidateBinarySHA256)
		require.Len(t, got.Samples, 2)
		require.False(t, strings.Contains(string(out), secret) || strings.Contains(string(out), scoped), "receipt carried connection input")
		// Well-formed CLI/inventory with the verifier substituted for the
		// candidate must fail before any PostgreSQL access.
		cmd = exec.CommandContext(t.Context(), auditBin, "-inventory", invPath, "-candidate", auditBin)
		cmd.Env = []string{auditDSNEnv + "=not-a-url"}
		out, err = cmd.Output()
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		require.Equal(t, errSource.Error(), strings.TrimSpace(string(exit.Stderr)))
		require.Empty(t, out)
	})
	t.Run("two fresh read-only tls observations", func(t *testing.T) {
		r, err := check(in, scoped)
		require.NoError(t, err)
		require.Len(t, r.Samples, 2)
		require.NotEqual(t, r.Samples[0].BackendPID, r.Samples[1].BackendPID)
		require.False(t, r.CustodyAuthorized || r.ExternalCoverageVerified)
		require.Equal(t, 1, int(r.Epoch))
		for _, s := range r.Samples {
			require.Equal(t, 4, s.MaxPrepared)
			require.Zero(t, s.Prepared)
			require.Zero(t, s.OtherBackends)
		}
		raw, err := common.Marshal(r)
		require.NoError(t, err)
		require.False(t, strings.Contains(string(raw), secret) || strings.Contains(string(raw), path) || strings.Contains(string(raw), scoped), "receipt leaked connection inputs")
	})
	t.Run("certificate identity", func(t *testing.T) {
		r, err := check(in, dsnFor(p.Auditor.Name, "wrong-host.test", fixtureDB))
		require.Error(t, err)
		require.Empty(t, r.Samples)
	})
	t.Run("active client in another database", func(t *testing.T) {
		conn := admin("postgres")
		defer conn.Close()
		r, err := check(in, scoped)
		require.ErrorIs(t, err, model.ErrLaneOperatorBusy)
		require.Empty(t, r.Samples)
	})
	t.Run("real prepared work survives disconnect in another database", func(t *testing.T) {
		conn := admin("postgres")
		execSQL(conn, "CREATE TABLE lane_prepared_probe (units bigint)")
		execSQL(conn, "BEGIN; INSERT INTO lane_prepared_probe VALUES (1); PREPARE TRANSACTION 'lane_audit_synthetic'")
		require.NoError(t, conn.Close())
		defer func() {
			conn := admin("postgres")
			execSQL(conn, "ROLLBACK PREPARED 'lane_audit_synthetic'")
			require.NoError(t, conn.Close())
		}()
		r, err := check(in, scoped)
		require.ErrorIs(t, err, model.ErrLaneOperatorPrepared)
		require.Empty(t, r.Samples)
	})
	t.Run("residual old writer ACL and direct unknown login", func(t *testing.T) {
		for _, tc := range []struct{ on, off string }{
			{"GRANT UPDATE(units) ON " + fixtureSchema + ".wallet_stub TO lane_old", "REVOKE UPDATE(units) ON " + fixtureSchema + ".wallet_stub FROM lane_old"},
			{"GRANT CONNECT ON DATABASE " + fixtureDB + " TO lane_unknown", "REVOKE CONNECT ON DATABASE " + fixtureDB + " FROM lane_unknown"},
			{"GRANT lane_owner TO lane_old WITH SET TRUE", "REVOKE lane_owner FROM lane_old"},
			{"GRANT pg_signal_backend TO pg_read_all_stats", "REVOKE pg_signal_backend FROM pg_read_all_stats"},
			{"GRANT ALTER SYSTEM ON PARAMETER statement_timeout TO lane_auditor", "REVOKE ALTER SYSTEM ON PARAMETER statement_timeout FROM lane_auditor"},
			{"GRANT SELECT(subconninfo) ON pg_catalog.pg_subscription TO lane_auditor", "REVOKE SELECT(subconninfo) ON pg_catalog.pg_subscription FROM lane_auditor"},
			{"CREATE FUNCTION pg_catalog.lane_audit_probe() RETURNS integer LANGUAGE sql SECURITY DEFINER AS 'SELECT 1'",
				"DROP FUNCTION pg_catalog.lane_audit_probe()"},
			{"CREATE RULE lane_audit_probe AS ON UPDATE TO pg_catalog.pg_settings DO ALSO SELECT 1",
				"DROP RULE lane_audit_probe ON pg_catalog.pg_settings"},
		} {
			conn := admin(fixtureDB)
			execSQL(conn, tc.on)
			require.NoError(t, conn.Close())
			_, err := check(in, scoped)
			require.ErrorIs(t, err, model.ErrLaneOperatorRoles)
			conn = admin(fixtureDB)
			execSQL(conn, tc.off)
			require.NoError(t, conn.Close())
		}
	})
	t.Run("manifest shadow and migration revision drift", func(t *testing.T) {
		for _, tc := range []struct{ on, off string }{
			{"CREATE TABLE public.lane_custody_catalog_v1(dummy integer)", "DROP TABLE public.lane_custody_catalog_v1"},
			{"UPDATE " + fixtureSchema + ".core_im21_schema_receipts SET run_id='stale' WHERE role='log'",
				"UPDATE " + fixtureSchema + ".core_im21_schema_receipts SET run_id='" + p.Transition.MigrationRunID + "' WHERE role='log'"},
		} {
			conn := admin(fixtureDB)
			execSQL(conn, tc.on)
			require.NoError(t, conn.Close())
			_, err := check(in, scoped)
			require.ErrorIs(t, err, model.ErrLaneOperatorCatalog)
			conn = admin(fixtureDB)
			execSQL(conn, tc.off)
			require.NoError(t, conn.Close())
		}
	})
	t.Run("wrong database identity or prepared mode", func(t *testing.T) {
		orig := p.DatabaseOID
		p.DatabaseOID = 777
		_, err := check(in, scoped)
		require.ErrorIs(t, err, model.ErrLaneOperatorIdentity)
		p.DatabaseOID = orig
		zero := 0
		p.MaxPrepared = &zero
		_, err = check(in, scoped)
		require.ErrorIs(t, err, model.ErrLaneOperatorPrepared)
	})
}
