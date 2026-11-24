//go:build lane_billing_integration_dev

package model

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The integration fixture owns this opt-in DDL, not ordinary startup. It only
// accepts dedicated lane_billing_dev_* schemas on loopback PostgreSQL. It does
// not create or change ordinary owner/treasury tables or provision any grants.
func installLaneBillingChildDevSchema(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil || db.Config == nil || db.DryRun || db.Dialector == nil || db.Dialector.Name() != "postgres" || db.Statement == nil {
		return ErrLaneBillingOwnerUnavailable
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return ErrLaneBillingOwnerUnavailable
	}
	if _, nested := db.Config.ConnPool.(gorm.TxCommitter); nested {
		return ErrLaneBillingOwnerUnavailable
	}
	return db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		if err := laneBillingFiniteKeyDevScopeTx(tx); err != nil {
			return err
		}
		if !tx.Migrator().HasTable(&BillingFinalization{}) || !tx.Migrator().HasTable(&SharedWalletShare{}) || !tx.Migrator().HasTable(&SharedWalletTransfer{}) {
			return ErrLaneBillingOwnerUnavailable
		}
		if err := tx.AutoMigrate(&laneBillingChildReceiptV1{}, &laneBillingFiniteKeyShareV1{}, &LaneBillingMeasuredDev{}, &laneBillingUsageOutboxDev{}); err != nil {
			return ErrLaneBillingOwnerStore
		}
		return nil
	})
}
