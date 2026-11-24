package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const BillingSettlementRecoveryMaxAttempts = 12

// BillingSettlementRecovery is executable evidence only when it was committed
// together with the measured actual under the existing held owner. It contains
// the original policy, never a model name used to reload today's price/policy.
// Version 1 covers ordinary measured text wallet settlements, not task callbacks,
// realtime segments, subscriptions or unknown/missing usage.
type BillingSettlementRecovery struct {
	Version      int      `json:"version"`
	AccountOrder []string `json:"account_order"`
	ChannelID    int      `json:"channel_id"`
	LogExpected  bool     `json:"log_expected"`
}

func (r BillingSettlementRecovery) validate(receipt BillingFinalization) error {
	if r.Version != 1 || receipt.Source != "wallet" || r.ChannelID < 0 || len(r.AccountOrder) == 0 || len(r.AccountOrder) > 3 {
		return billingRefundReviewError("recovery_invalid", "invalid billing settlement recovery evidence")
	}
	seen := make(map[string]bool, len(r.AccountOrder))
	for _, account := range r.AccountOrder {
		if !validAccount(LedgerAccount(account)) || seen[account] {
			return billingRefundReviewError("recovery_invalid", "invalid original settlement account order")
		}
		seen[account] = true
	}
	return receipt.validate()
}

func DecodeBillingSettlementRecovery(receipt BillingFinalization) (BillingSettlementRecovery, error) {
	var recovery BillingSettlementRecovery
	if receipt.SettlementRecovery == "" || len(receipt.SettlementRecovery) > 1024 {
		return recovery, errors.New("missing or oversized billing recovery evidence")
	}
	if err := common.UnmarshalJsonStr(receipt.SettlementRecovery, &recovery); err != nil {
		return recovery, billingRefundReviewError("recovery_invalid", "invalid billing recovery snapshot")
	}
	return recovery, recovery.validate(receipt)
}

// GetReadyBillingSettlements never scans ordinary held/unknown-delivery rows or
// historical review rows. Failed permanent validations have no next retry and
// remain operator-review liabilities. Oldest due ordering prevents starvation.
func GetReadyBillingSettlements(ctx context.Context, limit int) ([]BillingFinalization, error) {
	if limit <= 0 || limit > 128 {
		return nil, errors.New("invalid billing settlement recovery page size")
	}
	var rows []BillingFinalization
	err := DB.WithContext(ctx).Where("state = ? AND settlement_recovery <> '' AND next_retry_at > 0 AND next_retry_at <= ? AND attempts < ?", BillingSettlementReview, common.GetTimestamp(), BillingSettlementRecoveryMaxAttempts).
		Order("next_retry_at, request_id").Limit(limit).Find(&rows).Error
	return rows, err
}

// ClaimBillingSettlementRecovery grants a short database lease after rechecking
// eligibility under the owner lock. Duplicate schedulers do not spend attempts
// or bypass backoff. A crashed worker simply becomes due again; this is not
// separate financial authority, which the terminal owner still serializes.
func ClaimBillingSettlementRecovery(ctx context.Context, requestID string) (*BillingFinalization, error) {
	var claimed *BillingFinalization
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&r).Error; err != nil {
			return err
		}
		now := common.GetTimestamp()
		if r.State != BillingSettlementReview || r.SettlementRecovery == "" || r.NextRetryAt <= 0 || r.NextRetryAt > now || r.Attempts >= BillingSettlementRecoveryMaxAttempts {
			return nil
		}
		if err := tx.Model(&r).Update("next_retry_at", now+10).Error; err != nil {
			return err
		}
		claimed = &r
		return nil
	})
	return claimed, err
}
