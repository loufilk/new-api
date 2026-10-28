package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"gorm.io/gorm"
)

// PersistTaskBilling is the durable submit barrier. An upstream acceptance is
// never followed by another submission merely because this COMMIT ACK is lost.
func PersistTaskBilling(info *relaycommon.RelayInfo, task *model.Task, omits ...string) error {
	if info == nil || task == nil {
		return errors.New("missing task billing handoff")
	}
	s, ok := info.Billing.(*BillingSession)
	if !ok {
		if info.Billing != nil {
			return errors.New("task funding does not support an atomic handoff")
		}
		if task.Quota != 0 {
			return errors.New("charged task has no billing session")
		}
		// Free submissions still use the unique request owner: an uncertain
		// insertion cannot duplicate the task or its request-count statistic.
		receipt := model.BillingFinalization{RequestID: info.RequestId, UserID: info.UserId, Source: BillingSourceWallet}
		if !info.IsPlayground {
			receipt.TokenID = info.TokenId
			if receipt.TokenID > 0 {
				receipt.TokenFingerprint = model.BillingTokenFingerprint(info.TokenKey)
			}
		}
		task.PrivateData.BillingSource = BillingSourceWallet
		task.PrivateData.TokenId = receipt.TokenID
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := model.CommitBillingTask(ctx, receipt, 0, task, omits, func(tx *gorm.DB, destination *model.Task) error {
			destination.PrivateData.WalletDebit = []model.LedgerShare{}
			return nil
		})
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.durableTerminalSource() {
		return errors.New("task funding does not support a durable terminal owner")
	}
	if s.reservationUncertain {
		if err := s.resolveReservationLocked(); err != nil {
			return err
		}
	}
	receipt, err := s.terminalReceiptLocked()
	if err != nil {
		return err
	}
	funding, ok := s.funding.(transactionalSettlementFunding)
	if !ok {
		return errors.New("task funding does not support transactional settlement")
	}
	actual, delta := task.Quota, task.Quota-s.preConsumedQuota
	var result moneyport.SettlementResult
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	applied, err := model.CommitBillingTask(ctx, receipt, actual, task, omits, func(tx *gorm.DB, destination *model.Task) error {
		if delta != 0 || receipt.Source == BillingSourceSubscription {
			var settleErr error
			result, settleErr = funding.SettleInTx(tx, delta)
			if settleErr != nil {
				return settleErr
			}
			if result.Replayed {
				return errors.New("source settlement exists without atomic task ownership")
			}
		}
		if err := model.ApplyBillingTokenDeltaTx(tx, &receipt, -delta); err != nil {
			return err
		}
		if receipt.Source == BillingSourceWallet && actual == 0 && receipt.SourceHeld == 0 {
			destination.PrivateData.WalletDebit = []model.LedgerShare{}
		} else if receipt.Source == BillingSourceWallet {
			shares, err := model.WalletRequestReceiptTx(tx, receipt.UserID, receipt.RequestID)
			if err != nil {
				return err
			}
			if err := model.ValidateTaskWalletReceipt(shares, actual); err != nil {
				return err
			}
			destination.PrivateData.WalletDebit = shares
		}
		return nil
	})
	model.RefreshUserQuotaCacheAfterBillingFinalization(receipt.UserID)
	if err != nil {
		s.settlementUncertain = true
		return err
	}
	if applied && (delta != 0 || receipt.Source == BillingSourceSubscription) {
		funding.ApplySettlementResult(delta, result)
	}
	s.taskOwned, s.fundingSettled, s.actualSettled = true, true, actual
	s.settlementUncertain = false
	if receipt.Source == BillingSourceSubscription {
		info.SubscriptionPostDelta += int64(delta)
	}
	return nil
}

// PreserveUnknownTaskDelivery blocks the request-level failure defer from
// refunding an upstream acceptance/ambiguous send. The held receipt remains
// inspectable; neither held nor delivery_unknown_v2 is a recovery refund queue.
func PreserveUnknownTaskDelivery(info *relaycommon.RelayInfo) {
	PreserveUnknownBillingDelivery(info)
}

// PreserveUnknownBillingDelivery retains the original held evidence whenever
// external delivery/measurement cannot safely justify settlement or refund.
func PreserveUnknownBillingDelivery(info *relaycommon.RelayInfo) {
	if info == nil {
		return
	}
	s, ok := info.Billing.(*BillingSession)
	if !ok {
		return
	}
	s.mu.Lock()
	s.taskOwned = true // prohibits an in-process fallback even when SQL is down
	// Capture only an already frozen identity, never a later RelayInfo value.
	requestID := ""
	if s.terminalReceipt != nil {
		requestID = s.terminalReceipt.RequestID
	} else if s.billingIdentity != nil {
		requestID = s.billingIdentity.RequestID
	}
	stage := "reservation_probe"
	var unresolved error
	defer func() {
		s.mu.Unlock()
		if unresolved == nil {
			return
		}
		// Request IDs are server-generated. Reject malformed internal identities
		// instead of exporting arbitrary text or a truncated, ambiguous key.
		if len(requestID) > 64 {
			requestID = ""
		}
		for _, c := range requestID {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
				requestID = ""
				break
			}
		}
		code, class := model.BillingRefundErrorCode(unresolved)
		// An error, including a lost COMMIT ACK, does not prove rollback. The
		// existing owner must be read before any independently authorized action.
		logger.LogError(context.Background(), fmt.Sprintf("event=billing_delivery_unknown_unresolved priority=high request_id=%q request_id_unavailable=%t stage=%s code=%s class=%s action=manual_reconciliation", requestID, requestID == "", stage, code, class))
	}()
	if s.reservationUncertain {
		if unresolved = s.resolveReservationLocked(); unresolved != nil {
			return
		}
	}
	stage = "receipt"
	var receipt model.BillingFinalization
	receipt, unresolved = s.terminalReceiptLocked()
	if unresolved != nil {
		return
	}
	stage = "mark_unknown"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unresolved = model.MarkBillingDeliveryUnknown(ctx, receipt)
}
