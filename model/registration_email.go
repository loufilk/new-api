package model

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const AuthFlowPurposeRegistrationEmail = "registration_email"

func CreateRegistrationEmailCode(ctx context.Context, email, code string, lifetime time.Duration) error {
	email = NormalizeEmail(email)
	if email == "" || len(code) != 8 || lifetime <= 0 {
		return ErrAuthFlowInvalid
	}
	return DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		if err := lockNormalizedEmail(tx, email); err != nil {
			return err
		}
		if err := ensureEmailAvailableWithTx(tx, email, 0); err != nil {
			return err
		}
		return tx.Create(&AuthFlow{
			TokenHash: authFlowTokenHash("register:" + email + ":" + code),
			SessionId: authFlowTokenHash("register-email:" + email),
			Purpose:   AuthFlowPurposeRegistrationEmail, ExpiresAt: time.Now().Add(lifetime),
		}).Error
	})
}

// InsertWithRegistrationEmail consumes the most recently issued code only if
// the verified user record and its referral also commit successfully.
func (user *User) InsertWithRegistrationEmail(inviterID int, code string) error {
	email := NormalizeEmail(user.Email)
	if email == "" || len(code) != 8 {
		return ErrAuthFlowInvalid
	}
	original := *user
	match := AuthFlowMatch{Purpose: AuthFlowPurposeRegistrationEmail, SessionId: authFlowTokenHash("register-email:" + email)}
	if _, err := ConsumeAuthFlowWithAction("register:"+email+":"+code, match, func(tx *gorm.DB, flow *AuthFlow) error {
		tx = tx.Session(&gorm.Session{Logger: logger.Discard})
		if err := lockNormalizedEmail(tx, email); err != nil {
			return err
		}
		var latest int64
		if err := tx.Model(&AuthFlow{}).Select("id").Where("purpose = ? AND session_id = ?", match.Purpose, match.SessionId).Order("id DESC").Limit(1).Scan(&latest).Error; err != nil {
			return err
		}
		if latest != flow.Id {
			return ErrAuthFlowInvalid
		}
		MarkNewUserEmailVerified(user)
		return user.insertWithInvitationTx(tx, inviterID, InvitationSourcePassword)
	}); err != nil {
		*user = original
		return err
	}
	user.finishInsert(user.InviterId)
	return nil
}
