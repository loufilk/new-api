package authz

import (
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// CanInTx evaluates a registered permission from the authoritative policy rows
// in the caller's transaction, without consulting the in-memory enforcer.
// The caller must already hold the actor's user-row lock and pass its current
// role after validating the session. User permission updates take that same row
// lock, so a revocation either precedes this check or waits for the action to
// commit. This function does not authenticate a caller or accept client roles.
func CanInTx(tx *gorm.DB, userID int, systemRole int, permission Permission) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("authorization transaction is unavailable")
	}
	if userID <= 0 || !common.IsValidateRole(systemRole) || !isKnownPermission(permission) {
		return false, nil
	}
	roles := resolveSubjectRoles(userID, systemRole)
	if slices.ContainsFunc(roles, isSuperuserRole) {
		return true, nil
	}
	if len(roles) == 0 {
		return false, nil
	}
	userSubject := UserSubject(userID)
	subjects := []string{userSubject}
	for _, role := range roles {
		subjects = append(subjects, RoleSubject(role))
	}
	var rows []model.CasbinRule
	if err := tx.Where("ptype = ? AND v0 IN ? AND v1 = ? AND v2 = ?", "p", subjects, permission.Resource, permission.Action).Find(&rows).Error; err != nil {
		return false, err
	}
	// Resolve each subject independently: a user override wins over the role
	// baseline, while any deny for the same subject wins over its allows.
	effects := make(map[string]string, len(subjects))
	for _, row := range rows {
		effect := row.V3
		if effect == "" {
			effect = EffectAllow
		}
		if effect != EffectAllow && effect != EffectDeny {
			return false, fmt.Errorf("authorization policy %d has an invalid effect", row.Id)
		}
		// Match gormAdapter.LoadPolicy. Unsupported own/other legacy scopes
		// must not disappear and expose a wider admin baseline.
		if row.V5 != "" || (row.V4 != "" && row.V4 != "all") {
			effect = EffectDeny
		}
		if effects[row.V0] != EffectDeny {
			effects[row.V0] = effect
		}
	}
	if effect, found := effects[userSubject]; found {
		return effect == EffectAllow, nil
	}
	for _, role := range roles {
		if effects[RoleSubject(role)] == EffectAllow {
			return true, nil
		}
	}
	return false, nil
}
