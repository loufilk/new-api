package model

import (
	"context"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Only payment-backed codes have a verifiable invoice amount. Ordinary codes
// and agent credit allocations contain quota, not a customer cash receipt.
func invoiceRedemptionOrders(tx *gorm.DB, userID int, currency string, cutoff int64, ids []int, lock bool) ([]InvoiceOrder, error) {
	result := make([]InvoiceOrder, 0)
	query := tx.Select("id", "used_user_id", "redeemed_time", "agent_simple_payment_id", "budget_agent_id").
		Where("used_user_id = ? AND status = ? AND redeemed_time >= ? AND agent_simple_mode = ? AND agent_simple_payment_id > 0 AND budget_reserved = ? AND source_protected = ? AND delivery_id = 0", userID, common.RedemptionCodeStatusUsed, cutoff, AgentSimpleCodePayment, false, false).
		Where("id NOT IN (?)", tx.Model(&InvoiceRedemptionReservation{}).Select("redemption_id"))
	if ids != nil {
		query = query.Where("id IN ?", ids)
	}
	if lock {
		// A redemption credits its owner only after locking the unused code.
		// The used predicate must precede the lock to avoid the reverse order.
		query = lockForUpdate(query)
	}
	var codes []Redemption
	if err := query.Order("id").Find(&codes).Error; err != nil {
		return nil, err
	}
	if len(codes) == 0 {
		return result, nil
	}
	paymentIDs := make([]int64, 0, len(codes))
	for _, code := range codes {
		paymentIDs = append(paymentIDs, code.AgentSimplePaymentID)
	}
	var payments []AgentSimplePaymentOrder
	if err := tx.Select("id", "agent_user_id", "redemption_id", "payment_receipt_id", "sale_price_fen", "trade_no", "payment_provider", "paid_at").
		Where("id IN ? AND status = ? AND paid_at >= ? AND payment_receipt_id > 0", paymentIDs, AgentSimplePaymentRedeemed, cutoff).Find(&payments).Error; err != nil {
		return nil, err
	}
	if len(payments) == 0 {
		return result, nil
	}
	paymentByID := make(map[int64]AgentSimplePaymentOrder, len(payments))
	receiptIDs := make([]int64, 0, len(payments))
	for _, payment := range payments {
		paymentByID[payment.ID] = payment
		receiptIDs = append(receiptIDs, payment.PaymentReceiptID)
	}
	var receipts []PaymentReceipt
	if err := tx.Where("id IN ? AND purpose = ? AND currency = ? AND environment = ? AND paid_at >= ? AND verified_at >= paid_at AND paid_minor > 0 AND paid_minor <= ?", receiptIDs, "payment_code", currency, "live", cutoff, commerceMaxMinor).Find(&receipts).Error; err != nil {
		return nil, err
	}
	receiptByID := make(map[int64]PaymentReceipt, len(receipts))
	for _, receipt := range receipts {
		receiptByID[receipt.ID] = receipt
	}
	for _, code := range codes {
		payment, ok := paymentByID[code.AgentSimplePaymentID]
		if !ok || payment.RedemptionID != code.Id || payment.AgentUserID != code.BudgetAgentID {
			continue
		}
		receipt, ok := receiptByID[payment.PaymentReceiptID]
		if !ok || receipt.LocalOrderKey != payment.TradeNo || receipt.OwnerUserID != payment.AgentUserID || receipt.Provider != payment.PaymentProvider || receipt.PaidMinor != payment.SalePriceFen || receipt.PaidAt != payment.PaidAt || !validPaymentCurrency(receipt.Currency, receipt.CurrencyExponent) {
			continue
		}
		result = append(result, InvoiceOrder{SourceType: "redemption", RedemptionID: code.Id, TradeNo: "RDM" + strconv.Itoa(code.Id), Currency: receipt.Currency, CurrencyExponent: receipt.CurrencyExponent, AmountMinor: receipt.PaidMinor, CompletedAt: code.RedeemedTime})
	}
	return result, nil
}

type InvoiceSummaryAmount struct {
	AmountMinor string `json:"amount_minor"`
	Count       int64  `json:"count"`
}

type InvoiceSummary struct {
	Currency         string               `json:"currency"`
	CurrencyExponent int                  `json:"currency_exponent"`
	Available        InvoiceSummaryAmount `json:"available"`
	Processing       InvoiceSummaryAmount `json:"processing"`
	Issued           InvoiceSummaryAmount `json:"issued"`
}

// Summary totals are independent from the application-list page and selection
// limit. Decimal accumulation also preserves amounts above JavaScript's safe
// integer range; the API returns integer minor units as strings.
func GetInvoiceSummary(ctx context.Context, userID int, currency string, available []InvoiceOrder) (*InvoiceSummary, error) {
	exponent, ok := PaymentCurrencyExponent(currency)
	if !ok {
		exponent = 2
	}
	var applications []InvoiceApplication
	if err := DB.WithContext(ctx).Select("status", "amount_minor", "currency_exponent").
		Where("user_id = ? AND currency = ? AND status IN ?", userID, currency, []string{"awaiting_fee", "submitted", "approved", "issued"}).Find(&applications).Error; err != nil {
		return nil, err
	}
	result := &InvoiceSummary{Currency: currency, CurrencyExponent: exponent}
	availableAmount, processingAmount, issuedAmount := decimal.Zero, decimal.Zero, decimal.Zero
	for _, order := range available {
		if order.Currency != currency || order.AmountMinor < 0 || order.CurrencyExponent < 0 || order.CurrencyExponent > 4 {
			return nil, ErrInvoiceIneligible
		}
		result.CurrencyExponent = max(result.CurrencyExponent, order.CurrencyExponent)
		availableAmount = availableAmount.Add(decimal.NewFromInt(order.AmountMinor).Shift(-int32(order.CurrencyExponent)))
		result.Available.Count++
	}
	for _, application := range applications {
		if application.AmountMinor < 0 || application.CurrencyExponent < 0 || application.CurrencyExponent > 4 {
			return nil, ErrCommerceInvalid
		}
		result.CurrencyExponent = max(result.CurrencyExponent, application.CurrencyExponent)
		amount := decimal.NewFromInt(application.AmountMinor).Shift(-int32(application.CurrencyExponent))
		if application.Status == "issued" {
			issuedAmount = issuedAmount.Add(amount)
			result.Issued.Count++
		} else {
			processingAmount = processingAmount.Add(amount)
			result.Processing.Count++
		}
	}
	result.Available.AmountMinor = availableAmount.Shift(int32(result.CurrencyExponent)).StringFixed(0)
	result.Processing.AmountMinor = processingAmount.Shift(int32(result.CurrencyExponent)).StringFixed(0)
	result.Issued.AmountMinor = issuedAmount.Shift(int32(result.CurrencyExponent)).StringFixed(0)
	return result, nil
}
