package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestMaintenanceInspectDoesNotInitializeDatabase(t *testing.T) {
	previousPath := common.SQLitePath
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		common.SQLitePath = previousPath
		common.SetDatabaseTypes(previousMain, previousLog)
	})
	t.Setenv("SQL_DSN", "")
	common.SQLitePath = filepath.Join(t.TempDir(), "referral-inspect.db")

	_, err := model.OpenPrimaryForMaintenance(true)
	require.Error(t, err)
	_, err = os.Stat(common.SQLitePath)
	require.ErrorIs(t, err, os.ErrNotExist)

	seed, err := model.OpenPrimaryForMaintenance(false)
	require.NoError(t, err)
	require.NoError(t, seed.AutoMigrate(&model.Option{}))
	closeDatabase(seed)

	db, err := model.OpenPrimaryForMaintenance(true)
	require.NoError(t, err)
	t.Cleanup(func() { closeDatabase(db) })
	_, err = model.InspectLegacyAffiliateData(db)
	require.NoError(t, err)
	var count int64
	require.NoError(t, db.Model(&model.Option{}).Count(&count).Error)
	require.Zero(t, count, "inspection must not install options")
	require.Error(t, db.Create(&model.Option{Key: "fixture", Value: "test"}).Error)
}
