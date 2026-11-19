package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Lock in the same order independent of how checkboxes were selected. Re-read
// eligibility in a fresh statement after obtaining the locks: a competing
// application may have claimed an order while this transaction waited.
func lockInvoiceTopUps(tx *gorm.DB, owner int, ids []int) error {
	var orders []TopUp
	if err := lockForUpdate(tx).Select("id").Where("user_id = ? AND id IN ?", owner, ids).Order("id").Find(&orders).Error; err != nil {
		return err
	}
	if len(orders) != len(ids) {
		return ErrCustomerInvoiceConflict
	}
	return nil
}

func SubmitCustomerInvoice(ctx context.Context, owner int, policy CustomerInvoicePolicy, input CustomerInvoiceInput) (*CustomerInvoiceDetail, error) {
	if !policy.Enabled || !validInvoiceMerchant(policy) {
		return nil, ErrCustomerInvoiceDisabled
	}
	if owner <= 0 {
		return nil, ErrCustomerInvoiceInvalid
	}
	input, err := normalizeCustomerInvoice(input)
	if err != nil {
		return nil, err
	}
	encoded, err := common.Marshal(input)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	hash := hex.EncodeToString(sum[:])
	var out *CustomerInvoiceDetail
	err = invoiceDB(ctx).Transaction(func(tx *gorm.DB) error {
		a := CustomerInvoiceApplication{UserID: owner, IdempotencyKey: input.IdempotencyKey, RequestHash: hash, MerchantScope: policy.MerchantScope,
			Status: "pending", InvoiceType: input.InvoiceType, TitleType: input.TitleType, Title: input.Title, TaxID: input.TaxID, Category: input.Category,
			Email: input.Email, Address: input.Address, Phone: input.Phone, BankName: input.BankName, BankAccount: input.BankAccount}
		// Claim the caller's key before reserving any orders. Conflicting inserts
		// wait for the winning transaction, including a rollback on ineligibility.
		attempt := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&a)
		if attempt.Error != nil {
			return attempt.Error
		}
		if attempt.RowsAffected == 0 {
			var replay CustomerInvoiceApplication
			if err := tx.Where("user_id = ? AND idempotency_key = ?", owner, input.IdempotencyKey).Take(&replay).Error; err != nil {
				return err
			}
			if replay.RequestHash != hash || replay.MerchantScope != policy.MerchantScope {
				return ErrCustomerInvoiceConflict
			}
			out, err = invoiceDetail(tx, &replay, false)
			return err
		}
		if err := lockInvoiceTopUps(tx, owner, input.OrderIDs); err != nil {
			return err
		}
		var items []CustomerInvoiceItem
		err := availableInvoiceReceipts(tx, owner, policy.MerchantScope, common.GetTimestamp()).Where("t.id IN ?", input.OrderIDs).
			Select(invoiceReceiptColumns).Order("t.id").Scan(&items).Error
		if err != nil {
			return err
		}
		if len(items) != len(input.OrderIDs) {
			return ErrCustomerInvoiceConflict
		}
		for i := range items {
			line := &items[i]
			if line.TopUpID != input.OrderIDs[i] || line.AmountMinor <= 0 || a.AmountMinor > math.MaxInt64-line.AmountMinor {
				return ErrCustomerInvoiceConflict
			}
			line.ApplicationID = a.ID
			line.ActiveTopUpID = &line.TopUpID
			a.AmountMinor += line.AmountMinor
		}
		a.OrderCount = len(items)
		if err := tx.Create(&items).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ErrCustomerInvoiceConflict
			}
			return err
		}
		if err := tx.Model(&a).Updates(map[string]any{"amount_minor": a.AmountMinor, "order_count": a.OrderCount}).Error; err != nil {
			return err
		}
		out, err = invoiceDetail(tx, &a, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func CancelCustomerInvoice(ctx context.Context, owner int, id int64) (*CustomerInvoiceDetail, error) {
	return closePendingInvoice(ctx, owner, id, "cancelled", "", false)
}
func RejectCustomerInvoice(ctx context.Context, actor int, id int64, reason string) (*CustomerInvoiceDetail, error) {
	reason = strings.TrimSpace(reason)
	if !validInvoiceText(reason, 500) {
		return nil, ErrCustomerInvoiceInvalid
	}
	return closePendingInvoice(ctx, actor, id, "rejected", reason, true)
}

func closePendingInvoice(ctx context.Context, actor int, id int64, status, reason string, admin bool) (*CustomerInvoiceDetail, error) {
	var out *CustomerInvoiceDetail
	err := invoiceDB(ctx).Transaction(func(tx *gorm.DB) error {
		if admin {
			if err := requireInvoiceRoot(tx, actor, true); err != nil {
				return err
			}
		}
		a, err := invoiceApplication(tx, id, actor, admin, true)
		if err != nil {
			return err
		}
		if a.Status != status || a.Reason != reason || a.ActionBy != actor {
			if a.Status != "pending" || a.ProcessingBy != 0 || a.ProcessingAt != 0 {
				return ErrCustomerInvoiceConflict
			}
			released := tx.Model(&CustomerInvoiceItem{}).Where("application_id = ? AND active_top_up_id = top_up_id", a.ID).Update("active_top_up_id", nil)
			if released.Error != nil {
				return released.Error
			}
			if a.OrderCount <= 0 || released.RowsAffected != int64(a.OrderCount) {
				return ErrCustomerInvoiceConflict
			}
			if err := tx.Model(&a).Updates(map[string]any{"status": status, "reason": reason, "action_by": actor}).Error; err != nil {
				return err
			}
		}
		out, err = invoiceDetail(tx, &a, admin)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Reconcile each *claimed* snapshot to independent settlement evidence, not just
// a matching total or the customer's current balance. The 30-day cutoff is for
// intake; a finance delay doesn't invalidate a timely, still-unrefunded order.
func reconcileCustomerInvoice(tx *gorm.DB, a *CustomerInvoiceApplication) error {
	if a.OrderCount <= 0 || a.OrderCount > customerInvoiceMaxOrders || a.AmountMinor <= 0 {
		return ErrCustomerInvoiceConflict
	}
	var items []CustomerInvoiceItem
	if err := tx.Where("application_id = ?", a.ID).Order("top_up_id").Limit(customerInvoiceMaxOrders + 1).Find(&items).Error; err != nil {
		return err
	}
	if len(items) != a.OrderCount {
		return ErrCustomerInvoiceConflict
	}
	ids := make([]int, len(items))
	for i, item := range items {
		if item.ActiveTopUpID == nil || *item.ActiveTopUpID != item.TopUpID {
			return ErrCustomerInvoiceConflict
		}
		ids[i] = item.TopUpID
	}
	if err := lockInvoiceTopUps(tx, a.UserID, ids); err != nil {
		return err
	}
	var evidence []CustomerInvoiceItem
	if err := invoiceReceipts(tx, a.UserID, a.MerchantScope, common.GetTimestamp()).Where("t.id IN ?", ids).Select(invoiceReceiptColumns).Order("t.id").Scan(&evidence).Error; err != nil {
		return err
	}
	if len(evidence) != len(items) {
		return ErrCustomerInvoiceConflict
	}
	var total int64
	for i, r := range evidence {
		s := items[i]
		if r.TopUpID != s.TopUpID || r.ReceiptID != s.ReceiptID || r.Reference != s.Reference || r.PaidAt != s.PaidAt || r.AmountMinor != s.AmountMinor ||
			r.AmountMinor <= 0 || total > math.MaxInt64-r.AmountMinor {
			return ErrCustomerInvoiceConflict
		}
		total += r.AmountMinor
	}
	if total != a.AmountMinor {
		return ErrCustomerInvoiceConflict
	}
	return nil
}

// Claim before using the external invoicing system. The durable owner closes
// the gap between local review and manual external issuance; it is never
// automatically expired or stolen by another operator.
func ClaimCustomerInvoice(ctx context.Context, actor int, policy CustomerInvoicePolicy, id int64) (*CustomerInvoiceDetail, error) {
	if !validInvoiceMerchant(policy) {
		return nil, ErrCustomerInvoiceDisabled
	}
	return changeInvoiceProcessingClaim(ctx, actor, id, &policy)
}

// The HTTP caller must explicitly confirm that nothing was issued outside
// the gateway. Never use a timeout or process restart to release the claim.
func ReleaseCustomerInvoiceClaim(ctx context.Context, actor int, id int64) (*CustomerInvoiceDetail, error) {
	return changeInvoiceProcessingClaim(ctx, actor, id, nil)
}

func changeInvoiceProcessingClaim(ctx context.Context, actor int, id int64, claimPolicy *CustomerInvoicePolicy) (*CustomerInvoiceDetail, error) {
	var out *CustomerInvoiceDetail
	err := invoiceDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireInvoiceRoot(tx, actor, true); err != nil {
			return err
		}
		a, err := invoiceApplication(tx, id, actor, true, true)
		if err != nil {
			return err
		}
		if a.Status != "pending" {
			return ErrCustomerInvoiceConflict
		}
		if claimPolicy == nil {
			if a.ProcessingBy != actor || a.ProcessingAt <= 0 {
				return ErrCustomerInvoiceConflict
			}
			if err := tx.Model(&a).Updates(map[string]any{"processing_by": 0, "processing_at": 0}).Error; err != nil {
				return err
			}
		} else {
			if a.MerchantScope != claimPolicy.MerchantScope || (a.ProcessingBy != 0 && a.ProcessingBy != actor) ||
				(a.ProcessingBy == 0) != (a.ProcessingAt == 0) {
				return ErrCustomerInvoiceConflict
			}
			if err := reconcileCustomerInvoice(tx, &a); err != nil {
				return err
			}
			if a.ProcessingBy == 0 {
				if err := tx.Model(&a).Updates(map[string]any{"processing_by": actor, "processing_at": common.GetTimestamp()}).Error; err != nil {
					return err
				}
			}
		}
		out, err = invoiceDetail(tx, &a, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func IssueCustomerInvoice(ctx context.Context, actor int, policy CustomerInvoicePolicy, id int64, number string, pdf CustomerInvoicePDF) (*CustomerInvoiceDetail, error) {
	if !validInvoiceMerchant(policy) {
		return nil, ErrCustomerInvoiceDisabled
	}
	number = strings.ToUpper(strings.TrimSpace(number))
	if !invoiceNumberPattern.MatchString(number) || strings.Trim(number, "-") == "" {
		return nil, ErrCustomerInvoiceInvalid
	}
	var out *CustomerInvoiceDetail
	err := invoiceDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireInvoiceRoot(tx, actor, true); err != nil {
			return err
		}
		a, err := invoiceApplication(tx, id, actor, true, true)
		if err != nil {
			return err
		}
		if a.MerchantScope != policy.MerchantScope {
			return ErrCustomerInvoiceConflict
		}
		switch a.Status {
		case "issued":
			if a.InvoiceNumber == nil || *a.InvoiceNumber != number || a.ActionBy != actor || a.DocumentSHA256 != pdf.SHA256 {
				return ErrCustomerInvoiceConflict
			}
			digest, err := customerInvoicePDFDigest(pdf.Data)
			if err != nil {
				return err
			}
			if digest != a.DocumentSHA256 {
				return ErrCustomerInvoiceConflict
			}
		case "pending":
			if a.ProcessingBy != actor || a.ProcessingAt <= 0 {
				return ErrCustomerInvoiceConflict
			}
			// Persist only if the subsequent reconciliation AND transition commit.
			// No provider issuance, money movement, or separate file side effect.
			if err := PutCustomerInvoiceDocumentTx(tx, a.ID, pdf); err != nil {
				return err
			}
			if err := reconcileCustomerInvoice(tx, &a); err != nil {
				return err
			}
			err := tx.Model(&a).Updates(map[string]any{"status": "issued", "issued_at": common.GetTimestamp(), "action_by": actor,
				"invoice_number": number, "document_sha256": pdf.SHA256}).Error
			if isDuplicateKeyError(err) {
				return ErrCustomerInvoiceConflict
			}
			if err != nil {
				return err
			}
		default:
			return ErrCustomerInvoiceConflict
		}
		out, err = invoiceDetail(tx, &a, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Record an operator-observed refund after external issuance. This exception
// neither voids an issued invoice nor releases the underlying order; finance
// handles any legally required correction outside the gateway.
func FlagCustomerInvoiceReconciliation(ctx context.Context, actor int, id int64, reason string) (*CustomerInvoiceDetail, error) {
	reason = strings.TrimSpace(reason)
	if !validInvoiceText(reason, 500) {
		return nil, ErrCustomerInvoiceInvalid
	}
	var out *CustomerInvoiceDetail
	err := invoiceDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireInvoiceRoot(tx, actor, true); err != nil {
			return err
		}
		a, err := invoiceApplication(tx, id, actor, true, true)
		if err != nil {
			return err
		}
		if a.Status != "issued" {
			return ErrCustomerInvoiceConflict
		}
		if a.ReconciliationAt > 0 {
			if a.ReconciliationBy != actor || a.ReconciliationReason != reason {
				return ErrCustomerInvoiceConflict
			}
		} else if err := tx.Model(&a).Updates(map[string]any{"reconciliation_by": actor, "reconciliation_reason": reason, "reconciliation_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		out, err = invoiceDetail(tx, &a, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
