package model

import (
	"bytes"
	"database/sql/driver"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const invoiceMaxCategories = 30

var invoiceCategoryIDPattern = regexp.MustCompile("^[a-z][a-z0-9_]{0,31}$")

type CustomerInvoiceCategory struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type CustomerInvoiceCategories []CustomerInvoiceCategory

func defaultCustomerInvoiceCategories() CustomerInvoiceCategories {
	return CustomerInvoiceCategories{
		{ID: "technical_service", Name: "技术服务费", Enabled: true},
		{ID: "software_development", Name: "软件开发费", Enabled: true},
	}
}

// Omitted on legacy PUT means keep the current list. An explicit [] closes
// intake; neither null nor a missing enabled flag is an instruction to do so.
func (categories *CustomerInvoiceCategories) UnmarshalJSON(data []byte) error {
	var rows []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Enabled *bool  `json:"enabled"`
	}
	if err := common.DecodeJsonStrict(bytes.NewReader(data), &rows); err != nil || rows == nil || len(rows) > invoiceMaxCategories {
		return ErrCustomerInvoiceInvalid
	}
	out := make(CustomerInvoiceCategories, len(rows))
	for i, row := range rows {
		if row.Enabled == nil {
			return ErrCustomerInvoiceInvalid
		}
		out[i] = CustomerInvoiceCategory{ID: row.ID, Name: row.Name, Enabled: *row.Enabled}
	}
	*categories = out
	return nil
}

// Only SQL NULL identifies a revision from before category configuration.
// Corrupt non-NULL text must not silently reopen applications with defaults.
func (categories *CustomerInvoiceCategories) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*categories = nil
		return nil
	case []byte:
		return categories.UnmarshalJSON(value)
	case string:
		return categories.UnmarshalJSON([]byte(value))
	default:
		return ErrCustomerInvoiceInvalid
	}
}

func (categories CustomerInvoiceCategories) Value() (driver.Value, error) {
	if categories == nil {
		return nil, nil
	}
	encoded, err := common.Marshal(categories)
	return string(encoded), err
}

func (categories CustomerInvoiceCategories) normalized() (CustomerInvoiceCategories, error) {
	if len(categories) > invoiceMaxCategories {
		return nil, ErrCustomerInvoiceInvalid
	}
	normalized := slices.Clone(categories) // preserve nil (omitted) versus [] (explicitly empty)
	ids, names := make(map[string]bool, len(categories)), make(map[string]bool, len(categories))
	fold := cases.Fold()
	for i, category := range categories {
		name := strings.TrimSpace(category.Name)
		if !invoiceCategoryIDPattern.MatchString(category.ID) || ids[category.ID] || strings.IndexFunc(category.Name, unicode.IsControl) >= 0 || !validInvoiceText(name, 60) {
			return nil, ErrCustomerInvoiceInvalid
		}
		key := norm.NFKC.String(fold.String(norm.NFKC.String(name)))
		if names[key] {
			return nil, ErrCustomerInvoiceInvalid
		}
		ids[category.ID], names[key] = true, true
		normalized[i].Name = name
	}
	return normalized, nil
}

func (categories CustomerInvoiceCategories) EnabledOnly() CustomerInvoiceCategories {
	out := make(CustomerInvoiceCategories, 0, len(categories))
	for _, category := range categories {
		if category.Enabled {
			out = append(out, category)
		}
	}
	return out
}

func (categories CustomerInvoiceCategories) snapshot(id string) (string, error) {
	for _, category := range categories {
		if category.ID != id || !category.Enabled {
			continue
		}
		for _, original := range defaultCustomerInvoiceCategories() {
			if original.ID == id && original.Name == category.Name {
				// The historical ID already denotes this localized built-in label.
				return "", nil
			}
		}
		return category.Name, nil
	}
	return "", ErrCustomerInvoiceCategoryUnavailable
}
