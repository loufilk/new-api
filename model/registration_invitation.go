package model

import (
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// resolveInviterForInsert serializes registration with inviter disablement or
// role changes. Invalid invitations are ignored; storage errors abort the insert.
// No separate sales policy, test balance, or late attribution is involved.
func resolveInviterForInsert(tx *gorm.DB, userID, inviterID int) (int, error) {
	if inviterID <= 0 || inviterID == userID {
		return 0, nil
	}
	var inviter User
	err := lockForUpdate(tx).Select("id", "role", "status").First(&inviter, inviterID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if inviter.Status != common.UserStatusEnabled {
		return 0, nil
	}
	switch common.EffectiveUserRole(inviter.Role) {
	case common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser:
		return inviter.Id, nil
	default:
		return 0, nil
	}
}

// inviteUserWithTx is called once with a genuinely new user insertion, never
// from login or post-commit finalization. A failed counter write rolls back the
// inserted user and OAuth binding; it cannot award or modify a wallet balance.
func inviteUserWithTx(tx *gorm.DB, inviterID int) error {
	if inviterID == 0 {
		return nil
	}
	result := tx.Model(&User{}).Where("id = ? AND aff_count >= 0 AND aff_count < ?", inviterID, math.MaxInt32).
		Update("aff_count", gorm.Expr("aff_count + ?", 1))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
func inviteUser(inviterID int) error { return inviteUserWithTx(DB, inviterID) }
