package model

import (
	"database/sql/driver"
	"errors"
	"slices"

	"github.com/QuantumNous/new-api/common"
)

// ModelDisplayCategories is an optional, local catalog-only override. It never
// changes the model's capabilities, API endpoints, routing or billing.
// nil means unspecified on update; an explicit empty array restores automatic classification.
type ModelDisplayCategories []string

func (categories ModelDisplayCategories) Normalize() (ModelDisplayCategories, error) {
	if categories == nil {
		return nil, nil
	}
	allowed := []string{"text", "images", "video", "audio", "vectors", "decision"}
	if len(categories) > len(allowed) {
		return nil, errors.New("Invalid display categories")
	}
	for _, category := range categories {
		if !slices.Contains(allowed, category) {
			return nil, errors.New("Invalid display categories")
		}
	}
	normalized := make(ModelDisplayCategories, 0, len(categories))
	for _, category := range allowed {
		if slices.Contains(categories, category) {
			normalized = append(normalized, category)
		}
	}
	return normalized, nil
}

func (categories ModelDisplayCategories) Value() (driver.Value, error) {
	normalized, err := categories.Normalize()
	if err != nil || normalized == nil {
		return nil, err
	}
	encoded, err := common.Marshal(normalized)
	return string(encoded), err
}

// Corrupt or unknown stored display values fall back to automatic classification,
// rather than breaking the catalog or interpreting "all" as a model capability.
func (categories *ModelDisplayCategories) Scan(value any) error {
	*categories = nil
	var encoded []byte
	switch value := value.(type) {
	case string:
		encoded = []byte(value)
	case []byte:
		encoded = value
	default:
		return nil
	}
	var decoded ModelDisplayCategories
	if err := common.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	normalized, err := decoded.Normalize()
	if err == nil {
		*categories = normalized
	}
	return nil
}
