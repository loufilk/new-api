//go:build lane_billing_integration_dev

package model

import (
	"context"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewLaneKeyAvailabilityDev is solely for the isolated gateway fixture. It
// does not change any route registration or release authority. Unlike the
// fixture writer, this read must let an auth test use a stale Redis entry.
func NewLaneKeyAvailabilityDev(ctx context.Context, db *gorm.DB, key LaneBillingFiniteKeyIdentity) (*LaneKeyAvailabilityV1, error) {
	if ctx == nil || !key.valid() || !laneStorePGHandleOK(db) || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return nil, ErrLaneBillingOwnerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	schema, err := laneBillingDevPGSchemaTx(db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard}))
	if err != nil {
		return nil, err
	}
	return &LaneKeyAvailabilityV1{
		db: db, key: key,
		verifyStore: func(tx *gorm.DB) error {
			if actual, err := laneBillingDevPGSchemaTx(tx); err != nil || actual != schema {
				return ErrLaneBillingOwnerUnavailable
			}
			return nil
		},
	}, nil
}
