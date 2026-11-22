//go:build ignore

// Seed synthetic users only in the fixed, disposable A07 preview database.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func main() {
	previewHome, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	database := filepath.Join(previewHome, "Library/Application Support/WanchuanA07Preview/preview.db")
	if _, err := os.Stat(database); err != nil || os.Getenv("A07_PREVIEW_PASSWORD") == "" {
		panic("start the isolated preview database and supply its synthetic password first")
	}
	db, err := gorm.Open(sqlite.Open(database), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	hash, err := common.Password2Hash(os.Getenv("A07_PREVIEW_PASSWORD"))
	if err != nil {
		panic(err)
	}
	const unit = 500_000
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, fixture := range []struct {
			ID, Role int
			Name     string
		}{{87001, common.RoleRootUser, "a07_root"}, {87002, common.RoleCommonUser, "a07_andy"}, {87003, common.RoleCommonUser, "a07_user"}} {
			var count int64
			if err := tx.Model(&model.User{}).Where("id = ?", fixture.ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			user := model.User{Id: fixture.ID, Username: fixture.Name, Password: hash, DisplayName: fixture.Name, Role: fixture.Role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: fixture.Name, Quota: 220 * unit, QuotaPaid: 200 * unit, QuotaBonus: 20 * unit}
			if fixture.ID == 87002 {
				user.AffQuota, user.AffHistoryQuota, user.AffCount = 35*unit, 35*unit, 1
			}
			if fixture.ID == 87003 {
				user.InviterId, user.Quota, user.QuotaBonus = 87002, 223*unit, 23*unit
			}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AgentProfile{UserID: 87002, Status: model.AgentProfileActive, Version: 1, UpdatedBy: 87001, Reason: "synthetic local preview fixture"}).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AgentSimpleAccount{AgentUserID: 87002, Status: model.AgentSimpleStatusActive, CashQuota: 1000 * unit, CreditLimitQuota: 1000 * unit, Version: 1, LastActivityAt: time.Now().Unix()}).Error; err != nil {
			return err
		}
		for _, reward := range []model.ReferralCommission{
			{UserId: 87002, SourceUserId: 87003, ReferenceType: "topup", ReferenceId: "preview-released", PaidQuota: 600 * unit, RewardQuota: 30 * unit, Rate: .05, Status: model.ReferralCommissionStatusReleased, MaturesAt: time.Now().Add(-24 * time.Hour).Unix()},
			{UserId: 87002, SourceUserId: 87003, ReferenceType: "topup", ReferenceId: "preview-frozen", PaidQuota: 100 * unit, RewardQuota: 5 * unit, Rate: .05, Status: model.ReferralCommissionStatusFrozen, MaturesAt: time.Now().Add(5 * 24 * time.Hour).Unix()},
		} {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&reward).Error; err != nil {
				return err
			}
		}
		var setupCount int64
		if err := tx.Model(&model.Setup{}).Count(&setupCount).Error; err != nil {
			return err
		}
		if setupCount == 0 {
			if err := tx.Create(&model.Setup{Version: "a07-local-preview", InitializedAt: time.Now().Unix()}).Error; err != nil {
				return err
			}
		}
		for _, option := range []model.Option{{Key: "ServerAddress", Value: "http://127.0.0.1:3107"}, {Key: "PayAddress", Value: "https://payment.example.test"}, {Key: "EpayId", Value: "preview-merchant"}, {Key: "EpayKey", Value: "preview-test-key"}, {Key: "PayMethods", Value: `[{"type":"alipay","name":"支付宝"}]`}, {Key: "payment_setting.compliance_confirmed", Value: "true"}, {Key: "payment_setting.compliance_terms_version", Value: "v1"}} {
			if err := tx.Where("key = ?", option.Key).Assign(map[string]any{"value": option.Value}).FirstOrCreate(&option).Error; err != nil {
				return err
			}
		}
		fee := model.AgentSimplePaymentFee{PaymentMethod: "alipay", FeeBPS: 100, Enabled: true, Version: 1, UpdatedBy: 87001, UpdatedAt: time.Now().Unix()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&fee).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("Synthetic A07 preview ready; payment.example.test is a non-paying fixture.")
}
