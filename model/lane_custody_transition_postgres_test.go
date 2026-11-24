package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func laneCustodyBaseFixture(t *testing.T, db *gorm.DB, r LaneStoreReleaseV1) LaneCustodyTransitionV1 {
	t.Helper()
	input := LaneCustodyTransitionV1{Release: r, BaseReleaseID: strings.Repeat("d", 32), BaseBinarySHA256: strings.Repeat("e", 64)}
	input.MigrationRunID = input.BaseReleaseID + ":synthetic-migrator-v1"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := writeIM21Receipt(tx, "primary", input.BaseBinarySHA256, input.MigrationRunID); err != nil {
			return err
		}
		return writeIM21Receipt(tx, "log", input.BaseBinarySHA256, input.MigrationRunID)
	}))
	return input
}

func TestLaneCustodyTransitionBlocksServingWithUnfencedOldWriterPostgres(t *testing.T) {
	db, quote := laneStoreFixture(t, true)
	ctx := t.Context()
	in := laneCustodyBaseFixture(t, db, quote.Release)
	require.NoError(t, CheckLaneStoreOrdinaryStartup(ctx, db))
	require.NoError(t, InstallLaneStoreAuthorityV1(ctx, db, in.Release))

	// The older writer passed startup and retains a physical transaction;
	// the new installer/advisory lock cannot make that transaction disappear.
	old := db.Begin()
	require.NoError(t, old.Error)
	t.Cleanup(func() { _ = old.Rollback().Error })
	var before User
	require.NoError(t, old.Take(&before, quote.UserID).Error)
	require.ErrorIs(t, CheckLaneCustodyCapableStartupV1(ctx, db, db, in), ErrLaneStoreAuthorityState)
	require.False(t, LaneStoreRuntimeCapable)

	require.ErrorIs(t, InstallLaneCustodyTransitionV1(ctx, db, db.Session(&gorm.Session{NewDB: true}), in), ErrLaneStoreAuthorityEvidence)
	require.False(t, db.Migrator().HasTable(laneCustodyRelation))
	require.NoError(t, db.Exec("UPDATE core_im21_schema_receipts SET run_id=? WHERE role='log'", in.BaseReleaseID+":different-migrator").Error)
	require.ErrorIs(t, InstallLaneCustodyTransitionV1(ctx, db, db, in), ErrLaneStoreAuthorityEvidence)
	require.False(t, db.Migrator().HasTable(laneCustodyRelation), "one valid role does not create half a custody mark")
	require.NoError(t, db.Exec("UPDATE core_im21_schema_receipts SET run_id=? WHERE role='log'", in.MigrationRunID).Error)
	require.NoError(t, InstallLaneCustodyTransitionV1(ctx, db, db, in))
	require.NoError(t, InstallLaneCustodyTransitionV1(ctx, db, db, in))
	require.ErrorIs(t, CheckLaneCustodyCapableStartupV1(ctx, db, db, in), ErrLaneCustodyFleetFenceRequired)
	require.NoError(t, CheckLaneStoreOrdinaryStartup(ctx, db))
	require.False(t, db.Migrator().HasTable("shared_wallet_shares"))
	require.False(t, db.Migrator().HasTable("lane_billing_child_receipts_v1"))
	require.NoError(t, old.Exec("UPDATE users SET quota=quota+1,quota_paid=quota_paid+1 WHERE id=?", quote.UserID).Error)
	require.NoError(t, old.Commit().Error)
	var after User
	require.NoError(t, db.Take(&after, quote.UserID).Error)
	require.Equal(t, before.QuotaPaid+1, after.QuotaPaid, "a real old writer can still commit after the marker")
	require.ErrorIs(t, CheckLaneCustodyCapableStartupV1(ctx, db, db, in), ErrLaneCustodyFleetFenceRequired)

	// On a partial restore a plausible manifest and one intact receipt are
	// not enough, and rerunning setup may not adopt the mismatched row.
	require.NoError(t, db.Exec("UPDATE lane_custody_catalog_v1 SET schema_oid=schema_oid+1").Error)
	require.ErrorIs(t, CheckLaneCustodyCapableStartupV1(ctx, db, db, in), ErrLaneStoreAuthorityState)
	require.ErrorIs(t, InstallLaneCustodyTransitionV1(ctx, db, db, in), ErrLaneStoreAuthorityState)
}
