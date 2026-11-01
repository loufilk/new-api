package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// VerifiedPaymentFacts is populated only from a successfully verified
// provider callback. It is never bound directly from a public top-up request.
type VerifiedPaymentFacts struct {
	Provider          string `json:"provider"`
	ExternalReference string `json:"external_reference"`
	MerchantScope     string `json:"merchant_scope"`
	Environment       string `json:"environment"`
	Currency          string `json:"currency"`
	CurrencyExponent  int    `json:"currency_exponent"`
	PaidMinor         int64  `json:"paid_minor,string"`
}

// NewVerifiedPaymentFacts intentionally supports only currencies whose
// provider minor-unit conventions are pinned here. Unknown monetary units are
// never guessed or used as payment receipt evidence.
func NewVerifiedPaymentFacts(provider, reference, merchant, environment, currency, amount string, nativeMinorUnits bool) (VerifiedPaymentFacts, bool) {
	f := VerifiedPaymentFacts{Provider: provider, ExternalReference: strings.TrimSpace(reference), MerchantScope: strings.TrimSpace(merchant), Environment: environment, Currency: strings.ToUpper(strings.TrimSpace(currency))}
	var supported bool
	f.CurrencyExponent, supported = PaymentCurrencyExponent(f.Currency)
	if !supported {
		return VerifiedPaymentFacts{}, false
	}

	if f.ExternalReference == "" || f.MerchantScope == "" || (environment != "live" && environment != "test") {
		return VerifiedPaymentFacts{}, false
	}
	if provider == PaymentProviderStripe && !strings.HasPrefix(f.ExternalReference, "pi_") {
		return VerifiedPaymentFacts{}, false
	}
	value, err := decimal.NewFromString(strings.TrimSpace(amount))
	if err != nil || !value.IsPositive() {
		return VerifiedPaymentFacts{}, false
	}
	if !nativeMinorUnits {
		value = value.Shift(int32(f.CurrencyExponent))
	}
	if !value.Equal(value.Truncate(0)) || value.GreaterThan(decimal.NewFromInt(int64(common.MaxWalletQuota))) {
		return VerifiedPaymentFacts{}, false
	}
	f.PaidMinor = value.IntPart()
	return f, true
}

func applyVerifiedPaymentTx(tx *gorm.DB, topup *TopUp, facts []VerifiedPaymentFacts) error {
	if len(facts) == 0 {
		return nil
	}
	if len(facts) != 1 || facts[0].Provider != topup.PaymentProvider {
		return ErrPaymentReceiptConflict
	}
	f := facts[0]
	receipt, err := ClaimPaymentReceiptTx(tx, &PaymentReceipt{Provider: f.Provider, ProviderReference: f.ExternalReference, MerchantScope: f.MerchantScope, Environment: f.Environment, Purpose: "topup", LocalOrderKey: topup.TradeNo, OwnerUserID: topup.UserId, Currency: f.Currency, CurrencyExponent: f.CurrencyExponent, PaidMinor: f.PaidMinor, PaidAt: common.GetTimestamp(), VerifiedAt: common.GetTimestamp()})
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
