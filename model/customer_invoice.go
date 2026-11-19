package model

import (
	"errors"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrCustomerInvoiceInvalid  = errors.New("Invalid invoice request")
	ErrCustomerInvoiceNotFound = errors.New("Invoice application not found")
	ErrCustomerInvoiceConflict = errors.New("Invoice application or selected orders have changed")
	ErrCustomerInvoiceDisabled = errors.New("Invoice applications are unavailable")
)

// Invoice records describe externally issued documents; they are not wallet,
// fee, or tax issuance transactions. Never serialize a storage record.
type CustomerInvoiceApplication struct {
	ID                   int64   `json:"-" gorm:"primaryKey"`
	UserID               int     `json:"-" gorm:"not null;uniqueIndex:idx_ci_owner_key,priority:1;index:idx_ci_owner_status,priority:1"`
	IdempotencyKey       string  `json:"-" gorm:"type:varchar(96);not null;uniqueIndex:idx_ci_owner_key,priority:2"`
	RequestHash          string  `json:"-" gorm:"type:char(64);not null"`
	MerchantScope        string  `json:"-" gorm:"type:varchar(128);not null;uniqueIndex:idx_ci_merchant_number,priority:1"`
	InvoiceNumber        *string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_ci_merchant_number,priority:2"`
	Status               string  `json:"-" gorm:"type:varchar(16);not null;index:idx_ci_owner_status,priority:2;index:idx_ci_status_created,priority:1"`
	AmountMinor          int64   `json:"-" gorm:"not null"`
	FeeState             string  `json:"-" gorm:"type:varchar(16);not null;default:none"`
	FeeRail              string  `json:"-" gorm:"type:varchar(16);not null;default:''"`
	FeePaidMinor         int64   `json:"-" gorm:"not null;default:0"`
	FeePaidAt            int64   `json:"-" gorm:"not null;default:0"`
	WalletQuotaPerCent   int64   `json:"-" gorm:"not null;default:0"`
	FeeTradeNo           *string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_ci_fee_trade_no"`
	FeePaymentMethod     string  `json:"-" gorm:"type:varchar(50);not null;default:''"`
	FeeGateway           string  `json:"-" gorm:"type:varchar(512);not null;default:''"`
	FeeReceiptID         int64   `json:"-" gorm:"not null;default:0"`
	SettingsVersion      int64   `json:"-" gorm:"not null;default:0"`
	FeeMinor             int64   `json:"-" gorm:"not null;default:0"`
	FeeRateBPS           int     `json:"-" gorm:"column:fee_rate_bps;not null;default:0"`
	TaxRateBPS           int     `json:"-" gorm:"column:tax_rate_bps;not null;default:0"`
	OrderCount           int     `json:"-" gorm:"not null"`
	InvoiceType          string  `json:"-" gorm:"type:varchar(16);not null"`
	TitleType            string  `json:"-" gorm:"type:varchar(16);not null"`
	Title                string  `json:"-" gorm:"type:varchar(128);not null"`
	TaxID                string  `json:"-" gorm:"type:varchar(20);not null"`
	Category             string  `json:"-" gorm:"type:varchar(32);not null"`
	Email                string  `json:"-" gorm:"type:varchar(254);not null"`
	Address              string  `json:"-" gorm:"type:varchar(255);not null"`
	Phone                string  `json:"-" gorm:"type:varchar(32);not null"`
	BankName             string  `json:"-" gorm:"type:varchar(128);not null"`
	BankAccount          string  `json:"-" gorm:"type:varchar(40);not null"`
	Reason               string  `json:"-" gorm:"type:varchar(500);not null"`
	ActionBy             int     `json:"-" gorm:"not null"`
	ProcessingBy         int     `json:"-" gorm:"not null;default:0"`
	ProcessingAt         int64   `json:"-" gorm:"not null;default:0"`
	CreatedAt            int64   `json:"-" gorm:"autoCreateTime;index:idx_ci_status_created,priority:2"`
	UpdatedAt            int64   `json:"-" gorm:"autoUpdateTime"`
	IssuedAt             int64   `json:"-" gorm:"not null"`
	DocumentSHA256       string  `json:"-" gorm:"column:document_sha256;type:varchar(64);not null"`
	ReconciliationReason string  `json:"-" gorm:"type:varchar(500);not null"`
	ReconciliationBy     int     `json:"-" gorm:"not null"`
	ReconciliationAt     int64   `json:"-" gorm:"not null;index"`
}

type CustomerInvoiceItem struct {
	ID            int64 `json:"-" gorm:"primaryKey"`
	ApplicationID int64 `json:"-" gorm:"not null;uniqueIndex:idx_ci_item_order,priority:1"`
	TopUpID       int   `json:"-" gorm:"not null;uniqueIndex:idx_ci_item_order,priority:2"`
	// NULL only after rejection/cancellation. Issued orders retain their claim,
	// including after a refund review. Prior application snapshots are immutable.
	ActiveTopUpID *int   `json:"-" gorm:"uniqueIndex;check:chk_ci_active_order,active_top_up_id IS NULL OR active_top_up_id = top_up_id"`
	ReceiptID     int64  `json:"-" gorm:"not null"`
	Reference     string `json:"-" gorm:"type:varchar(128);not null"`
	PaidAt        int64  `json:"-" gorm:"not null"`
	AmountMinor   int64  `json:"-" gorm:"not null;check:chk_ci_line_amount,amount_minor > 0"`
}

func CustomerInvoiceModels() []any {
	return []any{&CustomerInvoiceApplication{}, &CustomerInvoiceItem{}, &CustomerInvoiceDocument{}, &CustomerInvoiceSettingsRevision{}}
}

type CustomerInvoiceQuery struct {
	Page, Size int
	Status     string
}

type CustomerInvoiceInput struct {
	SettingsVersion *int64 `json:"settings_version,omitempty"`
	OrderIDs        []int  `json:"order_ids"`
	IdempotencyKey  string `json:"idempotency_key"`
	InvoiceType     string `json:"invoice_type"`
	TitleType       string `json:"title_type"`
	Title           string `json:"title"`
	TaxID           string `json:"tax_id,omitempty"`
	Category        string `json:"category"`
	Email           string `json:"email"`
	Address         string `json:"address,omitempty"`
	Phone           string `json:"phone,omitempty"`
	BankName        string `json:"bank_name,omitempty"`
	BankAccount     string `json:"bank_account,omitempty"`
}

type CustomerInvoiceOrder struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Reference   string `json:"reference"`
	PaidAt      int64  `json:"paid_at"`
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
}
type CustomerInvoiceTotals struct {
	AvailableMinor string `json:"available_minor"`
	AvailableCount int64  `json:"available_count"`
	PendingMinor   string `json:"pending_minor"`
	PendingCount   int64  `json:"pending_count"`
	IssuedMinor    string `json:"issued_minor"`
	IssuedCount    int64  `json:"issued_count"`
	Currency       string `json:"currency"`
}
type CustomerInvoiceSummary struct {
	FeeState               string `json:"fee_state"`
	FeeRail                string `json:"fee_rail"`
	FeePaidMinor           string `json:"fee_paid_minor"`
	FeePaidAt              int64  `json:"fee_paid_at"`
	WalletQuotaPerCent     int64  `json:"wallet_quota_per_cent"`
	WalletFeeQuota         string `json:"wallet_fee_quota"`
	FeePaymentMethod       string `json:"fee_payment_method,omitempty"`
	SettingsVersion        int64  `json:"settings_version"`
	FeeRateBPS             int    `json:"fee_rate_bps"`
	TaxRateBPS             int    `json:"tax_rate_bps"`
	FeeMinor               string `json:"fee_minor"`
	ID                     int64  `json:"id"`
	UserID                 int    `json:"user_id,omitempty"`
	Status                 string `json:"status"`
	AmountMinor            string `json:"amount_minor"`
	Currency               string `json:"currency"`
	OrderCount             int    `json:"order_count"`
	InvoiceType            string `json:"invoice_type"`
	TitleType              string `json:"title_type"`
	Category               string `json:"category"`
	CreatedAt              int64  `json:"created_at"`
	UpdatedAt              int64  `json:"updated_at"`
	IssuedAt               int64  `json:"issued_at,omitempty"`
	HasDocument            bool   `json:"has_document"`
	Processing             bool   `json:"processing"`
	ProcessingBy           int    `json:"processing_by,omitempty"`
	RequiresReconciliation bool   `json:"requires_reconciliation,omitempty"`
}
type CustomerInvoiceDetail struct {
	FeeTradeNo           string `json:"fee_trade_no,omitempty"`
	FeeProviderReference string `json:"fee_provider_reference,omitempty"`
	CustomerInvoiceSummary
	Title                string                 `json:"title"`
	TaxID                string                 `json:"tax_id,omitempty"`
	Email                string                 `json:"email"`
	Address              string                 `json:"address,omitempty"`
	Phone                string                 `json:"phone,omitempty"`
	BankName             string                 `json:"bank_name,omitempty"`
	BankAccount          string                 `json:"bank_account,omitempty"`
	Reason               string                 `json:"reason,omitempty"`
	InvoiceNumber        string                 `json:"invoice_number,omitempty"`
	ReconciliationReason string                 `json:"reconciliation_reason,omitempty"`
	Orders               []CustomerInvoiceOrder `json:"orders"`
}

const customerInvoiceMaxOrders = 100

var (
	invoiceKeyPattern     = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,96}$`)
	invoiceTaxPattern     = regexp.MustCompile(`^[A-Z0-9]{15,20}$`)
	invoicePhonePattern   = regexp.MustCompile(`^[0-9()+ -]{1,32}$`)
	invoiceAccountPattern = regexp.MustCompile(`^[0-9]{1,40}$`)
	invoiceNumberPattern  = regexp.MustCompile(`^[A-Z0-9-]{1,64}$`)
)

func validInvoiceText(s string, max int) bool {
	return s != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && strings.IndexFunc(s, unicode.IsControl) < 0
}

func normalizeCustomerInvoice(input CustomerInvoiceInput) (CustomerInvoiceInput, error) {
	if input.SettingsVersion != nil {
		if *input.SettingsVersion < 0 || *input.SettingsVersion > invoiceMaxSettingsVersion {
			return input, ErrCustomerInvoiceInvalid
		}
		// No version and explicit zero denote identical legacy terms. Keep the
		// im.14 idempotency hash byte-for-byte stable for existing applications.
		if *input.SettingsVersion == 0 {
			input.SettingsVersion = nil
		}
	}
	for _, s := range []*string{&input.IdempotencyKey, &input.Title, &input.TaxID, &input.Email, &input.Address, &input.Phone, &input.BankName, &input.BankAccount} {
		*s = strings.TrimSpace(*s)
	}
	if !invoiceKeyPattern.MatchString(input.IdempotencyKey) || len(input.OrderIDs) == 0 || len(input.OrderIDs) > customerInvoiceMaxOrders ||
		(input.InvoiceType != "ordinary" && input.InvoiceType != "special") || (input.TitleType != "personal" && input.TitleType != "company") ||
		(input.Category != "technical_service" && input.Category != "software_development") || !validInvoiceText(input.Title, 128) || !validInvoiceText(input.Email, 254) || len(input.Email) > 254 {
		return input, ErrCustomerInvoiceInvalid
	}
	email, err := mail.ParseAddress(input.Email)
	if err != nil || email.Name != "" || email.Address != input.Email {
		return input, ErrCustomerInvoiceInvalid
	}
	input.TaxID = strings.ToUpper(input.TaxID)
	if input.TitleType == "company" && !invoiceTaxPattern.MatchString(input.TaxID) || input.TitleType == "personal" && input.TaxID != "" {
		return input, ErrCustomerInvoiceInvalid
	}
	if input.InvoiceType == "special" {
		if input.TitleType != "company" || !validInvoiceText(input.Address, 255) || !validInvoiceText(input.BankName, 128) ||
			!invoiceAccountPattern.MatchString(input.BankAccount) || !invoicePhonePattern.MatchString(input.Phone) || !strings.ContainsAny(input.Phone, "0123456789") {
			return input, ErrCustomerInvoiceInvalid
		}
	} else if input.Address != "" || input.Phone != "" || input.BankName != "" || input.BankAccount != "" {
		return input, ErrCustomerInvoiceInvalid
	}
	input.OrderIDs = slices.Clone(input.OrderIDs)
	slices.Sort(input.OrderIDs)
	for i, id := range input.OrderIDs {
		if id <= 0 || i > 0 && input.OrderIDs[i-1] == id {
			return input, ErrCustomerInvoiceInvalid
		}
	}
	return input, nil
}
