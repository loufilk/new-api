package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// RequestBillingRefund durably acquires the same unique terminal owner as
// settlement, before the request can leave an asynchronous refund behind.
// A successful settlement (including a lost COMMIT ACK) wins over refund.
func RequestBillingRefund(ctx context.Context, receipt BillingFinalization) (*BillingFinalization, error) {
	var owner *BillingFinalization
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		owner = r
		switch r.State {
		case BillingSettled, BillingTaskOwned, BillingRefunded, BillingRefundReady:
			return nil
		case "building":
			// Source-ledger history alone cannot prove the request's token hold.
			// Roll back this private claim instead of manufacturing refund authority.
			return billingRefundReviewError("owner_missing", "refund requires an atomically reserved request owner")
		case BillingHeld:
			if err := validateBillingSourceTx(tx, r); err != nil {
				return err
			}
			r.State = BillingRefundReady
			return tx.Model(r).Update("state", BillingRefundReady).Error
		default:
			return ErrBillingFinalized
		}
	})
	if err != nil {
		return nil, err
	}
	return owner, nil
}

// ApplyBillingRefund executes only an explicitly admitted v2 intent. The
// original source, original token, and refunded receipt either all commit or
// all roll back. A source-only historical replay is not completion evidence.
func ApplyBillingRefund(ctx context.Context, requestID string) (bool, error) {
	applied := false
	userID := 0
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&r).Error; err != nil {
			return err
		}
		userID = r.UserID
		if err := r.validate(); err != nil {
			return err
		}
		if r.State == BillingRefunded {
			return nil
		}
		if r.State != BillingRefundReady {
			return ErrBillingFinalized
		}
		if err := validateBillingSourceTx(tx, &r); err != nil {
			return err
		}
		if r.Source == "wallet" && r.SourceHeld > 0 {
			result, err := CreditQuotaSharesTx(tx, LedgerEntry{
				UserId: r.UserID, Account: AccountPaid, Reason: LedgerReasonBillingRefund,
				ReferenceType: "relay", ReferenceId: r.RequestID, RequestId: r.RequestID,
				IdempotencyKey: "billing:refund:" + r.RequestID,
			}, r.Shares())
			if err != nil {
				return err
			}
			if result.Replayed {
				return billingRefundReviewError("source_evidence", "source refund exists without atomic terminal completion")
			}
		} else if r.Source == "subscription" {
			identity, err := GetSubscriptionBillingIdentityTx(tx, r.RequestID, r.UserID, r.SubscriptionID, r.SubscriptionPeriod, int64(r.SourceHeld))
			if err != nil {
				return err
			}
			changed, err := RefundSubscriptionPreConsumeTx(tx, identity)
			if err != nil {
				return err
			}
			if !changed {
				return billingRefundReviewError("source_evidence", "subscription refund exists without atomic terminal completion")
			}
		}
		if err := ApplyBillingTokenDeltaTx(tx, &r, r.TokenHeld); err != nil {
			return err
		}
		if err := tx.Model(&r).Updates(map[string]any{"state": BillingRefunded, "actual_quota": 0, "next_retry_at": 0}).Error; err != nil {
			return err
		}
		applied = true
		return nil
	})
	if userID > 0 {
		RefreshUserQuotaCacheAfterBillingFinalization(userID)
	}
	return applied && err == nil, err
}

// RecordBillingRefundFailure never authorizes a refund or reopens a completed
// owner. Unknown COMMIT outcomes are retried only through the same frozen row.
func RecordBillingRefundFailure(ctx context.Context, requestID string, failure error) error {
	if failure == nil {
		return nil
	}
	code, class := BillingRefundErrorCode(failure)
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&r).Error; err != nil {
			return err
		}
		if r.State != BillingRefundReady {
			return nil
		}
		now := common.GetTimestamp()
		updates := map[string]any{"attempts": r.Attempts + 1, "last_error_code": code, "last_error_class": class, "last_attempt_at": now}
		if r.FirstFailedAt == 0 {
			updates["first_failed_at"], updates["first_error_code"], updates["first_error_class"] = now, code, class
		}
		if class == billingRefundReview {
			updates["state"], updates["escalated_at"], updates["next_retry_at"] = BillingRefundReview, now, 0
		} else {
			// Finite work per pass and a capped retry cadence, not a new queue.
			updates["next_retry_at"] = now + int64(min(300, 15*(1<<min(r.Attempts, 4))))
		}
		return tx.Model(&r).Updates(updates).Error
	})
}

func GetReadyBillingRefunds(ctx context.Context, limit int) ([]BillingFinalization, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("invalid billing refund batch size")
	}
	var rows []BillingFinalization
	err := DB.WithContext(ctx).Where("state = ? AND next_retry_at <= ?", BillingRefundReady, common.GetTimestamp()).Order("next_retry_at, request_id").Limit(limit).Find(&rows).Error
	return rows, err
}
