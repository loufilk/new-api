//go:build lane_billing_integration_dev

package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This fixture-only catalogue is not ordinary Stage 8 startup authority.
// The isolated schema is pinned on the SQL connection before selecting the
// one compatible release metadata table. No DDL or installer is added here.
func inspectLaneNativeChildFixtureCatalogTx(tx *gorm.DB) (laneStoreCatalog, error) {
	if common.RedisEnabled || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return laneStoreCatalog{}, ErrLaneBillingOwnerUnavailable
	}
	schema, err := laneBillingDevPGSchemaTx(tx)
	if err != nil {
		return laneStoreCatalog{}, err
	}
	var physical bool
	if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=? AND c.relname=? AND c.relkind='r')`, schema, laneStoreAuthorityRelation).Row().Scan(&physical); err != nil || !physical {
		return laneStoreCatalog{}, ErrLaneBillingOwnerUnavailable
	}
	return laneStoreCatalog{schema: schema, table: laneStoreQualified(schema, laneStoreAuthorityRelation)}, nil
}

// CaptureLaneNativeChildQuoteDev is isolated prehold evidence for the normal
// native route rehearsal. It never opens the fixture on an ordinary build and
// does not confer a hold, a dispatch grant, or permission to start this DB.
func CaptureLaneNativeChildQuoteDev(ctx context.Context, db *gorm.DB, input LaneStoreQuoteInputV1) (LaneStoreQuoteAuthorityV1, error) {
	if ctx == nil || !laneStorePGHandleOK(db) {
		return LaneStoreQuoteAuthorityV1{}, ErrLaneBillingOwnerUnavailable
	}
	ctx, done := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer done()
	var q LaneStoreQuoteAuthorityV1
	if err := db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		var err error
		q, err = lockLaneStoreQuoteWithCatalogV1Tx(tx, input, 0, inspectLaneNativeChildFixtureCatalogTx, true)
		return err
	}); err != nil {
		return LaneStoreQuoteAuthorityV1{}, ErrLaneBillingOwnerEvidence
	}
	return q, nil
}

func revalidateLaneNativeChildQuoteDevTx(tx *gorm.DB, q LaneStoreQuoteAuthorityV1) error {
	if !q.custody || q.schema == "" {
		return ErrLaneBillingOwnerEvidence
	}
	got, err := lockLaneStoreQuoteWithCatalogV1Tx(tx, q.input, 0, inspectLaneNativeChildFixtureCatalogTx, true)
	if err != nil || q != got {
		return ErrLaneBillingOwnerEvidence
	}
	return nil
}

type laneNativeChildDevStore struct {
	laneBillingVerifiedDevStore
	quote LaneStoreQuoteAuthorityV1
}

func (s *laneNativeChildDevStore) ReserveLaneBillingReceiptTx(tx *gorm.DB, id LaneBillingOwnerIdentity, cap int) (bool, error) {
	if err := revalidateLaneNativeChildQuoteDevTx(tx, s.quote); err != nil {
		return false, err
	}
	return s.laneBillingEnrolledPGChildStore.reserveAfterCustodyFence(tx, id, cap, func(tx *gorm.DB) error {
		return verifyLaneNativeChildWalletTx(tx, s.binding, cap)
	})
}

// ReserveLaneNativeChildDev uses exactly the ordinary canonical request PK,
// concrete child receipt and dual hold, through the existing owner
// transaction. It adds no API for public routing or untagged activation.
func ReserveLaneNativeChildDev(ctx context.Context, db *gorm.DB, q LaneStoreQuoteAuthorityV1, in LaneBillingChildDevBinding, cap int) (*LaneBillingChildDev, error) {
	b := laneBillingChildBinding(in)
	i := q.input
	if ctx == nil || !q.custody || !b.valid() || b.Owner.RequestID != i.RequestID || b.Owner.UserID != i.UserID ||
		b.Owner.TokenID != i.TokenID || b.Owner.TokenFingerprint != i.TokenFingerprint || b.Owner.Epoch != i.Release.Epoch {
		return nil, ErrLaneBillingOwnerEvidence
	}
	// Epoch is in the dedicated owner fields. Bind the release/build/store
	// identity as well, without an additional table or bearer credentials.
	policy, err := common.Marshal(struct {
		Release LaneStoreReleaseV1
		Policy  string
	}{i.Release, b.FundingPolicySHA256})
	if err != nil {
		return nil, ErrLaneBillingOwnerEvidence
	}
	h := sha256.Sum256(policy)
	b.FundingPolicySHA256 = hex.EncodeToString(h[:])
	store := &laneNativeChildDevStore{laneBillingVerifiedDevStore{laneBillingEnrolledPGChildStore{laneBillingPGChildStore{binding: b}}}, q}
	res, err := ReserveLaneBillingOwner(ctx, db, b.Owner, cap, store)
	if err != nil {
		return nil, err
	}
	if !res.Fresh {
		// A replay is observational only; never hand it another sender.
		return nil, ErrBillingRequestOwnerConflict
	}
	return &LaneBillingChildDev{db: db, binding: b, cap: cap, releaseQuote: q}, nil
}
