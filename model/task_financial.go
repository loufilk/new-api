package model

import (
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrTaskFinancialReview = errors.New("task financial state requires operator reconciliation")

// LoadTaskFinancialTx makes the durable task row the first lock in its financial
// lifecycle. A caller must not replace this current read with a polling snapshot.
func LoadTaskFinancialTx(tx *gorm.DB, id int64, userID int) (*Task, error) {
	if tx == nil || id <= 0 || userID <= 0 {
		return nil, errors.New("invalid task financial identity")
	}
	var task Task
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", id, userID).First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

func LoadMidjourneyFinancialTx(tx *gorm.DB, id, userID int) (*Midjourney, error) {
	if tx == nil || id <= 0 || userID <= 0 {
		return nil, errors.New("invalid Midjourney financial identity")
	}
	var task Midjourney
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", id, userID).First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

// ApplyTaskUsageDeltaTx keeps usage counters in the same transaction as the
// funding and token adjustment. Unlike the batch APIs it cannot acknowledge a
// queued write which later fails. Deleted channels have no counter to restore;
// an existing channel or user update must affect exactly its intended row.
func ApplyTaskUsageDeltaTx(tx *gorm.DB, userID, channelID, delta, requestIncrement int) error {
	if tx == nil || userID <= 0 || delta < -common.MaxQuota || delta > common.MaxQuota || requestIncrement < 0 || requestIncrement > 1 {
		return errors.New("invalid task accounting adjustment")
	}
	if delta == 0 && requestIncrement == 0 {
		return nil
	}
	var user User
	if err := lockForUpdate(tx).Select("id", "used_quota", "request_count").First(&user, userID).Error; err != nil {
		return err
	}
	if delta > 0 && user.UsedQuota > math.MaxInt-delta || delta < 0 && user.UsedQuota < math.MinInt-delta || requestIncrement > 0 && user.RequestCount >= math.MaxInt32 {
		return errors.New("task user accounting adjustment would overflow")
	}
	updates := map[string]any{"used_quota": gorm.Expr("used_quota + ?", delta)}
	if requestIncrement != 0 {
		updates["request_count"] = gorm.Expr("request_count + ?", requestIncrement)
	}
	result := tx.Model(&User{}).Where("id = ?", userID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("task user accounting update affected %d rows", result.RowsAffected)
	}
	if channelID <= 0 || delta == 0 {
		return nil
	}
	var channel Channel
	err := lockForUpdate(tx).Select("id", "used_quota").First(&channel, channelID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if delta > 0 && channel.UsedQuota > math.MaxInt64-int64(delta) || delta < 0 && channel.UsedQuota < math.MinInt64-int64(delta) {
		return errors.New("task channel accounting adjustment would overflow")
	}
	result = tx.Model(&Channel{}).Where("id = ?", channelID).Update("used_quota", gorm.Expr("used_quota + ?", delta))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("task channel accounting update affected %d rows", result.RowsAffected)
	}
	return nil
}

// ValidateTaskWalletReceipt rejects missing or invented original accounts. A
// zero quota is the only case where an empty receipt is complete evidence.
func ValidateTaskWalletReceipt(shares []LedgerShare, quota int) error {
	if quota < 0 || quota > common.MaxQuota {
		return fmt.Errorf("%w: invalid task quota", ErrTaskFinancialReview)
	}
	if _, err := AllocateWalletRefund(shares, nil, quota); err != nil {
		return fmt.Errorf("%w: %v", ErrTaskFinancialReview, err)
	}
	total := 0
	for _, share := range shares {
		if share.Amount > quota-total {
			return fmt.Errorf("%w: task receipt does not match outstanding quota", ErrTaskFinancialReview)
		}
		total += share.Amount
	}
	if total != quota {
		return fmt.Errorf("%w: task receipt does not cover outstanding quota", ErrTaskFinancialReview)
	}
	return nil
}

// RejectTaskSourceOnlyReplayTx protects pre-v2 partial outcomes. A wallet-only
// ledger row cannot prove that the original token, usage or task marker moved.
func RejectTaskSourceOnlyReplayTx(tx *gorm.DB, userID int, keys ...string) error {
	// Use the ledger's user-before-operation lock order, including on MySQL
	// where a missing operation lookup may acquire a next-key lock.
	var user User
	if err := lockForUpdate(tx).Select("id").First(&user, userID).Error; err != nil {
		return err
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		var operation BalanceLedgerOperation
		err := lockForUpdate(tx).Where("idempotency_key = ?", key).First(&operation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: source-only financial operation exists", ErrTaskFinancialReview)
	}
	return nil
}

// LockFinancialUserTx establishes the common account-before-source/token lock
// order for transactions that also update main-database usage statistics.
func LockFinancialUserTx(tx *gorm.DB, userID int) error {
	if tx == nil || userID <= 0 {
		return errors.New("invalid financial user lock")
	}
	var user User
	return lockForUpdate(tx).Select("id").First(&user, userID).Error
}
