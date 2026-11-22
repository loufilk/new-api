package model

import (
	"net/mail"
	"strings"
	"time"

	"gorm.io/gorm"
)

// VerifiedSupportEmail is deliberately stricter than having a recovery/profile
// email. Legacy records remain unverified until a new proof is completed. Read
// this from the primary user row, never from a profile DTO or cached snapshot.
func VerifiedSupportEmail(user *User) bool {
	if user == nil || user.EmailVerificationVersion <= 0 || user.EmailVerifiedAt <= 0 {
		return false
	}
	email := NormalizeEmail(user.Email)
	if email == "" || email != user.EmailVerifiedAddress || strings.ContainsAny(email, "\r\n") {
		return false
	}
	parsed, err := mail.ParseAddress(email)
	return err == nil && parsed.Address == email
}

// MarkNewUserEmailVerified is only for server-validated registration evidence:
// a consumed local verification code or Google's authoritative signed email.
// It does not save a user, overwrite an existing user's proof, or infer trust
// from an arbitrary OAuth, payment, administrator or client profile field.
func MarkNewUserEmailVerified(user *User) {
	if user == nil || user.Id != 0 || user.Email == "" {
		return
	}
	user.EmailVerifiedAddress = NormalizeEmail(user.Email)
	user.EmailVerificationVersion = 1
	user.EmailVerifiedAt = time.Now().Unix()
}

func invalidateVerifiedEmailWithTx(tx *gorm.DB, userID int) error {
	return tx.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
		"email_verified_address": "", "email_verified_at": 0,
		"email_verification_version": gorm.Expr("COALESCE(email_verification_version, 0) + 1"),
	}).Error
}
