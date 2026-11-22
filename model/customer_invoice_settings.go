package model

import (
	"context"
	"errors"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrCustomerInvoiceSettingsConflict      = errors.New("Invoice settings changed; review the current rates")
	ErrCustomerInvoiceCategoryUnavailable   = errors.New("This invoice category is currently unavailable")
	ErrCustomerInvoiceTypeDisabled          = errors.New("This invoice type is unavailable")
	ErrCustomerInvoiceFeePaymentUnavailable = errors.New("Invoice fee payment is unavailable")
)

const invoiceMaxSettingsVersion = 1<<53 - 1 // exact in JSON clients

type CustomerInvoiceTypeSettings struct {
	Enabled    bool `json:"enabled" gorm:"not null"`
	FeeEnabled bool `json:"fee_enabled" gorm:"not null"`
	FeeRateBPS int  `json:"fee_rate_bps" gorm:"not null"`
	TaxRateBPS int  `json:"tax_rate_bps" gorm:"not null"`
}

type CustomerInvoiceSettings struct {
	CenterEnabled bool                        `json:"center_enabled" gorm:"-"`
	Categories    CustomerInvoiceCategories   `json:"categories" gorm:"type:text"`
	Version       int64                       `json:"version" gorm:"primaryKey;autoIncrement:false"`
	Ordinary      CustomerInvoiceTypeSettings `json:"ordinary" gorm:"embedded;embeddedPrefix:ordinary_"`
	Special       CustomerInvoiceTypeSettings `json:"special" gorm:"embedded;embeddedPrefix:special_"`
}

// Each settings write appends an immutable audit record. Revision zero is the
// original fee-free policy and a permanent gate for concurrent admissions
// and editor saves, including the first edit on a fresh or upgraded database.
type CustomerInvoiceSettingsRevision struct {
	CustomerInvoiceSettings `json:"-" gorm:"embedded"`
	CenterEnabledValue      *bool `json:"-" gorm:"column:center_enabled"`
	ActorID                 int   `json:"-" gorm:"not null"`
	CreatedAt               int64 `json:"-" gorm:"autoCreateTime;not null"`
}

func (*CustomerInvoiceSettingsRevision) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*CustomerInvoiceSettingsRevision) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

func defaultCustomerInvoiceSettings() CustomerInvoiceSettings {
	return CustomerInvoiceSettings{CenterEnabled: true, Categories: defaultCustomerInvoiceCategories(), Ordinary: CustomerInvoiceTypeSettings{Enabled: true}, Special: CustomerInvoiceTypeSettings{Enabled: true}}
}

func (s CustomerInvoiceSettings) valid() bool {
	if s.Version < 0 || s.Version > invoiceMaxSettingsVersion {
		return false
	}
	for _, t := range []CustomerInvoiceTypeSettings{s.Ordinary, s.Special} {
		if t.FeeRateBPS < 0 || t.FeeRateBPS > 10000 || t.TaxRateBPS < 0 || t.TaxRateBPS > 10000 {
			return false
		}
	}
	return true
}

func currentCustomerInvoiceSettings(tx *gorm.DB) (CustomerInvoiceSettings, error) {
	var r CustomerInvoiceSettingsRevision
	err := tx.Order("version DESC").Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaultCustomerInvoiceSettings(), nil
	}
	if err != nil {
		return CustomerInvoiceSettings{}, err
	}
	if !r.valid() {
		return CustomerInvoiceSettings{}, ErrCustomerInvoiceInvalid
	}
	if r.CenterEnabledValue == nil && r.Version > 0 {
		// Older binaries omit this column when saving a new revision. Keep their
		// latest rates and categories, but inherit the most recent explicit
		// switch at or before the revision we read. Do not rewrite audit rows.
		var earlier CustomerInvoiceSettingsRevision
		err := tx.Select("center_enabled").Where("version < ? AND center_enabled IS NOT NULL", r.Version).
			Order("version DESC").Take(&earlier).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return CustomerInvoiceSettings{}, err
		}
		r.CenterEnabledValue = earlier.CenterEnabledValue
	}
	r.CenterEnabled = r.CenterEnabledValue == nil || *r.CenterEnabledValue
	if r.Categories == nil {
		// Prior revisions have SQL NULL. An explicitly saved empty array never
		// receives defaults and continues to block all new applications.
		r.Categories = defaultCustomerInvoiceCategories()
	} else if r.Categories, err = r.Categories.normalized(); err != nil {
		return CustomerInvoiceSettings{}, err
	}
	return r.CustomerInvoiceSettings, nil
}

func GetCustomerInvoiceSettings(ctx context.Context) (CustomerInvoiceSettings, error) {
	if DB == nil {
		return CustomerInvoiceSettings{}, gorm.ErrInvalidDB
	}
	return currentCustomerInvoiceSettings(invoiceDB(ctx))
}

func lockCustomerInvoiceSettings(tx *gorm.DB, edit bool) (CustomerInvoiceSettings, error) {
	centerEnabled := true
	gate := CustomerInvoiceSettingsRevision{CustomerInvoiceSettings: defaultCustomerInvoiceSettings(), CenterEnabledValue: &centerEnabled}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&gate).Error; err != nil {
		return CustomerInvoiceSettings{}, err
	}
	q := lockForUpdate(tx)
	if !edit && common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		q = tx.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if err := q.Select("version").Where("version = ?", 0).Take(&CustomerInvoiceSettingsRevision{}).Error; err != nil {
		return CustomerInvoiceSettings{}, err
	}
	// InnoDB's default repeatable-read needs a current read after taking the gate.
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		tx = lockForUpdate(tx)
	}
	return currentCustomerInvoiceSettings(tx)
}

func UpdateCustomerInvoiceSettings(ctx context.Context, actor int, next CustomerInvoiceSettings) (CustomerInvoiceSettings, error) {
	if !next.valid() {
		return CustomerInvoiceSettings{}, ErrCustomerInvoiceInvalid
	}
	var err error
	if next.Categories, err = next.Categories.normalized(); err != nil {
		return CustomerInvoiceSettings{}, err
	}
	err = invoiceDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireInvoiceRoot(tx, actor, true); err != nil {
			return err
		}
		current, err := lockCustomerInvoiceSettings(tx, true)
		if err != nil {
			return err
		}
		if current.Version != next.Version {
			return ErrCustomerInvoiceSettingsConflict
		}
		if next.Categories == nil {
			next.Categories = current.Categories
		}
		if current.CenterEnabled == next.CenterEnabled && current.Ordinary == next.Ordinary && current.Special == next.Special && slices.Equal(current.Categories, next.Categories) {
			return nil
		}
		if next.Version == invoiceMaxSettingsVersion {
			return ErrCustomerInvoiceInvalid
		}
		next.Version++
		centerEnabled := next.CenterEnabled
		return tx.Create(&CustomerInvoiceSettingsRevision{CustomerInvoiceSettings: next, CenterEnabledValue: &centerEnabled, ActorID: actor}).Error
	})
	if err != nil {
		return CustomerInvoiceSettings{}, err
	}
	return next, nil
}

// An integer cent is rounded half-up once on the complete verified principal,
// never once per order. Splitting before multiplying also handles MaxInt64.
func customerInvoiceFeeMinor(principal int64, rateBPS int) (int64, error) {
	if principal < 0 || rateBPS < 0 || rateBPS > 10000 {
		return 0, ErrCustomerInvoiceInvalid
	}
	rate := int64(rateBPS)
	return (principal/10000)*rate + ((principal%10000)*rate+5000)/10000, nil
}
