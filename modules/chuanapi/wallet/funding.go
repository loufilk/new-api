package wallet

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"gorm.io/gorm"
)

// Funding is the ChuanAPI wallet implementation of the generic moneyport
// runtime contract. Billing orchestration owns token quota; this type owns only
// wallet ledger/account mutations and receipt state.
type Funding struct {
	userID      int
	requestID   string
	consumed    int
	debitOrder  []model.LedgerAccount
	debitShares map[model.LedgerAccount]int
}

var (
	_ moneyport.FundingSource     = (*Funding)(nil)
	_ moneyport.ReservableFunding = (*Funding)(nil)
	_ moneyport.FundingReceipt    = (*Funding)(nil)
)

var ErrMissingDebitReceipt = model.ErrMissingWalletDebitReceipt

func NewFunding(request moneyport.FactoryRequest) (*Funding, error) {
	order := make([]model.LedgerAccount, 0, len(request.AccountOrder))
	for _, raw := range request.AccountOrder {
		account := model.LedgerAccount(raw)
		switch account {
		case model.AccountBonus, model.AccountReward, model.AccountPaid:
			order = append(order, account)
		default:
			return nil, fmt.Errorf("chuanapi wallet: unsupported account %q", raw)
		}
	}
	return &Funding{userID: request.UserID, requestID: request.RequestID, debitOrder: order}, nil
}

func RegisterFundingFactory(reg *moneyport.FactoryRegistry) error {
	if reg == nil {
		return errors.New("chuanapi wallet: nil funding factory registry")
	}
	return reg.Register(moneyport.SourceWallet, moneyport.FundingFactoryFunc(func(request moneyport.FactoryRequest) (moneyport.FundingSource, error) {
		return NewFunding(request)
	}))
}

func (w *Funding) UsesHostBillingFinalization() bool { return true }

func (w *Funding) Source() string { return moneyport.SourceWallet }

func (w *Funding) PreConsume(amount int) error {
	if amount <= 0 {
		return nil
	}
	return w.commitReservation(amount, w.PreConsumeInTx)
}

// PreConsumeInTx participates in the host's reservation transaction. Receipt
// and cache state are applied only by ApplyReservationResult after COMMIT.
func (w *Funding) PreConsumeInTx(tx *gorm.DB, amount int) (moneyport.SettlementResult, error) {
	if tx == nil {
		return moneyport.SettlementResult{}, errors.New("wallet reservation transaction is nil")
	}
	if amount <= 0 {
		return moneyport.SettlementResult{ReceiptKnown: true}, nil
	}
	operationID := "billing:preconsume:" + w.requestID
	entry := model.LedgerEntry{
		UserId:         w.userID,
		Delta:          amount,
		Reason:         model.LedgerReasonBillingPreConsume,
		ReferenceType:  "relay",
		ReferenceId:    w.requestID,
		RequestId:      w.requestID,
		IdempotencyKey: operationID,
	}
	return w.debitInTx(tx, entry)
}

func (w *Funding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	var result moneyport.SettlementResult
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		result = moneyport.SettlementResult{}
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			var settleErr error
			result, settleErr = w.SettleInTx(tx, delta)
			return settleErr
		})
		if !errors.Is(err, model.ErrLedgerDuplicate) {
			break
		}
	}
	if err != nil {
		return w.mapInsufficient(err)
	}
	w.ApplySettlementResult(delta, result)
	return nil
}

// SettleInTx participates in the caller-owned transaction used to make wallet
// settlement and token quota atomic. It does not mutate post-commit cache or
// receipt state.
func (w *Funding) SettleInTx(tx *gorm.DB, delta int) (moneyport.SettlementResult, error) {
	if tx == nil {
		return moneyport.SettlementResult{}, errors.New("wallet settlement transaction is nil")
	}
	if delta == 0 {
		return moneyport.SettlementResult{}, nil
	}
	entry := model.LedgerEntry{
		UserId:        w.userID,
		Account:       model.AccountPaid,
		Reason:        model.LedgerReasonBillingSettle,
		ReferenceType: "relay",
		ReferenceId:   w.requestID,
		RequestId:     w.requestID,
	}
	if delta > 0 {
		entry.Delta = delta
		entry.IdempotencyKey = fmt.Sprintf("billing:settle:%s:%d", w.requestID, delta)
		result, err := model.DebitQuotaTx(tx, entry, w.debitOrder...)
		if err != nil {
			return moneyport.SettlementResult{}, w.mapInsufficient(err)
		}
		receipt, err := model.WalletRequestReceiptTx(tx, w.userID, w.requestID)
		return settlementResultFromLedger(result, receipt), err
	}

	refund := -delta
	if w.consumed > 0 && len(w.debitShares) == 0 {
		return moneyport.SettlementResult{}, ErrMissingDebitReceipt
	}
	entry.IdempotencyKey = fmt.Sprintf("billing:settle_refund:%s:%d", w.requestID, refund)
	result, receipt, err := model.CreditWalletRefundTx(tx, entry, refund)
	return settlementResultFromLedger(result, receipt), err
}

func (w *Funding) ApplySettlementResult(delta int, result moneyport.SettlementResult) {
	model.RefreshUserQuotaCacheAfterBillingFinalization(w.userID)
	if result.ReceiptKnown {
		w.replaceReceipt(result.OutstandingShares)
	}
}

func (w *Funding) Refund() error {
	if w.consumed <= 0 {
		return nil
	}
	if _, err := model.AllocateWalletRefund(w.debitSharesInOrder(), nil, w.consumed); err != nil {
		return ErrMissingDebitReceipt
	}
	entry := model.LedgerEntry{
		UserId:         w.userID,
		Account:        model.AccountPaid,
		Reason:         model.LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    w.requestID,
		RequestId:      w.requestID,
		IdempotencyKey: "billing:refund:" + w.requestID,
	}
	var result moneyport.SettlementResult
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			ledger, receipt, refundErr := model.CreditWalletRefundTx(tx, entry, 0)
			result = settlementResultFromLedger(ledger, receipt)
			return refundErr
		})
		if !errors.Is(err, model.ErrLedgerDuplicate) {
			break
		}
	}
	if err != nil {
		return err
	}
	w.ApplySettlementResult(-w.consumed, result)
	return nil
}

func (w *Funding) Reserve(targetQuota int) error {
	if targetQuota <= 0 {
		return nil
	}
	// Another reconstructed funding object may have changed the outstanding
	// amount. Only the locked durable receipt can establish a positive no-op.
	return w.commitReservation(targetQuota, w.ReserveInTx)
}

func (w *Funding) ReserveInTx(tx *gorm.DB, targetQuota int) (moneyport.SettlementResult, error) {
	if tx == nil {
		return moneyport.SettlementResult{}, errors.New("wallet reservation transaction is nil")
	}
	receipt, err := model.WalletRequestReceiptTx(tx, w.userID, w.requestID)
	if err != nil && err != model.ErrMissingWalletDebitReceipt {
		return moneyport.SettlementResult{}, err
	}
	current := 0
	for _, share := range receipt {
		current += share.Amount
	}
	if targetQuota <= current {
		return settlementResultFromLedger(&model.LedgerResult{Replayed: true}, receipt), nil
	}
	entry := model.LedgerEntry{
		UserId:         w.userID,
		Delta:          targetQuota - current,
		Reason:         model.LedgerReasonBillingReserve,
		ReferenceType:  "relay",
		ReferenceId:    w.requestID,
		RequestId:      w.requestID,
		IdempotencyKey: fmt.Sprintf("billing:reserve:%s:%d", w.requestID, targetQuota),
	}
	return w.debitInTx(tx, entry)
}

func (w *Funding) ApplyReservationResult(target int, result moneyport.SettlementResult) {
	w.ApplySettlementResult(0, result)
}

func (w *Funding) Receipt() []moneyport.AccountShare {
	shares := w.debitSharesInOrder()
	if len(shares) == 0 {
		return nil
	}
	out := make([]moneyport.AccountShare, 0, len(shares))
	for _, share := range shares {
		out = append(out, moneyport.AccountShare{Account: string(share.Account), Amount: share.Amount})
	}
	return out
}

// ReservedQuota is read-only observability for tests/admin diagnostics. It is
// not OutstandingReserveReporter: BillingSession intentionally derives wallet
// failure-refund need from tokenConsumed to avoid duplicate refunds.
func (w *Funding) ReservedQuota() int { return w.consumed }

func (w *Funding) mapInsufficient(err error) error {
	if !errors.Is(err, model.ErrLedgerInsufficientBalance) {
		return err
	}
	if !w.bonusEligible() {
		return moneyport.ErrBonusFundingNotSupported
	}
	return moneyport.ErrWalletInsufficientBalance
}

func (w *Funding) bonusEligible() bool {
	for _, account := range w.debitOrder {
		if account == model.AccountBonus {
			return true
		}
	}
	return false
}

// debitInTx keeps a debit and its authoritative net receipt together. In
// particular, reconstructing a preconsume after an earlier partial refund must
// not hydrate the old gross receipt and make it refundable again.
func (w *Funding) debitInTx(tx *gorm.DB, entry model.LedgerEntry) (moneyport.SettlementResult, error) {
	ledger, err := model.DebitQuotaTx(tx, entry, w.debitOrder...)
	if err != nil {
		return moneyport.SettlementResult{}, w.mapInsufficient(err)
	}
	receipt, err := model.WalletRequestReceiptTx(tx, w.userID, w.requestID)
	return settlementResultFromLedger(ledger, receipt), err
}

func (w *Funding) commitReservation(target int, apply func(*gorm.DB, int) (moneyport.SettlementResult, error)) error {
	var result moneyport.SettlementResult
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			var reservationErr error
			result, reservationErr = apply(tx, target)
			return reservationErr
		})
		if !errors.Is(err, model.ErrLedgerDuplicate) {
			break
		}
	}
	if err != nil {
		return w.mapInsufficient(err)
	}
	w.ApplyReservationResult(target, result)
	return nil
}

func (w *Funding) replaceReceipt(shares []moneyport.AccountShare) {
	w.debitShares = make(map[model.LedgerAccount]int, len(shares))
	w.consumed = 0
	for _, share := range shares {
		w.debitShares[model.LedgerAccount(share.Account)] = share.Amount
		w.consumed += share.Amount
	}
}

func (w *Funding) debitSharesInOrder() []model.LedgerShare {
	if len(w.debitShares) == 0 {
		return nil
	}
	out := make([]model.LedgerShare, 0, len(w.debitShares))
	seen := make(map[model.LedgerAccount]bool)
	order := append([]model.LedgerAccount(nil), w.debitOrder...)
	order = append(order, model.AccountBonus, model.AccountReward, model.AccountPaid)
	for _, account := range order {
		if amount := w.debitShares[account]; amount > 0 && !seen[account] {
			out = append(out, model.LedgerShare{Account: account, Amount: amount})
			seen[account] = true
		}
	}
	return out
}

func settlementResultFromLedger(result *model.LedgerResult, receipt []model.LedgerShare) moneyport.SettlementResult {
	if result == nil {
		return moneyport.SettlementResult{}
	}
	out := moneyport.SettlementResult{Replayed: result.Replayed, TotalAfter: result.TotalAfter, ReceiptKnown: true}
	for _, share := range receipt {
		out.SourceHeld += share.Amount
		out.OutstandingShares = append(out.OutstandingShares, moneyport.AccountShare{Account: string(share.Account), Amount: share.Amount})
	}
	for _, row := range result.Rows {
		if row.Delta < 0 {
			out.DebitShares = append(out.DebitShares, moneyport.AccountShare{Account: row.Account, Amount: -row.Delta})
		} else if row.Delta > 0 {
			out.CreditShares = append(out.CreditShares, moneyport.AccountShare{Account: row.Account, Amount: row.Delta})
		}
	}
	return out
}
