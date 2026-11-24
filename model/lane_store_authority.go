package model

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	laneStoreAuthorityRelation = "lane_store_authority_v1"
	laneStoreAuthorityVersion  = 1
	laneStoreAuthorityDormant  = "dormant"
	laneStoreAuthorityRevoked  = "revoked"
	laneStoreAdvisoryLockID    = 0x4c414e4553544f

	// LaneStoreRuntimeCapable cannot be changed by configuration, a quote, or
	// a database row. Enabling it is not sufficient to make a release safe;
	// the all-writer and fleet rollout contracts must first be implemented.
	LaneStoreRuntimeCapable = false
)

var (
	ErrLaneStoreAuthorityUnavailable = errors.New("lane store PostgreSQL authority is unavailable")
	ErrLaneStoreAuthorityState       = errors.New("lane store authority is incompatible with this release")
	ErrLaneStoreAuthorityEvidence    = errors.New("lane store binding authority changed or is incomplete")
	ErrLaneStoreCommitUncertain      = errors.New("lane store authority commit is uncertain")
)

// LaneStoreReleaseV1 is a non-secret exact release/store epoch. StoreID
// distinguishes independent installations, not hostnames or connection URLs.
// License to write financial custody never follows from values of this type.
type LaneStoreReleaseV1 struct {
	StoreID      string
	ReleaseID    string
	BinarySHA256 string
	Epoch        int64
}

func laneStoreLowerHex(s string, n int) bool {
	if len(s) != n || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.Trim(s, "0") != ""
}

func (r LaneStoreReleaseV1) valid() bool {
	return laneStoreLowerHex(r.StoreID, 32) && laneStoreLowerHex(r.ReleaseID, 32) &&
		laneStoreLowerHex(r.BinarySHA256, 64) && r.Epoch > 0
}

type laneStoreAuthorityRow struct {
	Singleton     int
	SchemaVersion int
	State         string
	LaneStoreReleaseV1
}

type laneStoreCatalog struct {
	schema string
	table  string
}

type laneStoreCatalogRelation struct {
	SchemaName string
	Name       string
	Kind       string
}

// A manifest under another visible search-path schema, a shadow relation, or
// any visible tagged custody schema is never adopted by the ordinary runtime.
// This does not replace the separate historic enterprise_lanes startup fence.
func inspectLaneStoreCatalogTx(tx *gorm.DB) (laneStoreCatalog, error) {
	var schema string
	if err := tx.Raw("SELECT pg_catalog.current_schema()").Scan(&schema).Error; err != nil || schema == "" {
		return laneStoreCatalog{}, ErrLaneStoreAuthorityUnavailable
	}
	var relations []laneStoreCatalogRelation
	if err := tx.Raw(`SELECT n.nspname AS schema_name, c.relname AS name, c.relkind::text AS kind
		FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = ANY (pg_catalog.current_schemas(true))
		AND c.relname IN ('lane_store_authority_v1', 'lane_billing_child_receipts_v1', 'lane_billing_key_shares_v1',
			'lane_billing_key_enrollments_v1', 'lane_billing_key_transfers_v1', 'shared_wallet_shares', 'shared_wallet_transfers',
			'lane_billing_measured_dev_v1', 'lane_billing_usage_outbox_dev_v1', 'lane_billing_usage_destination_dev_v1',
			'lane_billing_usage_log_receipts_dev_v1')`).Scan(&relations).Error; err != nil {
		return laneStoreCatalog{}, ErrLaneStoreAuthorityUnavailable
	}
	c := laneStoreCatalog{schema: schema}
	for _, relation := range relations {
		if relation.SchemaName != schema || relation.Name != laneStoreAuthorityRelation || relation.Kind != "r" || c.table != "" {
			return laneStoreCatalog{}, ErrLaneStoreAuthorityState
		}
		c.table = laneStoreQualified(schema, laneStoreAuthorityRelation)
	}
	return c, nil
}

func laneStoreQualified(schema, table string) string {
	return `"` + strings.ReplaceAll(schema, `"`, `""`) + `"."` + table + `"`
}

func readLaneStoreAuthorityRowTx(tx *gorm.DB, c laneStoreCatalog, lock string) (laneStoreAuthorityRow, error) {
	if c.table == "" {
		return laneStoreAuthorityRow{}, ErrLaneStoreAuthorityUnavailable
	}
	suffix := ""
	switch lock {
	case "":
	case "SHARE":
		suffix = " FOR SHARE"
	case "UPDATE":
		suffix = " FOR UPDATE"
	default:
		return laneStoreAuthorityRow{}, ErrLaneStoreAuthorityUnavailable
	}
	var rows []laneStoreAuthorityRow
	err := tx.Raw("SELECT singleton, schema_version, state, store_id, release_id, binary_sha256, epoch FROM " + c.table + " LIMIT 2" + suffix).Scan(&rows).Error
	if err != nil || len(rows) != 1 || rows[0].Singleton != 1 || rows[0].SchemaVersion != laneStoreAuthorityVersion || !rows[0].LaneStoreReleaseV1.valid() {
		return laneStoreAuthorityRow{}, ErrLaneStoreAuthorityState
	}
	switch rows[0].State {
	case laneStoreAuthorityDormant, laneStoreAuthorityRevoked:
		return rows[0], nil
	default:
		// Active (including a seemingly matching release), transition,
		// unknown, and partially upgraded states must not serve in this code.
		return laneStoreAuthorityRow{}, ErrLaneStoreAuthorityState
	}
}

func laneStorePGHandleOK(db *gorm.DB) bool {
	if db == nil || db.Config == nil || db.Statement == nil || db.Dialector == nil || db.DryRun || db.Dialector.Name() != "postgres" {
		return false
	}
	_, a := db.Config.ConnPool.(gorm.TxCommitter)
	_, b := db.Statement.ConnPool.(gorm.TxCommitter)
	return !a && !b
}

// CheckLaneStoreOrdinaryStartup does not install, update or activate anything.
// A legacy PostgreSQL schema with no store evidence and non-PG startup retain
// ordinary behavior. It does not fence already-running or older binaries.
func CheckLaneStoreOrdinaryStartup(ctx context.Context, db *gorm.DB) error {
	// Even a mistakenly rebuilt capable binary must not fall through
	// to ordinary startup without a real independently enforced writer fence.
	if LaneStoreRuntimeCapable {
		return ErrLaneCustodyFleetFenceRequired
	}
	if ctx == nil || db == nil || db.Config == nil || db.Statement == nil || db.Dialector == nil || db.DryRun {
		return ErrLaneStoreAuthorityUnavailable
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	if !laneStorePGHandleOK(db) {
		return ErrLaneStoreAuthorityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	db = db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard, SkipHooks: true})
	err := db.Transaction(checkLaneStoreOrdinaryStartupTx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return laneStoreAuthorityError(ctx, err)
}

func checkLaneStoreOrdinaryStartupTx(tx *gorm.DB) error {
	// The managed migrate/serve commands also use this lower-level entry.
	if LaneStoreRuntimeCapable {
		return ErrLaneCustodyFleetFenceRequired
	}
	tx = tx.Session(&gorm.Session{NewDB: true, Logger: logger.Discard, SkipHooks: true})
	c, err := inspectLaneStoreCatalogTx(tx)
	if err != nil {
		return err
	}
	var base laneStoreAuthorityRow
	if c.table != "" {
		base, err = readLaneStoreAuthorityRowTx(tx, c, "")
		if err != nil {
			return err
		}
	}
	return checkLaneOrdinaryCustodyMarkerTx(tx, c, base)
}

// InstallLaneStoreAuthorityV1 is an explicit versioned schema step. Ordinary
// and managed startup never call it, and no enrollment, balance, owner, or
// configuration is written. Repeating only the same dormant install is safe;
// no release migration can adopt foreign metadata, revive a revoked epoch or
// make this build capable. Callers must supply a verified release identity.
func InstallLaneStoreAuthorityV1(ctx context.Context, db *gorm.DB, r LaneStoreReleaseV1) error {
	if !r.valid() || r.Epoch != 1 {
		return ErrLaneStoreAuthorityEvidence
	}
	return withLaneStoreMetadataTx(ctx, db, func(tx *gorm.DB) error {
		c, err := inspectLaneStoreCatalogTx(tx)
		if err != nil {
			return err
		}
		if c.table != "" {
			existing, err := readLaneStoreAuthorityRowTx(tx, c, "UPDATE")
			if err != nil {
				return err
			}
			if existing.LaneStoreReleaseV1 != r || existing.State != laneStoreAuthorityDormant {
				return ErrLaneStoreAuthorityEvidence
			}
			return nil
		}
		if strings.HasPrefix(c.schema, "pg_") || c.schema == "information_schema" {
			return ErrLaneStoreAuthorityUnavailable
		}
		table := laneStoreQualified(c.schema, laneStoreAuthorityRelation)
		if err := tx.Exec("CREATE TABLE " + table + ` (
			singleton smallint PRIMARY KEY CHECK (singleton = 1),
			schema_version smallint NOT NULL CHECK (schema_version = 1),
			state text NOT NULL CHECK (state IN ('dormant','revoked','active')),
			store_id text NOT NULL CHECK (store_id ~ '^[0-9a-f]{32}$'),
			release_id text NOT NULL CHECK (release_id ~ '^[0-9a-f]{32}$'),
			binary_sha256 text NOT NULL CHECK (binary_sha256 ~ '^[0-9a-f]{64}$'),
			epoch bigint NOT NULL CHECK (epoch > 0)
		)`).Error; err != nil {
			return err
		}
		return tx.Exec("INSERT INTO "+table+" (singleton,schema_version,state,store_id,release_id,binary_sha256,epoch) VALUES (1,1,?,?,?,?,?)",
			laneStoreAuthorityDormant, r.StoreID, r.ReleaseID, r.BinarySHA256, r.Epoch).Error
	})
}

// RevokeLaneStoreAuthorityV1 invalidates the exact dormant epoch once, with
// no reopening in this build. It changes only metadata, never quota or an
// existing request owner. A lost acknowledgment is not success or retry proof.
func RevokeLaneStoreAuthorityV1(ctx context.Context, db *gorm.DB, r LaneStoreReleaseV1) error {
	if !r.valid() || r.Epoch >= math.MaxInt64 {
		return ErrLaneStoreAuthorityEvidence
	}
	return withLaneStoreMetadataTx(ctx, db, func(tx *gorm.DB) error {
		c, err := inspectLaneStoreCatalogTx(tx)
		if err != nil {
			return err
		}
		current, err := readLaneStoreAuthorityRowTx(tx, c, "UPDATE")
		if err != nil {
			return err
		}
		if current.LaneStoreReleaseV1 != r || current.State != laneStoreAuthorityDormant {
			return ErrLaneStoreAuthorityEvidence
		}
		write := tx.Exec("UPDATE "+c.table+" SET state = ?, epoch = epoch + 1 WHERE singleton = 1 AND epoch = ?", laneStoreAuthorityRevoked, r.Epoch)
		if write.Error != nil {
			return write.Error
		}
		if write.RowsAffected != 1 {
			return ErrLaneStoreAuthorityEvidence
		}
		return nil
	})
}

func withLaneStoreMetadataTx(ctx context.Context, db *gorm.DB, run func(*gorm.DB) error) error {
	if ctx == nil || !laneStorePGHandleOK(db) {
		return ErrLaneStoreAuthorityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	db = db.Session(&gorm.Session{NewDB: true, Logger: logger.Discard, Context: ctx, SkipHooks: true})
	prepared := false
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_catalog.pg_advisory_xact_lock(?)", int64(laneStoreAdvisoryLockID)).Error; err != nil {
			return err
		}
		if err := run(tx); err != nil {
			return err
		}
		prepared = true
		return nil
	})
	if err != nil && prepared {
		return ErrLaneStoreCommitUncertain
	}
	return laneStoreAuthorityError(ctx, err)
}

func laneStoreAuthorityError(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, ErrLaneStoreAuthorityUnavailable) || errors.Is(err, ErrLaneStoreAuthorityState) || errors.Is(err, ErrLaneStoreAuthorityEvidence) || errors.Is(err, ErrLaneCustodyFleetFenceRequired) {
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		return errors.Join(ErrLaneStoreAuthorityUnavailable, ctx.Err())
	}
	return ErrLaneStoreAuthorityUnavailable // never expose database, SQL or authority identifiers
}
