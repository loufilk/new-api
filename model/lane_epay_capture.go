package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const laneEpayCaptureWaiting = "captured_waiting_credit"

var errLaneEpayCaptureEvidence = errors.New("Epay capture or principal evidence is incomplete or inconsistent")

// Scope is a private, already-authorized store/order binding, not authority
// to activate a gateway. No ordinary provider or legacy admin constructs it.
// This version deliberately requires the canonical wallet and log DB to be
// the very same configured PostgreSQL handle and isolated schema.
type laneEpaySettlementScope struct {
	db          *gorm.DB
	schema      string
	userID      int
	walletEpoch int64
	tradeNo     string
}

// The trusted callback verifier has already verified the Epay signature,
// merchant and terminal provider status. The order lock below must still
// verify all frozen quote facts. Reconciliation never accepts caller facts.
type laneVerifiedEpayCapture struct {
	tradeNo, method, money string
	facts                  VerifiedPaymentFacts
}

type laneEpaySettlementResult struct {
	state     string
	replayed  bool
	receiptID int64
}

func parkOrCreditVerifiedLaneEpay(ctx context.Context, scope laneEpaySettlementScope, capture laneVerifiedEpayCapture) (laneEpaySettlementResult, error) {
	return settleLaneEpay(ctx, scope, &capture)
}

func reconcileLaneEpayCapture(ctx context.Context, scope laneEpaySettlementScope) (laneEpaySettlementResult, error) {
	return settleLaneEpay(ctx, scope, nil)
}

func settleLaneEpay(ctx context.Context, scope laneEpaySettlementScope, received *laneVerifiedEpayCapture) (laneEpaySettlementResult, error) {
	return settleLaneEpayWithAuthority(ctx, scope, nil, received)
}

func validLaneEpaySettlementScope(ctx context.Context, scope laneEpaySettlementScope) bool {
	return ctx != nil && scope.db != nil && scope.db == DB && scope.db == LOG_DB &&
		laneStorePGHandleOK(scope.db) && common.UsingLogDatabase(common.DatabaseTypePostgreSQL) &&
		scope.userID > 0 && scope.walletEpoch > 0 && len(scope.tradeNo) <= 128 &&
		strings.TrimSpace(scope.tradeNo) == scope.tradeNo && scope.tradeNo != ""
}

// A live-capable binding has no ordinary issuer yet. Internal fixture
// callsites preserve their existing constraint; the opaque new callback and
// recovery adapter always supplies and checks the same-transaction authority.
func settleLaneEpayWithAuthority(ctx context.Context, scope laneEpaySettlementScope, bound *BoundLaneEpayOrder, received *laneVerifiedEpayCapture) (laneEpaySettlementResult, error) {
	var empty laneEpaySettlementResult
	if !validLaneEpaySettlementScope(ctx, scope) || (received != nil && scope.tradeNo != received.tradeNo) {
		return empty, ErrSharedWalletScope
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	var outcome laneEpaySettlementResult
	prepared := false
	err := scope.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if bound != nil {
			if err := bound.authority.CheckTx(tx, scope.db, LOG_DB, scope.walletEpoch); err != nil {
				return err
			}
		}
		var settleErr error
		outcome, settleErr = settleLaneEpayCaptureTx(tx, scope, received, bound)
		prepared = settleErr == nil
		return settleErr
	})
	if prepared && outcome.state == common.TopUpStatusSuccess && !outcome.replayed {
		// The mirror might have changed even on an uncertain COMMIT. Never
		// let a cache result reopen, acknowledge or undo payment settlement.
		RefreshUserQuotaCacheAfterBillingFinalization(scope.userID)
	}
	if err != nil {
		if prepared {
			return empty, ErrLaneStoreCommitUncertain
		}
		return empty, err
	}
	return outcome, nil
}

func settleLaneEpayCaptureTx(tx *gorm.DB, scope laneEpaySettlementScope, received *laneVerifiedEpayCapture, bound *BoundLaneEpayOrder) (laneEpaySettlementResult, error) {
	var empty laneEpaySettlementResult
	walletScope := laneWalletCustodyScope{tx: tx, schema: scope.schema, userID: scope.userID, walletEpoch: scope.walletEpoch}
	t, err := walletScope.lockableTx(tx, scope.userID)
	if err != nil {
		return empty, err
	}
	order, err := loadLaneEpayOrderTx(t, scope, true)
	if err != nil {
		return empty, err
	}
	pending := common.TopUpStatusPending
	if bound != nil {
		if err := verifyLaneEpayOrderBindingTx(t, scope, order, bound); err != nil {
			return empty, err
		}
		pending = laneEpayPendingCapture
	}
	if received != nil {
		if received.method != order.PaymentMethod {
			return empty, ErrPaymentMethodMismatch
		}
		amount, err := decimal.NewFromString(strings.TrimSpace(received.money))
		if err != nil || !amount.Equal(amount.Round(2)) || !amount.Shift(2).Equal(decimal.NewFromInt(order.QuotedPaymentMinor)) {
			return empty, ErrPaymentAmountMismatch
		}
		if err := validateLaneEpayQuoteFacts(&order, received.facts); err != nil {
			return empty, err
		}
	}
	entry := LedgerEntry{UserId: order.UserId, Account: AccountPaid, Delta: order.QuotedWalletQuota, Reason: LedgerReasonTopup,
		ReferenceType: ReferralReferenceTopUp, ReferenceId: order.TradeNo}
	switch order.Status {
	case common.TopUpStatusSuccess:
		facts, err := readLaneEpayReceiptTx(t, scope, &order)
		if err != nil {
			return empty, err
		}
		if received != nil && received.facts != facts {
			return empty, ErrPaymentReceiptConflict
		}
		if order.CompleteTime <= 0 {
			return empty, errLaneEpayCaptureEvidence
		}
		if err := verifyLaneEpayOutputsTx(t, &order, entry, true); err != nil {
			return empty, err
		}
		return laneEpaySettlementResult{state: order.Status, replayed: true, receiptID: order.PaymentReceiptID}, nil
	case pending, laneEpayCaptureWaiting:
		if order.CompleteTime != 0 {
			return empty, errLaneEpayCaptureEvidence
		}
	default:
		return empty, ErrTopUpStatusInvalid
	}
	statusBefore := order.Status
	if statusBefore == pending {
		if received == nil {
			return empty, ErrTopUpStatusInvalid
		}
		if !laneEpayHasNoPaidFacts(order) {
			return empty, errLaneEpayCaptureEvidence
		}
		if err := claimLaneEpayReceiptTx(t, scope, &order, received.facts); err != nil {
			return empty, err
		}
	}
	facts, err := readLaneEpayReceiptTx(t, scope, &order)
	if err != nil {
		return empty, err
	}
	if received != nil && received.facts != facts {
		return empty, ErrPaymentReceiptConflict
	}
	var user User
	if err := lockForUpdate(t).First(&user, order.UserId).Error; err != nil {
		return empty, err
	}
	if err := verifyLaneEpayOutputsTx(t, &order, entry, false); err != nil {
		return empty, err
	}
	escrow, err := readLockedLaneWalletCustodyTx(t, &user, scope.walletEpoch)
	if err != nil {
		// Missing/malformed custody and SQL failures are not a provable
		// capacity shortage; never acknowledge a new capture on this path.
		return empty, err
	}
	if entry.Delta > escrow.headroom {
		if statusBefore != laneEpayCaptureWaiting {
			order.Status = laneEpayCaptureWaiting
			if err := writeLaneEpayOrderTx(t, &order, statusBefore); err != nil {
				return empty, err
			}
		}
		return laneEpaySettlementResult{state: laneEpayCaptureWaiting, replayed: statusBefore == laneEpayCaptureWaiting, receiptID: order.PaymentReceiptID}, nil
	}

	order.CompleteTime, order.Status = common.GetTimestamp(), common.TopUpStatusSuccess
	if err := writeLaneEpayOrderTx(t, &order, statusBefore); err != nil {
		return empty, err
	}
	key, err := entry.resolveKey(ledgerOpCredit)
	if err != nil {
		return empty, err
	}
	// This branch already owns the user/slots and has proved sufficient
	// headroom. Any later error still rolls back, not a parking fallback.
	approval := &laneNetCreditApproval{scope: laneWalletCustodyScope{t, scope.schema, scope.userID, scope.walletEpoch},
		ledgerKey: key, signature: ledgerOperationSignature(entry, ledgerOpCredit)}
	credit, err := creditTopUpQuotaWithHeadroom(t, entry, approval)
	if err != nil {
		return empty, err
	}
	if credit == nil || credit.Replayed {
		return empty, errLaneEpayCaptureEvidence
	}
	other := NewLogOther()
	other.MergeAdmin(map[string]any{"payment_method": order.PaymentMethod, "callback_payment_method": PaymentProviderEpay})
	log := Log{UserId: order.UserId, Username: user.Username, Type: LogTypeTopup, CreatedAt: order.CompleteTime, Quota: entry.Delta,
		RequestId: laneEpayLogKey(order.Id), Content: other.setContent([]*common.Message{
			common.NewMessage("Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}",
				map[string]any{"quota": logger.FormatQuota(entry.Delta), "amount": decimal.NewFromInt(facts.PaidMinor).Shift(-2).StringFixed(2)}),
		}), Other: other.JSONString()}
	if err := t.Create(&log).Error; err != nil {
		return empty, err
	}
	if err := verifyLaneEpayOutputsTx(t, &order, entry, true); err != nil {
		return empty, err
	}
	return laneEpaySettlementResult{state: order.Status, receiptID: order.PaymentReceiptID}, nil
}

// The same frozen original-order contract applies to mutation and observation.
func loadLaneEpayOrderTx(tx *gorm.DB, scope laneEpaySettlementScope, lock bool) (TopUp, error) {
	var order TopUp
	if lock {
		tx = lockForUpdate(tx)
	}
	if err := tx.Where("trade_no = ?", scope.tradeNo).Take(&order).Error; err != nil {
		return TopUp{}, err
	}
	if order.Id <= 0 || !laneEpayQuoteMatchesScope(scope, order) {
		return TopUp{}, errLaneEpayCaptureEvidence
	}
	return order, nil
}

func laneEpayQuoteMatchesScope(scope laneEpaySettlementScope, order TopUp) bool {
	return order.UserId == scope.userID && order.TradeNo == scope.tradeNo &&
		order.PaymentProvider == PaymentProviderEpay && order.PaymentMethod != "" &&
		!order.NewUserBenefit && !order.AgentSimpleTopUp && order.Amount > 0 && order.QuotedWalletQuota > 0 &&
		order.QuotedWalletQuota <= common.MaxWalletQuota && order.QuotedPaymentMinor > 0 && order.QuotedPaymentCurrency == "CNY" &&
		order.QuotedPaymentScope != "" && (order.QuotedPaymentEnvironment == "test" || order.QuotedPaymentEnvironment == "live")
}

func validateLaneEpayQuoteFacts(order *TopUp, facts VerifiedPaymentFacts) error {
	if facts.Provider != PaymentProviderEpay || facts.ExternalReference == "" || facts.CurrencyExponent != 2 ||
		facts.CheckoutSubtotalMinor != 0 || facts.CheckoutDiscountMinor != 0 {
		return ErrPaymentReceiptConflict
	}
	return order.validateQuotedPayment([]VerifiedPaymentFacts{facts})
}

// The existing immutable PaymentReceipt's evidence hash binds the quote,
// exact wallet epoch, and this narrow state-machine version across parking
// and reconciliation. It is consistency evidence, not a database-writer MAC.
func laneEpayEvidenceHash(scope laneEpaySettlementScope, order *TopUp, facts VerifiedPaymentFacts) (string, error) {
	raw, err := common.Marshal([]any{"lane-epay-capture-v1", scope.schema, scope.userID, scope.walletEpoch,
		order.Id, order.TradeNo, order.PaymentMethod, order.QuotedWalletQuota, order.QuotedPaymentMinor,
		order.QuotedPaymentCurrency, order.QuotedPaymentScope, order.QuotedPaymentEnvironment, facts})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func claimLaneEpayReceiptTx(tx *gorm.DB, scope laneEpaySettlementScope, order *TopUp, facts VerifiedPaymentFacts) error {
	// A receipt-only partial restore cannot bootstrap a pristine pending
	// order. Canonical parking always writes the receipt and link together.
	var existing []PaymentReceipt
	if err := tx.Select("id").Where("purpose = ? AND local_order_key = ?", "topup", order.TradeNo).
		Limit(1).Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) != 0 {
		return errLaneEpayCaptureEvidence
	}
	hash, err := laneEpayEvidenceHash(scope, order, facts)
	if err != nil {
		return err
	}
	now := common.GetTimestamp()
	receipt, err := ClaimPaymentReceiptTx(tx, &PaymentReceipt{Provider: facts.Provider, ProviderReference: facts.ExternalReference,
		MerchantScope: facts.MerchantScope, Environment: facts.Environment, Purpose: "topup", LocalOrderKey: order.TradeNo,
		OwnerUserID: order.UserId, Currency: facts.Currency, CurrencyExponent: facts.CurrencyExponent, PaidMinor: facts.PaidMinor,
		PaidAt: now, VerifiedAt: now, EvidenceHash: hash})
	if err != nil {
		return err
	}
	if receipt == nil || receipt.ID <= 0 || receipt.EvidenceHash != hash {
		return errLaneEpayCaptureEvidence
	}
	order.PaymentReceiptID = receipt.ID
	order.PaidProviderReference, order.PaidMerchantScope, order.PaidEnvironment = facts.ExternalReference, facts.MerchantScope, facts.Environment
	order.PaidCurrency, order.PaidCurrencyExponent, order.PaidAmountMinor = facts.Currency, facts.CurrencyExponent, facts.PaidMinor
	return nil
}

func readLaneEpayReceiptTx(tx *gorm.DB, scope laneEpaySettlementScope, order *TopUp) (VerifiedPaymentFacts, error) {
	facts, _, err := inspectLaneEpayReceiptTx(tx, scope, order)
	return facts, err
}

func inspectLaneEpayReceiptTx(tx *gorm.DB, scope laneEpaySettlementScope, order *TopUp) (VerifiedPaymentFacts, int64, error) {
	var empty VerifiedPaymentFacts
	if order.PaymentReceiptID <= 0 {
		return empty, 0, errLaneEpayCaptureEvidence
	}
	var receipt PaymentReceipt
	if err := tx.First(&receipt, order.PaymentReceiptID).Error; err != nil {
		return empty, 0, err
	}
	facts := order.verifiedAgentPaymentFacts()
	hash, err := laneEpayEvidenceHash(scope, order, facts)
	if err != nil {
		return empty, 0, err
	}
	identity := sha256.Sum256([]byte(facts.Provider + "\x00" + facts.ExternalReference))
	if receipt.ReceiptKey != hex.EncodeToString(identity[:]) || receipt.EvidenceHash != hash || ValidatePaymentReceipt(&receipt) != nil || receipt.Purpose != "topup" || receipt.LocalOrderKey != order.TradeNo ||
		receipt.OwnerUserID != order.UserId || receipt.Provider != facts.Provider || receipt.ProviderReference != facts.ExternalReference ||
		receipt.MerchantScope != facts.MerchantScope || receipt.Environment != facts.Environment ||
		receipt.Currency != facts.Currency || receipt.CurrencyExponent != facts.CurrencyExponent || receipt.PaidMinor != facts.PaidMinor ||
		validateLaneEpayQuoteFacts(order, facts) != nil {
		return empty, 0, errLaneEpayCaptureEvidence
	}
	return facts, receipt.PaidAt, nil
}

func writeLaneEpayOrderTx(tx *gorm.DB, order *TopUp, expected string) error {
	result := tx.Model(&TopUp{}).Where("id = ? AND user_id = ? AND status = ?", order.Id, order.UserId, expected).
		Select("status", "complete_time", "payment_receipt_id", "paid_provider_reference", "paid_merchant_scope",
			"paid_environment", "paid_currency", "paid_currency_exponent", "paid_amount_minor").Updates(order)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errLaneEpayCaptureEvidence
	}
	return nil
}

func laneEpayLogKey(orderID int) string { return fmt.Sprintf("lane-epay-%d", orderID) }

// Exact canonical outputs are checked on both first commit and replay. A
// co-restored header or a success flag alone must not acknowledge payment.
func verifyLaneEpayOutputsTx(tx *gorm.DB, order *TopUp, entry LedgerEntry, credited bool) error {
	key, err := entry.resolveKey(ledgerOpCredit)
	if err != nil {
		return err
	}
	op, rows, err := loadLedgerOperation(tx.Limit(2), key)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if credited {
		if err != nil || !verifyReplay(op, entry, ledgerOpCredit) || !validLaneNetCreditReplay(entry, op, rows) {
			return errLaneEpayCaptureEvidence
		}
	} else {
		if err == nil {
			return errLaneEpayCaptureEvidence
		}
		var orphan []BalanceLedger
		if err := tx.Select("id").Where("idempotency_key = ?", key).Limit(1).Find(&orphan).Error; err != nil {
			return err
		}
		if len(orphan) != 0 {
			return errLaneEpayCaptureEvidence
		}
	}
	commissionKey, err := agentCommissionHash([]string{"topup", order.TradeNo})
	if err != nil {
		return err
	}
	var events []AgentCommissionOutbox
	if err := tx.Where("event_key = ?", commissionKey).Limit(2).Find(&events).Error; err != nil {
		return err
	}
	var logs []Log
	if err := tx.Where("request_id = ?", laneEpayLogKey(order.Id)).Limit(2).Find(&logs).Error; err != nil {
		return err
	}
	if !credited {
		if len(events) != 0 || len(logs) != 0 {
			return errLaneEpayCaptureEvidence
		}
		return nil
	}
	if len(events) != 1 || len(logs) != 1 || events[0].SourceUserID != order.UserId ||
		events[0].ReferenceID != order.TradeNo || events[0].Kind != "payment" || events[0].Route != "referral" ||
		logs[0].UserId != order.UserId || logs[0].Type != LogTypeTopup || logs[0].Quota != entry.Delta || logs[0].CreatedAt != order.CompleteTime {
		return errLaneEpayCaptureEvidence
	}
	var snapshot agentCommissionPaymentSnapshot
	if common.UnmarshalJsonStr(events[0].Payload, &snapshot) != nil || snapshot.UserID != order.UserId ||
		snapshot.PaidQuota != entry.Delta || snapshot.PaidAt != order.CompleteTime || snapshot.Payment != order.verifiedAgentPaymentFacts() {
		return errLaneEpayCaptureEvidence
	}
	return nil
}
