package model

import (
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentSimpleMigration struct {
	ID          int   `json:"id" gorm:"primaryKey;autoIncrement:false"`
	Transferred int64 `json:"transferred_quota" gorm:"type:bigint;not null"`
	Credited    int64 `json:"credited_quota" gorm:"type:bigint;not null"`
	UserCount   int   `json:"user_count" gorm:"not null"`
	CompletedAt int64 `json:"completed_at" gorm:"not null"`
}

// AgentSimpleCutoverPreflight preserves the retired-ledger gate on first startup.
// Only a committed A07 migration receipt enables its live additive schema.
func AgentSimpleCutoverPreflight(db *gorm.DB) ([]NonAgentCutoverBlock, error) {
	if db == nil {
		return nil, ErrAgentSimpleInvalid
	}
	completed := false
	if db.Migrator().HasTable(&AgentSimpleMigration{}) {
		var migration AgentSimpleMigration
		err := db.First(&migration, 1).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		completed = err == nil && migration.CompletedAt > 0 && migration.Transferred >= 0 && migration.Transferred == migration.Credited && migration.UserCount >= 0
		if err == nil && !completed {
			return nil, ErrAgentSimpleInvariant
		}
	}
	return nonAgentCutoverPreflight(db, completed)
}

// MigrateAgentSimpleA07WithDB runs only against the caller's startup database.
// A single transaction preserves both sides of every invitation transfer and
// records totals. Only history proven retired by the existing cutover rules is
// accepted; unknown or outstanding obligations still require a separate plan.
func MigrateAgentSimpleA07WithDB(db *gorm.DB) error {
	if db == nil {
		return ErrAgentSimpleInvalid
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var migration AgentSimpleMigration
		err := lockForUpdate(tx).First(&migration, 1).Error
		if err == nil {
			if migration.CompletedAt <= 0 || migration.Transferred < 0 || migration.Transferred != migration.Credited || migration.UserCount < 0 {
				return ErrAgentSimpleInvariant
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// Run the complete read-only retirement proof before creating our receipt:
		// an unfinished receipt is itself unknown agent history. This retains the
		// existing evidence checks for reclaimed promotions and settled accounts,
		// and rejects unknown tables instead of maintaining a weaker allowlist.
		if _, err := nonAgentCutoverPreflight(tx, false); err != nil {
			return fmt.Errorf("A07 migration requires review of existing agent financial history: %w", err)
		}
		var pending int64
		if err := tx.Model(&ReferralRewardOutbox{}).Where("status IS NULL OR status <> 'done'").Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return errors.New("A07 migration requires review of existing ordinary referral obligations")
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&AgentSimpleMigration{ID: 1}).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&migration, 1).Error; err != nil {
			return err
		}
		// Another startup may have committed while this transaction waited for
		// the unique receipt. Never transfer the same invitation balance twice.
		if migration.CompletedAt > 0 {
			if migration.Transferred < 0 || migration.Transferred != migration.Credited || migration.UserCount < 0 {
				return ErrAgentSimpleInvariant
			}
			return nil
		}
		var users []User
		if err := lockForUpdate(tx).Where("aff_quota <> 0").Order("id").Find(&users).Error; err != nil {
			return err
		}
		for _, user := range users {
			if user.AffQuota < 0 || migration.Transferred > math.MaxInt64-int64(user.AffQuota) {
				return ErrAgentSimpleInvariant
			}
			if _, err := CreditQuotaTx(tx, LedgerEntry{UserId: user.Id, Account: AccountPaid, Delta: user.AffQuota, Reason: "a07_referral_migration", ReferenceType: "a07_migration", ReferenceId: fmt.Sprint(user.Id), IdempotencyKey: fmt.Sprintf("a07_referral_migration:%d", user.Id)}); err != nil {
				return err
			}
			result := tx.Model(&User{}).Where("id = ? AND aff_quota = ?", user.Id, user.AffQuota).UpdateColumn("aff_quota", 0)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrAgentSimpleConflict
			}
			migration.Transferred += int64(user.AffQuota)
			migration.Credited += int64(user.AffQuota)
			migration.UserCount++
		}
		if migration.Transferred != migration.Credited {
			return ErrAgentSimpleInvariant
		}
		migration.CompletedAt = common.GetTimestamp()
		return tx.Save(&migration).Error
	})
}
