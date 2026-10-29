package model

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type Redemption struct {
	BudgetAgentID       int            `json:"budget_agent_id" gorm:"not null;default:0;index"`
	BudgetReserved      bool           `json:"budget_reserved" gorm:"not null;default:false"`
	BudgetVersion       int            `json:"-" gorm:"not null;default:0"`
	CreationOperationID int            `json:"-" gorm:"not null;default:0;index"`
	Id                  int            `json:"id"`
	UserId              int            `json:"user_id"`
	Key                 string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status              int            `json:"status" gorm:"default:1"`
	Name                string         `json:"name" gorm:"index"`
	Quota               int            `json:"quota" gorm:"default:100"`
	CreatedTime         int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime        int64          `json:"redeemed_time" gorm:"bigint"`
	Count               int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId          int            `json:"used_user_id"`
	DeletedAt           gorm.DeletedAt `gorm:"index"`
	ExpiredTime         int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, status string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{})

	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}

	if status != "" {
		now := common.GetTimestamp()
		switch status {
		case "expired":
			query = query.Where(
				"status = ? AND expired_time != 0 AND expired_time < ?",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusEnabled):
			query = query.Where(
				"status = ? AND (expired_time = 0 OR expired_time >= ?)",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusDisabled):
			query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
		case strconv.Itoa(common.RedemptionCodeStatusUsed):
			query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
		}
	}

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, common.NewMessage("ID is empty")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
}

func Redeem(key string, userId int) (quota int, err error) {
	if key == "" {
		return 0, errors.New("redemption code not provided")
	}
	if userId == 0 {
		return 0, errors.New("invalid user id")
	}
	redemption := &Redemption{}

	keyCol := "`key`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		keyCol = `"key"`
	}
	common.RandomSleep()
	var replayTotalAfter int
	ledgerApplied := false
	expired := false
	err = DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		err := lockForUpdate(tx).Where(keyCol+" = ?", key).First(redemption).Error
		if err != nil {
			return errors.New("invalid redemption code")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled || redemption.UsedUserId != 0 || redemption.RedeemedTime != 0 {
			return errors.New("redemption code already used")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			expired = true
			if err := updateRedemptionBudgetTx(tx, redemption, 0, 0, false, "expire"); err != nil {
				return err
			}
			return tx.Model(redemption).Select("budget_reserved", "budget_version").Updates(redemption).Error
		}
		if redemption.BudgetAgentID != 0 {
			if !redemption.BudgetReserved {
				return ErrAgentQuotaInvariant
			}
			if err := updateRedemptionBudgetTx(tx, redemption, userId, 0, true, "redeem"); err != nil {
				return err
			}
		}
		// Compare-and-swap on status: only the transaction that flips
		// enabled -> used may credit quota, so a concurrent redeem of the
		// same code loses here even without a row lock (e.g. on SQLite).
		result := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Updates(map[string]any{
				"redeemed_time":   common.GetTimestamp(),
				"status":          common.RedemptionCodeStatusUsed,
				"used_user_id":    userId,
				"budget_reserved": redemption.BudgetReserved,
				"budget_version":  redemption.BudgetVersion,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("redemption code already used")
		}

		// 兑换码入账并写账本（同一事务；幂等键 = redemption id，兑换码一次性，
		// 数据库状态 CAS 已保证不会被重复兑换）
		ledgerResult, err := CreditQuotaTx(tx, LedgerEntry{
			UserId:        userId,
			Account:       AccountPaid,
			Delta:         redemption.Quota,
			Reason:        LedgerReasonRedemption,
			ReferenceType: "redemption",
			ReferenceId:   strconv.Itoa(redemption.Id),
		})
		if err != nil {
			return err
		}
		if ledgerResult.Replayed {
			return ErrRedemptionUsedImmutable
		}
		replayTotalAfter = ledgerResult.TotalAfter
		ledgerApplied = true
		return nil

	})
	if err != nil {
		// The database error may contain a usable code. Never log its text.
		common.SysError("redemption failed")
		return 0, ErrRedeemFailed
	}
	if expired {
		return 0, ErrRedeemFailed
	}
	if ledgerApplied {
		// 账本提交后失效用户缓存；守卫式增量与失效叠加安全（对已删除哈希跳过）。
		RefreshUserQuotaCache(userId, replayTotalAfter)
	}
	RecordLog(userId, LogTypeTopup, common.NewMessage("Topped up {{quota}} with a redemption code, code ID {{id}}", map[string]any{"quota": logger.FormatQuota(redemption.Quota), "id": redemption.Id}))
	return redemption.Quota, nil
}

func (redemption *Redemption) Insert() error {
	if redemption.BudgetAgentID != 0 || redemption.BudgetReserved {
		return ErrAgentQuotaInvalid
	}
	if redemption.Quota <= 0 {
		return errors.New("redemption quota must be positive")
	}
	if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
		return err
	}
	return DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Create(redemption).Error
}

func (redemption *Redemption) SelectUpdate() error {
	updated, err := UpdateRedemptionScoped(RedemptionScope{Role: common.RoleRootUser, system: true}, RedemptionUpdateRequest{ID: redemption.Id, Status: redemption.Status}, true)
	if err == nil {
		*redemption = *updated
	}
	return err
}

// Update preserves use markers and runs value edits through budget accounting.
// Status transitions must use SelectUpdate or the scoped status-only API.
func (redemption *Redemption) Update() error {
	updated, err := UpdateRedemptionScoped(RedemptionScope{Role: common.RoleRootUser, system: true}, RedemptionUpdateRequest{ID: redemption.Id, Name: redemption.Name, Quota: redemption.Quota, ExpiredTime: redemption.ExpiredTime}, false)
	if err == nil {
		*redemption = *updated
	}
	return err
}

func (redemption *Redemption) Delete() error { return DeleteRedemptionById(redemption.Id) }

func DeleteRedemptionById(id int) error {
	_, err := DeleteRedemptionsScoped(RedemptionScope{Role: common.RoleRootUser, system: true}, []int{id}, false)
	return err
}

func DeleteInvalidRedemptions() (int64, error) {
	return DeleteRedemptionsScoped(RedemptionScope{Role: common.RoleRootUser, system: true}, nil, true)
}

func BatchDeleteRedemptions(ids []int) (int64, error) {
	return DeleteRedemptionsScoped(RedemptionScope{Role: common.RoleRootUser, system: true}, ids, false)
}
