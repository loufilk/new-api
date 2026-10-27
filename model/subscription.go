package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/samber/hot"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Subscription duration units
const (
	SubscriptionDurationYear   = "year"
	SubscriptionDurationMonth  = "month"
	SubscriptionDurationDay    = "day"
	SubscriptionDurationHour   = "hour"
	SubscriptionDurationCustom = "custom"
)

// Subscription quota reset period
const (
	SubscriptionResetNever   = "never"
	SubscriptionResetDaily   = "daily"
	SubscriptionResetWeekly  = "weekly"
	SubscriptionResetMonthly = "monthly"
	SubscriptionResetCustom  = "custom"
)

var (
	ErrSubscriptionOrderNotFound      = errors.New("subscription order not found")
	ErrSubscriptionOrderStatusInvalid = errors.New("subscription order status invalid")
)

// SubscriptionPlanUpdate is the authoritative write boundary for an admin
// plan edit. A nil group pointer means the client omitted that field; a
// non-nil empty string explicitly clears it.
type SubscriptionPlanUpdate struct {
	Plan           SubscriptionPlan
	UpgradeGroup   *string
	DowngradeGroup *string
}

// UpdateSubscriptionPlanTx updates a plan while validating the exact group
// values that will be written after acquiring the shared namespace lock.
func UpdateSubscriptionPlanTx(tx *gorm.DB, id int, request SubscriptionPlanUpdate) error {
	if tx == nil || id <= 0 {
		return errors.New("invalid subscription plan update")
	}
	if err := lockGroupNamespace(tx); err != nil {
		return err
	}
	var current SubscriptionPlan
	if err := lockForUpdate(tx).Where("id = ?", id).First(&current).Error; err != nil {
		return err
	}
	upgrade, downgrade := current.UpgradeGroup, current.DowngradeGroup
	if request.UpgradeGroup != nil {
		upgrade = strings.TrimSpace(*request.UpgradeGroup)
		if err := validateConfiguredGroupNamesForWriteTx(tx, upgrade); err != nil {
			return err
		}
	}
	if request.DowngradeGroup != nil {
		downgrade = strings.TrimSpace(*request.DowngradeGroup)
		if err := validateConfiguredGroupNamesForWriteTx(tx, downgrade); err != nil {
			return err
		}
	}
	updateMap := map[string]any{
		"title":                      request.Plan.Title,
		"subtitle":                   request.Plan.Subtitle,
		"price_amount":               request.Plan.PriceAmount,
		"currency":                   request.Plan.Currency,
		"duration_unit":              request.Plan.DurationUnit,
		"duration_value":             request.Plan.DurationValue,
		"custom_seconds":             request.Plan.CustomSeconds,
		"enabled":                    request.Plan.Enabled,
		"sort_order":                 request.Plan.SortOrder,
		"stripe_price_id":            request.Plan.StripePriceId,
		"creem_product_id":           request.Plan.CreemProductId,
		"waffo_pancake_product_id":   request.Plan.WaffoPancakeProductId,
		"max_purchase_per_user":      request.Plan.MaxPurchasePerUser,
		"total_amount":               request.Plan.TotalAmount,
		"upgrade_group":              upgrade,
		"downgrade_group":            downgrade,
		"quota_reset_period":         request.Plan.QuotaResetPeriod,
		"quota_reset_custom_seconds": request.Plan.QuotaResetCustomSeconds,
		"updated_at":                 common.GetTimestamp(),
	}
	if request.Plan.AllowBalancePay != nil {
		updateMap["allow_balance_pay"] = *request.Plan.AllowBalancePay
	}
	if request.Plan.AllowWalletOverflow != nil {
		updateMap["allow_wallet_overflow"] = *request.Plan.AllowWalletOverflow
	}
	// This writer already owns the namespace/row locks, validates the exact
	// map candidates, and sets updated_at explicitly. Skip SubscriptionPlan's
	// generic hook here so omitted group fields are not revalidated from the
	// receiver snapshot as if they were part of this request.
	return tx.Session(&gorm.Session{SkipHooks: true}).Model(&current).Updates(updateMap).Error
}

const (
	subscriptionPlanCacheNamespace     = "new-api:subscription_plan:v1"
	subscriptionPlanInfoCacheNamespace = "new-api:subscription_plan_info:v1"
)

var (
	subscriptionPlanCacheOnce     sync.Once
	subscriptionPlanInfoCacheOnce sync.Once

	subscriptionPlanCache     *cachex.HybridCache[SubscriptionPlan]
	subscriptionPlanInfoCache *cachex.HybridCache[SubscriptionPlanInfo]
)

func subscriptionPlanCacheTTL() time.Duration {
	ttlSeconds := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_CACHE_TTL", 300)
	if ttlSeconds <= 0 {
		ttlSeconds = 300
	}
	return time.Duration(ttlSeconds) * time.Second
}

func subscriptionPlanInfoCacheTTL() time.Duration {
	ttlSeconds := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_INFO_CACHE_TTL", 120)
	if ttlSeconds <= 0 {
		ttlSeconds = 120
	}
	return time.Duration(ttlSeconds) * time.Second
}

func subscriptionPlanCacheCapacity() int {
	capacity := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_CACHE_CAP", 5000)
	if capacity <= 0 {
		capacity = 5000
	}
	return capacity
}

func subscriptionPlanInfoCacheCapacity() int {
	capacity := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_INFO_CACHE_CAP", 10000)
	if capacity <= 0 {
		capacity = 10000
	}
	return capacity
}

func getSubscriptionPlanCache() *cachex.HybridCache[SubscriptionPlan] {
	subscriptionPlanCacheOnce.Do(func() {
		ttl := subscriptionPlanCacheTTL()
		subscriptionPlanCache = cachex.NewHybridCache[SubscriptionPlan](cachex.HybridCacheConfig[SubscriptionPlan]{
			Namespace: cachex.Namespace(subscriptionPlanCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[SubscriptionPlan]{},
			Memory: func() *hot.HotCache[string, SubscriptionPlan] {
				return hot.NewHotCache[string, SubscriptionPlan](hot.LRU, subscriptionPlanCacheCapacity()).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return subscriptionPlanCache
}

func getSubscriptionPlanInfoCache() *cachex.HybridCache[SubscriptionPlanInfo] {
	subscriptionPlanInfoCacheOnce.Do(func() {
		ttl := subscriptionPlanInfoCacheTTL()
		subscriptionPlanInfoCache = cachex.NewHybridCache[SubscriptionPlanInfo](cachex.HybridCacheConfig[SubscriptionPlanInfo]{
			Namespace: cachex.Namespace(subscriptionPlanInfoCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[SubscriptionPlanInfo]{},
			Memory: func() *hot.HotCache[string, SubscriptionPlanInfo] {
				return hot.NewHotCache[string, SubscriptionPlanInfo](hot.LRU, subscriptionPlanInfoCacheCapacity()).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return subscriptionPlanInfoCache
}

func subscriptionPlanCacheKey(id int) string {
	if id <= 0 {
		return ""
	}
	return strconv.Itoa(id)
}

func InvalidateSubscriptionPlanCache(planId int) {
	if planId <= 0 {
		return
	}
	cache := getSubscriptionPlanCache()
	_, _ = cache.DeleteMany([]string{subscriptionPlanCacheKey(planId)})
	infoCache := getSubscriptionPlanInfoCache()
	_ = infoCache.Purge()
}

// Subscription plan
type SubscriptionPlan struct {
	Id int `json:"id"`

	Title    string `json:"title" gorm:"type:varchar(128);not null"`
	Subtitle string `json:"subtitle" gorm:"type:varchar(255);default:''"`

	// Display money amount (follow existing code style: float64 for money)
	PriceAmount float64 `json:"price_amount" gorm:"type:decimal(10,6);not null;default:0"`
	Currency    string  `json:"currency" gorm:"type:varchar(8);not null;default:'USD'"`

	DurationUnit  string `json:"duration_unit" gorm:"type:varchar(16);not null;default:'month'"`
	DurationValue int    `json:"duration_value" gorm:"type:int;not null;default:1"`
	CustomSeconds int64  `json:"custom_seconds" gorm:"type:bigint;not null;default:0"`

	Enabled   bool `json:"enabled" gorm:"default:true"`
	SortOrder int  `json:"sort_order" gorm:"type:int;default:0"`

	AllowBalancePay *bool `json:"allow_balance_pay"`

	// Allow falling back to wallet balance after subscription quota is exhausted (empty = true)
	AllowWalletOverflow *bool `json:"allow_wallet_overflow"`

	StripePriceId         string `json:"stripe_price_id" gorm:"type:varchar(128);default:''"`
	CreemProductId        string `json:"creem_product_id" gorm:"type:varchar(128);default:''"`
	WaffoPancakeProductId string `json:"waffo_pancake_product_id" gorm:"type:varchar(128);default:''"`

	// Max purchases per user (0 = unlimited)
	MaxPurchasePerUser int `json:"max_purchase_per_user" gorm:"type:int;default:0"`

	// Upgrade user group after purchase (empty = no change)
	UpgradeGroup string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`

	// Downgrade user group on expiry (empty = revert to the group held before purchase)
	DowngradeGroup string `json:"downgrade_group" gorm:"type:varchar(64);default:''"`

	// Total quota (amount in quota units, 0 = unlimited)
	TotalAmount int64 `json:"total_amount" gorm:"type:bigint;not null;default:0"`

	// Quota reset period for plan
	QuotaResetPeriod        string `json:"quota_reset_period" gorm:"type:varchar(16);default:'never'"`
	QuotaResetCustomSeconds int64  `json:"quota_reset_custom_seconds" gorm:"type:bigint;default:0"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (p *SubscriptionPlan) BeforeCreate(tx *gorm.DB) error {
	if err := lockGroupNamespace(tx); err != nil {
		return err
	}
	if err := validateGroupNamesForWriteTx(tx, p.UpgradeGroup, p.DowngradeGroup); err != nil {
		return err
	}
	now := common.GetTimestamp()
	p.CreatedAt = now
	p.UpdatedAt = now
	return nil
}

func (p *SubscriptionPlan) BeforeUpdate(tx *gorm.DB) error {
	if err := lockGroupNamespace(tx); err != nil {
		return err
	}
	if err := validateGroupNamesForWriteTx(tx, p.UpgradeGroup, p.DowngradeGroup); err != nil {
		return err
	}
	p.UpdatedAt = common.GetTimestamp()
	return nil
}

func (p *SubscriptionPlan) NormalizeDefaults() {
	if p.AllowBalancePay == nil {
		p.AllowBalancePay = common.GetPointer(true)
	}
	if p.AllowWalletOverflow == nil {
		p.AllowWalletOverflow = common.GetPointer(true)
	}
}

// Subscription order (payment -> webhook -> create UserSubscription)
type SubscriptionOrder struct {
	Id     int     `json:"id"`
	UserId int     `json:"user_id" gorm:"index"`
	PlanId int     `json:"plan_id" gorm:"index"`
	Money  float64 `json:"money"`

	TradeNo         string `json:"trade_no" gorm:"unique;type:varchar(255);index"`
	PaymentMethod   string `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider string `json:"payment_provider" gorm:"type:varchar(50);default:''"`
	Status          string `json:"status"`
	CreateTime      int64  `json:"create_time"`
	CompleteTime    int64  `json:"complete_time"`

	ProviderPayload string `json:"provider_payload" gorm:"type:text"`
}

func (o *SubscriptionOrder) Insert() error {
	if o.CreateTime == 0 {
		o.CreateTime = common.GetTimestamp()
	}
	return DB.Create(o).Error
}

func (o *SubscriptionOrder) Update() error {
	return DB.Save(o).Error
}

func GetSubscriptionOrderByTradeNo(tradeNo string) *SubscriptionOrder {
	if tradeNo == "" {
		return nil
	}
	var order SubscriptionOrder
	if err := DB.Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
		return nil
	}
	return &order
}

// User subscription instance
type UserSubscription struct {
	Id     int `json:"id"`
	UserId int `json:"user_id" gorm:"index;index:idx_user_sub_active,priority:1"`
	PlanId int `json:"plan_id" gorm:"index"`

	AmountTotal int64 `json:"amount_total" gorm:"type:bigint;not null;default:0"`
	AmountUsed  int64 `json:"amount_used" gorm:"type:bigint;not null;default:0"`
	// QuotaPeriod identifies an allowance generation, independently of reset timestamps.
	// Zero is legacy/unknown. New reservations establish a positive generation;
	// historical zero-period receipts are never assigned to it retroactively.
	QuotaPeriod int64 `json:"quota_period" gorm:"type:bigint;not null;default:0"`

	StartTime int64  `json:"start_time" gorm:"bigint"`
	EndTime   int64  `json:"end_time" gorm:"bigint;index;index:idx_user_sub_active,priority:3"`
	Status    string `json:"status" gorm:"type:varchar(32);index;index:idx_user_sub_active,priority:2"` // active/expired/cancelled

	Source string `json:"source" gorm:"type:varchar(32);default:'order'"` // order/admin

	LastResetTime int64 `json:"last_reset_time" gorm:"type:bigint;default:0"`
	NextResetTime int64 `json:"next_reset_time" gorm:"type:bigint;default:0;index"`

	UpgradeGroup  string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`
	PrevUserGroup string `json:"prev_user_group" gorm:"type:varchar(64);default:''"`

	// Downgrade target group on expiry (snapshot from plan; empty = revert to PrevUserGroup)
	DowngradeGroup string `json:"downgrade_group" gorm:"type:varchar(64);default:''"`

	// Whether wallet fallback is allowed after this subscription's quota is exhausted (snapshot from plan)
	AllowWalletOverflow bool `json:"allow_wallet_overflow"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (s *UserSubscription) BeforeCreate(tx *gorm.DB) error {
	if err := lockGroupNamespace(tx); err != nil {
		return err
	}
	if err := validateGroupNamesForWriteTx(tx, s.UpgradeGroup, s.PrevUserGroup, s.DowngradeGroup); err != nil {
		return err
	}
	now := common.GetTimestamp()
	s.CreatedAt = now
	s.UpdatedAt = now
	return nil
}

func (s *UserSubscription) BeforeUpdate(tx *gorm.DB) error {
	groupFieldsSelected := len(tx.Statement.Selects) == 0
	for _, field := range tx.Statement.Selects {
		switch field {
		case "*", "upgrade_group", "UpgradeGroup", "prev_user_group", "PrevUserGroup", "downgrade_group", "DowngradeGroup":
			groupFieldsSelected = true
		}
	}
	if groupFieldsSelected {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		if err := validateGroupNamesForWriteTx(tx, s.UpgradeGroup, s.PrevUserGroup, s.DowngradeGroup); err != nil {
			return err
		}
	}
	s.UpdatedAt = common.GetTimestamp()
	return nil
}

type SubscriptionSummary struct {
	Subscription *UserSubscription `json:"subscription"`
}

type SubscriptionResetResult struct {
	PlanId           int    `json:"plan_id"`
	MatchedCount     int    `json:"matched_count"`
	ResetCount       int    `json:"reset_count"`
	UserCount        int    `json:"user_count"`
	AdvanceResetTime bool   `json:"advance_reset_time"`
	PlanTitle        string `json:"-"`
	AffectedUserIds  []int  `json:"-"`
}

func calcPlanEndTime(start time.Time, plan *SubscriptionPlan) (int64, error) {
	if plan == nil {
		return 0, errors.New("plan is nil")
	}
	if plan.DurationValue <= 0 && plan.DurationUnit != SubscriptionDurationCustom {
		return 0, errors.New("duration_value must be > 0")
	}
	switch plan.DurationUnit {
	case SubscriptionDurationYear:
		return start.AddDate(plan.DurationValue, 0, 0).Unix(), nil
	case SubscriptionDurationMonth:
		return start.AddDate(0, plan.DurationValue, 0).Unix(), nil
	case SubscriptionDurationDay:
		return start.Add(time.Duration(plan.DurationValue) * 24 * time.Hour).Unix(), nil
	case SubscriptionDurationHour:
		return start.Add(time.Duration(plan.DurationValue) * time.Hour).Unix(), nil
	case SubscriptionDurationCustom:
		if plan.CustomSeconds <= 0 {
			return 0, errors.New("custom_seconds must be > 0")
		}
		return start.Add(time.Duration(plan.CustomSeconds) * time.Second).Unix(), nil
	default:
		return 0, fmt.Errorf("invalid duration_unit: %s", plan.DurationUnit)
	}
}

func NormalizeResetPeriod(period string) string {
	switch strings.TrimSpace(period) {
	case SubscriptionResetDaily, SubscriptionResetWeekly, SubscriptionResetMonthly, SubscriptionResetCustom:
		return strings.TrimSpace(period)
	default:
		return SubscriptionResetNever
	}
}

func calcNextResetTime(base time.Time, plan *SubscriptionPlan, endUnix int64) int64 {
	if plan == nil {
		return 0
	}
	period := NormalizeResetPeriod(plan.QuotaResetPeriod)
	if period == SubscriptionResetNever {
		return 0
	}
	var next time.Time
	switch period {
	case SubscriptionResetDaily:
		next = time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location()).
			AddDate(0, 0, 1)
	case SubscriptionResetWeekly:
		// Align to next Monday 00:00
		weekday := int(base.Weekday()) // Sunday=0
		// Convert to Monday=1..Sunday=7
		if weekday == 0 {
			weekday = 7
		}
		daysUntil := 8 - weekday
		next = time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location()).
			AddDate(0, 0, daysUntil)
	case SubscriptionResetMonthly:
		// Align to first day of next month 00:00
		next = time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, base.Location()).
			AddDate(0, 1, 0)
	case SubscriptionResetCustom:
		if plan.QuotaResetCustomSeconds <= 0 {
			return 0
		}
		next = base.Add(time.Duration(plan.QuotaResetCustomSeconds) * time.Second)
	default:
		return 0
	}
	if endUnix > 0 && next.Unix() > endUnix {
		return 0
	}
	return next.Unix()
}

func GetSubscriptionPlanById(id int) (*SubscriptionPlan, error) {
	return getSubscriptionPlanByIdTx(nil, id)
}

func getSubscriptionPlanByIdTx(tx *gorm.DB, id int) (*SubscriptionPlan, error) {
	if id <= 0 {
		return nil, errors.New("invalid plan id")
	}
	key := subscriptionPlanCacheKey(id)
	if key != "" {
		if cached, found, err := getSubscriptionPlanCache().Get(key); err == nil && found {
			cached.NormalizeDefaults()
			return &cached, nil
		}
	}
	var plan SubscriptionPlan
	query := DB
	if tx != nil {
		query = tx
	}
	if err := query.Where("id = ?", id).First(&plan).Error; err != nil {
		return nil, err
	}
	plan.NormalizeDefaults()
	_ = getSubscriptionPlanCache().SetWithTTL(key, plan, subscriptionPlanCacheTTL())
	return &plan, nil
}

func CountUserSubscriptionsByPlan(userId int, planId int) (int64, error) {
	if userId <= 0 || planId <= 0 {
		return 0, errors.New("invalid userId or planId")
	}
	var count int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", userId, planId).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func getUserGroupByIdTx(tx *gorm.DB, userId int) (string, error) {
	if userId <= 0 {
		return "", errors.New("invalid userId")
	}
	if tx == nil {
		tx = DB
	}
	var group string
	if err := lockForUpdate(tx).Model(&User{}).Where("id = ?", userId).Select(commonGroupCol).Find(&group).Error; err != nil {
		return "", err
	}
	return group, nil
}

func downgradeUserGroupForSubscriptionTx(tx *gorm.DB, sub *UserSubscription, now int64) (string, error) {
	if tx == nil || sub == nil {
		return "", errors.New("invalid downgrade args")
	}
	if err := lockGroupNamespace(tx); err != nil {
		return "", err
	}
	downgradeGroup := strings.TrimSpace(sub.DowngradeGroup)
	upgradeGroup := strings.TrimSpace(sub.UpgradeGroup)
	// Nothing to do if neither an explicit downgrade target nor an upgrade snapshot exists.
	if downgradeGroup == "" && upgradeGroup == "" {
		return "", nil
	}
	currentGroup, err := getUserGroupByIdTx(tx, sub.UserId)
	if err != nil {
		return "", err
	}
	// If another active upgraded subscription exists, keep the current group.
	var activeSub UserSubscription
	activeQuery := tx.Where("user_id = ? AND status = ? AND end_time > ? AND id <> ? AND upgrade_group <> ''",
		sub.UserId, "active", now, sub.Id).
		Order("end_time desc, id desc").
		Limit(1).
		Find(&activeSub)
	if activeQuery.Error == nil && activeQuery.RowsAffected > 0 {
		return "", nil
	}
	// Determine the downgrade target: an explicit downgrade group takes precedence,
	// otherwise revert to the group held before purchase (legacy behavior).
	target := downgradeGroup
	if target == "" {
		// Legacy behavior: only revert when the subscription actually elevated the user.
		if currentGroup != upgradeGroup {
			return "", nil
		}
		target = strings.TrimSpace(sub.PrevUserGroup)
	}
	if target == "" || target == currentGroup {
		return "", nil
	}
	if err := updateUserGroupTx(tx, sub.UserId, target); err != nil {
		return "", err
	}
	return target, nil
}

func CreateUserSubscriptionFromPlanTx(tx *gorm.DB, userId int, plan *SubscriptionPlan, source string) (*UserSubscription, error) {
	if tx == nil {
		return nil, errors.New("tx is nil")
	}
	if err := lockGroupNamespace(tx); err != nil {
		return nil, err
	}
	if plan == nil || plan.Id == 0 {
		return nil, errors.New("invalid plan")
	}
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	if plan.MaxPurchasePerUser > 0 {
		var count int64
		if err := tx.Model(&UserSubscription{}).
			Where("user_id = ? AND plan_id = ?", userId, plan.Id).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			return nil, common.NewMessage("Purchase limit for this plan has been reached")
		}
	}
	nowUnix := GetDBTimestamp(tx)
	now := time.Unix(nowUnix, 0)
	endUnix, err := calcPlanEndTime(now, plan)
	if err != nil {
		return nil, err
	}
	resetBase := now
	nextReset := calcNextResetTime(resetBase, plan, endUnix)
	lastReset := int64(0)
	if nextReset > 0 {
		lastReset = now.Unix()
	}
	upgradeGroup := strings.TrimSpace(plan.UpgradeGroup)
	prevGroup := ""
	if upgradeGroup != "" {
		currentGroup, err := getUserGroupByIdTx(tx, userId)
		if err != nil {
			return nil, err
		}
		if currentGroup != upgradeGroup {
			prevGroup = currentGroup
			if err := updateUserGroupTx(tx, userId, upgradeGroup); err != nil {
				return nil, err
			}
		}
	}
	allowWalletOverflow := true
	if plan.AllowWalletOverflow != nil {
		allowWalletOverflow = *plan.AllowWalletOverflow
	}
	sub := &UserSubscription{
		UserId:              userId,
		PlanId:              plan.Id,
		AmountTotal:         plan.TotalAmount,
		AmountUsed:          0,
		StartTime:           now.Unix(),
		EndTime:             endUnix,
		Status:              "active",
		Source:              source,
		LastResetTime:       lastReset,
		NextResetTime:       nextReset,
		UpgradeGroup:        upgradeGroup,
		PrevUserGroup:       prevGroup,
		DowngradeGroup:      strings.TrimSpace(plan.DowngradeGroup),
		AllowWalletOverflow: allowWalletOverflow,
		CreatedAt:           common.GetTimestamp(),
		UpdatedAt:           common.GetTimestamp(),
	}
	if err := tx.Create(sub).Error; err != nil {
		return nil, err
	}
	return sub, nil
}

func refreshSubscriptionUserGroupCache(userId int, operation string) {
	if err := RefreshUserGroupCache(userId); err != nil {
		common.SysError(common.LogText("failed to refresh user group cache after %s for user %d: %v", operation, userId, err))
	}
}

// Complete a subscription order (idempotent). Creates a UserSubscription snapshot from the plan.
// expectedPaymentProvider guards against cross-gateway callback attacks (empty skips the check).
// actualPaymentMethod updates the order's PaymentMethod to reflect the real payment type used (empty skips update).
func CompleteSubscriptionOrder(tradeNo string, providerPayload string, expectedPaymentProvider string, actualPaymentMethod string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}
	var logUserId int
	var logPlanTitle string
	var logMoney float64
	var logPaymentMethod string
	var upgradeGroup string
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		var order SubscriptionOrder
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(&order).Error; err != nil {
			return ErrSubscriptionOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status == common.TopUpStatusSuccess {
			return nil
		}
		if order.Status != common.TopUpStatusPending {
			return ErrSubscriptionOrderStatusInvalid
		}
		plan, err := getSubscriptionPlanByIdTx(tx, order.PlanId)
		if err != nil {
			return err
		}
		if !plan.Enabled {
			// still allow completion for already purchased orders
		}
		// 锁定用户行：并发完成同一用户的不同订单（包括多实例部署下）时，
		// 使 CreateUserSubscriptionFromPlanTx 的 MaxPurchasePerUser 检查按用户串行。
		var userRow User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", order.UserId).First(&userRow).Error; err != nil {
			return err
		}
		subscription, err := CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, "order")
		if err != nil {
			return err
		}
		if subscription.PrevUserGroup != "" {
			upgradeGroup = strings.TrimSpace(subscription.UpgradeGroup)
		}
		if err := upsertSubscriptionTopUpTx(tx, &order); err != nil {
			return err
		}
		order.Status = common.TopUpStatusSuccess
		order.CompleteTime = common.GetTimestamp()
		if providerPayload != "" {
			order.ProviderPayload = providerPayload
		}
		if actualPaymentMethod != "" && order.PaymentMethod != actualPaymentMethod {
			order.PaymentMethod = actualPaymentMethod
		}
		if err := tx.Save(&order).Error; err != nil {
			return err
		}
		logUserId = order.UserId
		logPlanTitle = plan.Title
		logMoney = order.Money
		logPaymentMethod = order.PaymentMethod
		return nil
	})
	if err != nil {
		return err
	}
	if upgradeGroup != "" && logUserId > 0 {
		refreshSubscriptionUserGroupCache(logUserId, common.LogText("subscription payment completion"))
	}
	if logUserId > 0 {
		RecordLog(logUserId, LogTypeTopup, common.NewMessage("Subscription purchased, plan: {{plan}}, payment amount: {{amount}}, payment method: {{method}}", map[string]any{
			"plan":   logPlanTitle,
			"amount": fmt.Sprintf("%.2f", logMoney),
			"method": logPaymentMethod,
		}))
	}
	return nil
}

func upsertSubscriptionTopUpTx(tx *gorm.DB, order *SubscriptionOrder) error {
	if tx == nil || order == nil {
		return errors.New("invalid subscription order")
	}
	now := common.GetTimestamp()
	var topup TopUp
	if err := tx.Where("trade_no = ?", order.TradeNo).First(&topup).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			topup = TopUp{
				UserId:        order.UserId,
				Amount:        0,
				Money:         order.Money,
				TradeNo:       order.TradeNo,
				PaymentMethod: order.PaymentMethod,
				CreateTime:    order.CreateTime,
				CompleteTime:  now,
				Status:        common.TopUpStatusSuccess,
			}
			return tx.Create(&topup).Error
		}
		return err
	}
	topup.Money = order.Money
	if topup.PaymentMethod == "" {
		topup.PaymentMethod = order.PaymentMethod
	} else if topup.PaymentMethod != order.PaymentMethod {
		return ErrPaymentMethodMismatch
	}
	if topup.CreateTime == 0 {
		topup.CreateTime = order.CreateTime
	}
	topup.CompleteTime = now
	topup.Status = common.TopUpStatusSuccess
	return tx.Save(&topup).Error
}

func ExpireSubscriptionOrder(tradeNo string, expectedPaymentProvider string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(&order).Error; err != nil {
			return ErrSubscriptionOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status != common.TopUpStatusPending {
			return nil
		}
		order.Status = common.TopUpStatusExpired
		order.CompleteTime = common.GetTimestamp()
		return tx.Save(&order).Error
	})
}

// Admin bind (no payment). Creates a UserSubscription from a plan.
func AdminBindSubscription(userId int, planId int, sourceNote string) (*common.Message, error) {
	if userId <= 0 || planId <= 0 {
		return nil, errors.New("invalid userId or planId")
	}
	plan, err := GetSubscriptionPlanById(planId)
	if err != nil {
		return nil, err
	}
	groupChanged := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		// Namespace lock precedes the user row lock, matching group rename's
		// global lock order and preventing a PostgreSQL deadlock cycle.
		var userRow User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&userRow).Error; err != nil {
			return err
		}
		subscription, err := CreateUserSubscriptionFromPlanTx(tx, userId, plan, "admin")
		if err == nil {
			groupChanged = subscription.PrevUserGroup != ""
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if groupChanged {
		refreshSubscriptionUserGroupCache(userId, common.LogText("admin subscription creation"))
		return common.NewMessage("User group will be upgraded to {{group}}", map[string]any{"group": plan.UpgradeGroup}), nil
	}
	return nil, nil
}

func calcSubscriptionBalanceQuota(priceAmount float64) (int, error) {
	if priceAmount <= 0 {
		return 0, nil
	}
	if common.QuotaPerUnit <= 0 {
		return 0, common.NewMessage("Quota unit is misconfigured")
	}
	quota := decimal.NewFromFloat(priceAmount).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Ceil()
	return common.WalletQuotaFromDecimalStrict(quota)
}

// PurchaseSubscriptionWithBalance creates a subscription by deducting the user's wallet quota.
func PurchaseSubscriptionWithBalance(userId int, planId int) error {
	if userId <= 0 || planId <= 0 {
		return errors.New("invalid userId or planId")
	}

	var logPlanTitle string
	var logMoney float64
	var chargedQuota int
	var upgradeGroup string
	var ledgerTotalAfter int
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		plan, err := getSubscriptionPlanByIdTx(tx, planId)
		if err != nil {
			return err
		}
		if !plan.Enabled {
			return common.NewMessage("Subscription plan is not enabled")
		}
		if plan.PriceAmount < 0 {
			return common.NewMessage("Price cannot be negative")
		}
		if plan.AllowBalancePay != nil && !*plan.AllowBalancePay {
			return common.NewMessage("This plan does not allow balance redemption")
		}

		requiredQuota, err := calcSubscriptionBalanceQuota(plan.PriceAmount)
		if err != nil {
			return err
		}

		// 订单号在扣款前生成，作为余额扣减的幂等业务引用
		tradeNo := fmt.Sprintf("SUBBALUSR%dNO%s%d", userId, common.GetRandomString(6), time.Now().UnixNano())

		var user User
		if err := lockForUpdate(tx).Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		// P0-4 默认消费账户仅为 paid；bonus 资格与消费顺序在 P0-5 再启用。
		if requiredQuota > 0 && user.QuotaPaid < requiredQuota {
			return errors.New("余额不足")
		}
		if requiredQuota > 0 {
			// 余额扣减写账本（同一事务；幂等键 = 订阅订单号）
			result, err := DebitQuotaTx(tx, LedgerEntry{
				UserId:        userId,
				Delta:         requiredQuota,
				Reason:        LedgerReasonSubscriptionPurchase,
				ReferenceType: "subscription_order",
				ReferenceId:   tradeNo,
			}, AccountPaid)
			if err != nil {
				return err
			}
			ledgerTotalAfter = result.TotalAfter
		}

		subscription, err := CreateUserSubscriptionFromPlanTx(tx, userId, plan, PaymentMethodBalance)
		if err != nil {
			return err
		}

		now := common.GetTimestamp()
		order := &SubscriptionOrder{
			UserId:          userId,
			PlanId:          plan.Id,
			Money:           plan.PriceAmount,
			TradeNo:         tradeNo,
			PaymentMethod:   PaymentMethodBalance,
			PaymentProvider: PaymentProviderBalance,
			Status:          common.TopUpStatusSuccess,
			CreateTime:      now,
			CompleteTime:    now,
			ProviderPayload: fmt.Sprintf("charged_quota=%d", requiredQuota),
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}

		logPlanTitle = plan.Title
		logMoney = plan.PriceAmount
		chargedQuota = requiredQuota
		if subscription.PrevUserGroup != "" {
			upgradeGroup = strings.TrimSpace(subscription.UpgradeGroup)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if chargedQuota > 0 {
		// 事务成功后刷新额度缓存
		RefreshUserQuotaCache(userId, ledgerTotalAfter)
	}
	if upgradeGroup != "" {
		refreshSubscriptionUserGroupCache(userId, common.LogText("subscription balance purchase"))
	}
	RecordLog(userId, LogTypeTopup, common.NewMessage("Subscription purchased with balance, plan: {{plan}}, payment amount: {{amount}}, quota deducted: {{quota}}", map[string]any{
		"plan":   logPlanTitle,
		"amount": fmt.Sprintf("%.2f", logMoney),
		"quota":  chargedQuota,
	}))
	return nil
}

// GetAllActiveUserSubscriptions returns all active subscriptions for a user.
func GetAllActiveUserSubscriptions(userId int) ([]SubscriptionSummary, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var subs []UserSubscription
	err := DB.Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Order("end_time desc, id desc").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return buildSubscriptionSummaries(subs), nil
}

// HasActiveUserSubscription returns whether the user has any active subscription.
// This is a lightweight existence check to avoid heavy pre-consume transactions.
func HasActiveUserSubscription(userId int) (bool, error) {
	if userId <= 0 {
		return false, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var count int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// UserActiveSubscriptionsAllowWalletOverflow returns whether wallet balance may be used
// after the user's subscription quota is exhausted. A single active subscription that
// disallows wallet overflow (allow_wallet_overflow = false) blocks the fallback.
func UserActiveSubscriptionsAllowWalletOverflow(userId int) (bool, error) {
	if userId <= 0 {
		return false, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var strictCount int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND status = ? AND end_time > ? AND allow_wallet_overflow = ?",
			userId, "active", now, false).
		Count(&strictCount).Error; err != nil {
		return false, err
	}
	return strictCount == 0, nil
}

// GetAllUserSubscriptions returns all subscriptions (active and expired) for a user.
func GetAllUserSubscriptions(userId int) ([]SubscriptionSummary, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	var subs []UserSubscription
	err := DB.Where("user_id = ?", userId).
		Order("end_time desc, id desc").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return buildSubscriptionSummaries(subs), nil
}

func buildSubscriptionSummaries(subs []UserSubscription) []SubscriptionSummary {
	if len(subs) == 0 {
		return []SubscriptionSummary{}
	}
	result := make([]SubscriptionSummary, 0, len(subs))
	for _, sub := range subs {
		subCopy := sub
		result = append(result, SubscriptionSummary{
			Subscription: &subCopy,
		})
	}
	return result
}

// AdminInvalidateUserSubscription marks a user subscription as cancelled and ends it immediately.
func AdminInvalidateUserSubscription(userSubscriptionId int) (*common.Message, error) {
	if userSubscriptionId <= 0 {
		return nil, errors.New("invalid userSubscriptionId")
	}
	now := common.GetTimestamp()
	cacheGroup := ""
	downgradeGroup := ""
	var userId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		// Obtain the immutable owner before taking subscription locks, matching
		// financial source/token/stat transactions (namespace -> user -> sub).
		var owner UserSubscription
		if err := tx.Select("id", "user_id").First(&owner, userSubscriptionId).Error; err != nil {
			return err
		}
		if err := LockFinancialUserTx(tx, owner.UserId); err != nil {
			return err
		}
		var sub UserSubscription
		if err := lockForUpdate(tx).
			Where("id = ? AND user_id = ?", userSubscriptionId, owner.UserId).First(&sub).Error; err != nil {
			return err
		}
		userId = sub.UserId
		if err := tx.Model(&sub).Updates(map[string]any{
			"status":     "cancelled",
			"end_time":   now,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
		if err != nil {
			return err
		}
		if target != "" {
			cacheGroup = target
			downgradeGroup = target
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if cacheGroup != "" && userId > 0 {
		refreshSubscriptionUserGroupCache(userId, common.LogText("admin subscription update"))
	}
	if downgradeGroup != "" {
		return common.NewMessage("User group will be reverted to {{group}}", map[string]any{"group": downgradeGroup}), nil
	}
	return nil, nil
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
func AdminDeleteUserSubscription(userSubscriptionId int) (*common.Message, error) {
	if userSubscriptionId <= 0 {
		return nil, errors.New("invalid userSubscriptionId")
	}
	now := common.GetTimestamp()
	cacheGroup := ""
	downgradeGroup := ""
	var userId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		// Obtain the immutable owner before taking subscription locks, matching
		// financial source/token/stat transactions (namespace -> user -> sub).
		var owner UserSubscription
		if err := tx.Select("id", "user_id").First(&owner, userSubscriptionId).Error; err != nil {
			return err
		}
		if err := LockFinancialUserTx(tx, owner.UserId); err != nil {
			return err
		}
		var sub UserSubscription
		if err := lockForUpdate(tx).
			Where("id = ? AND user_id = ?", userSubscriptionId, owner.UserId).First(&sub).Error; err != nil {
			return err
		}
		userId = sub.UserId
		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
		if err != nil {
			return err
		}
		if target != "" {
			cacheGroup = target
			downgradeGroup = target
		}
		if err := tx.Where("id = ?", userSubscriptionId).Delete(&UserSubscription{}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if cacheGroup != "" && userId > 0 {
		refreshSubscriptionUserGroupCache(userId, common.LogText("admin subscription deletion"))
	}
	if downgradeGroup != "" {
		return common.NewMessage("User group will be reverted to {{group}}", map[string]any{"group": downgradeGroup}), nil
	}
	return nil, nil
}

func resetUserSubscriptionTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan, now int64, advanceResetTime bool) error {
	if tx == nil || sub == nil || plan == nil {
		return errors.New("invalid reset args")
	}
	if sub.QuotaPeriod < 0 || sub.QuotaPeriod == math.MaxInt64 {
		return errors.New("subscription quota period exhausted")
	}
	sub.QuotaPeriod++
	sub.AmountUsed = 0
	if advanceResetTime {
		nextReset := calcNextResetTime(time.Unix(now, 0), plan, sub.EndTime)
		sub.NextResetTime = nextReset
		if nextReset > 0 {
			sub.LastResetTime = now
		} else {
			sub.LastResetTime = 0
		}
	}
	return tx.Select("amount_used", "quota_period", "last_reset_time", "next_reset_time", "updated_at").Updates(sub).Error
}

func buildSubscriptionResetResult(plan *SubscriptionPlan, subs []UserSubscription, advanceResetTime bool) *SubscriptionResetResult {
	userIds := make([]int, 0, len(subs))
	seenUsers := make(map[int]struct{}, len(subs))
	for _, sub := range subs {
		if _, ok := seenUsers[sub.UserId]; ok {
			continue
		}
		seenUsers[sub.UserId] = struct{}{}
		userIds = append(userIds, sub.UserId)
	}
	return &SubscriptionResetResult{
		PlanId:           plan.Id,
		MatchedCount:     len(subs),
		ResetCount:       len(subs),
		UserCount:        len(userIds),
		AdvanceResetTime: advanceResetTime,
		PlanTitle:        plan.Title,
		AffectedUserIds:  userIds,
	}
}

func adminResetUserSubscriptionsByPlanTx(tx *gorm.DB, userId int, plan *SubscriptionPlan, now int64, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if tx == nil || plan == nil {
		return nil, errors.New("invalid reset args")
	}
	var subs []UserSubscription
	if err := lockForUpdate(tx).
		Where("user_id = ? AND plan_id = ? AND status = ? AND end_time > ?", userId, plan.Id, "active", now).
		Order("end_time asc, id asc").
		Find(&subs).Error; err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, common.NewMessage("This user has no active subscription to this plan")
	}
	for i := range subs {
		if err := resetUserSubscriptionTx(tx, &subs[i], plan, now, advanceResetTime); err != nil {
			return nil, err
		}
	}
	return buildSubscriptionResetResult(plan, subs, advanceResetTime), nil
}

func adminResetPlanSubscriptionsTx(tx *gorm.DB, plan *SubscriptionPlan, now int64, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if tx == nil || plan == nil {
		return nil, errors.New("invalid reset args")
	}
	var subs []UserSubscription
	if err := lockForUpdate(tx).
		Where("plan_id = ? AND status = ? AND end_time > ?", plan.Id, "active", now).
		Order("user_id asc, end_time asc, id asc").
		Find(&subs).Error; err != nil {
		return nil, err
	}
	for i := range subs {
		if err := resetUserSubscriptionTx(tx, &subs[i], plan, now, advanceResetTime); err != nil {
			return nil, err
		}
	}
	return buildSubscriptionResetResult(plan, subs, advanceResetTime), nil
}

func AdminResetUserSubscriptionsByPlan(userId int, planId int, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if userId <= 0 || planId <= 0 {
		return nil, errors.New("invalid userId or planId")
	}
	var result *SubscriptionResetResult
	now := GetDBTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := getSubscriptionPlanByIdTx(tx, planId)
		if err != nil {
			return err
		}
		result, err = adminResetUserSubscriptionsByPlanTx(tx, userId, plan, now, advanceResetTime)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func AdminResetPlanSubscriptions(planId int, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if planId <= 0 {
		return nil, errors.New("invalid planId")
	}
	var result *SubscriptionResetResult
	now := GetDBTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := getSubscriptionPlanByIdTx(tx, planId)
		if err != nil {
			return err
		}
		result, err = adminResetPlanSubscriptionsTx(tx, plan, now, advanceResetTime)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type SubscriptionPreConsumeResult struct {
	UserSubscriptionId int
	QuotaPeriod        int64
	Replayed           bool
	PreConsumed        int64
	ReservedAmount     int64
	AmountTotal        int64
	AmountUsedBefore   int64
	AmountUsedAfter    int64
}

// ExpireDueSubscriptions marks expired subscriptions and handles group downgrade.
func ExpireDueSubscriptions(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	if err := DB.Where("status = ? AND end_time > 0 AND end_time <= ?", "active", now).
		Order("end_time asc, id asc").
		Limit(limit).
		Find(&subs).Error; err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}
	expiredCount := 0
	userIds := make(map[int]struct{}, len(subs))
	for _, sub := range subs {
		if sub.UserId > 0 {
			userIds[sub.UserId] = struct{}{}
		}
	}
	for userId := range userIds {
		cacheGroup := ""
		err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockGroupNamespace(tx); err != nil {
				return err
			}
			if err := LockFinancialUserTx(tx, userId); err != nil {
				return err
			}
			res := tx.Model(&UserSubscription{}).
				Where("user_id = ? AND status = ? AND end_time > 0 AND end_time <= ?", userId, "active", now).
				Updates(map[string]any{
					"status":     "expired",
					"updated_at": common.GetTimestamp(),
				})
			if res.Error != nil {
				return res.Error
			}
			expiredCount += int(res.RowsAffected)

			// If there's an active upgraded subscription, keep current group.
			var activeSub UserSubscription
			activeQuery := tx.Where("user_id = ? AND status = ? AND end_time > ? AND upgrade_group <> ''",
				userId, "active", now).
				Order("end_time desc, id desc").
				Limit(1).
				Find(&activeSub)
			if activeQuery.Error == nil && activeQuery.RowsAffected > 0 {
				return nil
			}

			// Find the most recently expired subscription that defines a group transition
			// (an explicit downgrade target or an upgrade snapshot to revert).
			var lastExpired UserSubscription
			expiredQuery := tx.Where("user_id = ? AND status = ? AND (downgrade_group <> '' OR upgrade_group <> '')",
				userId, "expired").
				Order("end_time desc, id desc").
				Limit(1).
				Find(&lastExpired)
			if expiredQuery.Error != nil || expiredQuery.RowsAffected == 0 {
				return nil
			}
			currentGroup, err := getUserGroupByIdTx(tx, userId)
			if err != nil {
				return err
			}
			// An explicit downgrade group takes precedence; otherwise revert to the
			// group held before purchase (legacy behavior, only when the subscription
			// actually elevated the user).
			target := strings.TrimSpace(lastExpired.DowngradeGroup)
			if target == "" {
				upgradeGroup := strings.TrimSpace(lastExpired.UpgradeGroup)
				prevGroup := strings.TrimSpace(lastExpired.PrevUserGroup)
				if upgradeGroup == "" || prevGroup == "" {
					return nil
				}
				if currentGroup != upgradeGroup {
					return nil
				}
				target = prevGroup
			}
			if target == "" || target == currentGroup {
				return nil
			}
			if err := updateUserGroupTx(tx, userId, target); err != nil {
				return err
			}
			cacheGroup = target
			return nil
		})
		if err != nil {
			return expiredCount, err
		}
		if cacheGroup != "" {
			refreshSubscriptionUserGroupCache(userId, common.LogText("subscription expiration"))
		}
	}
	return expiredCount, nil
}

// SubscriptionPreConsumeRecord stores idempotent pre-consume operations per request.
type SubscriptionPreConsumeRecord struct {
	Id                 int    `json:"id"`
	RequestId          string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId             int    `json:"user_id" gorm:"index"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"index"`
	PreConsumed        int64  `json:"pre_consumed" gorm:"type:bigint;not null;default:0"`
	QuotaPeriod        int64  `json:"quota_period" gorm:"type:bigint;not null;default:0"`
	ReservedAmount     int64  `json:"reserved_amount" gorm:"type:bigint;not null;default:0"`
	SettledAmount      int64  `json:"settled_amount" gorm:"type:bigint;not null;default:0"`
	Status             string `json:"status" gorm:"type:varchar(32);index"` // consumed/settled/refunded
	CreatedAt          int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt          int64  `json:"updated_at" gorm:"bigint;index"`
}

func (r *SubscriptionPreConsumeRecord) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	r.CreatedAt = now
	r.UpdatedAt = now
	return nil
}

func (r *SubscriptionPreConsumeRecord) BeforeUpdate(tx *gorm.DB) error {
	r.UpdatedAt = common.GetTimestamp()
	return nil
}

func maybeResetUserSubscriptionWithPlanTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan, now int64) error {
	if tx == nil || sub == nil || plan == nil {
		return errors.New("invalid reset args")
	}
	if sub.NextResetTime > 0 && sub.NextResetTime > now {
		return nil
	}
	if NormalizeResetPeriod(plan.QuotaResetPeriod) == SubscriptionResetNever {
		return nil
	}
	baseUnix := sub.LastResetTime
	if baseUnix <= 0 {
		baseUnix = sub.StartTime
	}
	base := time.Unix(baseUnix, 0)
	next := calcNextResetTime(base, plan, sub.EndTime)
	advanced := false
	for next > 0 && next <= now {
		advanced = true
		base = time.Unix(next, 0)
		next = calcNextResetTime(base, plan, sub.EndTime)
	}
	if !advanced {
		if sub.NextResetTime == 0 && next > 0 {
			sub.NextResetTime = next
			sub.LastResetTime = base.Unix()
			return tx.Select("next_reset_time", "last_reset_time", "updated_at").Updates(sub).Error
		}
		return nil
	}
	if sub.QuotaPeriod < 0 || sub.QuotaPeriod == math.MaxInt64 {
		return errors.New("subscription quota period exhausted")
	}
	sub.QuotaPeriod++
	sub.AmountUsed = 0
	sub.LastResetTime = base.Unix()
	sub.NextResetTime = next
	return tx.Select("amount_used", "quota_period", "last_reset_time", "next_reset_time", "updated_at").Updates(sub).Error
}

// SubscriptionBillingIdentity binds an operation to the immutable reservation,
// not to whichever allowance happens to be current when a delayed reply arrives.
type SubscriptionBillingIdentity struct {
	RequestID      string
	UserID         int
	SubscriptionID int
	QuotaPeriod    int64
	PreConsumed    int64
}

func (r *SubscriptionPreConsumeRecord) billingIdentity() SubscriptionBillingIdentity {
	return SubscriptionBillingIdentity{
		RequestID: r.RequestId, UserID: r.UserId, SubscriptionID: r.UserSubscriptionId,
		QuotaPeriod: r.QuotaPeriod, PreConsumed: r.PreConsumed,
	}
}

func (r *SubscriptionPreConsumeRecord) validateBillingReceipt() error {
	if r.RequestId == "" || strings.TrimSpace(r.RequestId) != r.RequestId || len(r.RequestId) > 64 || r.UserId <= 0 || r.UserSubscriptionId <= 0 || r.PreConsumed <= 0 || r.PreConsumed > common.MaxQuota {
		return billingRefundReviewError("receipt_invalid", "invalid subscription reservation identity")
	}
	if r.QuotaPeriod <= 0 {
		// A migration default does not prove which allowance held the debit.
		// In particular, a reset timestamp cannot distinguish a manual reset.
		return billingRefundReviewError("subscription_period_unknown", "subscription reservation has no original quota period; manual reconciliation required")
	}
	if r.ReservedAmount < r.PreConsumed || r.ReservedAmount > common.MaxQuota || r.SettledAmount < 0 || r.SettledAmount > common.MaxQuota {
		return billingRefundReviewError("receipt_invalid", "invalid subscription reservation amounts")
	}
	switch r.Status {
	case "consumed", "refunded":
		if r.SettledAmount != 0 {
			return billingRefundReviewError("receipt_invalid", "unsettled subscription reservation has a settled amount")
		}
	case "settled":
	default:
		return billingRefundReviewError("receipt_invalid", "invalid subscription reservation state")
	}
	return nil
}

func lockSubscriptionBillingReceiptTx(tx *gorm.DB, identity SubscriptionBillingIdentity) (*SubscriptionPreConsumeRecord, error) {
	if tx == nil {
		return nil, errors.New("nil subscription billing transaction")
	}
	var record SubscriptionPreConsumeRecord
	if err := lockForUpdate(tx).Where("request_id = ?", identity.RequestID).First(&record).Error; err != nil {
		return nil, err
	}
	if err := record.validateBillingReceipt(); err != nil {
		return nil, err
	}
	if record.billingIdentity() != identity {
		return nil, billingRefundReviewError("identity_mismatch", "subscription reservation identity mismatch")
	}
	return &record, nil
}

// GetSubscriptionBillingIdentityTx recovers the immutable base from authoritative
// evidence for callers whose task/terminal receipt stores the total held amount.
// The caller must supply the original user, subscription, period and exact hold.
func GetSubscriptionBillingIdentityTx(tx *gorm.DB, requestID string, userID, subscriptionID int, period, held int64) (SubscriptionBillingIdentity, error) {
	var record SubscriptionPreConsumeRecord
	if tx == nil {
		return SubscriptionBillingIdentity{}, errors.New("nil subscription billing transaction")
	}
	if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&record).Error; err != nil {
		return SubscriptionBillingIdentity{}, err
	}
	if err := record.validateBillingReceipt(); err != nil {
		return SubscriptionBillingIdentity{}, err
	}
	if record.RequestId != requestID || record.UserId != userID || record.UserSubscriptionId != subscriptionID || record.QuotaPeriod != period || record.ReservedAmount != held {
		return SubscriptionBillingIdentity{}, billingRefundReviewError("identity_mismatch", "subscription held evidence mismatch")
	}
	return record.billingIdentity(), nil
}

func ValidateSubscriptionPreConsumeTx(tx *gorm.DB, identity SubscriptionBillingIdentity, held int64) error {
	record, err := lockSubscriptionBillingReceiptTx(tx, identity)
	if err != nil {
		return err
	}
	if record.Status != "consumed" {
		return ErrBillingFinalized
	}
	if record.ReservedAmount != held {
		return billingRefundReviewError("source_evidence", "subscription held amount mismatch")
	}
	return nil
}

// ReadSubscriptionPreConsumeTx returns the current authoritative reservation.
// Callers use it inside the host-owned transaction, before updating local state.
func ReadSubscriptionPreConsumeTx(tx *gorm.DB, identity SubscriptionBillingIdentity) (*SubscriptionPreConsumeResult, error) {
	record, err := lockSubscriptionBillingReceiptTx(tx, identity)
	if err != nil {
		return nil, err
	}
	if record.Status != "consumed" {
		return nil, ErrBillingFinalized
	}
	var sub UserSubscription
	if err := lockForUpdate(tx).Where("id = ?", identity.SubscriptionID).First(&sub).Error; err != nil {
		return nil, err
	}
	if sub.UserId != identity.UserID {
		return nil, billingRefundReviewError("identity_mismatch", "subscription owner mismatch")
	}
	return &SubscriptionPreConsumeResult{
		UserSubscriptionId: sub.Id, QuotaPeriod: record.QuotaPeriod, Replayed: true,
		PreConsumed: record.PreConsumed, ReservedAmount: record.ReservedAmount,
		AmountTotal: sub.AmountTotal, AmountUsedBefore: sub.AmountUsed, AmountUsedAfter: sub.AmountUsed,
	}, nil
}

// PreConsumeUserSubscription claims the request ID before taking an allowance
// lock. ON CONFLICT keeps PostgreSQL races usable, and the locking read also
// observes a MySQL winner without relying on an earlier repeatable-read snapshot.
func PreConsumeUserSubscription(requestId string, userId int, modelName string, quotaType int, amount int64) (*SubscriptionPreConsumeResult, error) {
	var result *SubscriptionPreConsumeResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = PreConsumeUserSubscriptionTx(tx, requestId, userId, modelName, quotaType, amount)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// PreConsumeUserSubscriptionTx permits source, token and host reservation owner
// to commit together. It never opens a nested/global database transaction.
func PreConsumeUserSubscriptionTx(tx *gorm.DB, requestId string, userId int, modelName string, quotaType int, amount int64) (*SubscriptionPreConsumeResult, error) {
	if tx == nil {
		return nil, errors.New("nil subscription pre-consume transaction")
	}
	if userId <= 0 || requestId == "" || strings.TrimSpace(requestId) != requestId || len(requestId) > 64 {
		return nil, errors.New("invalid subscription request identity")
	}
	if amount <= 0 || amount > common.MaxQuota {
		return nil, errors.New("invalid subscription pre-consume amount")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	building := "b:" + hex.EncodeToString(nonce[:])
	result := &SubscriptionPreConsumeResult{}
	claim := SubscriptionPreConsumeRecord{RequestId: requestId, UserId: userId, PreConsumed: amount, Status: building}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&claim).Error; err != nil {
		return nil, err
	}
	var record SubscriptionPreConsumeRecord
	if err := lockForUpdate(tx).Where("request_id = ?", requestId).First(&record).Error; err != nil {
		return nil, err
	}
	// Always verify caller identity, including conflict/replay/terminal paths.
	if record.RequestId != requestId || record.UserId != userId || record.PreConsumed != amount {
		return nil, billingRefundReviewError("identity_mismatch", "subscription pre-consume replay identity mismatch")
	}
	if record.Status != building {
		if err := record.validateBillingReceipt(); err != nil {
			return nil, err
		}
		if record.Status != "consumed" {
			return nil, ErrBillingFinalized
		}
		var sub UserSubscription
		if err := lockForUpdate(tx).Where("id = ?", record.UserSubscriptionId).First(&sub).Error; err != nil {
			return nil, err
		}
		if sub.UserId != userId {
			return nil, billingRefundReviewError("identity_mismatch", "subscription owner mismatch")
		}
		*result = SubscriptionPreConsumeResult{
			UserSubscriptionId: sub.Id, QuotaPeriod: record.QuotaPeriod, Replayed: true,
			PreConsumed: record.PreConsumed, ReservedAmount: record.ReservedAmount,
			AmountTotal: sub.AmountTotal, AmountUsedBefore: sub.AmountUsed, AmountUsedAfter: sub.AmountUsed,
		}
		return result, nil
	}

	now := GetDBTimestamp(tx)
	var subs []UserSubscription
	if err := lockForUpdate(tx).Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Order("end_time asc, id asc").Find(&subs).Error; err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, errors.New("no active subscription")
	}
	for _, sub := range subs {
		plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
		if err != nil {
			return nil, err
		}
		if err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, plan, now); err != nil {
			return nil, err
		}
		if sub.QuotaPeriod < 0 || sub.AmountUsed < 0 || sub.AmountTotal < 0 || sub.AmountUsed > math.MaxInt64-amount {
			return nil, billingRefundReviewError("source_evidence", "invalid subscription allowance")
		}
		if sub.AmountTotal > 0 && (sub.AmountUsed > sub.AmountTotal || amount > sub.AmountTotal-sub.AmountUsed) {
			continue
		}
		if sub.QuotaPeriod == 0 {
			sub.QuotaPeriod = 1
		}
		usedBefore := sub.AmountUsed
		sub.AmountUsed += amount
		if err := tx.Select("amount_used", "quota_period", "updated_at").Updates(&sub).Error; err != nil {
			return nil, err
		}
		record.UserSubscriptionId, record.QuotaPeriod = sub.Id, sub.QuotaPeriod
		record.ReservedAmount, record.Status = amount, "consumed"
		if err := tx.Select("user_subscription_id", "quota_period", "reserved_amount", "status", "updated_at").Updates(&record).Error; err != nil {
			return nil, err
		}
		*result = SubscriptionPreConsumeResult{
			UserSubscriptionId: sub.Id, QuotaPeriod: sub.QuotaPeriod, PreConsumed: amount, ReservedAmount: amount,
			AmountTotal: sub.AmountTotal, AmountUsedBefore: usedBefore, AmountUsedAfter: sub.AmountUsed,
		}
		return result, nil
	}
	return nil, fmt.Errorf("subscription quota insufficient, need=%d", amount)
}

// applySubscriptionReceiptDeltaTx changes only the original allowance. A reset
// retires that allowance: closing its receipt must not debit or credit the new
// period. Current-period underflow is corrupt evidence, never a clamp-to-zero.
func applySubscriptionReceiptDeltaTx(tx *gorm.DB, record *SubscriptionPreConsumeRecord, delta int64) error {
	return AdjustSubscriptionQuotaPeriodTx(tx, record.UserId, record.UserSubscriptionId, record.QuotaPeriod, delta)
}

// AdjustSubscriptionQuotaPeriodTx is for adjustments owned by another atomic
// financial operation (for example an independent fee). It is not idempotent by
// itself: the caller must hold and commit its unique operation/terminal marker.
func AdjustSubscriptionQuotaPeriodTx(tx *gorm.DB, userID, subscriptionID int, period, delta int64) error {
	if tx == nil || userID <= 0 || subscriptionID <= 0 || period <= 0 || delta < -common.MaxQuota || delta > common.MaxQuota {
		return billingRefundReviewError("receipt_invalid", "invalid subscription period adjustment")
	}
	var sub UserSubscription
	if err := lockForUpdate(tx).Where("id = ?", subscriptionID).First(&sub).Error; err != nil {
		return err
	}
	if sub.UserId != userID || sub.QuotaPeriod < period {
		return billingRefundReviewError("identity_mismatch", "subscription allowance identity mismatch")
	}
	if sub.QuotaPeriod != period {
		if delta > 0 {
			// The retired period's total usage is no longer available. Charging
			// the new period or assuming free capacity would both invent policy.
			return billingRefundReviewError("subscription_period_changed", "cannot increase usage in a retired subscription allowance")
		}
		return nil
	}
	if sub.AmountUsed < 0 || sub.AmountTotal < 0 || delta > 0 && sub.AmountUsed > math.MaxInt64-delta || delta < 0 && sub.AmountUsed < -delta {
		return billingRefundReviewError("source_evidence", "subscription allowance cannot apply reservation delta")
	}
	newUsed := sub.AmountUsed + delta
	if sub.AmountTotal > 0 && newUsed > sub.AmountTotal {
		return fmt.Errorf("subscription used exceeds total, used=%d total=%d", newUsed, sub.AmountTotal)
	}
	sub.AmountUsed = newUsed
	return tx.Select("amount_used", "updated_at").Updates(&sub).Error
}

// AdjustSubscriptionTaskQuotaTx reconciles a previously settled request after
// asynchronous task completion. The caller must lock its original task, verify
// the task's ownership, and persist its terminal marker in this transaction.
// A changed expected amount is a conflict, never evidence of a safe replay.
func AdjustSubscriptionTaskQuotaTx(tx *gorm.DB, identity SubscriptionBillingIdentity, previousQuota, actualQuota int64) error {
	if previousQuota < 0 || previousQuota > common.MaxQuota || actualQuota < 0 || actualQuota > common.MaxQuota {
		return errors.New("invalid subscription task amount")
	}
	record, err := lockSubscriptionBillingReceiptTx(tx, identity)
	if err != nil {
		return err
	}
	if record.Status != "settled" || record.SettledAmount != previousQuota {
		return billingRefundReviewError("source_evidence", "subscription task settled evidence mismatch")
	}
	if err := applySubscriptionReceiptDeltaTx(tx, record, actualQuota-previousQuota); err != nil {
		return err
	}
	record.SettledAmount = actualQuota
	return tx.Select("settled_amount", "updated_at").Updates(record).Error
}

// ReserveSubscriptionPreConsume raises an absolute durable hold. A retry after
// a lost commit ACK therefore cannot reserve the same increment a second time.
func ReserveSubscriptionPreConsume(identity SubscriptionBillingIdentity, target int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		return ReserveSubscriptionPreConsumeTx(tx, identity, target)
	})
}

func ReserveSubscriptionPreConsumeTx(tx *gorm.DB, identity SubscriptionBillingIdentity, target int64) error {
	if target < 0 || target > common.MaxQuota {
		return errors.New("invalid subscription reserve target")
	}
	record, err := lockSubscriptionBillingReceiptTx(tx, identity)
	if err != nil {
		return err
	}
	if record.Status != "consumed" {
		return ErrBillingFinalized
	}
	if target <= record.ReservedAmount {
		return nil
	}
	var sub UserSubscription
	if err := lockForUpdate(tx).Where("id = ?", record.UserSubscriptionId).First(&sub).Error; err != nil {
		return err
	}
	plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
	if err != nil {
		return err
	}
	now := GetDBTimestamp(tx)
	if err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, plan, now); err != nil {
		return err
	}
	if sub.UserId != identity.UserID || sub.QuotaPeriod != identity.QuotaPeriod || sub.Status != "active" || sub.EndTime <= now {
		return billingRefundReviewError("subscription_period_changed", "cannot reserve a retired subscription allowance")
	}
	if err := applySubscriptionReceiptDeltaTx(tx, record, target-record.ReservedAmount); err != nil {
		return err
	}
	record.ReservedAmount = target
	return tx.Select("reserved_amount", "updated_at").Updates(record).Error
}

// SettleSubscriptionPreConsumeTx finalizes the source in the caller's transaction
// so its token/terminal owner can commit with the same absolute final amount.
func SettleSubscriptionPreConsumeTx(tx *gorm.DB, identity SubscriptionBillingIdentity, actual int64) (bool, error) {
	if actual < 0 || actual > common.MaxQuota {
		return false, errors.New("invalid subscription settled amount")
	}
	record, err := lockSubscriptionBillingReceiptTx(tx, identity)
	if err != nil {
		return false, err
	}
	if record.Status == "settled" {
		if record.SettledAmount != actual {
			return false, billingRefundReviewError("identity_mismatch", "subscription settlement replay amount mismatch")
		}
		return false, nil
	}
	if record.Status != "consumed" {
		return false, ErrBillingFinalized
	}
	if err := applySubscriptionReceiptDeltaTx(tx, record, actual-record.ReservedAmount); err != nil {
		return false, err
	}
	record.Status, record.SettledAmount = "settled", actual
	if err := tx.Select("status", "settled_amount", "updated_at").Updates(record).Error; err != nil {
		return false, err
	}
	return true, nil
}

func RefundSubscriptionPreConsumeTx(tx *gorm.DB, identity SubscriptionBillingIdentity) (bool, error) {
	record, err := lockSubscriptionBillingReceiptTx(tx, identity)
	if err != nil {
		return false, err
	}
	if record.Status == "refunded" {
		return false, nil
	}
	if record.Status != "consumed" {
		return false, ErrBillingFinalized
	}
	if err := applySubscriptionReceiptDeltaTx(tx, record, -record.ReservedAmount); err != nil {
		return false, err
	}
	record.Status = "refunded"
	if err := tx.Select("status", "updated_at").Updates(record).Error; err != nil {
		return false, err
	}
	return true, nil
}

// RefundSubscriptionPreConsume is the request-only compatibility entry point.
// Both the allowance and its receipt commit together; a retry is a true no-op.
func RefundSubscriptionPreConsume(requestId string) error {
	if requestId == "" || strings.TrimSpace(requestId) != requestId || len(requestId) > 64 {
		return errors.New("invalid subscription request identity")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var record SubscriptionPreConsumeRecord
		if err := lockForUpdate(tx).Where("request_id = ?", requestId).First(&record).Error; err != nil {
			return err
		}
		if record.RequestId != requestId {
			return billingRefundReviewError("identity_mismatch", "subscription refund request identity mismatch")
		}
		_, err := RefundSubscriptionPreConsumeTx(tx, record.billingIdentity())
		return err
	})
}

// ResetDueSubscriptions resets subscriptions whose next_reset_time has passed.
func ResetDueSubscriptions(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	if err := DB.Where("next_reset_time > 0 AND next_reset_time <= ? AND status = ?", now, "active").
		Order("next_reset_time asc").
		Limit(limit).
		Find(&subs).Error; err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}
	resetCount := 0
	for _, sub := range subs {
		subCopy := sub
		plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
		if err != nil || plan == nil {
			continue
		}
		err = DB.Transaction(func(tx *gorm.DB) error {
			var locked UserSubscription
			if err := lockForUpdate(tx).
				Where("id = ? AND next_reset_time > 0 AND next_reset_time <= ?", subCopy.Id, now).
				First(&locked).Error; err != nil {
				return nil
			}
			if err := maybeResetUserSubscriptionWithPlanTx(tx, &locked, plan, now); err != nil {
				return err
			}
			resetCount++
			return nil
		})
		if err != nil {
			return resetCount, err
		}
	}
	return resetCount, nil
}

// CleanupSubscriptionPreConsumeRecords deliberately retains financial receipts.
// A time-based deletion can erase an in-flight task/refund or allow a finalized
// request ID to reserve again. GC needs an explicit shared retention contract
// with the durable request and task journals; age alone is not such proof.
func CleanupSubscriptionPreConsumeRecords(olderThanSeconds int64) (int64, error) {
	return 0, nil
}

type SubscriptionPlanInfo struct {
	PlanId    int
	PlanTitle string
}

func GetSubscriptionPlanInfoByUserSubscriptionId(userSubscriptionId int) (*SubscriptionPlanInfo, error) {
	if userSubscriptionId <= 0 {
		return nil, errors.New("invalid userSubscriptionId")
	}
	cacheKey := fmt.Sprintf("sub:%d", userSubscriptionId)
	if cached, found, err := getSubscriptionPlanInfoCache().Get(cacheKey); err == nil && found {
		return &cached, nil
	}
	var sub UserSubscription
	if err := DB.Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
		return nil, err
	}
	plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
	if err != nil {
		return nil, err
	}
	info := &SubscriptionPlanInfo{
		PlanId:    sub.PlanId,
		PlanTitle: plan.Title,
	}
	_ = getSubscriptionPlanInfoCache().SetWithTTL(cacheKey, *info, subscriptionPlanInfoCacheTTL())
	return info, nil
}

// PostConsumeUserSubscriptionDelta is a legacy, periodless compatibility API.
// Request billing must use the receipt-bound APIs above, never this delta API.
func PostConsumeUserSubscriptionDelta(userSubscriptionId int, delta int64) error {
	if userSubscriptionId <= 0 {
		return errors.New("invalid userSubscriptionId")
	}
	if delta == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := lockForUpdate(tx).
			Where("id = ?", userSubscriptionId).
			First(&sub).Error; err != nil {
			return err
		}
		if sub.AmountUsed < 0 || delta > 0 && sub.AmountUsed > math.MaxInt64-delta || delta == math.MinInt64 {
			return errors.New("invalid subscription quota delta")
		}
		newUsed := max(sub.AmountUsed+delta, 0)
		if sub.AmountTotal > 0 && newUsed > sub.AmountTotal {
			return fmt.Errorf("subscription used exceeds total, used=%d total=%d", newUsed, sub.AmountTotal)
		}
		sub.AmountUsed = newUsed
		return tx.Save(&sub).Error
	})
}
