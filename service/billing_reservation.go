package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

type transactionalReservationFunding interface {
	PreConsumeInTx(*gorm.DB, int) (moneyport.SettlementResult, error)
	ReserveInTx(*gorm.DB, int) (moneyport.SettlementResult, error)
	ApplyReservationResult(int, moneyport.SettlementResult)
}

func (s *BillingSession) reserveDurableLocked(target int, initial bool) error {
	funding, ok := s.funding.(transactionalReservationFunding)
	if !ok {
		return errors.New("durable funding source cannot reserve atomically")
	}
	s.captureBillingIdentity()
	if s.billingIdentity == nil {
		return errors.New("missing billing reservation identity")
	}
	var previous *model.BillingFinalization
	if !initial {
		r, err := s.terminalReceiptLocked()
		if err != nil {
			return err
		}
		previous = &r
	}
	var result moneyport.SettlementResult
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	stored, attempt, err := model.ReserveBillingFinalization(ctx, *s.billingIdentity, previous, target, func(tx *gorm.DB, receipt *model.BillingFinalization) error {
		var sourceErr error
		if initial {
			result, sourceErr = funding.PreConsumeInTx(tx, target)
		} else {
			result, sourceErr = funding.ReserveInTx(tx, target)
		}
		if sourceErr != nil {
			return sourceErr
		}
		if result.Replayed {
			return errors.New("source reservation exists without its atomic held receipt")
		}
		receipt.SourceHeld = result.SourceHeld
		receipt.Paid, receipt.Bonus, receipt.Reward = 0, 0, 0
		if receipt.Source == BillingSourceWallet {
			if !result.ReceiptKnown {
				return errors.New("wallet reservation has no authoritative receipt")
			}
			for _, share := range result.OutstandingShares {
				switch model.LedgerAccount(share.Account) {
				case model.AccountPaid:
					receipt.Paid += share.Amount
				case model.AccountBonus:
					receipt.Bonus += share.Amount
				case model.AccountReward:
					receipt.Reward += share.Amount
				default:
					return errors.New("unknown wallet reservation account")
				}
			}
		} else {
			receipt.SubscriptionID, receipt.SubscriptionPeriod = result.SourceID, result.SourcePeriod
		}
		tokenDelta := target
		if previous != nil {
			tokenDelta -= previous.PreConsumed
		}
		if err := model.ReserveBillingTokenTx(tx, receipt, tokenDelta, s.relayInfo.TokenUnlimited); err != nil {
			return err
		}
		if receipt.TokenID > 0 {
			receipt.TokenHeld += tokenDelta
		}
		return nil
	}, func(tx *gorm.DB, r model.BillingFinalization) error {
		var readErr error
		result, readErr = s.reservationResultTx(tx, r)
		return readErr
	})
	cancel()
	model.RefreshUserQuotaCacheAfterBillingFinalization(s.relayInfo.UserId)
	if err != nil {
		s.reservationUncertain = true
		if initial && attempt.FreshInitialPrepared {
			// No caller has received this new session, hence this attempt cannot
			// have dispatched upstream. Do not use this authority for a replayed
			// held owner or a later reserve on an already live session.
			s.refundFailedInitialReservation(stored)
		}
		return err // Preserve the original failure; never dispatch after recovery.
	}
	funding.ApplyReservationResult(stored.PreConsumed, result)
	s.applyHeldReceipt(stored)
	return nil
}

func (s *BillingSession) reservationResultTx(tx *gorm.DB, r model.BillingFinalization) (moneyport.SettlementResult, error) {
	result := moneyport.SettlementResult{ReceiptKnown: true, SourceHeld: r.SourceHeld, SourceID: r.SubscriptionID, SourcePeriod: r.SubscriptionPeriod}
	for _, share := range r.Shares() {
		result.OutstandingShares = append(result.OutstandingShares, moneyport.AccountShare{Account: string(share.Account), Amount: share.Amount})
	}
	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		identity, err := model.GetSubscriptionBillingIdentityTx(tx, r.RequestID, r.UserID, r.SubscriptionID, r.SubscriptionPeriod, int64(r.SourceHeld))
		if err != nil {
			return result, err
		}
		if identity.PreConsumed != sub.amount {
			return result, errors.New("subscription reservation replay original amount mismatch")
		}
		result.OriginalAmount = int(identity.PreConsumed)
		// A new period is not an old-request preconsume snapshot. Preserve the
		// frozen monetary authorization but explicitly suppress advisory balance
		// math/notifications when its period has already rolled over.
		var allowance model.UserSubscription
		if err := tx.Where("id = ? AND user_id = ?", r.SubscriptionID, r.UserID).First(&allowance).Error; err != nil {
			return result, err
		}
		result.SnapshotUnknown = allowance.QuotaPeriod != r.SubscriptionPeriod
		if !result.SnapshotUnknown {
			result.AmountTotal, result.AmountUsedAfter = allowance.AmountTotal, allowance.AmountUsed
		}
		result.SourcePlanID = allowance.PlanId
		var plan model.SubscriptionPlan
		if err := tx.Select("id", "title").First(&plan, allowance.PlanId).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return result, err
		}
		result.SourcePlanTitle = plan.Title
	}
	return result, nil
}

func (s *BillingSession) applyHeldReceipt(r model.BillingFinalization) {
	s.terminalReceipt = &r
	s.preConsumedQuota = r.PreConsumed
	s.tokenConsumed = r.TokenHeld
	if s.relayInfo.IsPlayground {
		s.tokenConsumed = r.PreConsumed
	}
	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		s.extraReserved = sub.reserveDelta
	}
	s.reservationUncertain = false
	s.syncRelayInfo()
}

func (s *BillingSession) resolveReservationLocked() error {
	if s.billingIdentity == nil {
		return errors.New("missing uncertain reservation authorization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result moneyport.SettlementResult
	r, err := model.ReadBillingReservation(ctx, *s.billingIdentity, func(tx *gorm.DB, r model.BillingFinalization) error {
		var readErr error
		result, readErr = s.reservationResultTx(tx, r)
		return readErr
	})
	if err != nil {
		return err
	}
	if r.State != model.BillingHeld {
		return model.ErrBillingFinalized
	}
	funding, ok := s.funding.(transactionalReservationFunding)
	if !ok {
		return errors.New("unsupported reservation recovery")
	}
	funding.ApplyReservationResult(r.PreConsumed, result)
	s.applyHeldReceipt(*r)
	return nil
}

// refundFailedInitialReservation crosses the unique-owner barrier directly.
// A separate SELECT could miss an in-flight INSERT; RequestBillingRefund instead
// waits for its outcome, checks the exact receipt/source and atomically admits
// the existing queue. No balance, replacement token or source-only row is used
// to infer a refund. This also closes zero-held trusted reservations.
func (s *BillingSession) refundFailedInitialReservation(receipt model.BillingFinalization) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	owner, err := model.RequestBillingRefund(ctx, receipt)
	cancel()
	if err == nil {
		if owner.State == model.BillingRefundReady {
			gopool.Go(func() { executeBillingRefund(context.Background(), receipt.RequestID) })
		}
		return
	}
	code, _ := model.BillingRefundErrorCode(err)
	// owner_missing proves the initial transaction rolled back, through the
	// unique-key barrier's private claim which was itself rolled back.
	if code == "owner_missing" {
		return
	}
	logInitialReservationRefundFailure(receipt.RequestID, "refund_admission", err)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if recordErr := model.RecordInitialReservationRefundFailure(ctx, receipt); recordErr != nil {
		logInitialReservationRefundFailure(receipt.RequestID, "review_metadata", recordErr)
	}
}

func logInitialReservationRefundFailure(requestID, stage string, failure error) {
	// Preserve the frozen server-generated identity or explicitly report its
	// absence; never export arbitrary internal text, credentials or raw errors.
	if len(requestID) > 64 {
		requestID = ""
	}
	for _, char := range requestID {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_') {
			requestID = ""
			break
		}
	}
	code, class := model.BillingRefundErrorCode(failure)
	logger.LogError(context.Background(), fmt.Sprintf("event=billing_initial_reservation_refund_unresolved priority=high request_id=%q request_id_unavailable=%t stage=%s code=%s class=%s action=manual_reconciliation", requestID, requestID == "", stage, code, class))
}
