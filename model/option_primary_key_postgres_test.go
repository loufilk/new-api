package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLegacyOptionPrimaryKeyMigrationPreservesEvidencePostgres(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires disposable PostgreSQL")
	}
	rollback := errors.New("rollback option migration fixture")
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict_%t", conflict), func(t *testing.T) {
			err := DB.WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
				schema := "option_repair_" + common.GetUUID()
				require.NoError(t, tx.Exec("CREATE SCHEMA "+schema).Error)
				require.NoError(t, tx.Exec("SET LOCAL search_path TO "+schema).Error)
				require.NoError(t, tx.Exec("CREATE TABLE options (key text, value text)").Error)
				last := "value"
				if conflict {
					last = "other"
				}
				require.NoError(t, tx.Exec("INSERT INTO options (key, value) VALUES (?, ?), (?, ?)", "fee", "value", "fee", last).Error)
				err := migrateOptionPrimaryKey(tx)
				if conflict {
					require.ErrorContains(t, err, "conflicting historical values")
				} else {
					require.NoError(t, err)
				}
				unique, err := optionsKeyIsUnique(tx)
				require.NoError(t, err)
				assert.Equal(t, !conflict, unique)
				var rows []Option
				require.NoError(t, tx.Order("value").Find(&rows).Error)
				if conflict {
					assert.Equal(t, []Option{{Key: "fee", Value: "other"}, {Key: "fee", Value: "value"}}, rows)
				} else {
					assert.Equal(t, []Option{{Key: "fee", Value: "value"}}, rows)
				}
				return rollback
			})
			require.ErrorIs(t, err, rollback)
		})
	}
}
