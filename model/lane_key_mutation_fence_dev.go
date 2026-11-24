//go:build lane_billing_integration_dev

package model

import (
	"context"
	_ "embed"
	"strings"

	"github.com/jackc/pgx/v5"
	"gorm.io/gorm"
)

//go:embed testdata/lane_key_mutation_fence_dev.sql
var laneKeyMutationFenceDevSQL string

// No runtime callers or automatic migration. Without an explicit install in
// an isolated finite-Key fixture, this build tag alone changes no Key writer.
func installLaneKeyMutationFenceDev(ctx context.Context, db *gorm.DB) error {
	return withLaneBillingFiniteKeyDevTx(ctx, db, func(tx *gorm.DB) error {
		if !tx.Migrator().HasTable(&Token{}) || !tx.Migrator().HasTable(&laneBillingFiniteKeyEnrollmentV1{}) {
			return ErrLaneBillingOwnerUnavailable
		}
		var schema string
		if err := tx.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
			return err
		}
		return tx.Exec(strings.ReplaceAll(laneKeyMutationFenceDevSQL, "@schema@", pgx.Identifier{schema}.Sanitize())).Error
	})
}
