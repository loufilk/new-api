package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// AgentVerifiedPaymentFacts is populated only from a successfully verified
// provider callback. It is never bound directly from a public top-up request.
type AgentVerifiedPaymentFacts struct {
	Provider          string `json:"provider"`
	ExternalReference string `json:"external_reference"`
	MerchantScope     string `json:"merchant_scope"`
	Environment       string `json:"environment"`
	Currency          string `json:"currency"`
	CurrencyExponent  int    `json:"currency_exponent"`
	PaidMinor         int64  `json:"paid_minor,string"`
}

// NewAgentVerifiedPaymentFacts intentionally supports only currencies whose
// provider minor-unit conventions are pinned here. Unknown evidence continues
// through the legacy reward route; it is never guessed into cash entitlement.
func NewAgentVerifiedPaymentFacts(provider, reference, merchant, environment, currency, amount string, nativeMinorUnits bool) (AgentVerifiedPaymentFacts, bool) {
	f := AgentVerifiedPaymentFacts{Provider: provider, ExternalReference: strings.TrimSpace(reference), MerchantScope: strings.TrimSpace(merchant), Environment: environment, Currency: strings.ToUpper(strings.TrimSpace(currency))}
	var supported bool
	f.CurrencyExponent, supported = AgentCurrencyExponent(f.Currency)
	if !supported {
		return AgentVerifiedPaymentFacts{}, false
	}

	if f.ExternalReference == "" || f.MerchantScope == "" || (environment != "live" && environment != "test") {
		return AgentVerifiedPaymentFacts{}, false
	}
	if provider == PaymentProviderStripe && !strings.HasPrefix(f.ExternalReference, "pi_") {
		return AgentVerifiedPaymentFacts{}, false
	}
	value, err := decimal.NewFromString(strings.TrimSpace(amount))
	if err != nil || !value.IsPositive() {
		return AgentVerifiedPaymentFacts{}, false
	}
	if !nativeMinorUnits {
		value = value.Shift(int32(f.CurrencyExponent))
	}
	if !value.Equal(value.Truncate(0)) || value.GreaterThan(decimal.NewFromInt(MaxAgentMoneyMinor)) {
		return AgentVerifiedPaymentFacts{}, false
	}
	f.PaidMinor = value.IntPart()
	return f, true
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
	receipt, err := ClaimAgentPaymentReceiptTx(tx, &AgentPaymentReceipt{Provider: f.Provider, ProviderReference: f.ExternalReference, MerchantScope: f.MerchantScope, Environment: f.Environment, Purpose: "topup", LocalOrderKey: topup.TradeNo, OwnerUserID: topup.UserId, Currency: f.Currency, CurrencyExponent: f.CurrencyExponent, PaidMinor: f.PaidMinor, PaidAt: common.GetTimestamp(), VerifiedAt: common.GetTimestamp()})
	if err != nil {
		return err
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
