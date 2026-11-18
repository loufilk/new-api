package model

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const commerceMaxMinor int64 = 9_007_199_254_740_991

type InvoiceApplication struct {
	ID                  int64           `json:"id" gorm:"primaryKey"`
	UserID              int             `json:"user_id" gorm:"index;uniqueIndex:idx_invoice_user_request,priority:1"`
	RequestKey          string          `json:"-" gorm:"type:varchar(96);uniqueIndex:idx_invoice_user_request,priority:2"`
	RequestHash         string          `json:"-" gorm:"type:varchar(64)"`
	Kind                string          `json:"kind" gorm:"type:varchar(16)"`
	TitleType           string          `json:"title_type" gorm:"type:varchar(16)"`
	Title               string          `json:"title" gorm:"type:varchar(200)"`
	TaxID               string          `json:"tax_id" gorm:"type:varchar(64)"`
	Email               string          `json:"email" gorm:"type:varchar(254)"`
	Address             string          `json:"address" gorm:"type:varchar(300)"`
	Phone               string          `json:"phone" gorm:"type:varchar(64)"`
	BankName            string          `json:"bank_name" gorm:"type:varchar(200)"`
	BankAccount         string          `json:"bank_account" gorm:"type:varchar(80)"`
	Currency            string          `json:"currency" gorm:"type:varchar(3)"`
	CurrencyExponent    int             `json:"currency_exponent"`
	AmountMinor         int64           `json:"amount_minor,string"`
	FeeMinor            int64           `json:"fee_minor,string"`
	FeeBPS              int             `json:"fee_bps"`
	FeeReceiptReference string          `json:"fee_receipt_reference" gorm:"type:varchar(200)"`
	FeeRefundStatus     string          `json:"fee_refund_status" gorm:"type:varchar(16)"`
	FeeRefundReference  string          `json:"fee_refund_reference" gorm:"type:varchar(200)"`
	Status              string          `json:"status" gorm:"type:varchar(24);index"`
	Revision            int64           `json:"revision"`
	ReviewNote          string          `json:"review_note" gorm:"type:varchar(1000)"`
	InvoiceNumber       string          `json:"invoice_number" gorm:"type:varchar(100)"`
	DeliveryURL         string          `json:"delivery_url" gorm:"type:text"`
	CreatedAt           int64           `json:"created_at" gorm:"autoCreateTime:false;index"`
	UpdatedAt           int64           `json:"updated_at" gorm:"autoUpdateTime:false"`
	ReviewedBy          int             `json:"reviewed_by"`
	Orders              []InvoiceOrder  `json:"orders" gorm:"-"`
	Events              []CommerceAudit `json:"events,omitempty" gorm:"-"`
}

type InvoiceOrder struct {
	ID               int64  `json:"-" gorm:"primaryKey"`
	InvoiceID        int64  `json:"invoice_id" gorm:"index"`
	TopUpID          int    `json:"topup_id" gorm:"index"`
	TradeNo          string `json:"trade_no" gorm:"type:varchar(255)"`
	Currency         string `json:"currency" gorm:"type:varchar(3)"`
	CurrencyExponent int    `json:"currency_exponent"`
	AmountMinor      int64  `json:"amount_minor,string"`
	CompletedAt      int64  `json:"completed_at"`
}

// An order has at most one active invoice reservation. Terminal rejection or
// cancellation releases this row, never the immutable application/order history.
type InvoiceReservation struct {
	TopUpID   int   `json:"topup_id" gorm:"primaryKey;autoIncrement:false"`
	InvoiceID int64 `json:"invoice_id" gorm:"index"`
}

type InvoiceCreate struct {
	OrderIDs    []int  `json:"order_ids"`
	Kind        string `json:"kind"`
	TitleType   string `json:"title_type"`
	Title       string `json:"title"`
	TaxID       string `json:"tax_id"`
	Email       string `json:"email"`
	Address     string `json:"address"`
	Phone       string `json:"phone"`
	BankName    string `json:"bank_name"`
	BankAccount string `json:"bank_account"`
}

type CommerceReview struct {
	ExpectedRevision    int64  `json:"expected_revision"`
	Action              string `json:"action"`
	Note                string `json:"note"`
	InvoiceNumber       string `json:"invoice_number"`
	DeliveryURL         string `json:"delivery_url"`
	FeeReceiptReference string `json:"fee_receipt_reference"`
	FeeRefundReference  string `json:"fee_refund_reference"`
}

func EligibleInvoiceOrders(ctx context.Context, userID int) ([]InvoiceOrder, *CommerceConfig, error) {
	config, err := GetCommerceConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	orders := make([]InvoiceOrder, 0)
	if !config.InvoiceEnabled {
		return orders, config, nil
	}
	var topups []TopUp
	err = DB.WithContext(ctx).Where("user_id = ? AND status = ? AND agent_simple_top_up = ? AND complete_time >= ? AND paid_currency = ? AND paid_amount_minor > ? AND paid_currency_exponent >= ? AND paid_currency_exponent <= ?", userID, common.TopUpStatusSuccess, false, common.GetTimestamp()-int64(config.InvoiceWindowDays)*86400, config.InvoiceCurrency, 0, 0, 4).
		Where("id NOT IN (?)", DB.Model(&InvoiceReservation{}).Select("top_up_id")).Order("id DESC").Limit(config.InvoiceMaxOrders).Find(&topups).Error
	for _, order := range topups {
		orders = append(orders, InvoiceOrder{TopUpID: order.Id, TradeNo: order.TradeNo, Currency: order.PaidCurrency, CurrencyExponent: order.PaidCurrencyExponent, AmountMinor: order.PaidAmountMinor, CompletedAt: order.CompleteTime})
	}
	return orders, config, err
}

func CreateInvoiceApplication(ctx context.Context, userID int, requestKey string, input InvoiceCreate) (*InvoiceApplication, error) {
	input.Title, input.TaxID, input.Email = strings.TrimSpace(input.Title), strings.TrimSpace(input.TaxID), strings.TrimSpace(input.Email)
	input.Address, input.Phone = strings.TrimSpace(input.Address), strings.TrimSpace(input.Phone)
	input.BankName, input.BankAccount = strings.TrimSpace(input.BankName), strings.TrimSpace(input.BankAccount)
	if !commerceRequestKey(requestKey) || len(input.OrderIDs) == 0 || len(input.OrderIDs) > 100 || !slices.Contains([]string{"general", "special"}, input.Kind) || !slices.Contains([]string{"individual", "company"}, input.TitleType) || !commerceText(input.Title, 2, 200) || !commerceEmail(input.Email) || !commerceText(input.TaxID, 0, 64) || !commerceText(input.Address, 0, 300) || !commerceText(input.Phone, 0, 64) || !commerceText(input.BankName, 0, 200) || !commerceText(input.BankAccount, 0, 80) {
		return nil, ErrCommerceInvalid
	}
	if input.TitleType == "company" && input.TaxID == "" || input.Kind == "special" && (input.TitleType != "company" || input.Address == "" || input.Phone == "" || input.BankName == "" || input.BankAccount == "") {
		return nil, ErrCommerceInvalid
	}
	slices.Sort(input.OrderIDs)
	for i, id := range input.OrderIDs {
		if id <= 0 || i > 0 && input.OrderIDs[i-1] == id {
			return nil, ErrCommerceInvalid
		}
	}
	hash, err := commerceRequestHash(input)
	if err != nil {
		return nil, err
	}
	var result InvoiceApplication
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := commerceActor(tx, userID, false); err != nil {
			return err
		}
		found := tx.Where("user_id = ? AND request_key = ?", userID, requestKey).First(&result)
		if found.Error == nil {
			if result.RequestHash != hash {
				return ErrCommerceConflict
			}
			return nil
		}
		if !errors.Is(found.Error, gorm.ErrRecordNotFound) {
			return found.Error
		}
		config := DefaultCommerceConfig()
		if err := tx.Where("id = ?", 1).Limit(1).Find(&config).Error; err != nil {
			return err
		}
		if !config.InvoiceEnabled {
			return ErrCommerceDisabled
		}
		if len(input.OrderIDs) > config.InvoiceMaxOrders || input.Kind == "special" && !config.SpecialInvoiceEnabled {
			return ErrCommerceInvalid
		}
		var orders []TopUp
		cutoff := common.GetTimestamp() - int64(config.InvoiceWindowDays)*86400
		// Payment settlement locks its pending order before crediting the user.
		// Exclude unsettled rows before taking locks, so an invalid invoice
		// request cannot invert that lock order and interrupt a payment callback.
		if err := lockForUpdate(tx).Where("id IN ? AND user_id = ? AND status = ? AND agent_simple_top_up = ? AND complete_time >= ? AND paid_currency = ? AND paid_amount_minor > ? AND paid_currency_exponent >= ? AND paid_currency_exponent <= ?", input.OrderIDs, userID, common.TopUpStatusSuccess, false, cutoff, config.InvoiceCurrency, 0, 0, 4).Order("id").Find(&orders).Error; err != nil {
			return err
		}
		if len(orders) != len(input.OrderIDs) {
			return ErrInvoiceIneligible
		}
		var reserved int64
		if err := tx.Model(&InvoiceReservation{}).Where("top_up_id IN ?", input.OrderIDs).Count(&reserved).Error; err != nil {
			return err
		}
		if reserved > 0 {
			return ErrInvoiceIneligible
		}
		var amount int64
		exponent := orders[0].PaidCurrencyExponent
		for _, order := range orders {
			if order.Status != common.TopUpStatusSuccess || order.AgentSimpleTopUp || order.CompleteTime < cutoff || order.PaidCurrency != config.InvoiceCurrency || order.PaidCurrencyExponent != exponent || exponent < 0 || exponent > 4 || order.PaidAmountMinor <= 0 || order.PaidAmountMinor > commerceMaxMinor-amount {
				return ErrInvoiceIneligible
			}
			amount += order.PaidAmountMinor
		}
		feeBPS := config.GeneralFeeBPS
		if input.Kind == "special" {
			feeBPS = config.SpecialFeeBPS
		}
		fee := decimal.NewFromInt(amount).Mul(decimal.NewFromInt(int64(feeBPS))).Div(decimal.NewFromInt(10000)).Round(0).IntPart()
		status := "submitted"
		if fee > 0 {
			status = "awaiting_fee"
		}
		now := common.GetTimestamp()
		result = InvoiceApplication{UserID: userID, RequestKey: requestKey, RequestHash: hash, Kind: input.Kind, TitleType: input.TitleType, Title: input.Title, TaxID: input.TaxID, Email: input.Email, Address: input.Address, Phone: input.Phone, BankName: input.BankName, BankAccount: input.BankAccount, Currency: config.InvoiceCurrency, CurrencyExponent: exponent, AmountMinor: amount, FeeMinor: fee, FeeBPS: feeBPS, Status: status, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		for _, order := range orders {
			line := InvoiceOrder{InvoiceID: result.ID, TopUpID: order.Id, TradeNo: order.TradeNo, Currency: order.PaidCurrency, CurrencyExponent: exponent, AmountMinor: order.PaidAmountMinor, CompletedAt: order.CompleteTime}
			if err := tx.Create(&line).Error; err != nil {
				return err
			}
			if err := tx.Create(&InvoiceReservation{TopUpID: order.Id, InvoiceID: result.ID}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&CommerceAudit{Kind: "invoice", ResourceID: result.ID, ActorID: userID, UserID: userID, Action: "submitted", ToStatus: status, CreatedAt: now}).Error
	})
	if err != nil {
		return nil, err
	}
	return GetInvoiceApplication(ctx, userID, result.ID, false)
}

func GetInvoiceApplication(ctx context.Context, userID int, id int64, admin bool) (*InvoiceApplication, error) {
	var result InvoiceApplication
	query := DB.WithContext(ctx).Where("id = ?", id)
	if !admin {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.First(&result).Error; err != nil {
		return nil, err
	}
	result.Orders = make([]InvoiceOrder, 0)
	if err := DB.WithContext(ctx).Where("invoice_id = ?", id).Order("id").Find(&result.Orders).Error; err != nil {
		return nil, err
	}
	if err := DB.WithContext(ctx).Where("kind = ? AND resource_id = ?", "invoice", id).Order("id").Find(&result.Events).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func ListInvoiceApplications(ctx context.Context, userID int, admin bool, status string, offset, limit int) ([]InvoiceApplication, int64, error) {
	items := make([]InvoiceApplication, 0)
	query := DB.WithContext(ctx).Model(&InvoiceApplication{})
	if !admin {
		query = query.Where("user_id = ?", userID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(max(0, offset)).Limit(min(max(1, limit), 100)).Find(&items).Error
	return items, count, err
}

func ReviewInvoiceApplication(ctx context.Context, actorID int, id int64, admin bool, input CommerceReview, permissions ...AdminUserPermissionCheck) (*InvoiceApplication, error) {
	input.Note, input.InvoiceNumber = strings.TrimSpace(input.Note), strings.TrimSpace(input.InvoiceNumber)
	input.DeliveryURL, input.FeeReceiptReference = strings.TrimSpace(input.DeliveryURL), strings.TrimSpace(input.FeeReceiptReference)
	input.FeeRefundReference = strings.TrimSpace(input.FeeRefundReference)
	if input.ExpectedRevision < 1 || !commerceText(input.Note, 0, 1000) {
		return nil, ErrCommerceInvalid
	}
	var row InvoiceApplication
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := commerceActor(tx, actorID, admin, permissions...); err != nil {
			return err
		}
		query := lockForUpdate(tx).Where("id = ?", id)
		if !admin {
			query = query.Where("user_id = ?", actorID)
		}
		if err := query.First(&row).Error; err != nil {
			return err
		}
		if row.Revision != input.ExpectedRevision {
			return ErrCommerceConflict
		}
		before := row.Status
		switch input.Action {
		case "record_fee_refund":
			if !admin || !slices.Contains([]string{"rejected", "cancelled"}, row.Status) || row.FeeRefundStatus != "pending" || !commerceText(input.FeeRefundReference, 3, 200) {
				return ErrCommerceInvalid
			}
			row.FeeRefundStatus, row.FeeRefundReference = "refunded", input.FeeRefundReference
		case "cancel":
			if admin || !slices.Contains([]string{"submitted", "awaiting_fee"}, row.Status) {
				return ErrCommerceConflict
			}
			row.Status = "cancelled"
		case "confirm_fee":
			if !admin || !slices.Contains([]string{"awaiting_fee", "cancelled", "rejected"}, row.Status) || row.FeeMinor <= 0 || row.FeeReceiptReference != "" || !commerceText(input.FeeReceiptReference, 3, 200) {
				return ErrCommerceInvalid
			}
			row.FeeReceiptReference = input.FeeReceiptReference
			if row.Status == "awaiting_fee" {
				row.Status = "submitted"
			}
		case "approve":
			if !admin || row.Status != "submitted" {
				return ErrCommerceConflict
			}
			row.Status = "approved"
		case "reject":
			if !admin || !slices.Contains([]string{"submitted", "approved", "awaiting_fee"}, row.Status) || input.Note == "" {
				return ErrCommerceInvalid
			}
			row.Status = "rejected"
		case "issue":
			if !admin || row.Status != "approved" || !commerceText(input.InvoiceNumber, 1, 100) || !commerceHTTPS(input.DeliveryURL) {
				return ErrCommerceInvalid
			}
			row.Status, row.InvoiceNumber, row.DeliveryURL = "issued", input.InvoiceNumber, input.DeliveryURL
		default:
			return ErrCommerceInvalid
		}
		if admin {
			row.ReviewedBy = actorID
			if input.Note != "" && input.Action != "confirm_fee" && input.Action != "record_fee_refund" {
				row.ReviewNote = input.Note
			}
		}
		if (row.Status == "rejected" || row.Status == "cancelled") && row.FeeMinor > 0 && row.FeeReceiptReference != "" && row.FeeRefundStatus == "" {
			row.FeeRefundStatus = "pending"
		}
		row.Revision++
		row.UpdatedAt = common.GetTimestamp()
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if row.Status == "rejected" || row.Status == "cancelled" {
			if err := tx.Where("invoice_id = ?", row.ID).Delete(&InvoiceReservation{}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&CommerceAudit{Kind: "invoice", ResourceID: row.ID, UserID: row.UserID, ActorID: actorID, Action: input.Action, FromStatus: before, ToStatus: row.Status, Note: input.Note, CreatedAt: row.UpdatedAt}).Error
	})
	if err != nil {
		return nil, err
	}
	return GetInvoiceApplication(ctx, actorID, row.ID, admin)
}
