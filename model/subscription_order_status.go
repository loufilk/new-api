package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// FailPendingSubscriptionOrder records a creation failure without overwriting a
// completed subscription's timestamp, provider payload, payment method or grant.
// The provider is required: this failure path must not cross payment gateways.
func FailPendingSubscriptionOrder(tradeNo string, expectedPaymentProvider string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	if expectedPaymentProvider == "" {
		return ErrPaymentMethodMismatch
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := lockForUpdate(tx).Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSubscriptionOrderNotFound
			}
			return err
		}
		if order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status != common.TopUpStatusPending {
			return ErrSubscriptionOrderStatusInvalid
		}

		result := tx.Model(&SubscriptionOrder{}).
			Where("id = ? AND trade_no = ? AND payment_provider = ? AND status = ?", order.Id, tradeNo, expectedPaymentProvider, common.TopUpStatusPending).
			Update("status", common.TopUpStatusFailed)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrSubscriptionOrderStatusInvalid
		}
		return nil
	})
}
