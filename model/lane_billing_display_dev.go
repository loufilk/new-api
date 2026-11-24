//go:build lane_billing_integration_dev

package model

import (
	"context"

	"gorm.io/gorm"
)

type laneBillingDisplayFixtureStore struct {
	schema       string
	release      LaneStoreReleaseV1
	primary, log *gorm.DB
	walletEpoch  int64
}

func (s laneBillingDisplayFixtureStore) CheckReadTx(tx, primary, log *gorm.DB, walletEpoch int64) error {
	if primary != s.primary || log != s.log || walletEpoch != s.walletEpoch {
		return ErrLaneBillingOwnerUnavailable
	}
	schema, err := laneBillingDevPGSchemaTx(tx)
	if err != nil || schema != s.schema {
		return ErrLaneBillingOwnerUnavailable
	}
	var physical bool
	if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=? AND c.relname=? AND c.relkind='r')`, schema, laneStoreAuthorityRelation).Row().Scan(&physical); err != nil || !physical {
		return ErrLaneBillingOwnerUnavailable
	}
	row, err := readLaneStoreAuthorityRowTx(tx, laneStoreCatalog{schema: schema, table: laneStoreQualified(schema, laneStoreAuthorityRelation)}, "")
	if err != nil || row.State != laneStoreAuthorityDormant || row.LaneStoreReleaseV1 != s.release {
		return ErrLaneBillingOwnerEvidence
	}
	return nil
}

// NewLaneBillingDisplayDev binds a pre-existing, isolated exact store
// manifest. It cannot write that manifest or install custody.
func NewLaneBillingDisplayDev(ctx context.Context, db *gorm.DB, release LaneStoreReleaseV1, id LaneBillingFiniteKeyIdentity, walletEpoch int64) (*LaneBillingDisplayV1, error) {
	if ctx == nil || db != DB || db != LOG_DB || !laneStorePGHandleOK(db) || !release.valid() || !id.valid() || walletEpoch <= 0 {
		return nil, ErrLaneBillingOwnerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	schema, err := laneBillingDevPGSchemaTx(db.Session(&gorm.Session{NewDB: true, Context: ctx}))
	if err != nil {
		return nil, err
	}
	r := &LaneBillingDisplayV1{primary: db, log: db, scope: id, walletEpoch: walletEpoch, store: laneBillingDisplayFixtureStore{
		schema: schema, release: release, primary: db, log: db, walletEpoch: walletEpoch,
	}}
	if err := r.snapshot(ctx, func(*gorm.DB) error { return nil }); err != nil {
		return nil, err
	}
	return r, nil
}
