package model

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	laneCustodyRelation = "lane_custody_catalog_v1"
	laneCustodyPrepared = "no_custody"
	laneCustodyFixture  = "isolated_fixture"
	laneCustodyVersion  = 1
)

// This error is intentional even for an exact, complete transition catalogue.
// An ordinary process that already passed startup can still write via the old
// accounting paths. Only a separately enforced fleet/DB fence can change that.
var ErrLaneCustodyFleetFenceRequired = errors.New("lane custody requires an independently enforced old-writer fence")

// LaneCustodyTransitionV1 identifies the exact existing migration (both roles)
// and the later dormant store release. It is not authority to use escrow.
type LaneCustodyTransitionV1 struct {
	Release                                         LaneStoreReleaseV1
	BaseReleaseID, BaseBinarySHA256, MigrationRunID string
}

func (s LaneCustodyTransitionV1) valid() bool {
	return s.Release.valid() && laneStoreLowerHex(s.BaseReleaseID, 32) && laneStoreLowerHex(s.BaseBinarySHA256, 64) &&
		len(s.MigrationRunID) > len(s.BaseReleaseID)+1 && len(s.MigrationRunID) <= 160 &&
		strings.HasPrefix(s.MigrationRunID, s.BaseReleaseID+":") && strings.TrimSpace(s.MigrationRunID) == s.MigrationRunID
}

type laneCustodySpaceV1 struct {
	Schema                 string
	DatabaseOID, SchemaOID int64
}

type laneCustodyRowV1 struct {
	Singleton, SchemaVersion int
	Profile                  string
	LaneStoreReleaseV1
	WalletEpoch      int64  `gorm:"column:wallet_epoch"`
	DatabaseOID      int64  `gorm:"column:database_oid"`
	SchemaOID        int64  `gorm:"column:schema_oid"`
	BaseReleaseID    string `gorm:"column:base_release_id"`
	BaseBinarySHA256 string `gorm:"column:base_binary_sha256"`
	MigrationRunID   string `gorm:"column:migration_run_id"`
}

func laneCustodySpaceTx(tx *gorm.DB) (laneCustodySpaceV1, error) {
	var s laneCustodySpaceV1
	if tx == nil || tx.Config == nil || tx.Statement == nil || tx.Dialector == nil || tx.DryRun || tx.Dialector.Name() != "postgres" {
		return s, ErrLaneStoreAuthorityUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return s, ErrLaneStoreAuthorityUnavailable
	}
	err := tx.Raw(`SELECT n.nspname AS schema, d.oid::bigint AS database_oid, n.oid::bigint AS schema_oid
		FROM pg_catalog.pg_namespace n CROSS JOIN pg_catalog.pg_database d
		WHERE n.nspname=pg_catalog.current_schema() AND d.datname=pg_catalog.current_database()
		AND pg_catalog.current_schemas(false)=ARRAY[n.nspname]::name[] AND pg_catalog.pg_my_temp_schema()=0`).Row().
		Scan(&s.Schema, &s.DatabaseOID, &s.SchemaOID)
	if err != nil || s.DatabaseOID <= 0 || s.SchemaOID <= 0 || s.Schema == "" ||
		strings.HasPrefix(s.Schema, "pg_") || s.Schema == "information_schema" {
		return laneCustodySpaceV1{}, ErrLaneStoreAuthorityState
	}
	return s, nil
}

func laneCustodyPhysicalTableTx(tx *gorm.DB, space laneCustodySpaceV1, name string) (bool, error) {
	var relations []laneStoreCatalogRelation
	err := tx.Raw(`SELECT n.nspname AS schema_name, c.relname AS name, c.relkind::text AS kind
		FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		WHERE c.relname=? AND n.nspname=ANY(pg_catalog.current_schemas(true))`, name).Scan(&relations).Error
	if err != nil || len(relations) > 1 || (len(relations) == 1 && (relations[0].SchemaName != space.Schema || relations[0].Kind != "r")) {
		return false, ErrLaneStoreAuthorityState
	}
	return len(relations) == 1, nil
}

func readLaneCustodyRowTx(tx *gorm.DB, space laneCustodySpaceV1, lock bool) (laneCustodyRowV1, bool, error) {
	var empty laneCustodyRowV1
	exists, err := laneCustodyPhysicalTableTx(tx, space, laneCustodyRelation)
	if err != nil || !exists {
		return empty, exists, err
	}
	table := laneStoreQualified(space.Schema, laneCustodyRelation)
	var layoutOK bool
	if err := tx.Raw(`SELECT
		(SELECT string_agg(a.attname || ':' || pg_catalog.format_type(a.atttypid,a.atttypmod) || ':' || a.attnotnull::text,',' ORDER BY a.attnum)
		FROM pg_catalog.pg_attribute a WHERE a.attrelid=?::regclass AND a.attnum>0 AND NOT a.attisdropped)=?
		AND EXISTS (SELECT FROM pg_catalog.pg_constraint x
		WHERE x.conrelid=?::regclass AND x.contype='p' AND x.convalidated AND x.conkey=ARRAY[1]::smallint[])`,
		table, "singleton:smallint:true,schema_version:smallint:true,profile:text:true,store_id:text:true,release_id:text:true,binary_sha256:text:true,epoch:bigint:true,wallet_epoch:bigint:true,database_oid:bigint:true,schema_oid:bigint:true,base_release_id:text:true,base_binary_sha256:text:true,migration_run_id:text:true", table).Row().Scan(&layoutOK); err != nil || !layoutOK {
		return empty, true, ErrLaneStoreAuthorityState
	}
	query := "SELECT singleton,schema_version,profile,store_id,release_id,binary_sha256,epoch,wallet_epoch,database_oid,schema_oid,base_release_id,base_binary_sha256,migration_run_id FROM " + table + " LIMIT 2"
	if lock {
		query += " FOR SHARE"
	}
	var rows []laneCustodyRowV1
	if err := tx.Raw(query).Scan(&rows).Error; err != nil || len(rows) != 1 {
		return empty, true, ErrLaneStoreAuthorityState
	}
	r := rows[0]
	if r.Singleton != 1 || r.SchemaVersion != laneCustodyVersion || !r.LaneStoreReleaseV1.valid() ||
		r.DatabaseOID != space.DatabaseOID || r.SchemaOID != space.SchemaOID {
		return empty, true, ErrLaneStoreAuthorityState
	}
	return r, true, nil
}

func createLaneCustodyRowTx(tx *gorm.DB, space laneCustodySpaceV1, r laneCustodyRowV1) error {
	t := laneStoreQualified(space.Schema, laneCustodyRelation)
	if err := tx.Exec("CREATE TABLE " + t + ` (
		singleton smallint PRIMARY KEY CHECK(singleton=1),
		schema_version smallint NOT NULL CHECK(schema_version=1),
		profile text NOT NULL CHECK(profile IN ('no_custody','isolated_fixture')),
		store_id text NOT NULL, release_id text NOT NULL, binary_sha256 text NOT NULL,
		epoch bigint NOT NULL CHECK(epoch>0), wallet_epoch bigint NOT NULL,
		database_oid bigint NOT NULL CHECK(database_oid>0), schema_oid bigint NOT NULL CHECK(schema_oid>0),
		base_release_id text NOT NULL, base_binary_sha256 text NOT NULL, migration_run_id text NOT NULL,
		CHECK ((profile='no_custody' AND wallet_epoch=0) OR (profile='isolated_fixture' AND wallet_epoch>0))
	)`).Error; err != nil {
		return err
	}
	return tx.Exec("INSERT INTO "+t+" (singleton,schema_version,profile,store_id,release_id,binary_sha256,epoch,wallet_epoch,database_oid,schema_oid,base_release_id,base_binary_sha256,migration_run_id) VALUES (1,1,?,?,?,?,?,?,?,?,?,?,?)",
		r.Profile, r.StoreID, r.ReleaseID, r.BinarySHA256, r.Epoch, r.WalletEpoch, r.DatabaseOID, r.SchemaOID, r.BaseReleaseID, r.BaseBinarySHA256, r.MigrationRunID).Error
}

func verifyLaneCustodyTransitionTx(tx *gorm.DB, input LaneCustodyTransitionV1) (laneCustodySpaceV1, error) {
	space, err := laneCustodySpaceTx(tx)
	if err != nil {
		return space, err
	}
	// Coordinate with managed schema work, not with old application writers.
	// Try rather than wait while holding the independent store installer lock.
	var free bool
	if err := tx.Raw("SELECT pg_catalog.pg_try_advisory_xact_lock_shared(832653145, ?::int)", space.SchemaOID).
		Row().Scan(&free); err != nil || !free {
		return space, ErrLaneStoreAuthorityState
	}
	c, err := inspectLaneStoreCatalogTx(tx)
	if err != nil || c.schema != space.Schema {
		return space, ErrLaneStoreAuthorityState
	}
	base, err := readLaneStoreAuthorityRowTx(tx, c, "")
	if err != nil || base.State != laneStoreAuthorityDormant || base.LaneStoreReleaseV1 != input.Release {
		return space, ErrLaneStoreAuthorityState
	}
	physical, err := laneCustodyPhysicalTableTx(tx, space, im21ReceiptTable)
	if err != nil || !physical {
		return space, ErrLaneStoreAuthorityEvidence
	}
	// Read both rows as one bounded, schema-qualified set. Resolve no
	// relation via search_path or an untyped format() placeholder.
	var receipts []struct {
		Role         string `gorm:"column:role"`
		BinarySHA256 string `gorm:"column:binary_sha256"`
		RunID        string `gorm:"column:run_id"`
	}
	if err := tx.Raw("SELECT role,binary_sha256,run_id FROM " + laneStoreQualified(space.Schema, im21ReceiptTable) +
		" ORDER BY role LIMIT 3").Scan(&receipts).Error; err != nil || len(receipts) != 2 || receipts[0].Role != "log" || receipts[1].Role != "primary" {
		return space, ErrLaneStoreAuthorityEvidence
	}
	for _, receipt := range receipts {
		if receipt.BinarySHA256 != input.BaseBinarySHA256 || receipt.RunID != input.MigrationRunID {
			return space, ErrLaneStoreAuthorityEvidence
		}
	}
	return space, nil
}

func laneCustodyPreparedRow(s laneCustodySpaceV1, input LaneCustodyTransitionV1) laneCustodyRowV1 {
	return laneCustodyRowV1{Singleton: 1, SchemaVersion: laneCustodyVersion, Profile: laneCustodyPrepared,
		LaneStoreReleaseV1: input.Release, DatabaseOID: s.DatabaseOID, SchemaOID: s.SchemaOID,
		BaseReleaseID: input.BaseReleaseID, BaseBinarySHA256: input.BaseBinarySHA256, MigrationRunID: input.MigrationRunID}
}

// InstallLaneCustodyTransitionV1 deliberately writes only no-custody metadata.
// Both existing role receipts must match on one configured PostgreSQL handle.
// No ordinary startup calls it. It creates no writer/owner/wallet authority.
func InstallLaneCustodyTransitionV1(ctx context.Context, primary, log *gorm.DB, input LaneCustodyTransitionV1) error {
	if primary == nil || primary != log || !input.valid() {
		return ErrLaneStoreAuthorityEvidence
	}
	return withLaneStoreMetadataTx(ctx, primary, func(tx *gorm.DB) error {
		s, err := verifyLaneCustodyTransitionTx(tx, input)
		if err != nil {
			return err
		}
		expected := laneCustodyPreparedRow(s, input)
		got, exists, err := readLaneCustodyRowTx(tx, s, true)
		if err != nil {
			return err
		}
		if exists {
			if got != expected {
				return ErrLaneStoreAuthorityState
			}
			return nil
		}
		return createLaneCustodyRowTx(tx, s, expected)
	})
}

// CheckLaneCustodyCapableStartupV1 is an executable no-custody handover gate.
// It verifies the prepared form without writes, but this build cannot attest
// or enforce fleet admission. In particular, it never returns nil on success.
func CheckLaneCustodyCapableStartupV1(ctx context.Context, primary, log *gorm.DB, input LaneCustodyTransitionV1) error {
	if ctx == nil || primary == nil || primary != log || !input.valid() || !laneStorePGHandleOK(primary) {
		return ErrLaneStoreAuthorityEvidence
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	db := primary.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard, SkipHooks: true})
	err := db.Transaction(func(tx *gorm.DB) error {
		s, err := verifyLaneCustodyTransitionTx(tx, input)
		if err != nil {
			return err
		}
		got, exists, err := readLaneCustodyRowTx(tx, s, false)
		if err != nil {
			return err
		}
		if !exists || got != laneCustodyPreparedRow(s, input) {
			return ErrLaneStoreAuthorityState
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return laneStoreAuthorityError(ctx, err)
	}
	return ErrLaneCustodyFleetFenceRequired
}
