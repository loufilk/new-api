package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ApplySubscriptionSupplement applies an existing independent fee/realtime
// segment. Its separate stable operation identity prevents it from sharing the
// main request's terminal owner. The supplied period and token are captured
// authorization, not values inferred from the user's current allowance/key.
func ApplySubscriptionSupplement(ctx context.Context, receipt BillingFinalization, amount int, after ...func(*gorm.DB) error) (bool, error) {
	if receipt.Source != "subscription" || receipt.SourceHeld != 0 || receipt.PreConsumed != 0 || receipt.TokenHeld != 0 || amount <= 0 || amount > common.MaxQuota {
		return false, errors.New("invalid independent subscription adjustment")
	}
	applied := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stored, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		if stored.State == BillingSettled {
			if stored.ActualQuota != amount {
				return ErrBillingFinalized
			}
			return nil
		}
		if stored.State != "building" {
			return ErrBillingFinalized
		}
		if err := LockFinancialUserTx(tx, stored.UserID); err != nil {
			return err
		}
		if err := AdjustSubscriptionQuotaPeriodTx(tx, stored.UserID, stored.SubscriptionID, stored.SubscriptionPeriod, int64(amount)); err != nil {
			return err
		}
		if err := ApplyBillingTokenDeltaTx(tx, stored, -amount); err != nil {
			return err
		}
		for _, account := range after {
			if account != nil {
				if err := account(tx); err != nil {
					return err
				}
			}
		}
		if err := tx.Model(stored).Updates(map[string]any{"state": BillingSettled, "actual_quota": amount}).Error; err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied && err == nil, err
}
