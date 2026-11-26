package model

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	USDTCreating   = "creating"
	USDTPending    = "pending"
	USDTConfirming = "confirming"
	USDTPaid       = "paid"
	USDTExpired    = "expired"
	USDTReview     = "review"
	USDTFailed     = "failed"
)

var (
	ErrUSDTConflict = errors.New("USDT order conflict")
	ErrUSDTReview   = errors.New("USDT order requires review")
)

// Only availability/pricing are stored. Gateway credentials are environment-only.
type USDTTopUpConfig struct {
	ID   int    `gorm:"primaryKey;autoIncrement:false"`
	JSON string `gorm:"type:text;not null"`
}

func LoadUSDTTopUpConfig(ctx context.Context, result any) (bool, error) {
	var config USDTTopUpConfig
	err := DB.WithContext(ctx).First(&config, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(config.JSON), result)
}
func SaveUSDTTopUpConfig(ctx context.Context, config any) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"json"}),
	}).Create(&USDTTopUpConfig{ID: 1, JSON: string(raw)}).Error
}

// Dedicated table: no fiat or manual completion endpoint can settle its rows.
type USDTTopUpOrder struct {
	ID              string  `gorm:"type:varchar(32);primaryKey"`
	UserID          int     `gorm:"not null;uniqueIndex:uniq_usdt_user_request,priority:1;index:idx_usdt_user_created,priority:1"`
	RequestKey      string  `gorm:"type:varchar(80);not null;uniqueIndex:uniq_usdt_user_request,priority:2"`
	CreatedAt       int64   `gorm:"not null;index:idx_usdt_user_created,priority:2,sort:desc"`
	Network         string  `gorm:"type:varchar(16);not null"`
	AmountMinor     int64   `gorm:"not null"`
	PrincipalQuota  int     `gorm:"type:bigint;not null"`
	BonusQuota      int     `gorm:"type:bigint;not null"`
	BonusMinor      int64   `gorm:"not null"`
	Status          string  `gorm:"type:varchar(16);not null"`
	GatewayOrigin   string  `gorm:"type:varchar(255);not null"`
	GatewayMerchant string  `gorm:"type:varchar(80);not null"`
	GatewayTradeID  *string `gorm:"type:varchar(80);uniqueIndex"`
	Address         string  `gorm:"type:varchar(80);not null"`
	CheckoutURL     string  `gorm:"type:varchar(512);not null"`
	USDTAmount      string  `gorm:"type:varchar(40);not null"`
	ExpiresAt       int64   `gorm:"not null"`
	// No event index in GMPay: block replay of the whole transaction per chain.
	ChainTransaction *string `gorm:"type:varchar(100);uniqueIndex"`
	CompletedAt      int64   `gorm:"not null"`
	LastPollAt       int64   `gorm:"not null"`
	ReviewReason     string  `gorm:"type:varchar(32);not null"`
}

// DisplayStatus accounts for elapsed checkout time without mutating the order.
func (o USDTTopUpOrder) DisplayStatus(now int64) string {
	if o.Status == USDTCreating && now > o.CreatedAt+20 {
		return USDTReview
	}
	if o.ExpiresAt > 0 && now > o.ExpiresAt {
		switch o.Status {
		case USDTPending:
			return USDTExpired
		case USDTConfirming:
			return USDTReview
		}
	}
	return o.Status
}

func FindUSDTTopUpRequest(ctx context.Context, userID int, key string) (USDTTopUpOrder, error) {
	var order USDTTopUpOrder
	err := DB.WithContext(ctx).Where("user_id = ? AND request_key = ?", userID, key).First(&order).Error
	return order, err
}
func InsertUSDTTopUpOrder(ctx context.Context, o *USDTTopUpOrder, expected any) (bool, error) {
	raw, err := json.Marshal(expected)
	if err != nil {
		return false, err
	}
	var created bool
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cfg USDTTopUpConfig
		// Serialize admission with disabling. Never hold the lock over provider I/O.
		if err := lockForUpdate(tx).First(&cfg, 1).Error; err != nil {
			return err
		}
		if cfg.JSON != string(raw) {
			return ErrUSDTConflict
		}
		r := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}, {Name: "request_key"}}, DoNothing: true,
		}).Create(o)
		created = r.RowsAffected == 1
		return r.Error
	})
	return created, err
}
func FindUSDTTopUpOrder(ctx context.Context, id string, userID int) (USDTTopUpOrder, error) {
	var order USDTTopUpOrder
	q := DB.WithContext(ctx).Where("id = ?", id)
	if userID > 0 {
		q = q.Where("user_id = ?", userID)
	}
	err := q.First(&order).Error
	return order, err
}
func ListUSDTTopUpOrders(ctx context.Context, userID int) ([]USDTTopUpOrder, error) {
	orders := make([]USDTTopUpOrder, 0)
	err := DB.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC, id DESC").Limit(50).Find(&orders).Error
	return orders, err
}
func SaveUSDTTopUpQuote(ctx context.Context, o USDTTopUpOrder) error {
	r := DB.WithContext(ctx).Model(&USDTTopUpOrder{}).Where("id = ? AND status = ?", o.ID, USDTCreating).Updates(map[string]any{
		"status": USDTPending, "gateway_trade_id": o.GatewayTradeID, "address": o.Address,
		"checkout_url": o.CheckoutURL, "usdt_amount": o.USDTAmount, "expires_at": o.ExpiresAt,
	})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrUSDTConflict
	}
	return nil
}
func SetUSDTTopUpState(ctx context.Context, id, status, reason string, from ...string) error {
	return DB.WithContext(ctx).Model(&USDTTopUpOrder{}).Where("id = ? AND status IN ?", id, from).
		Updates(map[string]any{"status": status, "review_reason": reason}).Error
}
func ClaimUSDTTopUpPoll(ctx context.Context, order USDTTopUpOrder, now int64) (bool, error) {
	r := DB.WithContext(ctx).Model(&USDTTopUpOrder{}).Where(
		"id = ? AND status IN ? AND last_poll_at <= ?", order.ID, []string{USDTPending, USDTConfirming}, now-15,
	).Update("last_poll_at", now)
	return r.RowsAffected == 1, r.Error
}
func SettleUSDTTopUp(ctx context.Context, expected USDTTopUpOrder, hash string, receivedAt int64) error {
	var after, user int
	inputKey := expected.Network + ":" + hash
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order USDTTopUpOrder
		if err := lockForUpdate(tx).Where("id = ?", expected.ID).First(&order).Error; err != nil {
			return err
		}
		if order.GatewayTradeID == nil || expected.GatewayTradeID == nil || *order.GatewayTradeID != *expected.GatewayTradeID ||
			order.GatewayMerchant != expected.GatewayMerchant || order.GatewayOrigin != expected.GatewayOrigin || order.Network != expected.Network ||
			order.Address != expected.Address || order.AmountMinor != expected.AmountMinor || order.USDTAmount != expected.USDTAmount ||
			order.ExpiresAt != expected.ExpiresAt || order.PrincipalQuota != expected.PrincipalQuota || order.BonusQuota != expected.BonusQuota || order.UserID != expected.UserID {
			return ErrUSDTConflict
		}
		if order.Status == USDTPaid {
			if order.ChainTransaction != nil && *order.ChainTransaction == inputKey {
				return nil
			}
			return ErrUSDTConflict
		}
		if order.Status != USDTPending && order.Status != USDTConfirming {
			return ErrUSDTReview
		}
		if receivedAt > order.ExpiresAt || order.PrincipalQuota <= 0 || order.BonusQuota < 0 {
			return ErrUSDTReview
		}
		// First acquire the transaction-hash uniqueness guard; rollback guards and credit together.
		r := tx.Model(&USDTTopUpOrder{}).Where("id = ? AND status IN ?", order.ID, []string{USDTPending, USDTConfirming}).
			Updates(map[string]any{"chain_transaction": inputKey, "status": USDTPaid, "completed_at": time.Now().Unix()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrUSDTConflict
		}
		shares := []LedgerShare{{Account: AccountPaid, Amount: order.PrincipalQuota}}
		if order.BonusQuota > 0 {
			shares = append(shares, LedgerShare{Account: AccountBonus, Amount: order.BonusQuota})
		}
		credit, err := CreditQuotaSharesTx(tx, LedgerEntry{
			UserId: order.UserID, Reason: LedgerReasonTopup, ReferenceType: "usdt_topup", ReferenceId: order.ID, IdempotencyKey: "usdt-topup:" + order.ID,
		}, shares)
		if err != nil {
			return err
		}
		after, user = credit.TotalAfter, order.UserID
		return nil
	})
	if err != nil {
		var existing USDTTopUpOrder
		if lookup := DB.WithContext(ctx).Where("chain_transaction = ? AND id <> ?", inputKey, expected.ID).First(&existing).Error; lookup == nil {
			return ErrUSDTConflict
		}
		return err
	}
	if user > 0 {
		RefreshUserQuotaCache(user, after)
	}
	return nil
}
