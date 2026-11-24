package model

import (
	"database/sql"
	"strings"

	"gorm.io/gorm"
)

// LaneCustodyAuthorityV1 is opaque and cannot be issued by the ordinary
// binary. A no-custody transition record, a successful read, and a public
// value of LaneStoreReleaseV1 cannot be converted into this capability.
type LaneCustodyAuthorityV1 struct {
	primary *gorm.DB
	pool    *sql.DB
	space   laneCustodySpaceV1
	catalog laneCustodyRowV1
	roster  string
}

// CheckTx validates the configured store and wallet generation inside the
// existing write transaction. Release then catalogue locks precede any
// optional financial action; this method writes nothing.
func (a *LaneCustodyAuthorityV1) CheckTx(tx, primary, log *gorm.DB, walletEpoch int64) error {
	return a.checkTx(tx, primary, log, walletEpoch, true)
}

// CheckReadTx supplies the same store/epoch verification to a repeatable,
// read-only snapshot without taking row locks or allowing any fallback.
func (a *LaneCustodyAuthorityV1) CheckReadTx(tx, primary, log *gorm.DB, walletEpoch int64) error {
	if a == nil || tx == nil || tx.Config == nil || tx.Statement == nil || tx.Dialector == nil || tx.DryRun || tx.Dialector.Name() != "postgres" {
		return ErrLaneStoreAuthorityEvidence
	}
	var readOnly, isolation string
	if err := tx.Raw("SELECT pg_catalog.current_setting('transaction_read_only'),pg_catalog.current_setting('transaction_isolation')").
		Row().Scan(&readOnly, &isolation); err != nil || readOnly != "on" || isolation != "repeatable read" {
		return ErrLaneStoreAuthorityEvidence
	}
	return a.checkTx(tx, primary, log, walletEpoch, false)
}

func (a *LaneCustodyAuthorityV1) checkTx(tx, primary, log *gorm.DB, walletEpoch int64, lock bool) error {
	if a == nil || a.primary == nil || a.primary != primary || primary != log || a.pool == nil ||
		a.catalog.Profile != laneCustodyFixture || a.catalog.WalletEpoch <= 0 || a.catalog.WalletEpoch != walletEpoch {
		return ErrLaneStoreAuthorityEvidence
	}
	s, err := laneCustodySpaceTx(tx)
	if err != nil || s != a.space {
		return ErrLaneStoreAuthorityEvidence
	}
	pool, err := tx.DB()
	if err != nil || pool != a.pool {
		return ErrLaneStoreAuthorityEvidence
	}
	mode := ""
	if lock {
		mode = "SHARE"
	}
	if err := readLaneCustodyReleaseTx(tx, s, a.catalog.LaneStoreReleaseV1, mode); err != nil {
		return err
	}
	row, exists, err := readLaneCustodyRowTx(tx, s, lock)
	if err != nil || !exists || row != a.catalog {
		return ErrLaneStoreAuthorityEvidence
	}
	roster, err := readLaneCustodyFixtureRosterTx(tx, s)
	if err != nil || roster != a.roster {
		return ErrLaneStoreAuthorityEvidence
	}
	return nil
}

func readLaneCustodyReleaseTx(tx *gorm.DB, s laneCustodySpaceV1, release LaneStoreReleaseV1, lock string) error {
	physical, err := laneCustodyPhysicalTableTx(tx, s, laneStoreAuthorityRelation)
	if err != nil || !physical {
		return ErrLaneStoreAuthorityEvidence
	}
	base, err := readLaneStoreAuthorityRowTx(tx, laneStoreCatalog{schema: s.Schema, table: laneStoreQualified(s.Schema, laneStoreAuthorityRelation)}, lock)
	if err != nil || base.State != laneStoreAuthorityDormant || base.LaneStoreReleaseV1 != release {
		return ErrLaneStoreAuthorityEvidence
	}
	return nil
}

// This is intentionally the already-proven isolated roster, not a live
// schema specification. Its physical relation IDs prohibit shadowing and
// prevent an issued scope from silently adopting dropped/recreated tables.
func readLaneCustodyFixtureRosterTx(tx *gorm.DB, s laneCustodySpaceV1) (string, error) {
	names := []string{
		"balance_ledger", "balance_ledger_operations", "billing_finalizations", "lane_billing_child_receipts_v1",
		"lane_billing_key_enrollments_v1", "lane_billing_key_shares_v1", "lane_billing_key_transfers_v1",
		"lane_billing_measured_dev_v1", "lane_billing_usage_outbox_dev_v1", laneCustodyRelation, laneStoreAuthorityRelation,
		"logs", "shared_wallet_shares", "shared_wallet_transfers", "tokens", "users",
	}
	var count int
	var roster string
	err := tx.Raw(`SELECT count(*),coalesce(string_agg(c.relname||':'||c.oid::text,',' ORDER BY c.relname),'')
		FROM pg_catalog.pg_class c
		WHERE c.relnamespace=?::oid AND c.relkind='r' AND c.relname IN ?`, s.SchemaOID, names).Row().Scan(&count, &roster)
	if err != nil || count != len(names) || strings.Count(roster, ",") != len(names)-1 {
		return "", ErrLaneStoreAuthorityEvidence
	}
	return roster, nil
}
