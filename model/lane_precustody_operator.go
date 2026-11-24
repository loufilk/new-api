package model

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	ErrLaneOperatorIdentity = errors.New("PostgreSQL physical or session identity did not match")
	ErrLaneOperatorRoles    = errors.New("PostgreSQL role or privilege policy did not match")
	ErrLaneOperatorBusy     = errors.New("PostgreSQL has additional client or unrecognized backends")
	ErrLaneOperatorPrepared = errors.New("PostgreSQL prepared-transaction mode or count did not match")
	ErrLaneOperatorCatalog  = errors.New("PostgreSQL exact dormant catalogue or migration receipt did not match")
)

type LaneOperatorRoleV1 struct {
	Name string `json:"name"`
	OID  int64  `json:"oid"`
}

// LaneOperatorStoreV1 is externally inventoried expected data, not a scope or
// credential. This command checks only one direct, quiesced MAIN+LOG store.
type LaneOperatorStoreV1 struct {
	SystemID       string                  `json:"system_id"`
	Timeline       int64                   `json:"timeline"`
	ServerAddr     string                  `json:"server_addr"`
	ServerPort     int                     `json:"server_port"`
	VersionNum     int                     `json:"server_version_num"`
	Database       string                  `json:"database"`
	Schema         string                  `json:"schema"`
	DatabaseOID    int64                   `json:"database_oid"`
	SchemaOID      int64                   `json:"schema_oid"`
	AuthorityOID   int64                   `json:"authority_oid"`
	CatalogOID     int64                   `json:"catalog_oid"`
	MigrationOID   int64                   `json:"migration_oid"`
	MaxPrepared    *int                    `json:"max_prepared_transactions"`
	Auditor        LaneOperatorRoleV1      `json:"auditor"`
	Owner          LaneOperatorRoleV1      `json:"owner"`
	RetiredWriters []LaneOperatorRoleV1    `json:"retired_writers"`
	NextWriters    []LaneOperatorRoleV1    `json:"next_writers"`
	ControlAdmins  []LaneOperatorRoleV1    `json:"control_admins"`
	Transition     LaneCustodyTransitionV1 `json:"transition"`
}

type LaneOperatorSampleV1 struct {
	BackendPID    int       `json:"backend_pid"`
	BackendStart  time.Time `json:"backend_start"`
	Postmaster    time.Time `json:"postmaster_start"`
	ObservedAt    time.Time `json:"observed_at"`
	OtherBackends int       `json:"other_backends"`
	MaxPrepared   int       `json:"max_prepared_transactions"`
	Prepared      int       `json:"prepared_transactions"`
}

func laneOperatorPredicate(tx *gorm.DB, query string, args ...any) bool {
	var good bool
	return tx.Raw(query, args...).Row().Scan(&good) == nil && good
}

// CheckLaneOperatorSnapshotV1 opens a bounded read-only transaction. The CLI
// calls it on a new physical socket per sample; this function never connects
// elsewhere, seals a release, changes permissions, or returns wallet scope.
func CheckLaneOperatorSnapshotV1(ctx context.Context, db *gorm.DB, p LaneOperatorStoreV1) (out LaneOperatorSampleV1, err error) {
	if ctx == nil || !laneStorePGHandleOK(db) || !p.Transition.valid() || p.MaxPrepared == nil ||
		len(p.RetiredWriters) == 0 || len(p.NextWriters) == 0 || len(p.ControlAdmins) == 0 {
		return out, ErrLaneOperatorIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db = db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard, SkipHooks: true})
	err = db.Transaction(func(tx *gorm.DB) error {
		if tx.Exec("SET LOCAL statement_timeout='3s'").Error != nil ||
			tx.Exec("SET LOCAL lock_timeout='500ms'").Error != nil ||
			tx.Exec("SET LOCAL search_path="+`"`+strings.ReplaceAll(p.Schema, `"`, `""`)+`"`).Error != nil {
			return ErrLaneOperatorIdentity
		}
		space, err := laneCustodySpaceTx(tx)
		if err != nil || space != (laneCustodySpaceV1{Schema: p.Schema, DatabaseOID: p.DatabaseOID, SchemaOID: p.SchemaOID}) {
			return ErrLaneOperatorIdentity
		}
		var addr string
		var good bool
		err = tx.Raw(`SELECT pg_catalog.host(pg_catalog.inet_server_addr()),
			pg_catalog.pg_backend_pid(), pg_catalog.pg_postmaster_start_time(),
			pg_catalog.current_database()=? AND CURRENT_USER=? AND SESSION_USER=?
			AND pg_catalog.inet_server_port()=?
			AND NOT pg_catalog.pg_is_in_recovery()
			AND pg_catalog.current_setting('transaction_read_only')='on'
			AND pg_catalog.current_setting('track_activities')='on'
			AND pg_catalog.current_setting('server_version_num')::integer=?
			AND s.system_identifier::text=? AND c.timeline_id::bigint=?
			AND EXISTS(SELECT FROM pg_catalog.pg_stat_ssl WHERE pid=pg_catalog.pg_backend_pid() AND ssl)
			FROM pg_catalog.pg_control_system() s CROSS JOIN pg_catalog.pg_control_checkpoint() c`,
			p.Database, p.Auditor.Name, p.Auditor.Name, p.ServerPort, p.VersionNum, p.SystemID, p.Timeline).
			Row().Scan(&addr, &out.BackendPID, &out.Postmaster, &good)
		ip, ipErr := netip.ParseAddr(addr)
		if err != nil || !good || ipErr != nil || ip.String() != p.ServerAddr || out.BackendPID <= 0 || out.Postmaster.IsZero() {
			return ErrLaneOperatorIdentity
		}
		if !laneOperatorRolePolicy(tx, p) {
			return ErrLaneOperatorRoles
		}
		if !laneOperatorCatalogPolicy(tx, p, space) {
			return ErrLaneOperatorCatalog
		}
		if tx.Exec("SELECT pg_catalog.pg_stat_clear_snapshot()").Error != nil {
			return ErrLaneOperatorBusy
		}
		// PostgreSQL 18.6 has DB-less I/O workers. The stock logical
		// replication launcher has the bootstrap role but no database;
		// any subscription or DB-affiliated apply worker still blocks.
		err = tx.Raw(`SELECT
			(SELECT backend_start FROM pg_catalog.pg_stat_activity WHERE pid=pg_catalog.pg_backend_pid()
			 AND datid=?::oid AND usesysid=?::oid AND backend_type='client backend'),
			pg_catalog.clock_timestamp(), count(*)::integer
			FROM pg_catalog.pg_stat_activity WHERE pid<>pg_catalog.pg_backend_pid()
			AND NOT (datid IS NULL AND coalesce(
			 (usesysid IS NULL AND backend_type IN ('autovacuum launcher','background writer','checkpointer','io worker','walwriter'))
			 OR (usesysid=10 AND backend_type='logical replication launcher'),false))`,
			p.DatabaseOID, p.Auditor.OID).Row().Scan(&out.BackendStart, &out.ObservedAt, &out.OtherBackends)
		if err != nil || out.BackendStart.IsZero() || out.ObservedAt.Before(out.BackendStart) || out.OtherBackends != 0 {
			return ErrLaneOperatorBusy
		}
		err = tx.Raw(`SELECT pg_catalog.current_setting('max_prepared_transactions')::integer, count(*)::integer
			FROM pg_catalog.pg_prepared_xacts`).Row().Scan(&out.MaxPrepared, &out.Prepared)
		if err != nil || out.MaxPrepared != *p.MaxPrepared || out.Prepared != 0 {
			return ErrLaneOperatorPrepared
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		for _, known := range []error{ErrLaneOperatorIdentity, ErrLaneOperatorRoles, ErrLaneOperatorBusy, ErrLaneOperatorPrepared, ErrLaneOperatorCatalog} {
			if errors.Is(err, known) {
				return LaneOperatorSampleV1{}, known
			}
		}
		return LaneOperatorSampleV1{}, ErrLaneOperatorIdentity
	}
	return out, nil
}

func laneOperatorRolePolicy(tx *gorm.DB, p LaneOperatorStoreV1) bool {
	embargoed := append(append([]LaneOperatorRoleV1{}, p.RetiredWriters...), p.NextWriters...)
	blocked := make([]int64, 0, len(embargoed)+1)
	for _, r := range append(embargoed, p.Owner) {
		if !laneOperatorPredicate(tx, `SELECT EXISTS(SELECT FROM pg_catalog.pg_roles WHERE
			oid=?::oid AND rolname=? AND NOT (rolcanlogin OR rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls))`, r.OID, r.Name) {
			return false
		}
		blocked = append(blocked, r.OID)
	}
	admins := make([]int64, 0, len(p.ControlAdmins))
	for _, r := range p.ControlAdmins {
		if !laneOperatorPredicate(tx, `SELECT EXISTS(SELECT FROM pg_catalog.pg_roles WHERE
			oid=?::oid AND rolname=? AND rolsuper AND rolcanlogin)`, r.OID, r.Name) {
			return false
		}
		admins = append(admins, r.OID)
	}
	if !laneOperatorPredicate(tx, `SELECT EXISTS(SELECT FROM pg_catalog.pg_roles WHERE oid=?::oid AND rolname=? AND rolcanlogin
		AND NOT (rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls))
		AND pg_catalog.pg_has_role(?::oid,'pg_read_all_stats','USAGE')
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_auth_members m WHERE m.roleid IN ? OR m.member IN ? OR m.roleid=?::oid
			OR m.member='pg_read_all_stats'::regrole OR m.roleid IN ? OR m.member IN ?
			OR (m.member=?::oid AND (m.roleid<>'pg_read_all_stats'::regrole OR m.admin_option OR m.set_option OR NOT m.inherit_option)))
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_roles WHERE (rolsuper OR rolreplication OR rolcreaterole OR rolcreatedb OR rolbypassrls)
			AND oid NOT IN ?)`, p.Auditor.OID, p.Auditor.Name, p.Auditor.OID, blocked, blocked, p.Auditor.OID, admins, admins, p.Auditor.OID, admins) {
		return false
	}
	nonowners := append(blocked[:len(blocked)-1:len(blocked)-1], p.Auditor.OID)
	// Stock pg_settings UPDATE changes only the caller's session parameters;
	// do not confuse its built-in PUBLIC ACL with a persistent write grant.
	return laneOperatorPredicate(tx, `SELECT EXISTS(SELECT FROM pg_catalog.pg_database d WHERE d.oid=?::oid AND d.datdba=?::oid AND d.datallowconn
		AND NOT EXISTS(SELECT FROM pg_catalog.aclexplode(coalesce(d.datacl,pg_catalog.acldefault('d',d.datdba))) a
			WHERE a.grantee=0 AND a.privilege_type IN ('CONNECT','TEMPORARY','CREATE'))
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_roles r WHERE r.rolcanlogin
			AND r.oid NOT IN ? AND r.oid<>?::oid AND pg_catalog.has_database_privilege(r.oid,d.oid,'CONNECT'))
		AND pg_catalog.has_database_privilege(?::oid,d.oid,'CONNECT')
		AND NOT pg_catalog.has_database_privilege(?::oid,d.oid,'CONNECT WITH GRANT OPTION')
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_roles r WHERE r.oid IN ?
			AND (pg_catalog.has_database_privilege(r.oid,d.oid,'CREATE,TEMPORARY')
			OR (r.oid<>?::oid AND pg_catalog.has_database_privilege(r.oid,d.oid,'CONNECT')))))
		AND EXISTS(SELECT FROM pg_catalog.pg_namespace WHERE oid=?::oid AND nspowner=?::oid)
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_namespace n CROSS JOIN pg_catalog.pg_roles r WHERE r.oid IN ?
			AND n.nspname<>'information_schema' AND left(n.nspname,3)<>'pg_'
			AND pg_catalog.has_schema_privilege(r.oid,n.oid,'CREATE'))
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
			CROSS JOIN pg_catalog.pg_roles r WHERE r.oid IN ? AND c.oid<>'pg_catalog.pg_settings'::regclass
			AND (c.relowner=r.oid
			  OR (c.relkind='S' AND pg_catalog.has_sequence_privilege(r.oid,c.oid,'USAGE,UPDATE'))
			  OR (c.relkind IN ('r','p','v','m','f') AND
			   (pg_catalog.has_table_privilege(r.oid,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES,MAINTAIN')
			    OR pg_catalog.has_any_column_privilege(r.oid,c.oid,'INSERT,UPDATE,REFERENCES')))))
		AND NOT pg_catalog.has_column_privilege(?::oid,'pg_catalog.pg_subscription','subconninfo','SELECT')
		AND NOT EXISTS(SELECT oid FROM pg_catalog.pg_subscription)
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_parameter_acl a CROSS JOIN pg_catalog.pg_roles r
			WHERE r.oid IN ? AND pg_catalog.has_parameter_privilege(r.oid,a.parname,'ALTER SYSTEM'))
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_foreign_server)
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_event_trigger)
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_extension WHERE extname<>'plpgsql')
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_proc x JOIN pg_catalog.pg_namespace n ON n.oid=x.pronamespace
			WHERE x.proowner IN ? OR (x.prosecdef AND (x.oid>=16384 OR x.proowner<>10
			 OR n.nspname NOT IN ('pg_catalog','information_schema'))))
		AND EXISTS(SELECT FROM pg_catalog.pg_class c JOIN pg_catalog.pg_rewrite w ON w.ev_class=c.oid
			WHERE c.oid='pg_catalog.pg_settings'::regclass AND c.oid<16384 AND c.relowner=10 AND c.relkind='v'
			GROUP BY c.oid HAVING count(*)=3 AND bool_and(w.ev_enabled='O') AND
			  pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to(pg_catalog.string_agg(
			   pg_catalog.pg_get_ruledef(w.oid,false),chr(10) ORDER BY w.rulename),'UTF8')),'hex')
			  ='3a9d6592e1d0f726bcacbac2e0d014511b81b7984016383c63fd8b21977fabd9')`,
		p.DatabaseOID, p.Owner.OID, admins, p.Auditor.OID, p.Auditor.OID, p.Auditor.OID, nonowners, p.Auditor.OID,
		p.SchemaOID, p.Owner.OID, nonowners, nonowners, p.Auditor.OID, nonowners, nonowners)
}

func laneOperatorCatalogPolicy(tx *gorm.DB, p LaneOperatorStoreV1, space laneCustodySpaceV1) bool {
	// Check physical shape before opening any relation named by the model.
	// Extra lane-shaped objects in another schema are unsafe, even invisible.
	if !laneOperatorPredicate(tx, `SELECT count(*)=3 AND count(*) FILTER(WHERE c.relnamespace=?::oid AND c.relowner=?::oid
		AND c.relkind='r' AND c.relpersistence='p' AND NOT (c.relrowsecurity OR c.relforcerowsecurity)
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_rewrite WHERE ev_class=c.oid)
		AND NOT EXISTS(SELECT FROM pg_catalog.pg_trigger WHERE tgrelid=c.oid)
		AND ((c.relname='lane_store_authority_v1' AND c.oid=?::oid)
		  OR (c.relname='lane_custody_catalog_v1' AND c.oid=?::oid)
		  OR (c.relname='core_im21_schema_receipts' AND c.oid=?::oid)))=3
		FROM pg_catalog.pg_class c WHERE c.relkind NOT IN ('i','I') AND
		(c.relname='core_im21_schema_receipts' OR c.relname LIKE 'lane\_%'
		OR c.relname LIKE 'enterprise\_lane%' OR c.relname LIKE 'shared\_wallet%')`,
		p.SchemaOID, p.Owner.OID, p.AuthorityOID, p.CatalogOID, p.MigrationOID) {
		return false
	}
	s, err := verifyLaneCustodyTransitionTx(tx, p.Transition)
	if err != nil || s != space {
		return false
	}
	row, exists, err := readLaneCustodyRowTx(tx, s, false)
	return err == nil && exists && row == laneCustodyPreparedRow(s, p.Transition)
}
