package model

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const AuthFlowPurposePasswordRecovery = "password_recovery"

// A profile address, including an email imported from an OAuth userinfo
// response, is not by itself evidence that the mailbox can recover the user.
func IssuePasswordRecovery(ctx context.Context, email string, lifetime time.Duration) (string, error) {
	var token string
	email = NormalizeEmail(email)
	err := DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		var candidates []User
		if err := lockForUpdate(tx).Where("LOWER(email) = ?", email).Order("id").Limit(2).Find(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) != 1 || candidates[0].Status != common.UserStatusEnabled || !VerifiedSupportEmail(&candidates[0]) {
			return ErrAuthFlowInvalid
		}
		user := candidates[0]
		var err error
		token, _, err = createAuthFlowWithTx(tx, AuthFlowCreate{
			Purpose: AuthFlowPurposePasswordRecovery, UserId: user.Id,
			SessionId: strconv.FormatInt(user.AuthVersion, 10),
			Payload:   strconv.FormatInt(user.EmailVerificationVersion, 10),
			ExpiresAt: time.Now().Add(lifetime),
		})
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// FinishPasswordRecovery atomically consumes the newest eligible flow with
// the password/auth-epoch write. The two epochs make address changes and
// intervening credential changes invalidate outstanding links.
func FinishPasswordRecovery(email, token, password string) (int, error) {
	email = NormalizeEmail(email)
	if email == "" || len(token) != 43 {
		return 0, ErrAuthFlowInvalid
	}
	match := AuthFlowMatch{Purpose: AuthFlowPurposePasswordRecovery}
	if _, err := GetAuthFlow(token, match); err != nil {
		return 0, err
	}
	hashed, err := common.HashAccountPassword(password)
	if err != nil {
		return 0, err
	}
	var userID int
	_, err = ConsumeAuthFlowWithAction(token, match, func(tx *gorm.DB, flow *AuthFlow) error {
		tx = tx.Session(&gorm.Session{Logger: logger.Discard})
		var user User
		if err := lockForUpdate(tx).First(&user, flow.UserId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAuthFlowInvalid
			}
			return err
		}
		if user.Status != common.UserStatusEnabled || NormalizeEmail(user.Email) != email ||
			!VerifiedSupportEmail(&user) || flow.Payload != strconv.FormatInt(user.EmailVerificationVersion, 10) ||
			flow.SessionId != strconv.FormatInt(user.AuthVersion, 10) {
			return ErrAuthFlowInvalid
		}
		var latest int64
		if err := tx.Model(&AuthFlow{}).Select("id").Where("purpose = ? AND user_id = ?", match.Purpose, user.Id).Order("id DESC").Limit(1).Scan(&latest).Error; err != nil {
			return err
		}
		if flow.Id != latest {
			return ErrAuthFlowInvalid
		}
		if _, err := IncrementUserAuthVersionWithTx(tx, user.Id); err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("password", hashed).Error; err != nil {
			return err
		}
		userID = user.Id
		return nil
	})
	if err != nil {
		return 0, err
	}
	return userID, nil
}
