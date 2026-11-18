package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrCommerceInvalid   = errors.New("Invalid application details")
	ErrCommerceConflict  = errors.New("This application changed. Reload and try again.")
	ErrCommerceForbidden = errors.New("Unauthorized, insufficient privileges")
	ErrCommerceDisabled  = errors.New("Applications are currently closed")
	ErrInvoiceIneligible = errors.New("One or more orders are no longer eligible for invoicing")
)

// CommerceConfig controls invoice eligibility and the quoted service fee.
// Fee payments and refunds use the invoice payment state machine and ledger;
// invoice approval and delivery remain manual operations.
type CommerceConfig struct {
	ID                    int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Revision              int64  `json:"revision" gorm:"not null"`
	InvoiceEnabled        bool   `json:"invoice_enabled"`
	InvoiceWindowDays     int    `json:"invoice_window_days"`
	InvoiceMaxOrders      int    `json:"invoice_max_orders"`
	InvoiceCurrency       string `json:"invoice_currency" gorm:"type:varchar(3)"`
	GeneralFeeBPS         int    `json:"general_fee_bps"`
	SpecialInvoiceEnabled bool   `json:"special_invoice_enabled"`
	SpecialFeeBPS         int    `json:"special_fee_bps"`
	InvoiceInstructions   string `json:"invoice_instructions" gorm:"type:text"`
	PartnerEnabled        bool   `json:"partner_enabled"`
	PartnerInstructions   string `json:"partner_instructions" gorm:"type:text"`
	UpdatedAt             int64  `json:"updated_at" gorm:"autoUpdateTime:false"`
	UpdatedBy             int    `json:"updated_by"`
}

type CommerceAudit struct {
	ID         int64  `json:"id" gorm:"primaryKey"`
	Kind       string `json:"kind" gorm:"type:varchar(16);index:idx_commerce_audit_resource,priority:1"`
	ResourceID int64  `json:"resource_id" gorm:"index:idx_commerce_audit_resource,priority:2"`
	ActorID    int    `json:"actor_id"`
	UserID     int    `json:"user_id" gorm:"index"`
	Action     string `json:"action" gorm:"type:varchar(32)"`
	FromStatus string `json:"from_status" gorm:"type:varchar(24)"`
	ToStatus   string `json:"to_status" gorm:"type:varchar(24)"`
	Note       string `json:"note,omitempty" gorm:"type:varchar(1000)"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime:false"`
}

func CommerceModels() []any {
	return []any{&CommerceConfig{}, &CommerceAudit{}, &InvoiceApplication{}, &InvoiceOrder{}, &InvoiceReservation{}, &InvoiceRedemptionReservation{}, &InvoiceFeePayment{}, &PartnerApplication{}}
}

func DefaultCommerceConfig() CommerceConfig {
	return CommerceConfig{ID: 1, InvoiceWindowDays: 30, InvoiceMaxOrders: 100, InvoiceCurrency: "CNY", PartnerEnabled: true}
}

func GetCommerceConfig(ctx context.Context) (*CommerceConfig, error) {
	config := DefaultCommerceConfig()
	result := DB.WithContext(ctx).Where("id = ?", 1).Limit(1).Find(&config)
	return &config, result.Error
}

func SaveCommerceConfig(ctx context.Context, actorID int, input CommerceConfig, permissions ...AdminUserPermissionCheck) (*CommerceConfig, error) {
	input.InvoiceCurrency = strings.ToUpper(strings.TrimSpace(input.InvoiceCurrency))
	input.InvoiceInstructions = strings.TrimSpace(input.InvoiceInstructions)
	input.PartnerInstructions = strings.TrimSpace(input.PartnerInstructions)
	if input.Revision < 0 || input.InvoiceWindowDays < 1 || input.InvoiceWindowDays > 3650 || input.InvoiceMaxOrders < 1 || input.InvoiceMaxOrders > 100 || len(input.InvoiceCurrency) != 3 || input.GeneralFeeBPS < 0 || input.GeneralFeeBPS > 10000 || input.SpecialFeeBPS < 0 || input.SpecialFeeBPS > 10000 || !commerceText(input.InvoiceInstructions, 0, 3000) || !commerceText(input.PartnerInstructions, 0, 3000) {
		return nil, ErrCommerceInvalid
	}
	for _, char := range input.InvoiceCurrency {
		if char < 'A' || char > 'Z' {
			return nil, ErrCommerceInvalid
		}
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := commerceActor(tx, actorID, true, permissions...); err != nil {
			return err
		}
		defaults := DefaultCommerceConfig()
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&defaults).Error; err != nil {
			return err
		}
		var current CommerceConfig
		if err := lockForUpdate(tx).First(&current, 1).Error; err != nil {
			return err
		}
		if input.Revision != current.Revision {
			return ErrCommerceConflict
		}
		input.ID, input.Revision, input.UpdatedBy, input.UpdatedAt = 1, current.Revision+1, actorID, common.GetTimestamp()
		if err := tx.Save(&input).Error; err != nil {
			return err
		}
		return tx.Create(&CommerceAudit{Kind: "settings", ResourceID: 1, ActorID: actorID, Action: "updated", CreatedAt: input.UpdatedAt}).Error
	})
	return &input, err
}

func commerceActor(tx *gorm.DB, actorID int, admin bool, permissions ...AdminUserPermissionCheck) error {
	var actor User
	if err := lockForUpdate(tx).Select("id", "role", "status").First(&actor, actorID).Error; err != nil {
		return ErrCommerceForbidden
	}
	if actor.Status != common.UserStatusEnabled || !common.IsValidateRole(actor.Role) || (admin && actor.Role < common.RoleAdminUser) {
		return ErrCommerceForbidden
	}
	for _, check := range permissions {
		if check == nil {
			return ErrCommerceForbidden
		}
		if err := check(tx, &actor); err != nil {
			return err
		}
	}
	return nil
}

func commerceText(value string, minLength, maxLength int) bool {
	if len([]rune(value)) < minLength || len([]rune(value)) > maxLength {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}

func commerceEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && len(value) <= 254
}

func commerceHTTPS(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && len(value) <= 2048
}

func commerceRequestHash(value any) (string, error) {
	encoded, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func commerceRequestKey(value string) bool {
	if len(value) < 8 || len(value) > 96 {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	})
}
