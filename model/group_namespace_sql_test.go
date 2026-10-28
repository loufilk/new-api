package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Exercise the production predicates with the actual MySQL SQL builder, without
// a server connection. The real database matrix remains mandatory in CI.
func TestGroupNamespaceMySQLQuotesOptionKeys(t *testing.T) {
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true,
	})
	require.NoError(t, err)
	previousDB, previousType := DB, common.MainDatabaseType()
	captureGroupRenameRuntimeState(t)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	initCol()
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		initCol()
	})
	var statements []string
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("group_namespace_capture_query", func(tx *gorm.DB) {
		statements = append(statements, tx.Statement.SQL.String())
	}))
	require.NoError(t, db.Callback().Update().After("gorm:update").Register("group_namespace_capture_update", func(tx *gorm.DB) {
		statements = append(statements, tx.Statement.SQL.String())
	}))
	for _, scenario := range []struct {
		name string
		run  func() error
	}{
		{"configured_groups", func() error {
			_, err := loadConfiguredGroupNamesTx(db)
			return err
		}},
		{"version_update", func() error {
			_, err := bumpGroupConfigVersionTx(db)
			return err
		}},
		{"runtime_reload", func() error {
			requestPolicyOptionMutex.Lock()
			defer requestPolicyOptionMutex.Unlock()
			return reloadGroupRuntimeOptions()
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			statements = nil
			require.NoError(t, scenario.run())
			require.NotEmpty(t, statements)
			for _, statement := range statements {
				assert.Contains(t, statement, "`key`", statement)
				assert.NotRegexp(t, "(?i)(?:WHERE|AND|OR)[[:space:]]+key[[:space:]]*(?:IN|=)", statement)
			}
		})
	}
}
