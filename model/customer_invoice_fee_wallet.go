package model

import (
	"context"
	"errors"
	"os"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// This is the merchant's explicit price for the invoice-fee wallet option.
// A paid wallet holds service credits, not RMB. Neither a UI currency exchange
// rate nor the acquisition price of an arbitrary historical top-up is authority.
func CustomerInvoiceWalletQuotaPerCent() int64 {
	s := os.Getenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n > common.MaxWalletQuota || strconv.FormatInt(n, 10) != s {
		return 0 // Missing, invalid or non-canonical: option disabled.
	}
	return n
}

func CustomerInvoiceWalletFeeQuote(feeMinor, quotaPerCent int64) (int, error) {
	if feeMinor <= 0 || quotaPerCent <= 0 || feeMinor > common.MaxWalletQuota/quotaPerCent {
		return 0, ErrCustomerInvoiceFeePaymentUnavailable
	}
	return common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(feeMinor * quotaPerCent))
}

type CustomerInvoiceWalletConfirmation struct {
	FeeMinor           string `json:"fee_minor"`
	WalletQuota        string `json:"wallet_quota"`
	WalletQuotaPerCent int64  `json:"wallet_quota_per_cent"`
}

func customerInvoiceWalletDebit(a *CustomerInvoiceApplication, quota int) (LedgerEntry, error) {
	// Keep the evidence deterministic across nodes without relying on JSON map
	// member ordering. Neither customer invoice identity nor payment PII belongs here.
	meta, err := common.Marshal(struct {
		FeeMinor     string `json:"cny_minor"`
		QuotaPerCent int64  `json:"quota_per_cny_cent"`
	}{strconv.FormatInt(a.FeeMinor, 10), a.WalletQuotaPerCent})
	if err != nil {
		return LedgerEntry{}, err
	}
	ref := strconv.FormatInt(a.ID, 10)
	e := LedgerEntry{UserId: a.UserID, Account: AccountPaid, Delta: quota, Reason: "invoice_fee", ReferenceType: "invoice_fee", ReferenceId: ref,
		OperatorId: a.UserID, IdempotencyKey: "invoice_fee:debit:" + ref, DebitOrder: []LedgerAccount{AccountPaid}}
	e.Metadata = string(meta)
	return e, nil
}

// Verify the exact debit and its still-unrefunded provenance. The fee status or
// a large enough current balance by itself can never justify a refund or issue.
func verifyCustomerInvoiceWalletFeeTx(tx *gorm.DB, a *CustomerInvoiceApplication) error {
	quota, err := CustomerInvoiceWalletFeeQuote(a.FeeMinor, a.WalletQuotaPerCent)
	if err != nil || a.FeeState != "paid" || a.FeeRail != "wallet" || a.FeePaidMinor != a.FeeMinor || a.FeePaidAt <= 0 ||
		a.FeeTradeNo != nil || a.FeeReceiptID != 0 || a.FeeGateway != "" || a.FeePaymentMethod != "" {
		return ErrCustomerInvoiceConflict
	}
	e, err := customerInvoiceWalletDebit(a, quota)
	if err != nil {
		return err
	}
	op, rows, err := loadLedgerOperation(tx, e.IdempotencyKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrCustomerInvoiceConflict
	}
	if err != nil {
		return err
	}
	if !verifyReplay(op, e, ledgerOpDebit) || len(rows) != 1 {
		return ErrCustomerInvoiceConflict
	}
	row := rows[0]
	if row.Account != string(AccountPaid) || row.Delta != -quota || row.UserId != a.UserID || row.OperatorId != e.OperatorId ||
		row.ReferenceType != e.ReferenceType || row.ReferenceId != e.ReferenceId || row.Reason != e.Reason || row.Metadata != e.Metadata ||
		row.IdempotencyKey != e.IdempotencyKey || row.TotalBefore != op.TotalBefore || row.TotalAfter != op.TotalAfter ||
		row.TotalAfter < 0 || row.TotalBefore > common.MaxWalletQuota || row.TotalBefore < quota || row.TotalBefore-quota != row.TotalAfter ||
		row.BalanceAfter < 0 || row.BalanceBefore > common.MaxWalletQuota || row.BalanceBefore < quota || row.BalanceBefore-quota != row.BalanceAfter {
		return ErrCustomerInvoiceConflict
	}
	var refunds int64
	if err := tx.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", "invoice_fee:refund:"+e.ReferenceId).Count(&refunds).Error; err != nil {
		return err
	}
	if refunds != 0 {
		return ErrCustomerInvoiceConflict
	}
	return nil
}

// Caller holds the application lock, records the closed/refunded state in this
// transaction, then invalidates the owner's cache only after commit. Return the
// exact original service credits to paid, never to cash, bonus or rewards.
func refundCustomerInvoiceWalletFeeTx(tx *gorm.DB, a *CustomerInvoiceApplication, actor int) error {
	if a.Status != "pending" || a.ProcessingBy != 0 || a.ProcessingAt != 0 || actor <= 0 {
		return ErrCustomerInvoiceConflict
	}
	if err := verifyCustomerInvoiceWalletFeeTx(tx, a); err != nil {
		return err
	}
	quota, err := CustomerInvoiceWalletFeeQuote(a.FeeMinor, a.WalletQuotaPerCent)
	if err != nil {
		return err
	}
	e, err := customerInvoiceWalletDebit(a, quota)
	if err != nil {
		return err
	}
	e.Reason, e.DebitOrder, e.OperatorId, e.IdempotencyKey = "invoice_fee_refund", nil, actor, "invoice_fee:refund:"+e.ReferenceId
	_, err = CreditQuotaTx(tx, e)
	return err
}

func PayCustomerInvoiceWalletFee(ctx context.Context, owner int, policy CustomerInvoicePolicy, id int64, confirmation CustomerInvoiceWalletConfirmation) (*CustomerInvoiceDetail, error) {
	// The intake switch gates new applications, not a customer paying a frozen
	// existing quote. The merchant pin and dedicated wallet switch still apply.
	if !validInvoiceMerchant(policy) {
		return nil, ErrCustomerInvoiceDisabled
	}
	var out *CustomerInvoiceDetail
	err := invoiceDB(ctx).Transaction(func(tx *gorm.DB) error {
		a, err := invoiceApplication(tx, id, owner, false, true)
		if err != nil {
			return err
		}
		fee, err := customerInvoiceFeeMinor(a.AmountMinor, a.FeeRateBPS)
		if err != nil || fee != a.FeeMinor || a.MerchantScope != policy.MerchantScope || a.TitleType != "company" {
			return ErrCustomerInvoiceConflict
		}
		quota, err := CustomerInvoiceWalletFeeQuote(a.FeeMinor, a.WalletQuotaPerCent)
		if err != nil {
			return err
		}
		if confirmation.FeeMinor != strconv.FormatInt(a.FeeMinor, 10) || confirmation.WalletQuota != strconv.Itoa(quota) || confirmation.WalletQuotaPerCent != a.WalletQuotaPerCent {
			return ErrCustomerInvoiceConflict
		}
		if a.FeeState == "paid" && a.FeeRail == "wallet" {
			if a.Status != "pending" && a.Status != "issued" {
				return ErrCustomerInvoiceConflict
			}
			if err := verifyCustomerInvoiceWalletFeeTx(tx, &a); err != nil {
				return err
			}
			out, err = invoiceDetail(tx, &a, false)
			return err
		}
		if a.Status != "awaiting_payment" || a.FeeState != "unpaid" || a.FeeRail != "" || a.FeePaidMinor != 0 || a.FeePaidAt != 0 || a.ProcessingBy != 0 || a.ProcessingAt != 0 ||
			a.FeeTradeNo != nil || a.FeeReceiptID != 0 || a.FeeGateway != "" || a.FeePaymentMethod != "" {
			return ErrCustomerInvoiceConflict
		}
		// Changes to a positive rate affect future quotes only. Removing the rail
		// is a kill switch for NEW debits, never for refunds or committed retries.
		if !policy.WalletFeeAvailable || CustomerInvoiceWalletQuotaPerCent() == 0 {
			return ErrCustomerInvoiceFeePaymentUnavailable
		}
		if err := reconcileCustomerInvoiceOrders(tx, &a); err != nil {
			return err
		}
		e, err := customerInvoiceWalletDebit(&a, quota)
		if err != nil {
			return err
		}
		receipt, err := DebitQuotaTx(tx, e, AccountPaid)
		if err != nil {
			return err
		}
		if receipt.Replayed {
			return ErrCustomerInvoiceConflict // a real committed retry is already paid
		}
		a.Status, a.FeeRail, a.FeeState, a.FeePaidMinor, a.FeePaidAt = "pending", "wallet", "paid", a.FeeMinor, common.GetTimestamp()
		if err := verifyCustomerInvoiceWalletFeeTx(tx, &a); err != nil {
			return err
		}
		if err := tx.Model(&a).Updates(map[string]any{"status": a.Status, "fee_rail": a.FeeRail, "fee_state": a.FeeState, "fee_paid_minor": a.FeePaidMinor, "fee_paid_at": a.FeePaidAt}).Error; err != nil {
			return err
		}
		out, err = invoiceDetail(tx, &a, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	RefreshUserQuotaCache(owner, 0)
	return out, nil
}
