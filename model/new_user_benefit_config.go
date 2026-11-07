package model

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxNewUserBenefitCents int64 = 100_000_000

var (
	ErrNewUserBenefitConfigChanged = errors.New("New user benefit settings changed. Reload and confirm the current amounts.")
	ErrNewUserBenefitInvalidConfig = errors.New("Enter valid CNY amounts with at most two decimal places. Total credit must be at least the payment amount.")
	newUserBenefitMoneyPattern     = regexp.MustCompile(`^(0|[1-9][0-9]{0,6})(\.[0-9]{1,2})?$`)
)

// NewUserBenefitConfig is read directly from the primary database. A single row
// and version serialize all three settings across application instances.
type NewUserBenefitConfig struct {
	ID                int   `gorm:"primaryKey;autoIncrement:false"`
	Enabled           bool  `gorm:"not null"`
	PayAmountCents    int64 `gorm:"not null"`
	CreditAmountCents int64 `gorm:"not null"`
	Version           int64 `gorm:"not null"`
	UpdatedAt         int64 `gorm:"not null"`
	UpdatedBy         int   `gorm:"not null"`
}

// The audit snapshot commits with the configuration, so a failed audit write
// cannot publish a price that has no corresponding change record.
type NewUserBenefitConfigAudit struct {
	ID                int64 `gorm:"primaryKey"`
	Version           int64 `gorm:"not null;uniqueIndex"`
	Enabled           bool  `gorm:"not null"`
	PayAmountCents    int64 `gorm:"not null"`
	CreditAmountCents int64 `gorm:"not null"`
	ActorID           int   `gorm:"not null"`
	CreatedAt         int64 `gorm:"not null"`
}

func defaultNewUserBenefitConfig() NewUserBenefitConfig {
	return NewUserBenefitConfig{ID: 1, PayAmountCents: 10, CreditAmountCents: 100}
}

// ParseNewUserBenefitCNY accepts decimal currency strings, never float input or
// exponent notation. Bounds are checked before converting to integer cents.
func ParseNewUserBenefitCNY(amount string) (int64, error) {
	if !newUserBenefitMoneyPattern.MatchString(amount) {
		return 0, ErrNewUserBenefitInvalidConfig
	}
	whole, fraction, _ := strings.Cut(amount, ".")
	for len(fraction) < 2 {
		fraction += "0"
	}
	cents, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil || cents <= 0 || cents > MaxNewUserBenefitCents {
		return 0, ErrNewUserBenefitInvalidConfig
	}
	return cents, nil
}

func FormatNewUserBenefitCNY(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func ValidateNewUserBenefitConfig(config NewUserBenefitConfig) error {
	if config.PayAmountCents <= 0 || config.PayAmountCents > MaxNewUserBenefitCents ||
		config.CreditAmountCents < config.PayAmountCents || config.CreditAmountCents > MaxNewUserBenefitCents {
		return ErrNewUserBenefitInvalidConfig
	}
	return nil
}

func GetNewUserBenefitConfig(ctx context.Context) (*NewUserBenefitConfig, error) {
	config := defaultNewUserBenefitConfig()
	err := DB.WithContext(ctx).First(&config, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &config, nil
	}
	if err != nil {
		return nil, err
	}
	if err = ValidateNewUserBenefitConfig(config); err != nil {
		return nil, err
	}
	return &config, nil
}

// LockNewUserBenefitConfigTx is shared by administrator edits and new order
// admission. INSERT ... ON CONFLICT also serializes the first-ever save.
func LockNewUserBenefitConfigTx(tx *gorm.DB) (*NewUserBenefitConfig, error) {
	config := defaultNewUserBenefitConfig()
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&config).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).First(&config, 1).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

func UpdateNewUserBenefitConfig(ctx context.Context, expectedVersion int64, actorID int, next NewUserBenefitConfig) (*NewUserBenefitConfig, error) {
	if actorID <= 0 || expectedVersion < 0 || expectedVersion >= 9_007_199_254_740_991 {
		return nil, ErrNewUserBenefitInvalidConfig
	}
	if err := ValidateNewUserBenefitConfig(next); err != nil {
		return nil, err
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := LockNewUserBenefitConfigTx(tx)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return ErrNewUserBenefitConfigChanged
		}
		next.ID = 1
		next.Version = current.Version + 1
		next.UpdatedBy = actorID
		next.UpdatedAt = time.Now().Unix()
		result := tx.Model(&NewUserBenefitConfig{}).Where("id = ? AND version = ?", 1, expectedVersion).
			Select("enabled", "pay_amount_cents", "credit_amount_cents", "version", "updated_at", "updated_by").Updates(&next)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNewUserBenefitConfigChanged
		}
		return tx.Create(&NewUserBenefitConfigAudit{
			Version: next.Version, Enabled: next.Enabled, PayAmountCents: next.PayAmountCents,
			CreditAmountCents: next.CreditAmountCents, ActorID: actorID, CreatedAt: next.UpdatedAt,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &next, nil
}
