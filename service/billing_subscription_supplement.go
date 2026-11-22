package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"gorm.io/gorm"
)

// Independent subscription charges inherit the authorization and original
// period captured before the request. Mutable RelayInfo is checked, never used
// to grant a new token or a new period authority halfway through a request.
func applySubscriptionSupplement(info *relaycommon.RelayInfo, amount int, suffix, purpose string) (bool, error) {
	s, ok := info.Billing.(*BillingSession)
	if !ok {
		return false, errors.New("subscription supplement has no original billing authorization")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reservationUncertain {
		if err := s.resolveReservationLocked(); err != nil {
			return false, err
		}
	}
	original, err := s.terminalReceiptLocked()
	if err != nil {
		return false, err
	}
	if original.Source != BillingSourceSubscription || original.RequestID != info.RequestId || original.UserID != info.UserId || original.SubscriptionID != info.SubscriptionId || original.SubscriptionPeriod != info.SubscriptionQuotaPeriod {
		return false, errors.New("subscription supplement identity changed")
	}
	if !info.IsPlayground && (original.TokenID != info.TokenId || original.TokenFingerprint != model.BillingTokenFingerprint(info.TokenKey)) || info.IsPlayground && original.TokenID != 0 {
		return false, errors.New("subscription supplement token identity changed")
	}
	operationID := model.BillingTokenFingerprint("subscription:supplement:" + original.RequestID + ":" + suffix + ":" + purpose)
	receipt := model.BillingFinalization{RequestID: operationID, UserID: original.UserID, Source: original.Source, TokenID: original.TokenID, TokenFingerprint: original.TokenFingerprint, SubscriptionID: original.SubscriptionID, SubscriptionPeriod: original.SubscriptionPeriod}
	var account func(*gorm.DB) error
	if purpose == "violation_fee" {
		account = func(tx *gorm.DB) error {
			return model.ApplyTaskUsageDeltaTx(tx, original.UserID, info.ChannelId, amount, 1)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	applied, err := model.ApplySubscriptionSupplement(ctx, receipt, amount, account)
	if err != nil {
		return false, err
	}
	// This map only deduplicates advisory notification/log totals. Durable
	// money idempotency is exclusively the SQL operation owner above.
	if s.subscriptionSupplements == nil {
		s.subscriptionSupplements = make(map[string]bool)
	}
	if !s.subscriptionSupplements[operationID] {
		info.SubscriptionPostDelta += int64(amount)
		s.subscriptionSupplements[operationID] = true
	}
	return applied, nil
}
