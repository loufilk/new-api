package model

import (
	"context"
	"errors"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var invoiceFeeTradePattern = regexp.MustCompile(`^CIF[0-9a-f]{29}$`)

type CustomerInvoiceFeeCheckout struct {
	TradeNo  string
	FeeMinor int64
}

// There is only one independent Epay order for an invoice fee. Reopening a
// checkout reuses its order, method and gateway; do not issue a second payable
// order or fall back to a balance debit while a bank payment might still arrive.
func BeginCustomerInvoiceEpay(ctx context.Context, owner int, id int64, merchant, method, gateway string) (*CustomerInvoiceFeeCheckout, error) {
	if !validInvoiceMerchant(CustomerInvoicePolicy{MerchantScope: merchant}) || !validInvoiceText(method, 50) || !validInvoiceText(gateway, 512) {
		return nil, ErrCustomerInvoiceInvalid
	}
	var out CustomerInvoiceFeeCheckout
	err := invoiceDB(ctx).Transaction(func(tx *gorm.DB) error {
		a, err := invoiceApplication(tx, id, owner, false, true)
		if err != nil {
			return err
		}
		fee, err := customerInvoiceFeeMinor(a.AmountMinor, a.FeeRateBPS)
		if err != nil || fee != a.FeeMinor || fee <= 0 || fee > int64(common.MaxWalletQuota) || a.MerchantScope != merchant ||
			a.TitleType != "company" || a.Status != "awaiting_payment" || a.ProcessingBy != 0 || a.ProcessingAt != 0 || a.FeePaidMinor != 0 || a.FeeReceiptID != 0 || a.FeePaidAt != 0 {
			return ErrCustomerInvoiceConflict
		}
		switch a.FeeState {
		case "unpaid":
			if a.FeeRail != "" || a.FeeTradeNo != nil || a.FeePaymentMethod != "" || a.FeeGateway != "" {
				return ErrCustomerInvoiceConflict
			}
			if err := reconcileCustomerInvoiceOrders(tx, &a); err != nil {
				return err
			}
			trade := "CIF" + common.GetUUID()[:29]
			if err := tx.Model(&a).Updates(map[string]any{"fee_state": "checkout", "fee_rail": "epay",
				"fee_trade_no": trade, "fee_payment_method": method, "fee_gateway": gateway}).Error; err != nil {
				return err
			}
			out.TradeNo = trade
		case "checkout":
			if a.FeeRail != "epay" || a.FeeTradeNo == nil || !invoiceFeeTradePattern.MatchString(*a.FeeTradeNo) ||
				a.FeeGateway != gateway || a.FeePaymentMethod != method {
				return ErrCustomerInvoiceConflict
			}
			if err := reconcileCustomerInvoiceOrders(tx, &a); err != nil {
				return err
			}
			out.TradeNo = *a.FeeTradeNo
		default:
			return ErrCustomerInvoiceConflict
		}
		out.FeeMinor = a.FeeMinor
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Called only with independently signature-verified payment facts. The shared
// receipt registry prevents reuse across top-ups, A07 and invoice fees; neither
// wallet credit nor a top-up is posted. Never discard received money merely
// because invoice settings changed or an underlying order now needs review.
func SettleCustomerInvoiceEpay(ctx context.Context, tradeNo, method string, f VerifiedPaymentFacts, gateway string) error {
	if !invoiceFeeTradePattern.MatchString(tradeNo) || !validInvoiceText(gateway, 512) || !validInvoiceText(method, 50) ||
		f.Provider != PaymentProviderEpay || f.Environment != "live" || f.Currency != "CNY" || f.CurrencyExponent != 2 || f.PaidMinor <= 0 {
		return ErrCustomerInvoiceInvalid
	}
	return invoiceDB(ctx).Transaction(func(tx *gorm.DB) error {
		var a CustomerInvoiceApplication
		err := lockForUpdate(tx).Where("fee_trade_no = ?", tradeNo).Take(&a).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCustomerInvoiceNotFound
		}
		if err != nil {
			return err
		}
		fee, err := customerInvoiceFeeMinor(a.AmountMinor, a.FeeRateBPS)
		if err != nil || fee != f.PaidMinor || f.PaidMinor != a.FeeMinor || a.FeeRail != "epay" || a.MerchantScope != f.MerchantScope || a.FeePaymentMethod != method || a.FeeGateway != gateway {
			return ErrCustomerInvoiceConflict
		}
		now := common.GetTimestamp()
		receipt, err := ClaimPaymentReceiptTx(tx, &PaymentReceipt{Provider: f.Provider, ProviderReference: f.ExternalReference, Environment: f.Environment,
			MerchantScope: f.MerchantScope, Purpose: "invoice_fee", LocalOrderKey: tradeNo, OwnerUserID: a.UserID, Currency: f.Currency,
			CurrencyExponent: f.CurrencyExponent, PaidMinor: f.PaidMinor, PaidAt: now, VerifiedAt: now})
		if errors.Is(err, ErrPaymentReceiptConflict) {
			return ErrCustomerInvoiceConflict
		}
		if err != nil {
			return err
		}
		switch a.FeeState {
		case "paid", "refund_review":
			if a.FeeReceiptID != receipt.ID || a.FeePaidMinor != f.PaidMinor || a.FeePaidAt != receipt.PaidAt {
				return ErrCustomerInvoiceConflict
			}
			return nil // including duplicate callbacks after operator rejection or external issuance
		case "checkout":
			if a.Status != "awaiting_payment" || a.FeePaidMinor != 0 || a.FeePaidAt != 0 || a.FeeReceiptID != 0 {
				return ErrCustomerInvoiceConflict
			}
			return tx.Model(&a).Updates(map[string]any{"status": "pending", "fee_state": "paid", "fee_paid_minor": f.PaidMinor, "fee_paid_at": receipt.PaidAt, "fee_receipt_id": receipt.ID}).Error
		default:
			return ErrCustomerInvoiceConflict
		}
	})
}

func verifyCustomerInvoiceFeeTx(tx *gorm.DB, a *CustomerInvoiceApplication) error {
	fee, err := customerInvoiceFeeMinor(a.AmountMinor, a.FeeRateBPS)
	if err != nil || fee != a.FeeMinor {
		return ErrCustomerInvoiceConflict
	}
	if fee == 0 {
		if a.FeeState != "none" || a.FeeRail != "" || a.FeePaidAt != 0 || a.FeePaidMinor != 0 || a.FeeReceiptID != 0 || a.FeeTradeNo != nil || a.FeeGateway != "" || a.FeePaymentMethod != "" {
			return ErrCustomerInvoiceConflict
		}
		return nil
	}
	if a.FeeState != "paid" || a.FeePaidAt <= 0 || a.FeePaidMinor != fee {
		return ErrCustomerInvoiceFeePaymentUnavailable
	}
	if a.FeeRail == "wallet" {
		if a.FeeReceiptID != 0 || a.FeeTradeNo != nil {
			return ErrCustomerInvoiceConflict
		}
		return verifyCustomerInvoiceWalletFeeTx(tx, a)
	}
	if a.FeeRail != "epay" || a.FeeReceiptID <= 0 || a.FeeTradeNo == nil || !invoiceFeeTradePattern.MatchString(*a.FeeTradeNo) {
		return ErrCustomerInvoiceConflict
	}
	var r PaymentReceipt
	err = tx.Where("id = ? AND provider = ? AND purpose = 'invoice_fee' AND local_order_key = ? AND owner_user_id = ? AND merchant_scope = ?",
		a.FeeReceiptID, PaymentProviderEpay, *a.FeeTradeNo, a.UserID, a.MerchantScope).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrCustomerInvoiceConflict
	}
	if err != nil {
		return err
	}
	if ValidatePaymentReceipt(&r) != nil || r.PaidMinor != fee || r.PaidAt != a.FeePaidAt {
		return ErrCustomerInvoiceConflict
	}
	return nil
}
