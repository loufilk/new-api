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
		!common.IsValidateRole(role) || role >= actor.Role {
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

// ApplyUserManagementActionTx serializes privilege changes with agent budget
// and invitation transactions. It writes only role/status, never a stale full
// user snapshot over a concurrent balance, referral or profile change.
// HTTP callers must pass the authenticated receipt; internal callers may omit it.
func ApplyUserManagementActionTx(tx *gorm.DB, actorID int, expected User, action string, identities ...AuthSessionIdentity) (*User, error) {
	if action == "set_agent" || action == "unset_agent" {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return nil, err
		}
	}
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
	case "set_agent", "unset_agent":
		profile, err := LockAgentProfileTx(tx, target.Id)
		if err != nil {
			return nil, err
		}
		request := AgentProfileMutation{Status: AgentProfileActive, Reason: "Agent qualification enabled by administrator"}
		if profile != nil {
			request.ExpectedVersion = profile.Version
			request.SalesFrozen, request.SettlementFrozen = profile.SalesFrozen, profile.SettlementFrozen
		}
		if action == "unset_agent" {
			if profile == nil {
				return nil, ErrAgentProfilePermission
			}
			request.Status, request.Reason = AgentProfileExited, "Agent qualification exited by administrator"
		}
		if _, err := updateAgentProfileTx(tx, actor.Id, target.Id, request); err != nil {
			return nil, err
		}
		if target.Role == common.RoleAgentUser {
			if err := tx.Model(&User{}).Where("id = ?", target.Id).Update("role", common.RoleCommonUser).Error; err != nil {
				return nil, err
			}
			target.Role = common.RoleCommonUser
		}
		return target, nil
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
