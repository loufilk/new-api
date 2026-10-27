package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

func (s *BillingSession) durableTerminalSource() bool {
	capability, ok := s.funding.(moneyport.HostBillingFinalizationSource)
	return ok && capability.UsesHostBillingFinalization() && (s.funding.Source() == BillingSourceWallet || s.funding.Source() == BillingSourceSubscription)
}

// Authorization identity is captured before preconsume. The lazy initialization
// supports internal, manually constructed sessions; it never reads a replacement
// credential from the database. RelayInfo is not a source of later mutations.
func (s *BillingSession) captureBillingIdentity() {
	if s.billingIdentity != nil || s.relayInfo == nil {
		return
	}
	info := s.relayInfo
	r := model.BillingFinalization{RequestID: info.RequestId, UserID: info.UserId, Source: s.funding.Source()}
	if !info.IsPlayground && info.TokenId > 0 {
		r.TokenID = info.TokenId
		r.TokenFingerprint = model.BillingTokenFingerprint(info.TokenKey)
	}
	s.billingIdentity = &r
}

func (s *BillingSession) terminalReceiptLocked() (model.BillingFinalization, error) {
	if s.terminalReceipt != nil {
		return *s.terminalReceipt, nil
	}
	s.captureBillingIdentity()
	if s.billingIdentity == nil {
		return model.BillingFinalization{}, errors.New("missing billing authorization identity")
	}
	r := *s.billingIdentity
	if s.relayInfo.RequestId != r.RequestID || s.relayInfo.UserId != r.UserID || s.funding.Source() != r.Source {
		return r, errors.New("billing session identity changed")
	}
	r.PreConsumed = s.preConsumedQuota
	if r.TokenID > 0 {
		r.TokenHeld = s.tokenConsumed
	}
	if r.Source == BillingSourceWallet {
		for _, share := range s.Receipt() {
			if share.Amount < 0 {
				return r, errors.New("invalid wallet held receipt")
			}
			switch model.LedgerAccount(share.Account) {
			case model.AccountPaid:
				r.Paid += share.Amount
			case model.AccountBonus:
				r.Bonus += share.Amount
			case model.AccountReward:
				r.Reward += share.Amount
			default:
				return r, errors.New("unknown wallet held account")
			}
		}
		r.SourceHeld = r.Paid + r.Bonus + r.Reward
	} else if sub, ok := s.funding.(*SubscriptionFunding); ok {
		identity := sub.BillingIdentity()
		r.SubscriptionID, r.SubscriptionPeriod = identity.SubscriptionID, identity.QuotaPeriod
		r.SourceHeld = int(sub.preConsumed) + sub.reserveDelta
	} else {
		return r, errors.New("unsupported durable billing funding")
	}
	s.terminalReceipt = &r
	return r, nil
}

func (s *BillingSession) settleDurableLocked(actual int) error {
	if s.refunded || s.taskOwned {
		return model.ErrBillingFinalized
	}
	if actual < 0 || actual > common.MaxQuota {
		return errors.New("invalid billing actual quota")
	}
	if s.settlementIntent != nil && *s.settlementIntent != actual {
		return errors.New("billing settlement replay amount mismatch")
	}
	// Freeze locally before any SQL. Even if the durable intent write fails or
	// its COMMIT acknowledgement is lost, a later failure defer cannot refund
	// already observed work. This flag alone is NOT durable recovery evidence.
	s.settlementIntent = &actual
	receipt, err := s.terminalReceiptLocked()
	if err != nil {
		return err
	}
	funding, ok := s.funding.(transactionalSettlementFunding)
	if !ok {
		return fmt.Errorf("funding source %T does not support transactional settlement", s.funding)
	}
	delta := actual - s.preConsumedQuota
	var result moneyport.SettlementResult
	// Settlement survives a disconnected client, but its SQL waits and precommit
	// Redis fence share one finite cooperative budget while the session is locked.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	apply := func(tx *gorm.DB) error {
		// Wallet delta zero has no money operation.
		if delta != 0 || receipt.Source == BillingSourceSubscription {
			var err error
			result, err = funding.SettleInTx(tx, delta)
			if err != nil {
				return err
			}
			if result.Replayed {
				return errors.New("source settlement exists without atomic terminal owner")
			}
		}
		if err := model.ApplyBillingTokenDeltaTx(tx, &receipt, -delta); err != nil {
			return err
		}
		if s.realtime != nil && s.realtime.observed {
			count := 0
			if s.realtime.usage.TotalTokens > 0 {
				count = 1
			}
			return model.ApplyTaskUsageDeltaTx(tx, receipt.UserID, s.realtime.channelID, actual, count)
		}
		return nil
	}
	var applied bool
	for attempt := 0; attempt < 3; attempt++ {
		result = moneyport.SettlementResult{}
		applied, err = model.SettleBillingFinalization(ctx, receipt, actual, apply)
		if err == nil {
			break
		}
		// Record each failed attempt independently of its expired/rolled-back
		// transaction. A successful but unacknowledged COMMIT remains settled.
		recordParent := ctx
		if ctx.Err() != nil {
			// At most one final one-second evidence tail after the main budget.
			recordParent = context.Background()
		}
		recordCtx, stop := context.WithTimeout(recordParent, time.Second)
		recordErr := model.RecordBillingSettlementFailure(recordCtx, receipt, actual, err)
		stop()
		if recordErr != nil {
			code, class := model.BillingSettlementFailureCode(err)
			common.SysError(fmt.Sprintf("event=billing_settlement_evidence_unconfirmed request_id=%q actual_quota=%d source_held=%d code=%s class=%s action=manual_reconciliation", receipt.RequestID, actual, receipt.SourceHeld, code, class))
		}
		if attempt == 2 || ctx.Err() != nil || !model.IsTransientBillingSettlementError(err) {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
		}
	}
	if receipt.Source == BillingSourceWallet && (err != nil || !applied || delta == 0) {
		// Cache cleanup has its own bounded budget, including after a lost COMMIT
		// acknowledgement; it cannot classify the financial result as a rollback.
		// Applied nonzero wallet deltas instead clean up in ApplySettlementResult.
		model.RefreshUserQuotaCacheAfterBillingFinalization(receipt.UserID)
	}
	if err != nil {
		// A deadline (like a lost COMMIT ACK) is not proof of rollback. Preserve
		// the frozen receipt for the existing terminal-owner reconciliation.
		s.settlementUncertain = true
		return err
	}
	s.settlementUncertain = false
	if s.realtime != nil {
		s.realtime.displayEligible = applied
	}
	if applied && (delta != 0 || receipt.Source == BillingSourceSubscription) {
		funding.ApplySettlementResult(delta, result)
	}
	if applied && receipt.Source == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	s.settled, s.fundingSettled = true, true
	s.actualSettled = actual
	return nil
}

func (s *BillingSession) resolveSettlementBeforeReserveLocked() error {
	if s.terminalReceipt == nil {
		return errors.New("missing uncertain settlement receipt")
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stored, err := model.ProbeBillingFinalization(probeCtx, *s.terminalReceipt)
	if err != nil {
		return err
	}
	s.settlementUncertain = false
	if stored == nil {
		s.terminalReceipt = nil
		return nil
	}
	switch stored.State {
	case model.BillingHeld:
		s.terminalReceipt = stored
		return nil
	case model.BillingSettled:
		s.settled = true
		s.fundingSettled = true
		s.actualSettled = stored.ActualQuota
	case model.BillingTaskOwned:
		s.taskOwned = true
	case model.BillingRefunded:
		s.refunded = true
	case model.BillingRefundPending, model.BillingRefundReady, model.BillingRefundReview, model.BillingDeliveryUnknown, model.BillingSettlementReview:
	default:
		return errors.New("unknown billing terminal owner")
	}
	return model.ErrBillingFinalized
}

// refundDurableLocked acquires the durable terminal owner synchronously. The
// worker never has to infer rollback from an error or trust session flags.
func (s *BillingSession) refundDurableLocked() {
	receipt, err := s.terminalReceiptLocked()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var owner *model.BillingFinalization
		owner, err = model.RequestBillingRefund(ctx, receipt)
		cancel()
		if err == nil {
			s.settlementUncertain = false
			switch owner.State {
			case model.BillingSettled:
				s.settled, s.fundingSettled, s.actualSettled = true, true, owner.ActualQuota
				return
			case model.BillingTaskOwned:
				s.taskOwned = true
				return
			case model.BillingRefunded:
				s.refunded = true
				return
			case model.BillingRefundReady:
				// This flag means ownership is durable, not that SQL has refunded.
				s.refunded = true
				gopool.Go(func() { executeBillingRefund(context.Background(), receipt.RequestID) })
				return
			}
		}
	}
	// No money is changed on an unconfirmed admission. A committed v2 intent
	// remains recoverable by its exact owner key, even if this ACK was lost.
	s.settlementUncertain = true
	code, class := model.BillingRefundErrorCode(err)
	common.SysError(fmt.Sprintf("billing refund admission unresolved: request_id=%s code=%s class=%s", receipt.RequestID, code, class))
}
