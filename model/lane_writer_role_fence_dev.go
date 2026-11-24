//go:build lane_billing_integration_dev

package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	fenceDevDB      = "lane_writer_fence_test"
	fenceDevSchema  = "lane_billing_dev_writerfence"
	fenceDevAudit   = "agent"
	fenceDevOwner   = "lane_fence_owner"
	fenceDevOld     = "lane_fence_legacy"
	fenceDevNew     = "lane_fence_writer"
	fenceDevProbe   = "lane_fence_probe"
	fenceDevSlot    = "lane_writer_fence_slot_v1"
	fenceDevReceipt = "lane_writer_fence_receipt_v1"
)

var (
	errFenceDevPolicy   = errors.New("isolated PostgreSQL writer policy is incomplete")
	errFenceDevSessions = errors.New("isolated PostgreSQL database still has a client or background backend")
	errFenceDevPrepared = errors.New("isolated PostgreSQL database still has prepared transactions")
	errFenceDevReceipt  = errors.New("isolated PostgreSQL writer fence receipt is missing or changed")
	fenceDevDataPath    = regexp.MustCompile(`^/tmp/lane-role-fence\.[A-Za-z0-9]+/pg$`)
	fenceDevNonce       = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

type fenceDevRole struct {
	Name               string
	OID                int64 `gorm:"column:oid"`
	CanLogin, Elevated bool
}

type fenceDevRelation struct {
	Name, Schema, Kind, Persistence string
	Constraints                     string
	RowSecurity                     bool
	OID                             int64 `gorm:"column:oid"`
	Owner                           int64
}

type fenceDevSnapshot struct {
	Version                                       int
	SystemID                                      string
	MaxPreparedTransactions, PreparedTransactions int
	Space                                         laneCustodySpaceV1
	Transition                                    LaneCustodyTransitionV1
	Roles                                         []fenceDevRole
	Relations                                     []fenceDevRelation
}

func fenceDevTrue(tx *gorm.DB, q string, args ...any) error {
	var ok bool
	if err := tx.Raw(q, args...).Row().Scan(&ok); err != nil || !ok {
		return errFenceDevPolicy
	}
	return nil
}

// This verifier never changes authentication, ACLs, or session state.
// It only understands the isolated fixture and issues no runtime authority.
func inspectWriterFenceDevTx(tx *gorm.DB, in LaneCustodyTransitionV1) (fenceDevSnapshot, bool, error) {
	var out fenceDevSnapshot
	if !in.valid() {
		return out, false, errFenceDevPolicy
	}
	schema, err := laneBillingDevPGSchemaTx(tx)
	if err != nil || schema != fenceDevSchema {
		return out, false, errFenceDevPolicy
	}
	var dbName, current, session, systemID, dataDir, nonce string
	var regular bool
	err = tx.Raw(`SELECT pg_catalog.current_database(),CURRENT_USER,SESSION_USER,s.system_identifier::text,
  NOT pg_catalog.pg_is_in_recovery(),pg_catalog.current_setting('data_directory'),
  coalesce(pg_catalog.current_setting('lane_fence.fixture_nonce',true),'')
  FROM pg_catalog.pg_control_system() s`).Row().Scan(&dbName, &current, &session, &systemID, &regular, &dataDir, &nonce)
	if err != nil || dbName != fenceDevDB || current != fenceDevAudit || session != fenceDevAudit || systemID == "" || !regular ||
		!fenceDevDataPath.MatchString(dataDir) || !fenceDevNonce.MatchString(nonce) {
		return out, false, errFenceDevPolicy
	}
	// Deliberately a strict fixture shape, not a production HBA parser.
	// Store no password/hash. Fresh positive and negative authentication
	// probes in the rehearsal separately prove the actual loaded policy.
	if err := fenceDevTrue(tx, `SELECT pg_catalog.current_setting('unix_socket_directories')='' AND count(*)=1 AND count(*) FILTER(WHERE
		error IS NULL AND coalesce(cardinality(options),0)=0 AND database=ARRAY['all']::text[] AND
		type='host' AND user_name=ARRAY['all']::text[] AND address='127.0.0.1' AND
		netmask='255.255.255.255' AND auth_method='scram-sha-256')=1
		FROM pg_catalog.pg_hba_file_rules`); err != nil {
		return out, false, err
	}
	if err := fenceDevTrue(tx, `SELECT pg_catalog.current_setting('track_activities')='on'
  AND EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname=SESSION_USER AND rolsuper)`); err != nil {
		return out, false, err
	}
	names := []string{fenceDevOld, fenceDevNew, fenceDevOwner, fenceDevProbe}
	var roles []fenceDevRole
	if err := tx.Raw(`SELECT rolname::text AS name,oid::bigint AS oid,rolcanlogin AS can_login,
  (rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls) AS elevated
  FROM pg_catalog.pg_roles WHERE rolname IN ? ORDER BY oid`, names).Scan(&roles).Error; err != nil || len(roles) != len(names) {
		return out, false, errFenceDevPolicy
	}
	m := map[string]int64{}
	ids := make([]int64, 0, len(roles))
	for _, r := range roles {
		if r.OID <= 0 || r.Elevated || r.CanLogin != (r.Name == fenceDevNew || r.Name == fenceDevProbe) {
			return out, false, errFenceDevPolicy
		}
		ids = append(ids, r.OID)
		m[r.Name] = r.OID
	}
	owner, old, writer, probe := m[fenceDevOwner], m[fenceDevOld], m[fenceDevNew], m[fenceDevProbe]
	if owner == 0 || old == 0 || writer == 0 || probe == 0 {
		return out, false, errFenceDevPolicy
	}
	if err := fenceDevTrue(tx, `SELECT NOT EXISTS(SELECT FROM pg_catalog.pg_auth_members WHERE roleid IN ? OR member IN ?)
  AND EXISTS (SELECT FROM pg_catalog.pg_database d WHERE d.datname=pg_catalog.current_database()
    AND d.datdba=?::oid AND d.datallowconn
    AND NOT EXISTS (SELECT FROM pg_catalog.aclexplode(coalesce(d.datacl,pg_catalog.acldefault('d',d.datdba))) a
      WHERE a.grantee=0 AND a.privilege_type IN ('CONNECT','TEMPORARY','CREATE'))
    AND pg_catalog.has_database_privilege(?::oid,d.oid,'CONNECT') AND NOT pg_catalog.has_database_privilege(?::oid,d.oid,'CONNECT WITH GRANT OPTION')
    AND pg_catalog.has_database_privilege(?::oid,d.oid,'CONNECT') AND NOT pg_catalog.has_database_privilege(?::oid,d.oid,'CONNECT WITH GRANT OPTION')
    AND NOT pg_catalog.has_database_privilege(?::oid,d.oid,'CONNECT')
    AND NOT EXISTS (SELECT FROM pg_catalog.pg_roles r WHERE r.rolcanlogin
      AND r.rolname<>SESSION_USER AND r.oid NOT IN (?::oid,?::oid) AND pg_catalog.has_database_privilege(r.oid,d.oid,'CONNECT')))`,
		ids, ids, owner, writer, writer, probe, probe, old, writer, probe); err != nil {
		return out, false, err
	}

	// Check every DB-affiliated backend (not only legacy PIDs), then 2PC.
	// Continuing admission and ACLs, not this sample, deny late old writes.
	if err := tx.Exec("SELECT pg_catalog.pg_stat_clear_snapshot()").Error; err != nil {
		return out, false, errFenceDevPolicy
	}
	if err := fenceDevTrue(tx, `SELECT NOT EXISTS(SELECT FROM pg_catalog.pg_stat_activity
  WHERE datid=(SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database())
  AND pid<>pg_catalog.pg_backend_pid())`); err != nil {
		return out, false, errFenceDevSessions
	}
	if err := fenceDevTrue(tx, `SELECT NOT EXISTS(SELECT FROM pg_catalog.pg_prepared_xacts
  WHERE database=pg_catalog.current_database())`); err != nil {
		return out, false, errFenceDevPrepared
	}
	var maxPrepared int
	if err := tx.Raw("SELECT pg_catalog.current_setting('max_prepared_transactions')::int").Row().Scan(&maxPrepared); err != nil || maxPrepared < 0 {
		return out, false, errFenceDevPolicy
	}
	space, err := verifyLaneCustodyTransitionTx(tx, in)
	if err != nil || space.Schema != fenceDevSchema {
		return out, false, errFenceDevPolicy
	}
	marker, exists, err := readLaneCustodyRowTx(tx, space, true)
	if err != nil || !exists || marker != laneCustodyPreparedRow(space, in) {
		return out, false, errFenceDevPolicy
	}
	nonowners := []int64{old, writer, probe}
	if err := fenceDevTrue(tx, `SELECT
  EXISTS(SELECT FROM pg_catalog.pg_namespace n WHERE n.oid=?::oid AND n.nspowner=?::oid) AND
  NOT EXISTS(SELECT FROM pg_catalog.pg_namespace n WHERE n.nspname NOT IN ('public','information_schema',?)
    AND left(n.nspname,3)<>'pg_') AND
  NOT EXISTS(SELECT FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON p.pronamespace=n.oid WHERE n.nspname IN ('public',?)) AND
  NOT EXISTS(SELECT FROM pg_catalog.pg_event_trigger) AND
  NOT EXISTS(SELECT FROM pg_catalog.pg_subscription WHERE subdbid=?::oid) AND
  NOT EXISTS(SELECT FROM pg_catalog.pg_roles r CROSS JOIN pg_catalog.pg_namespace n
   WHERE r.oid IN ? AND n.nspname IN ('public',?) AND pg_catalog.has_schema_privilege(r.oid,n.oid,'CREATE')) AND
  NOT EXISTS(SELECT FROM pg_catalog.pg_roles r CROSS JOIN pg_catalog.pg_database d
   WHERE r.oid IN ? AND d.datname=pg_catalog.current_database() AND pg_catalog.has_database_privilege(r.oid,d.oid,'CREATE,TEMPORARY'))`,
		space.SchemaOID, owner, space.Schema, space.Schema, space.DatabaseOID, nonowners, space.Schema, nonowners); err != nil {
		return out, false, err
	}
	var relations []fenceDevRelation
	if err := tx.Raw(`SELECT c.relname::text AS name,n.nspname::text AS schema,c.oid::bigint AS oid,
  c.relowner::bigint AS owner,c.relkind::text AS kind,c.relpersistence::text AS persistence,
		(c.relrowsecurity OR c.relforcerowsecurity) AS row_security,
		coalesce((SELECT string_agg(pg_catalog.pg_get_constraintdef(con.oid,true),chr(10) ORDER BY con.oid)
			FROM pg_catalog.pg_constraint con WHERE con.conrelid=c.oid),'') AS constraints
  FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname IN ('public',?) AND c.relkind NOT IN ('i','I') ORDER BY c.relname`, space.Schema).Scan(&relations).Error; err != nil {
		return out, false, errFenceDevPolicy
	}
	required := map[string]bool{laneCustodyRelation: false, laneStoreAuthorityRelation: false, im21ReceiptTable: false, fenceDevSlot: false}
	haveReceipt := false
	for _, r := range relations {
		if r.Schema != space.Schema || r.Kind != "r" || r.Persistence != "p" || r.RowSecurity || r.Owner != owner || r.OID <= 0 {
			return out, false, errFenceDevPolicy
		}
		_, known := required[r.Name]
		switch {
		case known && !required[r.Name]:
			required[r.Name] = true
		case r.Name == fenceDevReceipt && !haveReceipt:
			haveReceipt = true
		default:
			return out, false, errFenceDevPolicy
		}
		if err := fenceDevTrue(tx, `SELECT NOT EXISTS(SELECT FROM pg_catalog.pg_trigger WHERE tgrelid=?::oid)
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_rewrite WHERE ev_class=?::oid)
    AND NOT EXISTS(SELECT FROM pg_catalog.pg_roles a WHERE
    (a.oid IN ? AND (pg_catalog.has_table_privilege(a.oid,?::oid,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES,MAINTAIN')
      OR pg_catalog.has_any_column_privilege(a.oid,?::oid,'INSERT,UPDATE,REFERENCES')))
    OR (a.oid=?::oid AND (pg_catalog.has_table_privilege(a.oid,?::oid,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES,MAINTAIN')
      OR pg_catalog.has_any_column_privilege(a.oid,?::oid,'INSERT,REFERENCES,UPDATE WITH GRANT OPTION'))))`,
			r.OID, r.OID, []int64{old, probe}, r.OID, r.OID, writer, r.OID, r.OID); err != nil {
			return out, false, err
		}
		var columns []string
		if err := tx.Raw(`SELECT attname::text FROM pg_catalog.pg_attribute
   WHERE attrelid=?::oid AND attnum>0 AND NOT attisdropped
   AND pg_catalog.has_column_privilege(?::oid,?::oid,attname,'UPDATE') ORDER BY attname`, r.OID, writer, r.OID).Scan(&columns).Error; err != nil {
			return out, false, errFenceDevPolicy
		}
		if (r.Name == fenceDevSlot && (len(columns) != 1 || columns[0] != "units")) || (r.Name != fenceDevSlot && len(columns) != 0) {
			return out, false, errFenceDevPolicy
		}
	}
	for _, ok := range required {
		if !ok {
			return out, false, errFenceDevPolicy
		}
	}
	return fenceDevSnapshot{Version: 1, SystemID: systemID, MaxPreparedTransactions: maxPrepared, PreparedTransactions: 0, Space: space, Transition: in, Roles: roles, Relations: relations}, haveReceipt, nil
}

// Persist exact current evidence under the dedicated protected owner.
// Verify-only never creates or repairs a row. No mode enables serving.
func sealWriterFenceDev(ctx context.Context, db *gorm.DB, in LaneCustodyTransitionV1, verifyOnly bool) (string, error) {
	if ctx == nil || !laneStorePGHandleOK(db) {
		return "", errFenceDevPolicy
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	var digest string
	prepared := false
	err := db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard, SkipHooks: true}).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_catalog.pg_advisory_xact_lock(?)", int64(laneStoreAdvisoryLockID)).Error; err != nil {
			return errFenceDevPolicy
		}
		s, exists, err := inspectWriterFenceDevTx(tx, in)
		if err != nil {
			return err
		}
		table := laneStoreQualified(s.Space.Schema, fenceDevReceipt)
		created := false
		if !exists {
			if verifyOnly {
				return errFenceDevReceipt
			}
			if err := tx.Exec("CREATE TABLE " + table + " (singleton smallint PRIMARY KEY CHECK(singleton=1),digest text NOT NULL,evidence text NOT NULL)").Error; err != nil {
				return errFenceDevReceipt
			}
			if err := tx.Exec("ALTER TABLE " + table + " OWNER TO " + fenceDevOwner).Error; err != nil {
				return errFenceDevReceipt
			}
			s, exists, err = inspectWriterFenceDevTx(tx, in)
			if err != nil || !exists {
				return errFenceDevReceipt
			}
			created = true
		}
		raw, err := common.Marshal(s)
		if err != nil {
			return errFenceDevReceipt
		}
		sum := sha256.Sum256(raw)
		digest = hex.EncodeToString(sum[:])
		if created {
			if err := tx.Exec("INSERT INTO "+table+" VALUES (1,?,?)", digest, string(raw)).Error; err != nil {
				return errFenceDevReceipt
			}
		} else {
			var rows []struct {
				Singleton        int
				Digest, Evidence string
			}
			if err := tx.Raw("SELECT singleton,digest,evidence FROM " + table + " LIMIT 2 FOR SHARE").Scan(&rows).Error; err != nil || len(rows) != 1 ||
				rows[0].Singleton != 1 || rows[0].Digest != digest || rows[0].Evidence != string(raw) {
				return errFenceDevReceipt
			}
		}
		prepared = true
		return nil
	})
	if err != nil {
		if prepared {
			return "", ErrLaneStoreCommitUncertain
		}
		return "", err
	}
	return digest, nil
}
