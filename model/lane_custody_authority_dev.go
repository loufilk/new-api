//go:build lane_billing_integration_dev

package model

import (
	"context"

	"gorm.io/gorm"
)

// InstallLaneCustodyFixtureV1 attests the existing disposable fixture roster.
// It is not a way to convert a prepared store, install financial tables,
// authorize an actual provider, or enroll any writer. The capability is
// returned only after the versioned row has committed in the same store.
func InstallLaneCustodyFixtureV1(ctx context.Context, primary, log *gorm.DB, release LaneStoreReleaseV1, walletEpoch int64) (*LaneCustodyAuthorityV1, error) {
	if ctx == nil || primary == nil || primary != log || !laneStorePGHandleOK(primary) || !release.valid() || walletEpoch <= 0 {
		return nil, ErrLaneStoreAuthorityEvidence
	}
	pool, err := primary.DB()
	if err != nil {
		return nil, ErrLaneStoreAuthorityEvidence
	}
	var authority *LaneCustodyAuthorityV1
	err = withLaneStoreMetadataTx(ctx, primary, func(tx *gorm.DB) error {
		schema, err := laneBillingDevPGSchemaTx(tx)
		if err != nil {
			return ErrLaneStoreAuthorityEvidence
		}
		space, err := laneCustodySpaceTx(tx)
		if err != nil || space.Schema != schema {
			return ErrLaneStoreAuthorityEvidence
		}
		if err := readLaneCustodyReleaseTx(tx, space, release, "SHARE"); err != nil {
			return err
		}
		row := laneCustodyRowV1{Singleton: 1, SchemaVersion: laneCustodyVersion, Profile: laneCustodyFixture,
			LaneStoreReleaseV1: release, WalletEpoch: walletEpoch, DatabaseOID: space.DatabaseOID, SchemaOID: space.SchemaOID}
		old, exists, err := readLaneCustodyRowTx(tx, space, true)
		if err != nil {
			return err
		}
		if exists {
			if row != old {
				return ErrLaneStoreAuthorityEvidence
			}
		} else if err := createLaneCustodyRowTx(tx, space, row); err != nil {
			return err
		}
		roster, err := readLaneCustodyFixtureRosterTx(tx, space)
		if err != nil {
			return err
		}
		authority = &LaneCustodyAuthorityV1{primary: primary, pool: pool, space: space, catalog: row, roster: roster}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return authority, nil
}
