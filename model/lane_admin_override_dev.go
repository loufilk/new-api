//go:build lane_billing_integration_dev

package model

import (
	"fmt"

	"gorm.io/gorm"
)

// Called only from the scoped synthetic schema installer, on its transaction.
// This is negative authority only: a grant, return or child is never enabled.
// As of this version the ledger operation header includes a signature but not
// the account name. Refuse ALL new absolute overrides for the affected wallet
// rather than trying to derive a target from the opaque signature. A past
// signed operation replays before this boundary; even a fresh raw "no-op"
// inserts a header and must fail. A drained wallet is not automatically safe
// to reopen: receipts remain until there is a separate reconciliation policy.
func installLaneAdminOverrideFenceDevTx(tx *gorm.DB) error {
	if err := laneBillingFiniteKeyDevScopeTx(tx); err != nil {
		return err
	}
	var schema string
	if err := tx.Raw("SELECT pg_catalog.current_schema()").Scan(&schema).Error; err != nil ||
		!laneBillingOwnerIDValid(schema) {
		return ErrLaneBillingOwnerUnavailable
	}
	// Scope validation excludes non-loopback, unpinned search paths and
	// temporary relations. Fully qualify the retained schema references so
	// another connection cannot switch search_path to bypass the denial.
	q := fmt.Sprintf("%q", schema)
	const function = "lane_dev_guard_absolute_override_v1"
	if err := tx.Exec(fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s.%s() RETURNS trigger
		LANGUAGE plpgsql SECURITY INVOKER SET search_path = pg_catalog AS $guard$
		BEGIN
			IF EXISTS (SELECT 1 FROM %s.shared_wallet_shares WHERE user_id = NEW.user_id)
				OR EXISTS (SELECT 1 FROM %s.shared_wallet_transfers WHERE user_id = NEW.user_id) THEN
				RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='lane_dev_custody_override_v1',
					MESSAGE='Absolute wallet override refused while custody history exists';
			END IF;
			RETURN NEW;
		END;
		$guard$`, q, function, q, q)).Error; err != nil {
		return ErrLaneBillingOwnerStore
	}
	// No authority lookup on debits, credits, transfers or replay. Existing
	// custody changes and canonical overrides take the same users row lock.
	// Refuse either half of a partial receipt rather than treating it as zero.
	if err := tx.Exec(fmt.Sprintf(`CREATE OR REPLACE TRIGGER lane_dev_custody_override_v1
		BEFORE INSERT ON %s.balance_ledger_operations
		FOR EACH ROW WHEN (NEW.operation = 'override')
		EXECUTE FUNCTION %s.%s()`, q, q, function)).Error; err != nil {
		return ErrLaneBillingOwnerStore
	}
	return nil
}
