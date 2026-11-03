package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// AgentVerifiedPaymentFacts is populated only from a successfully verified
// provider callback. It is never bound directly from a public top-up request.
type AgentVerifiedPaymentFacts = VerifiedPaymentFacts

func NewAgentVerifiedPaymentFacts(provider, reference, merchant, environment, currency, amount string, nativeMinorUnits bool) (AgentVerifiedPaymentFacts, bool) {
	return NewVerifiedPaymentFacts(provider, reference, merchant, environment, currency, amount, nativeMinorUnits)
}

func (t *TopUp) verifiedAgentPaymentFacts() AgentVerifiedPaymentFacts {
	return AgentVerifiedPaymentFacts{Provider: t.PaymentProvider, ExternalReference: t.PaidProviderReference, MerchantScope: t.PaidMerchantScope, Environment: t.PaidEnvironment, Currency: t.PaidCurrency, CurrencyExponent: t.PaidCurrencyExponent, PaidMinor: t.PaidAmountMinor}
}

func applyAgentVerifiedPaymentTx(tx *gorm.DB, topup *TopUp, facts []AgentVerifiedPaymentFacts) error {
	if len(facts) == 0 {
		return nil
	}
	if len(facts) != 1 || facts[0].Provider != topup.PaymentProvider {
		return ErrAgentPaymentReceiptConflict
	}
	f := facts[0]
	purpose := "topup"
	if topup.AgentSimpleTopUp {
		purpose = "agent_recharge"
		if f.Currency != "CNY" || f.CurrencyExponent != 2 || !decimal.NewFromInt(f.PaidMinor).Equal(decimal.NewFromFloat(topup.Money).Round(2).Shift(2)) {
			return ErrAgentPaymentReceiptConflict
		}
	}
	receipt, err := ClaimAgentPaymentReceiptTx(tx, &AgentPaymentReceipt{Provider: f.Provider, ProviderReference: f.ExternalReference, MerchantScope: f.MerchantScope, Environment: f.Environment, Purpose: purpose, LocalOrderKey: topup.TradeNo, OwnerUserID: topup.UserId, Currency: f.Currency, CurrencyExponent: f.CurrencyExponent, PaidMinor: f.PaidMinor, PaidAt: common.GetTimestamp(), VerifiedAt: common.GetTimestamp()})
	if err != nil {
		return err
	}
	if topup.PaymentReceiptID != 0 && topup.PaymentReceiptID != receipt.ID {
		return ErrAgentPaymentReceiptConflict
	}
	topup.PaymentReceiptID = receipt.ID
	topup.PaidProviderReference = f.ExternalReference
	topup.PaidMerchantScope = f.MerchantScope
	topup.PaidEnvironment = f.Environment
	topup.PaidCurrency = f.Currency
	topup.PaidCurrencyExponent = f.CurrencyExponent
	topup.PaidAmountMinor = f.PaidMinor
	return nil
}
