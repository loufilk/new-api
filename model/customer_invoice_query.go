package model

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const invoiceWindowSeconds int64 = 30 * 86400

// SQL tracing, including debug/error paths, must not record invoice identity,
// bank, document, or contact data. The HTTP boundary returns generic errors.
func invoiceDB(ctx context.Context) *gorm.DB {
	return DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
}

func validInvoiceMerchant(policy CustomerInvoicePolicy) bool {
	return validInvoiceText(policy.MerchantScope, 128) && len(policy.MerchantScope) <= 128 && policy.MerchantScope == strings.TrimSpace(policy.MerchantScope)
}

// HTTP checks the live session and role. The model independently limits public
// projections to the owner or an active root account. On mutations the actor
// row remains locked until commit, including when a role is concurrently revoked.
func requireInvoiceRoot(tx *gorm.DB, actor int, lock bool) error {
	var user User
	if actor <= 0 {
		return ErrCustomerInvoiceNotFound
	}
	if lock {
		tx = lockForUpdate(tx)
	}
	err := tx.Select("id").Where("id = ? AND role = ? AND status = ?", actor, common.RoleRootUser, common.UserStatusEnabled).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrCustomerInvoiceNotFound
	}
	return err
}

// The receipt, not wallet/quota/bonus amounts or a client subtotal, is the
// authority. Historical orders without matching evidence are manual-review
// only. A newly registered promotional order may invoice the cash paid, never
// the credited/gift amount. Ordinary Epay has no complete refund feed: an
// operator must also verify external settlement/refund evidence before issue.
func invoiceReceipts(tx *gorm.DB, owner int, merchant string, now int64) *gorm.DB {
	return tx.Table("top_ups AS t").
		Joins("JOIN agent_payment_receipts AS r ON r.id = t.payment_receipt_id AND r.local_order_key = t.trade_no AND r.owner_user_id = t.user_id").
		Where("t.user_id = ? AND t.status = ? AND t.complete_time > 0 AND t.agent_simple_top_up = ?", owner, common.TopUpStatusSuccess, false).
		Where("r.local_order_key <> '' AND r.local_order_key = TRIM(r.local_order_key) AND LENGTH(r.receipt_key) = 64").
		Where("r.purpose = 'topup' AND r.provider = ? AND t.payment_provider = r.provider AND r.environment = 'live' AND t.paid_environment = r.environment", PaymentProviderEpay).
		Where("r.merchant_scope = ? AND r.merchant_scope = t.paid_merchant_scope AND r.provider_reference <> '' AND r.provider_reference = TRIM(r.provider_reference) AND r.provider_reference = t.paid_provider_reference", merchant).
		Where("r.currency = 'CNY' AND t.paid_currency = r.currency AND r.currency_exponent = 2 AND t.paid_currency_exponent = r.currency_exponent").
		Where("r.paid_minor > 0 AND r.paid_minor <= ? AND t.paid_amount_minor = r.paid_minor", int64(common.MaxWalletQuota)).
		Where("r.paid_at > 0 AND r.verified_at >= r.paid_at AND r.verified_at <= ?", now).
		Where("NOT EXISTS (SELECT 1 FROM agent_commission_refund_facts f WHERE f.top_up_reference_id = t.trade_no AND f.cumulative_refunded_minor > 0)").
		Where("r.provider_reference NOT IN (SELECT f.payment_reference FROM agent_commission_refund_facts f WHERE f.provider = ? AND f.cumulative_refunded_minor > 0)", PaymentProviderEpay).
		Where("NOT EXISTS (SELECT 1 FROM referral_commissions f WHERE f.reference_type = ? AND f.reference_id = t.trade_no AND f.refunded_minor > 0)", ReferralReferenceTopUp)
}

func availableInvoiceReceipts(tx *gorm.DB, owner int, merchant string, now int64) *gorm.DB {
	return invoiceReceipts(tx, owner, merchant, now).
		Where("r.paid_at >= ?", now-invoiceWindowSeconds).
		Where("NOT EXISTS (SELECT 1 FROM customer_invoice_items i WHERE i.active_top_up_id = t.id)")
}

const invoiceReceiptColumns = "t.id AS top_up_id, r.id AS receipt_id, t.trade_no AS reference, r.paid_at AS paid_at, r.paid_minor AS amount_minor"

func (i CustomerInvoiceItem) order() CustomerInvoiceOrder {
	return CustomerInvoiceOrder{ID: i.TopUpID, Type: "topup", Reference: i.Reference, PaidAt: i.PaidAt, AmountMinor: strconv.FormatInt(i.AmountMinor, 10), Currency: "CNY"}
}

func invoicePage(q CustomerInvoiceQuery) (int, int, error) {
	p, size := q.Page, q.Size
	if p == 0 {
		p = 1
	}
	if size == 0 {
		size = 20
	}
	if p < 1 || p > 1_000_000 || size < 1 || size > 100 {
		return 0, 0, ErrCustomerInvoiceInvalid
	}
	return (p - 1) * size, size, nil
}

func ListCustomerInvoiceOrders(ctx context.Context, owner int, policy CustomerInvoicePolicy, q CustomerInvoiceQuery) ([]CustomerInvoiceOrder, int64, error) {
	orders := make([]CustomerInvoiceOrder, 0)
	if owner <= 0 {
		return nil, 0, ErrCustomerInvoiceInvalid
	}
	offset, size, err := invoicePage(q)
	if err != nil {
		return nil, 0, err
	}
	if !policy.Enabled || !validInvoiceMerchant(policy) {
		return orders, 0, nil
	}
	query := availableInvoiceReceipts(invoiceDB(ctx), owner, policy.MerchantScope, common.GetTimestamp())
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []CustomerInvoiceItem
	if err := query.Select(invoiceReceiptColumns).Order("r.paid_at DESC,t.id DESC").Offset(offset).Limit(size).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	for _, row := range rows {
		orders = append(orders, row.order())
	}
	return orders, total, nil
}

func SummarizeCustomerInvoices(ctx context.Context, owner int, policy CustomerInvoicePolicy) (CustomerInvoiceTotals, error) {
	totals := CustomerInvoiceTotals{AvailableMinor: "0", PendingMinor: "0", IssuedMinor: "0", Currency: "CNY"}
	if owner <= 0 {
		return totals, ErrCustomerInvoiceInvalid
	}
	tx := invoiceDB(ctx)
	history := tx.Model(&CustomerInvoiceApplication{}).Where("user_id = ? AND status IN ?", owner, []string{"awaiting_payment", "pending", "issued"}).
		Select("CASE WHEN status = 'awaiting_payment' THEN 'pending' ELSE status END AS bucket, COUNT(*) AS n, SUM(amount_minor) AS total").Group("CASE WHEN status = 'awaiting_payment' THEN 'pending' ELSE status END")
	query := history
	if policy.Enabled && validInvoiceMerchant(policy) {
		available := availableInvoiceReceipts(tx, owner, policy.MerchantScope, common.GetTimestamp()).
			Select("'available' AS bucket, COUNT(*) AS n, COALESCE(SUM(r.paid_minor), 0) AS total")
		// A single statement ensures a concurrent submission or cancellation
		// cannot count the same cash twice (or not at all) across the cards.
		query = tx.Raw("? UNION ALL ?", available, history)
	}
	var rows []struct {
		Bucket string
		N      int64
		Total  string
	}
	if err := query.Scan(&rows).Error; err != nil {
		return totals, err
	}
	for _, r := range rows {
		switch r.Bucket {
		case "available":
			totals.AvailableCount, totals.AvailableMinor = r.N, r.Total
		case "pending":
			totals.PendingCount, totals.PendingMinor = r.N, r.Total
		case "issued":
			totals.IssuedCount, totals.IssuedMinor = r.N, r.Total
		}
	}
	return totals, nil
}

func (a *CustomerInvoiceApplication) summary(admin bool) CustomerInvoiceSummary {
	walletQuote, _ := CustomerInvoiceWalletFeeQuote(a.FeeMinor, a.WalletQuotaPerCent)
	s := CustomerInvoiceSummary{FeeState: a.FeeState, FeeRail: a.FeeRail, FeePaidMinor: strconv.FormatInt(a.FeePaidMinor, 10), FeePaidAt: a.FeePaidAt,
		WalletQuotaPerCent: a.WalletQuotaPerCent, WalletFeeQuota: strconv.Itoa(walletQuote), FeePaymentMethod: a.FeePaymentMethod, ID: a.ID, Status: a.Status, AmountMinor: strconv.FormatInt(a.AmountMinor, 10), Currency: "CNY", OrderCount: a.OrderCount,
		SettingsVersion: a.SettingsVersion, FeeRateBPS: a.FeeRateBPS, TaxRateBPS: a.TaxRateBPS, FeeMinor: strconv.FormatInt(a.FeeMinor, 10),
		InvoiceType: a.InvoiceType, TitleType: a.TitleType, Category: a.Category, CategoryName: a.CategoryName, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, IssuedAt: a.IssuedAt,
		HasDocument: a.Status == "issued" && a.DocumentSHA256 != "",
		Processing:  a.Status == "pending" && (a.ProcessingBy != 0 || a.ProcessingAt != 0)}
	if admin {
		s.UserID = a.UserID
		if s.Processing {
			s.ProcessingBy = a.ProcessingBy
		}
		s.RequiresReconciliation = a.ReconciliationAt > 0
	}
	return s
}

func ListCustomerInvoices(ctx context.Context, owner int, admin bool, q CustomerInvoiceQuery) ([]CustomerInvoiceSummary, int64, error) {
	offset, size, err := invoicePage(q)
	if owner <= 0 || err != nil {
		return nil, 0, ErrCustomerInvoiceInvalid
	}
	tx := invoiceDB(ctx)
	query := tx.Model(&CustomerInvoiceApplication{})
	if admin {
		if err := requireInvoiceRoot(tx, owner, false); err != nil {
			return nil, 0, err
		}
	} else {
		query = query.Where("user_id = ?", owner)
	}
	switch q.Status {
	case "":
	case "awaiting_payment", "pending", "issued", "rejected", "cancelled":
		query = query.Where("status = ?", q.Status)
	case "refund_review":
		if !admin {
			return nil, 0, ErrCustomerInvoiceInvalid
		}
		query = query.Where("status = 'issued' AND reconciliation_at > 0")
	case "fee_refund_review":
		if !admin {
			return nil, 0, ErrCustomerInvoiceInvalid
		}
		query = query.Where("fee_state = 'refund_review'")
	default:
		return nil, 0, ErrCustomerInvoiceInvalid
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []CustomerInvoiceApplication
	// List operations deliberately do not load private form/finance details.
	if err := query.Select("id,user_id,status,amount_minor,fee_state,fee_rail,fee_paid_minor,fee_paid_at,wallet_quota_per_cent,fee_payment_method,settings_version,fee_minor,fee_rate_bps,tax_rate_bps,order_count,invoice_type,title_type,category,category_name,created_at,updated_at,issued_at,document_sha256,reconciliation_at,processing_by,processing_at").
		Order("created_at DESC,id DESC").Offset(offset).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	result := make([]CustomerInvoiceSummary, len(rows))
	for i := range rows {
		result[i] = rows[i].summary(admin)
	}
	return result, total, nil
}

func invoiceApplication(tx *gorm.DB, id int64, owner int, admin, lock bool) (CustomerInvoiceApplication, error) {
	var a CustomerInvoiceApplication
	if id <= 0 || owner <= 0 {
		return a, ErrCustomerInvoiceNotFound
	}
	if lock {
		tx = lockForUpdate(tx)
	}
	if !admin {
		tx = tx.Where("user_id = ?", owner)
	}
	err := tx.Where("id = ?", id).Take(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return a, ErrCustomerInvoiceNotFound
	}
	return a, err
}

func invoiceDetail(tx *gorm.DB, a *CustomerInvoiceApplication, admin bool) (*CustomerInvoiceDetail, error) {
	var items []CustomerInvoiceItem
	if err := tx.Where("application_id = ?", a.ID).Order("top_up_id").Find(&items).Error; err != nil {
		return nil, err
	}
	d := &CustomerInvoiceDetail{CustomerInvoiceSummary: a.summary(admin), Title: a.Title, TaxID: a.TaxID, Email: a.Email, Address: a.Address, Phone: a.Phone,
		BankName: a.BankName, BankAccount: a.BankAccount, Reason: a.Reason, Orders: make([]CustomerInvoiceOrder, len(items))}
	for i := range items {
		d.Orders[i] = items[i].order()
	}
	if a.InvoiceNumber != nil {
		d.InvoiceNumber = *a.InvoiceNumber
	}
	if admin {
		d.ReconciliationReason = a.ReconciliationReason
		if a.FeeRail == "epay" && a.FeeTradeNo != nil {
			d.FeeTradeNo = *a.FeeTradeNo
			if a.FeeReceiptID > 0 {
				var r PaymentReceipt
				if err := tx.Select("provider_reference").Where("id = ? AND provider = ? AND purpose = 'invoice_fee' AND owner_user_id = ? AND local_order_key = ? AND merchant_scope = ?", a.FeeReceiptID, PaymentProviderEpay, a.UserID, d.FeeTradeNo, a.MerchantScope).Take(&r).Error; err != nil {
					return nil, err
				}
				d.FeeProviderReference = r.ProviderReference
			}
		}
	}
	return d, nil
}

func GetCustomerInvoice(ctx context.Context, owner int, admin bool, id int64) (*CustomerInvoiceDetail, error) {
	tx := invoiceDB(ctx)
	if admin {
		if err := requireInvoiceRoot(tx, owner, false); err != nil {
			return nil, err
		}
	}
	a, err := invoiceApplication(tx, id, owner, admin, false)
	if err != nil {
		return nil, err
	}
	return invoiceDetail(tx, &a, admin)
}
