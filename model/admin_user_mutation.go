package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// AdminUserMutationGuard binds an administrative mutation to the authoritative
// actor session and the target version shown when the operation was confirmed.
type AdminUserMutationGuard struct {
	Identity AuthSessionIdentity
	Expected User
}

// ValidateAdminUserCreationTx keeps the creator's role and session authoritative
// until user insertion and permission configuration commit.
func ValidateAdminUserCreationTx(tx *gorm.DB, identity AuthSessionIdentity, role int) error {
	var actor User
	if err := lockForUpdate(tx).First(&actor, identity.UserID).Error; err != nil {
		return err
	}
	if actor.Status != common.UserStatusEnabled || actor.Role < common.RoleAdminUser ||
		!common.IsValidateRole(role) || role == common.RoleAgentUser || role >= actor.Role {
		return errors.New("Unauthorized, insufficient privileges")
	}
	return ValidateAuthSessionWithTx(tx.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}), identity)
}

func lockAdminUserMutationTx(tx *gorm.DB, actorID int, expected User, identities ...AuthSessionIdentity) (*User, *User, error) {
	if actorID <= 0 || expected.Id <= 0 || actorID == expected.Id {
		return nil, nil, errors.New("Unauthorized, insufficient privileges")
	}
	var users []User
	if err := lockForUpdate(tx).Where("id IN ?", []int{actorID, expected.Id}).Order("id").Find(&users).Error; err != nil {
		return nil, nil, err
	}
	var actor, target *User
	for i := range users {
		if users[i].Id == actorID {
			actor = &users[i]
		} else if users[i].Id == expected.Id {
			target = &users[i]
		}
	}
	if actor == nil || target == nil || actor.Status != common.UserStatusEnabled || actor.Role < common.RoleAdminUser ||
		target.Role == common.RoleRootUser || (actor.Role != common.RoleRootUser && actor.Role <= target.Role) {
		return nil, nil, errors.New("Unauthorized, insufficient privileges")
	}
	if target.Role != expected.Role || target.Status != expected.Status || target.AuthVersion != expected.AuthVersion {
		return nil, nil, errors.New("User permissions changed; reload and try again")
	}
	if len(identities) > 0 {
		if identities[0].UserID != actorID {
			return nil, nil, ErrUserSessionInactive
		}
		if err := ValidateAuthSessionWithTx(tx.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}), identities[0]); err != nil {
			return nil, nil, err
		}
	}
	return actor, target, nil
}

// ApplyUserManagementActionTx serializes privilege changes with authoritative user state. It writes only role/status, never a stale full
// user snapshot over a concurrent balance, referral or profile change.
// HTTP callers must pass the authenticated receipt; internal callers may omit it.
func ApplyUserManagementActionTx(tx *gorm.DB, actorID int, expected User, action string, identities ...AuthSessionIdentity) (*User, error) {
	actor, target, err := lockAdminUserMutationTx(tx, actorID, expected, identities...)
	if err != nil {
		return nil, err
	}
	role, status := target.Role, target.Status
	switch action {
	case "disable":
		status = common.UserStatusDisabled
	case "enable":
		status = common.UserStatusEnabled
	case "promote":
		if actor.Role != common.RoleRootUser || target.Role >= common.RoleAdminUser {
			return nil, errors.New("Unauthorized, insufficient privileges")
		}
		role = common.RoleAdminUser
	case "demote":
		role = common.RoleCommonUser
	default:
		return nil, errors.New("Invalid parameters")
	}
	if role == target.Role && status == target.Status {
		return target, nil
	}
	version, err := IncrementUserAuthVersionWithTx(tx, target.Id)
	if err != nil {
		return nil, err
	}
	if err := tx.Model(&User{}).Where("id = ?", target.Id).Updates(map[string]any{"role": role, "status": status}).Error; err != nil {
		return nil, err
	}
	target.Role, target.Status, target.AuthVersion = role, status, version
	return target, nil
}
