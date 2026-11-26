package model

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const modelSquareNamespace = "model_square_setting"

var (
	modelSquareOptionMu        sync.Mutex
	ErrModelSquareOrderChanged = errors.New("model square order settings changed")
)

func isModelSquareOption(key string) bool {
	return strings.HasPrefix(key, modelSquareNamespace+".")
}

func decodeModelSquareOrder(values map[string]string) (operation_setting.ModelSquareSetting, error) {
	order := operation_setting.DefaultModelSquareSetting()
	err := config.UpdateConfigFromMap(&order, values)
	if order.SortModels == nil {
		order.SortModels = []string{}
	}
	return order, err
}

func readModelSquareOrder(db *gorm.DB) (operation_setting.ModelSquareSetting, map[string]string, error) {
	var options []Option
	keys := []string{
		modelSquareNamespace + ".sort_mode",
		modelSquareNamespace + ".sort_metric",
		modelSquareNamespace + ".sort_days",
		modelSquareNamespace + ".sort_models",
	}
	if err := db.Where(commonKeyCol+" IN ?", keys).Find(&options).Error; err != nil {
		return operation_setting.ModelSquareSetting{}, nil, err
	}
	values := make(map[string]string, len(options))
	for _, option := range options {
		values[strings.TrimPrefix(option.Key, modelSquareNamespace+".")] = option.Value
	}
	order, err := decodeModelSquareOrder(values)
	return order, values, err
}

// ReadModelSquareOrder reads committed option rows instead of a node's
// periodically refreshed option cache. This is only used by the editor.
func ReadModelSquareOrder(ctx context.Context) (operation_setting.ModelSquareSetting, error) {
	order, _, err := readModelSquareOrder(DB.WithContext(ctx))
	return order, err
}

// UpdateModelSquareOrderOptions validates both maps and commits just the
// changed fields. expected=nil is reserved for the existing single-option API.
func UpdateModelSquareOrderOptions(ctx context.Context, changes, expected map[string]string) error {
	if len(changes) == 0 || expected != nil && len(changes) != len(expected) {
		return common.NewMessage("Invalid model square order setting")
	}
	for key, value := range changes {
		name := modelSquareNamespace + "." + key
		if err := operation_setting.ValidateModelSquareOption(name, value); err != nil {
			return err
		}
		if expected != nil {
			old, ok := expected[key]
			if !ok {
				return common.NewMessage("Invalid model square order setting")
			}
			if err := operation_setting.ValidateModelSquareOption(name, old); err != nil {
				return err
			}
		}
	}
	modelSquareOptionMu.Lock()
	defer modelSquareOptionMu.Unlock()

	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, raw, err := readModelSquareOrder(tx)
		if err != nil {
			return err
		}
		values, err := config.ConfigToMap(&current)
		if err != nil {
			return err
		}
		// Deterministic row order prevents overlapping edits locking rows in
		// opposite order. Match the persisted old value as well, since a
		// different node may write after the SELECT.
		for _, key := range slices.Sorted(maps.Keys(changes)) {
			if expected != nil && expected[key] != values[key] {
				return ErrModelSquareOrderChanged
			}
			qualified := modelSquareNamespace + "." + key
			var result *gorm.DB
			if old, exists := raw[key]; exists {
				if old == changes[key] {
					continue
				}
				result = tx.Model(&Option{}).Where(commonKeyCol+" = ? AND value = ?", qualified, old).Update("value", changes[key])
			} else {
				result = tx.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "key"}}, DoNothing: true,
				}).Create(&Option{Key: qualified, Value: changes[key]})
			}
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrModelSquareOrderChanged
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Refresh from the same durable rows on success, including any disjoint
	// edit another node committed meanwhile. Polling uses the same local lock
	// and cannot reapply an earlier snapshot after this write.
	BumpSyncTopic(SyncTopicOptions)
	_, values, err := readModelSquareOrder(DB)
	if err != nil {
		return err
	}
	return applyModelSquareOrderOptions(values)
}

func applyModelSquareOrderOptions(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if _, err := config.GlobalConfig.Update(modelSquareNamespace, values); err != nil {
		return err
	}
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	for key, value := range values {
		common.OptionMap[modelSquareNamespace+"."+key] = value
	}
	return nil
}
