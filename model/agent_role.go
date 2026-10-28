package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ApplyUserManagementActionTx serializes privilege changes with agent budget
// and invitation transactions. It writes only role/status, never a stale full
// user snapshot over a concurrent balance, referral or profile change.
func ApplyUserManagementActionTx(tx *gorm.DB, actorID int, expected User, action string) (*User, error) {
	if actorID <= 0 || expected.Id <= 0 || actorID == expected.Id {
		return nil, errors.New("Unauthorized, insufficient privileges")
	}
	var users []User
	if err := lockForUpdate(tx).Where("id IN ?", []int{actorID, expected.Id}).Order("id").Find(&users).Error; err != nil {
		return nil, err
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
		return nil, errors.New("Unauthorized, insufficient privileges")
	}
	if target.Role != expected.Role || target.Status != expected.Status || target.AuthVersion != expected.AuthVersion {
		return nil, errors.New("User permissions changed; reload and try again")
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
	case "set_agent":
		if target.Role != common.RoleCommonUser {
			return nil, errors.New("Only common users can become agent administrators")
		}
		role = common.RoleAgentUser
	case "unset_agent":
		if target.Role != common.RoleAgentUser {
			return nil, errors.New("User is not an agent administrator")
		}
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
	if role == common.RoleAgentUser && target.Role != common.RoleAgentUser {
		// Do not resurrect pre-agent API credentials on a later downgrade.
		if _, err := DeleteUserAccessTokensWithTx(tx, target.Id); err != nil {
			return nil, err
		}
		var tokens []Token
		if err := lockForUpdate(tx).Where("user_id = ?", target.Id).Order("id").Find(&tokens).Error; err != nil {
			return nil, err
		}
		for _, token := range tokens {
			if err := advanceTokenPermissionRevisionTx(tx, token.Id); err != nil {
				return nil, err
			}
		}
		if err := tx.Model(&Token{}).Where("user_id = ?", target.Id).Update("status", common.TokenStatusDisabled).Error; err != nil {
			return nil, err
		}
	}
	if err := tx.Model(&User{}).Where("id = ?", target.Id).Updates(map[string]any{"role": role, "status": status}).Error; err != nil {
		return nil, err
	}
	target.Role, target.Status, target.AuthVersion = role, status, version
	return target, nil
}
