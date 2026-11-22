package model

import (
	"crypto/sha256"
	"encoding/hex"

	"gorm.io/gorm"
)

const (
	ExternalIdentityProviderGoogle = "google"
	GoogleIdentityIssuer           = "https://accounts.google.com"
)

// googleIdentityClaimSubject preserves Google's case-sensitive issuer/subject
// identity within the existing 128-byte claim column. The original subject is
// retained in users.google_id; neither email nor an untrusted issuer is a key.
func googleIdentityClaimSubject(subject string) (string, error) {
	if subject == "" || len(subject) > 255 {
		return "", ErrAccountBindingChanged
	}
	for i := range len(subject) {
		if subject[i] < 0x21 || subject[i] > 0x7e {
			return "", ErrAccountBindingChanged
		}
	}
	digest := sha256.Sum256([]byte(GoogleIdentityIssuer + "\x00" + subject))
	return hex.EncodeToString(digest[:]), nil
}

func IsGoogleIDAlreadyTaken(subject string) bool {
	claimSubject, err := googleIdentityClaimSubject(subject)
	if err != nil {
		return true
	}
	var count int64
	if err := DB.Model(&ExternalIdentityClaim{}).Where("provider = ? AND subject = ?", ExternalIdentityProviderGoogle, claimSubject).Count(&count).Error; err != nil || count != 0 {
		return true
	}
	// Fail closed for a legacy/raw binding while startup backfill is pending.
	err = DB.Unscoped().Model(&User{}).Where("google_id = ?", subject).Count(&count).Error
	return err != nil || count != 0
}

func GetUserByGoogleID(subject string) (*User, error) {
	claimSubject, err := googleIdentityClaimSubject(subject)
	if err != nil {
		return nil, err
	}
	var claim ExternalIdentityClaim
	if err := DB.Where("provider = ? AND subject = ?", ExternalIdentityProviderGoogle, claimSubject).First(&claim).Error; err != nil {
		return nil, err
	}
	var user User
	if err := DB.Where("id = ? AND google_id = ?", claim.UserId, subject).First(&user).Error; err != nil {
		return nil, err
	}
	if user.GoogleId != subject {
		return nil, ErrAccountBindingChanged
	}
	return &user, nil
}

// UpdateGoogleBindingWithTx is shared by registration and account linking.
// Callers own the transaction. A nonempty existing binding cannot be replaced
// silently; unlinking must pass through the explicit authenticated operation.
func UpdateGoogleBindingWithTx(tx *gorm.DB, userID int, subject string) error {
	if tx == nil || userID <= 0 {
		return ErrAccountBindingChanged
	}
	var user User
	if err := lockForUpdate(tx).Select("id", "google_id").First(&user, userID).Error; err != nil {
		return err
	}
	if subject == "" {
		if user.GoogleId != "" {
			if _, err := IncrementUserAuthVersionWithTx(tx, userID); err != nil {
				return err
			}
		}
		if err := tx.Model(&User{}).Where("id = ?", userID).Update("google_id", "").Error; err != nil {
			return err
		}
		return ReleaseExternalIdentityWithTx(tx, ExternalIdentityProviderGoogle, userID)
	}
	claimSubject, err := googleIdentityClaimSubject(subject)
	if err != nil {
		return err
	}
	if user.GoogleId != "" && user.GoogleId != subject {
		return ErrExternalIdentityAlreadyClaimed
	}
	var count int64
	if err := tx.Unscoped().Model(&User{}).Where("google_id = ? AND id <> ?", subject, userID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrExternalIdentityAlreadyClaimed
	}
	if err := ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderGoogle, claimSubject, userID); err != nil {
		return err
	}
	return tx.Model(&User{}).Where("id = ?", userID).Update("google_id", subject).Error
}

func BindGoogleForSessionWithTx(tx *gorm.DB, identity AuthSessionIdentity, subject string) error {
	if subject == "" {
		return ErrAccountBindingChanged
	}
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return err
	}
	return UpdateGoogleBindingWithTx(tx, identity.UserID, subject)
}

// UnbindGoogleForSession runs after an operation-specific security proof.
// Session validity, ownership and a usable alternative login are rechecked
// under the same user lock as removing both the binding and unique claim.
func UnbindGoogleForSession(identity AuthSessionIdentity, enabled AccountLoginMethods) error {
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			return err
		}
		var current User
		if err := tx.First(&current, identity.UserID).Error; err != nil {
			return err
		}
		if current.GoogleId == "" {
			return ErrAccountBindingChanged
		}
		current.GoogleId = ""
		hasLogin, err := hasAccountLoginMethodWithTx(tx, current, enabled, 0)
		if err != nil {
			return err
		}
		if !hasLogin {
			return ErrLastLoginMethod
		}
		return UpdateGoogleBindingWithTx(tx, identity.UserID, "")
	}); err != nil {
		return err
	}
	return PublishUserAuthCache(identity.UserID)
}

// ClearGoogleBindingByAdministrator rechecks the actor session and both users'
// current privileges under transaction locks, not a pre-request role snapshot.
func ClearGoogleBindingByAdministrator(identity AuthSessionIdentity, expected User) error {
	if err := DB.Transaction(func(tx *gorm.DB) error {
		_, target, err := lockAdminUserMutationTx(tx, identity.UserID, expected, identity)
		if err != nil {
			return err
		}
		if target.GoogleId != expected.GoogleId {
			return ErrAccountBindingChanged
		}
		return UpdateGoogleBindingWithTx(tx, expected.Id, "")
	}); err != nil {
		return err
	}
	return PublishUserAuthCache(expected.Id)
}

// initializeGoogleIdentityClaims also validates restored/legacy data. A
// duplicate, malformed or reassigned subject aborts startup rather than making
// an ambiguous Google account capable of logging in.
func initializeGoogleIdentityClaims(tx *gorm.DB) error {
	var users []User
	if err := tx.Unscoped().Select("id", "google_id").Where("google_id <> ?", "").Find(&users).Error; err != nil {
		return err
	}
	for _, user := range users {
		subject, err := googleIdentityClaimSubject(user.GoogleId)
		if err != nil {
			return err
		}
		if err := ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderGoogle, subject, user.Id); err != nil {
			return err
		}
	}
	return nil
}
