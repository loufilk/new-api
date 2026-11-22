package model

import (
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 模型赠金资格（TokenMix P0-5）
//
// IsModelBonusEligible 采用 fail-closed 语义：只有 models 表中显式配置为
// true 的模型才可使用赠金消费；false / NULL / 模型不存在 / 缓存缺失一律返回
// false。资格映射来自定价缓存（与 /api/pricing 展示同源，含前缀/后缀/包含
// 规则匹配），并在模型元数据变更时由 InvalidateModelBonusEligibleCache
// 立即失效，最长延迟一个刷新周期。
// ---------------------------------------------------------------------------

const modelBonusEligibleCacheTTL = 60 * time.Second

var (
	modelBonusEligibleMap        = make(map[string]bool) // 仅收录显式 true 的模型名
	modelBonusEligibleLock       sync.RWMutex
	modelBonusEligibleTime       time.Time
	modelBonusEligibleOnce       sync.Mutex
	modelBonusEligibleGeneration uint64
)

// IsModelBonusEligible 返回模型是否允许使用赠金消费（fail-closed）。
// 命中资格映射（仅收录显式 true）返回 true；其余情况回到数据库精确名查询，
// 仍只有显式 true 才返回 true。
func IsModelBonusEligible(modelName string) bool {
	if modelName == "" {
		return false
	}
	modelBonusEligibleLock.RLock()
	fresh := modelBonusEligibleCacheFresh()
	eligible := fresh && modelBonusEligibleMap[modelName]
	modelBonusEligibleLock.RUnlock()
	if !fresh {
		loadModelBonusEligible()
		modelBonusEligibleLock.RLock()
		eligible = modelBonusEligibleCacheFresh() && modelBonusEligibleMap[modelName]
		modelBonusEligibleLock.RUnlock()
	}
	if eligible {
		return true
	}

	// 定价缓存未覆盖的模型（如暂无可启用渠道）：回退精确名查询，
	// 同样 fail-closed，仅显式 true 视为可用。
	var row struct {
		BonusEligible *bool
	}
	if err := DB.Model(&Model{}).
		Where("model_name = ?", modelName).
		Select("bonus_eligible").
		First(&row).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.SysError("query model bonus_eligible failed: " + err.Error())
		}
		return false
	}
	return row.BonusEligible != nil && *row.BonusEligible
}

// Caller must hold modelBonusEligibleLock (read or write).
func modelBonusEligibleCacheFresh() bool {
	return !modelBonusEligibleTime.IsZero() && time.Since(modelBonusEligibleTime) < modelBonusEligibleCacheTTL
}

// loadModelBonusEligible 从定价缓存重建资格映射（与 /api/pricing 同源）。
func loadModelBonusEligible() {
	modelBonusEligibleOnce.Lock()
	defer modelBonusEligibleOnce.Unlock()

	// 双检：另一个 goroutine 可能已刷新
	modelBonusEligibleLock.RLock()
	fresh := modelBonusEligibleCacheFresh()
	generation := modelBonusEligibleGeneration
	modelBonusEligibleLock.RUnlock()
	if fresh {
		return
	}

	pricingSnapshot, snapshotTime := getPricingSnapshot()
	loaded := make(map[string]bool)
	for _, pricing := range pricingSnapshot {
		if pricing.BonusEligible != nil && *pricing.BonusEligible {
			loaded[pricing.ModelName] = true
		}
	}

	modelBonusEligibleLock.Lock()
	// An invalidation after the query began wins over this older snapshot.
	// Do not acquire modelBonusEligibleOnce from invalidation: pricing refresh
	// already holds the pricing lock while this loader may be waiting for it.
	if generation == modelBonusEligibleGeneration {
		modelBonusEligibleMap = loaded
		// Reusing a pricing snapshot must not extend its original TTL.
		modelBonusEligibleTime = snapshotTime
	}
	modelBonusEligibleLock.Unlock()
}

// InvalidateModelBonusEligibleCache 使赠金资格缓存立即失效，下次查询重建。
// 模型元数据（bonus_eligible）发生变更时必须调用，避免资格判定使用旧值。
func InvalidateModelBonusEligibleCache() {
	modelBonusEligibleLock.Lock()
	modelBonusEligibleGeneration++
	modelBonusEligibleMap = make(map[string]bool)
	modelBonusEligibleTime = time.Time{}
	modelBonusEligibleLock.Unlock()
}
