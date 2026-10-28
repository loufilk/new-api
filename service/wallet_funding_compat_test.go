package service

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"gorm.io/gorm"
)

// WalletFunding is a test-only compatibility adapter for historical service
// tests. Production service code has no concrete wallet implementation after
// Phase 5. Normal operations delegate to the real wallet module.
type WalletFunding struct {
	userId     int
	requestId  string
	consumed   int
	debitOrder []model.LedgerAccount
	inner      *walletmodule.Funding
}

func (w *WalletFunding) ensure() *walletmodule.Funding {
	if w.inner != nil {
		return w.inner
	}
	accountOrder := make([]string, 0, len(w.debitOrder))
	for _, account := range w.debitOrder {
		accountOrder = append(accountOrder, string(account))
	}
	funding, err := walletmodule.NewFunding(moneyport.FactoryRequest{
		UserID: w.userId, RequestID: w.requestId, AccountOrder: accountOrder,
	})
	if err != nil {
		panic(err)
	}
	w.inner = funding
	return funding
}

func (w *WalletFunding) UsesHostBillingFinalization() bool { return true }

func (w *WalletFunding) Source() string { return moneyport.SourceWallet }

func (w *WalletFunding) PreConsume(amount int) error {
	err := w.ensure().PreConsume(amount)
	if err == nil {
		w.consumed = w.inner.ReservedQuota()
	}
	return err
}

func (w *WalletFunding) Settle(delta int) error {
	err := w.ensure().Settle(delta)
	if err == nil {
		w.consumed = w.inner.ReservedQuota()
	}
	return err
}

func (w *WalletFunding) Refund() error { return w.ensure().Refund() }

func (w *WalletFunding) Receipt() []moneyport.AccountShare {
	if w.inner == nil {
		return nil
	}
	return w.inner.Receipt()
}

func (w *WalletFunding) PreConsumeInTx(tx *gorm.DB, amount int) (moneyport.SettlementResult, error) {
	return w.ensure().PreConsumeInTx(tx, amount)
}

func (w *WalletFunding) ReserveInTx(tx *gorm.DB, target int) (moneyport.SettlementResult, error) {
	return w.ensure().ReserveInTx(tx, target)
}

func (w *WalletFunding) ApplyReservationResult(target int, result moneyport.SettlementResult) {
	w.ensure().ApplyReservationResult(target, result)
	w.consumed = w.inner.ReservedQuota()
}

func (w *WalletFunding) SettleInTx(tx *gorm.DB, delta int) (moneyport.SettlementResult, error) {
	return w.ensure().SettleInTx(tx, delta)
}

func (w *WalletFunding) ApplySettlementResult(delta int, result moneyport.SettlementResult) {
	w.ensure().ApplySettlementResult(delta, result)
	w.consumed = w.inner.ReservedQuota()
}

func (w *WalletFunding) Reserve(target int) error {
	if w.inner != nil || w.consumed == 0 {
		err := w.ensure().Reserve(target)
		if err == nil {
			w.consumed = w.inner.ReservedQuota()
		}
		return err
	}
	// Two tiered-billing tests intentionally start from an already-consumed
	// in-memory state without replaying the original pre-consume. Preserve that
	// fixture semantics without adding a production state-mutation API.
	if target <= w.consumed {
		return nil
	}
	delta := target - w.consumed
	entry := model.LedgerEntry{
		UserId: w.userId, Delta: delta, Reason: model.LedgerReasonBillingReserve,
		ReferenceType: "relay", ReferenceId: w.requestId, RequestId: w.requestId,
		IdempotencyKey: fmt.Sprintf("billing:reserve:%s:%d", w.requestId, target),
	}
	result, err := model.DebitQuota(entry, w.debitOrder...)
	if err != nil {
		if errors.Is(err, model.ErrLedgerInsufficientBalance) {
			bonusEligible := false
			for _, account := range w.debitOrder {
				bonusEligible = bonusEligible || account == model.AccountBonus
			}
			if !bonusEligible {
				return moneyport.ErrBonusFundingNotSupported
			}
			return moneyport.ErrWalletInsufficientBalance
		}
		return err
	}
	if result != nil {
		model.RefreshUserQuotaCache(w.userId, result.TotalAfter)
	}
	w.consumed = target
	return nil
}
